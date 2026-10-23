# Chat request lifecycle and account admission

## 1. Scope / Trigger

Apply this contract when changing `/v1/chat/completions`, account selection or
refresh, streaming HTTP/SSE, runtime account reload, or their configuration.
The implementation lives in `internal/server/{chat,lifecycle}.go`,
`internal/pool/{admission,refresh,pool,upstream_busy}.go`,
`internal/upstream/{client,sse,stream_body,session_lifecycle}.go`,
and `cmd/server/{config,main}.go`. Preserve Go 1.22 compatibility.

The October 2026 incident reproduced requests retained after protocol completion,
ineffective stream-body timeouts, waiting behind a busy first account, cancellation
penalties, and incorrect slot release after a failed retry acquisition. Finite
readers and tests of unused selection helpers did not exercise these failures.
The October 23 follow-up also proved that local lease release and HTTP EOF do
not release the provider's tracked root session. Apply the remote session contract
to every actual inference attempt, including background availability probes.

## 2. Signatures

```go
type AccountCandidate struct { Name string; Priority int; Sticky bool }
type Lease struct { Account *Account /* private ownership */ }
func (*Lease) Release()
func (*Pool) AdmitRequest(context.Context) (release func(), err error)
func (*Pool) AcquireAccount(context.Context, []AccountCandidate, time.Duration) (*Lease, error)
func (*Pool) ValidateContext(context.Context, *Account) (bool, error)
func (*Pool) RefreshTokenContext(context.Context, string) error
func (*Pool) NoteUpstreamBusy(*Account, *auth.Auth, time.Duration) time.Duration
func (*Pool) UpstreamBusy(*Account) bool
func (*Pool) NoteSuccessForAuth(*Account, *auth.Auth)
func (*Account) AuthSnapshot() *auth.Auth

func upstream.New(time.Duration) *upstream.Client
func upstream.NewWithChatEndpoint(time.Duration, string) *upstream.Client
func upstream.NewWithSessionEndpoint(time.Duration, string) *upstream.Client
func upstream.AggregateRaw(io.Reader) (*upstream.RawCompletion, error)
func upstream.StreamCaptureWithUsage(http.ResponseWriter, io.Reader, string,
    func(*upstream.RawCompletion), func(*upstream.RawCompletion) map[string]any) error
```

`pool.ErrQueueFull`, `pool.ErrAcquireTimeout`, `pool.ErrNoHealthyAccount` and
`pool.ErrUpstreamBusy`
support `errors.Is`. `*upstream.IdleTimeoutError` implements `net.Error` and wraps
`context.DeadlineExceeded`. `*upstream.DownstreamWriteError` wraps the downstream
write/flush error and supports `errors.As`.
`*pool.UpstreamBusyError` unwraps `pool.ErrUpstreamBusy` and carries
`RetryAfter time.Duration`; this is retry advice rather than a provider quota or
promised release time.

The provider lifecycle API is:

```http
PUT {origin(inference_endpoint)}/snap-manager/v1/chat-session/heartbeat?status=busy
Content-Type: application/json
Accept: application/json
x-client-type: kernel
X-Language: zh-cn
x-snap-traceid: <fresh notification ID>
user-session-id: <this attempt's unique root session ID>
X-Security-Token: <selected attempt STS token>
Authorization: SDK-HMAC-SHA256 ...

{}
```

`status` is exactly `busy` or `idle`. The origin has no `/api/v2` prefix.
Both inference and notifications sign the same `user-session-id`, using the
selected attempt's AK/SK and token. Set the final query and headers before
`signRequest`; never add `maas_type` to a heartbeat.

## 3. Contracts

### Admission, fairness, ownership

- Acquire a pool-wide lifecycle ticket **before reading the request body**. Hold
  it until handler cleanup completes, including retry sleeps and response writes.
  The cap is enabled account capacity plus `MaxPending`. A slot-waiter bound alone
  cannot bound requests sleeping between rapid upstream 429 responses.
- `AcquireAccount` atomically selects and reserves capacity, with at most
  `MaxPending` queued slot waiters. Compatible existing waiters receive released
  capacity before newcomers. Selection ranks available accounts by ascending
  capability priority, least active/max utilization, then round-robin ties.
- Chat candidate construction reads cached catalogs and negative availability;
  it performs no synchronous model discovery. Unknown catalogs remain eligible.
  Missing entries in partial or stale catalogs also remain unknown. Use the atomic
  `AccountModelKnowledgeForAuth` read to exclude by absence only for complete,
  current discovery; actual provider denials remain authoritative independently.
  A positive availability entry alone must not raise an unknown account's
  priority and starve other unknown accounts.
- Account-specific capability rejections do not consume the generic `MaxRotate`
  failure budget. Keep the tried-account set and startup budget finite. More
  capability-refusing accounts than `MaxRotate` must not prevent reaching a later
  capable account. A global 404 requires exhausting the evaluated eligible
  capability candidates; remaining untried candidates require truthful failure.
- Explicit upstream concurrent-session rejection can move to an untried eligible
  account before sleeping/retrying the current account, only when the generic
  rotation budget permits another selection. Eligibility does not establish local
  free capacity; `AcquireAccount` still owns the atomic slot decision. Keep the
  existing same-account retry budget when no eligible backup or rotation remains.
  Do not apply this shortcut to ordinary rate/TPM failures.
- Derived sticky affinity requires explicit `conversation_id`, one of the
  `X-Codearts-Chat-Id` / `X-Session-Affinity` / `X-Session-Id` headers, or actual
  assistant/tool/function history. Identical independent first-turn prompts use
  fair allocation. Preserve upstream conversation ID generation and tool chains.
- Each attempt owns its exact lease and response body. Close the body and release
  the lease before retry sleeping; a failed reacquisition owns no slot. Release
  is idempotent and targets the acquired object even across removal/reload.
  Background model probes also use leases and abandon an acquisition if its
  account object differs from the object originally selected for the probe.
- Read credentials using `AuthSnapshot`, then the auth object's synchronized
  accessors. Share at most one bounded refresh per account. Canceling one waiter
  releases that caller promptly while the shared refresh can persist a rotated
  refresh token using the credential's original OAuth ClientID. Refresh must not
  overwrite a newer login, resurrect a removed account, or undo a manual disable.

### Remote session ownership

- Production `upstream.New` manages remote sessions at `sendChatV2`, shared by
  actual chat and availability probes. `NewWithChatEndpoint` deliberately keeps
  the legacy chat-only fixture boundary; use `NewWithSessionEndpoint` for a local
  fixture that serves both inference and heartbeat routes at the same origin.
  A chat-only fixture passing does not establish session lifecycle behavior.
- Each actual attempt creates a unique 32-character random hex `user-session-id`.
  Keep `Chat-Id`, derived `Session-Id` and semantic conversation history separate.
  Parallel attempts with the same conversation must not share remote ownership;
  idle from one attempt cannot release another attempt's active root session.
- Make an initial bounded busy notification before inference, then renew busy
  every 30 seconds while active. Initial notification and inference share caller
  cancellation and the existing startup budget. Each notification has an
  independent timeout of at most three seconds, also respecting the HTTP client's
  configured shorter timeout. There is no separate user-facing heartbeat setting.
- Keep at most one renewal worker per attempt. On success, explicit close,
  provider/transport/preflight failure, caller cancellation or timeout, cancel
  renewal, close/cancel the owned inference transport, send bounded idle with
  `context.WithoutCancel` plus its own timeout, and join the worker before returning
  from managed body cleanup. The local account lease remains owned until this
  cleanup completes. A response arriving after cancellation must also be closed.
- A failed busy notification still requires idle cleanup. Heartbeat failures log
  only fixed outcome/status metadata; discard bounded response bodies and never
  log credentials, signed headers or provider text. Notification/cleanup failure
  does not replace a genuine inference result or trigger another generation.
- Publish transport ownership under a short mutex, then perform network close and
  notifications outside locks. No per-read goroutines, detached unbounded cleanup,
  per-notification retry loops or renewal worker surviving close are permitted.
- SSE consumers still stop at protocol completion rather than waiting for HTTP
  EOF, and their owner must close the returned body. A canceled caller also causes
  automatic transport/remote cleanup if the handed-off body is no longer read.

The protocol and direct real-account control are recorded in
[official-session-heartbeat-contract.md](../../tasks/10-21-live-connection-logs-delivery/research/official-session-heartbeat-contract.md).
The official hooks are asynchronous; waiting for initial busy before inference
is this gateway's deliberate deterministic ordering. Successful direct calls do
not by themselves establish repaired full-gateway or concurrency acceptance.

### Transient provider-busy admission

- Record explicit concurrent-session refusal with
  `NoteUpstreamBusy(acquiredAccount, capturedAuth, baseDelay)` before releasing the
  owned attempt. Busy state is account-object/auth-owner scoped and shared across
  handlers using that pool. Removed/re-added accounts and replacement logins
  cannot inherit a late old-owner result.
- Backoff starts at the configured queue retry delay (nonpositive base defaults
  to two seconds), doubles for repeated explicit refusals and caps at 30 seconds.
  Expiry permits another bounded acquisition; it does not prove provider release.
  A genuine successful completion calls `NoteSuccessForAuth` to clear that login's
  transient state. Heartbeat HTTP200 alone is not a generation success.
- `AcquireAccount` skips remembered-busy candidates while another eligible account
  can be acquired. When all healthy candidates are remembered busy, wait within
  existing cancellation/account-wait/startup/admission bounds and wake at the next
  expiry or state change. Exhaustion reports typed provider saturation rather than
  inventing model absence or unhealthy-account state.
- Sticky candidate construction retains eligible backups and marks the affinity
  candidate explicitly. Ordinary local slot occupancy preserves that affinity:
  wait for its lease instead of dispatching the conversation to a free backup.
  A provider-busy, disabled or removed sticky candidate releases the preference.
  Re-evaluate it inside atomic acquisition on every state-change wake, including
  for already queued waiters, so a newly recorded explicit busy refusal can send
  those waiters to a free eligible backup immediately.
- Remembered busy does not increment health errors, disable the account, set a
  whole-account health cooldown, or write a model-capability denial. Keep ordinary
  rate/TPM policy distinct. This backoff limits repeated hammering; it cannot
  substitute for remote idle or enlarge the provider's account quota.

### Time budgets and configuration

| JSON field | Environment override | Default | Meaning |
|---|---|---|---|
| `max_concurrent` | `CA2A_MAX_CONCURRENT` | 1 | Per-account active slots |
| `max_pending` | `CA2A_MAX_PENDING` | 64 | Pool-wide waiting limit and extra lifecycle tickets |
| `startup_timeout_seconds` | `CA2A_STARTUP_TIMEOUT_SECONDS` | 90 | Body read through first upstream data, including waits/retries/rotation/refresh |
| `account_wait_seconds` | `CA2A_ACCOUNT_WAIT_SECONDS` | 10 | Cumulative account-slot wait across the request |
| `upstream.timeout_seconds` | `CA2A_UPSTREAM_TIMEOUT_SECONDS` | 60 | Header, first body, and later blocked upstream I/O waits |
| `stream_write_timeout_seconds` | `CA2A_STREAM_WRITE_TIMEOUT_SECONDS` | 30 | Each downstream write/flush |
| `queue_max_attempts` | `CA2A_QUEUE_MAX_ATTEMPTS` | 3 | Total attempts per account, including the initial attempt |
| `queue_retry_seconds` | `CA2A_QUEUE_RETRY_SECONDS` | 2 | Delay between queue retries; never after the last attempt |

Nonpositive values normalize to defaults in the configuration loader. Existing
explicit values remain effective; initial, added, and reloaded account clients
must use the same configured upstream timeout. One 401 refresh retry can occur
in addition to the queue-attempt count; all startup work shares one budget.

Stop the startup timer when upstream preflight succeeds without detaching caller
cancellation. A fixed `context.WithTimeout` covering the whole generation would
incorrectly truncate healthy long streams. The upstream client uses one
race-safe request/body timer during blocked I/O and cancels net/http directly;
do not spawn a goroutine per body read. Downstream writes use their own deadline.
The real HTTP body-read deadline may also cancel `Request.Context`; a body startup
timeout still needs the explicit 504 response and connection cleanup.

### Completion and responses

Recognize literal `[DONE]`, named `done`/`end`/`finish` events, and CodeArts JSON
DONE text. Shared decoding is used by preflight, aggregation, and streaming.
Errors take precedence over success-shaped DONE fields. Apply final content,
reasoning, tools, and usage before stopping. `finish_reason` alone is not terminal:
usage can follow it. Both consumers stop at completion without waiting for EOF.

Before response commitment, errors use the existing OpenAI JSON envelope.
After streaming starts, upstream read/error paths emit an SSE error and `[DONE]`
when the downstream is writable and return the actual error. A downstream
write/flush failure returns immediately. Success callbacks/account success apply
only to successful completion. Logs contain account/model, attempts, slot wait,
first-data latency, total duration, and outcome; omit messages and credentials.

## 4. Validation & Error Matrix

| Condition | Response / behavior | Account treatment |
|---|---|---|
| Lifecycle cap or slot queue full; account wait exhausted | 429 `server_busy`, `Retry-After` | No penalty |
| All healthy candidates remain in remembered concurrent-session backoff at wait exhaustion | 429 `upstream_busy`, bounded `Retry-After` advice | No health or model-capability penalty |
| No eligible healthy account | 503 `no_healthy_account` | No invented provider failure |
| Startup or upstream inactivity before headers | 504 `upstream_timeout` | No cooldown from timeout alone |
| Late stream read failure | SSE error + DONE if writable; release resources | Classify actual error; timeout alone is neutral |
| Client cancellation or downstream write/flush failure | Stop waits/I/O/retries, release ticket/body/lease | No cooldown, disable, availability change, or failover |
| Provider parameter rejection | 400 `invalid_request`, or terminal SSE error | No rotation or account penalty |
| Model unavailable on one account | Cache that account/model result; try another before response commitment | No whole-account cooldown |
| All eligible model candidates unavailable | 404 `model_not_available` | Keep other model capabilities intact |
| Queue retries exhausted | 429 `upstream_busy`, `Retry-After` when applicable | Preserve provider rate policy; explicit concurrency is neutral for HTTP400/429 and early/late SSE |
| Provider 401 after refresh handling / 5xx | Existing auth-disable / provider cooldown policy | Apply only to genuine provider errors |
| Inference completes at protocol terminal without HTTP EOF | Close owned transport, stop renewal, bounded detached idle, join worker before local lease release | Apply only the actual inference result |
| Busy/renewal/idle notification fails or stalls | Fixed safe diagnostics; at most three seconds per notification; still attempt final idle | Preserve actual generation outcome; no regeneration or invented capability rejection |

## 5. Good / Base / Bad Cases

- **Good:** Account A is busy and B is free. Unaffined traffic uses B immediately;
  two handlers sharing the pool enforce the same limits. A continuing stream can
  outlast startup/idle durations. Protocol DONE returns while upstream stays open.
- **Base:** One account and default settings yield one active slot and 64 extra
  lifecycle tickets; each request can wait at most 10 cumulative seconds for slots.
  These are process-local limits. Multiple replicas sharing accounts must divide
  the actual account capacity between replicas.
  The existing derived conversation ID uses the first user-message anchor, so
  unrelated conversations with that same anchor and assistant history can share
  an affinity key. Clients requiring distinct affinity should supply a unique
  explicit conversation/session ID. Independent first-turn traffic remains fair.
- With defaults and three selected accounts always rejecting concurrency, the
  bounded sequence is A/B/CCC (five actual calls), rather than three calls to each
  before switching. A single account or `MaxRotate=1` retains at most three queue
  attempts. A 401 refresh remains separately bounded; five is not a universal cap.
- Explicit `TM.00001041` must not increment account errors or set whole-account
  soft cooldown merely because preflight wrapped it as 429. Keep authentication
  rejection precedence and ordinary 429/TPM penalties. A late SSE concurrency
  error is neutral for account health but still ends the response without retry.
- **Good:** More than three immediate calls on one configured local slot each
  use a distinct matching busy/inference/idle identity and leave no remote
  occupancy. A stream exceeding 30 seconds renews without losing content.
- **Base:** Two parallel attempts with the same `Chat-Id` preserve conversation
  affinity but own distinct `user-session-id` values. Closing the first releases
  only its remote session; closing the second releases the remaining session.
- **Bad:** Healthy local slots and zero active audit rows do not prove remote
  release. Draining HTTP EOF, sleeping 20 seconds, globally lowercasing model IDs,
  or increasing `max_concurrent` cannot replace the evidenced provider protocol.
- **Bad:** An endless slow body, silent HTTP-200 SSE, downstream stalled writes,
  failed retry acquisition, or canceled shared-refresh waiter cannot indefinitely
  retain a chat slot. Excess retry-sleep waves fail promptly at admission.

## 6. Tests Required

- `internal/upstream/lifecycle_test.go`: terminal-without-EOF readers/live servers,
  error-over-DONE, trailing usage/tool calls, stalled headers/preflight/error/body
  reads, cancellation, active streams lasting multiple idle periods, downstream
  write and flush failure, and no success callback on failure.
- `internal/pool/{admission,refresh}_test.go`: priority/utilization/fair ties,
  compatible FIFO, waiter/ticket caps, cancellations, expiry, disable/remove/reload,
  idempotent release, concurrent saturation and refresh/reload races, rotated-token
  persistence, ClientID preservation, and initial/add/reload timeout propagation.
- Server lifecycle/concurrency/affinity tests must exercise the actual HTTP chat
  route. Assert free-account selection, first-turn fairness, explicit/history
  affinity, account-scoped model failures, retry exhaustion/cancellation,
  failed-reacquisition ownership, slow body/write cleanup, silent 504, long active
  streams, shared-handler caps, fast rejection, responsive health, and reuse.
  Include `TestProbeLeaseSurvivesAccountRemoveAndReAdd` and
  `TestChatStartupTimeoutDuringRefreshWaitReleasesEverything` for the background
  and credential-wait integration paths.
- Capability rotation tests must cover five accounts with `MaxRotate=3`, only the
  fifth capable, all-unavailable exhaustion, and the unchanged generic 5xx bound.
  `TestProductionWaveEighteenMixedRequestsWithPartialCooling` exercises live HTTP
  with three cooling accounts, five silent holders, mixed queued calls and
  cancellation; assert explicit failures, health response, ticket/slot reuse and
  later success/capability/retry behavior. Use fresh account/model cache keys.
- Run the full Go tests, race detector, vet, build, and `git diff --check`. Review
  new untracked source files explicitly because plain `git diff` omits them.
  Deterministic localhost saturation proves bounds, not production throughput.
- `concurrency_limit_test.go` exercises HTTP400/429/first-SSE backup selection,
  single/all-busy/max-rotation sequences, unchanged ordinary rate/TPM policy,
  late-SSE no-regeneration, audit counts and complete lease/admission reuse.
- `internal/upstream/session_lifecycle_test.go` exercises actual signed local HTTP:
  more than three sequential content-bearing calls, unique attempt identities,
  same-conversation parallel isolation, matching busy/inference/idle signatures,
  no heartbeat benefit header, fresh trace IDs and close idempotency. Assert
  inference transport closure before idle, no HTTP-EOF wait, periodic renewal on
  a continuing stream, stopped/joined workers and no later notifications.
  Include HTTP rejection, early/late SSE error, transport/header/first-data/later
  timeout, explicit close, cancellation and an unread handed-off body. Notification
  HTTP/timeouts must preserve successful content; closing must cancel and join an
  in-flight renewal. Private shortened intervals exercise scheduling, not a claim
  that a real 30-second upstream generation passed.
- An actual server HTTP route fixture must retain a synthetic provider's three
  sessions until matching idle. Reproduce its unmanaged successful-then-busy
  behavior, then assert managed repeated calls, a bounded 18-request wave,
  responsive health, balanced remote sessions and complete local ticket/lease
  reuse. Do not infer provider throughput from this synthetic cap.
- `internal/pool/upstream_busy_test.go` and `internal/server/upstream_busy_test.go`
  assert shared login-scoped backoff, expiry wake-up, cancellation, free-backup
  selection, neutral health/capability, genuine-success reset and replacement
  account/auth isolation. Do not broaden these checks into a sticky-policy change.
- Real-account acceptance separately requires the rebuilt full gateway: lower and
  display-case GLM/DeepSeek, actual content/DONE, repeated calls, a generation
  beyond the production 30-second heartbeat interval, progressive concurrency,
  correlated truthful audits, responsive health and cleanup. Direct successful
  upstream calls or local fixtures cannot be reported as that acceptance.

## 7. Wrong vs Correct

Wrong: select one account, poll without context, release by name unconditionally,
and rely on EOF or a global HTTP timeout to finish all responses.

```go
a := p.PickExcluding(tried)
p.AcquireLockWait(a.Name, 180*time.Second)
defer p.ReleaseLock(a.Name)
```

Correct: bound request lifetime admission and acquire an owned, cancellable lease
over the eligible candidates, keeping each retry's ownership separate.

```go
release, err := p.AdmitRequest(ctx)
if err != nil { return err }
defer release()
lease, err := p.AcquireAccount(ctx, candidates, remainingAccountWait)
if err != nil { return err }
defer lease.Release()
// Close the upstream body on every exit; stop consuming at protocol completion.
```

Wrong: treat local transport completion as proof that the provider released its
tracked root session, or reuse the conversation ID as every parallel attempt's
remote session ID.

```go
// HTTP EOF and lease release alone leave remote ownership unbalanced.
response, _ := http.DefaultClient.Do(unmanagedInferenceRequest)
_, _ = io.Copy(io.Discard, response.Body)
_ = response.Body.Close()
lease.Release()
```

Correct: use the managed production client and finish its owned body before
releasing the attempt's lease; the shared client boundary balances remote idle.

```go
defer lease.Release()
body, err := account.Client.ChatStreamWithExactModel(ctx, chatID, messages,
    traceID, selectedCredential, userName, exactAccountModelID, options, benefit)
if err != nil { return err } // Failed sends also finish their remote session.
defer body.Close()          // Close/idle/join runs before lease release.
result, err := upstream.AggregateRaw(body)
// Stop at protocol completion; no need to wait for transport EOF.
```
