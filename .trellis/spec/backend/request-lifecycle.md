# Chat request lifecycle and account admission

## 1. Scope / Trigger

Apply this contract when changing `/v1/chat/completions`, account selection or
refresh, streaming HTTP/SSE, runtime account reload, or their configuration.
The implementation lives in `internal/server/{chat,lifecycle}.go`,
`internal/pool/{admission,refresh,pool}.go`, `internal/upstream/{client,sse,stream_body}.go`,
and `cmd/server/{config,main}.go`. Preserve Go 1.22 compatibility.

The October 2026 incident reproduced requests retained after protocol completion,
ineffective stream-body timeouts, waiting behind a busy first account, cancellation
penalties, and incorrect slot release after a failed retry acquisition. Finite
readers and tests of unused selection helpers did not exercise these failures.

## 2. Signatures

```go
type AccountCandidate struct { Name string; Priority int }
type Lease struct { Account *Account /* private ownership */ }
func (*Lease) Release()
func (*Pool) AdmitRequest(context.Context) (release func(), err error)
func (*Pool) AcquireAccount(context.Context, []AccountCandidate, time.Duration) (*Lease, error)
func (*Pool) ValidateContext(context.Context, *Account) (bool, error)
func (*Pool) RefreshTokenContext(context.Context, string) error
func (*Account) AuthSnapshot() *auth.Auth

func upstream.New(time.Duration) *upstream.Client
func upstream.AggregateRaw(io.Reader) (*upstream.RawCompletion, error)
func upstream.StreamCaptureWithUsage(http.ResponseWriter, io.Reader, string,
    func(*upstream.RawCompletion), func(*upstream.RawCompletion) map[string]any) error
```

`pool.ErrQueueFull`, `pool.ErrAcquireTimeout`, and `pool.ErrNoHealthyAccount`
support `errors.Is`. `*upstream.IdleTimeoutError` implements `net.Error` and wraps
`context.DeadlineExceeded`. `*upstream.DownstreamWriteError` wraps the downstream
write/flush error and supports `errors.As`.

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
| No eligible healthy account | 503 `no_healthy_account` | No invented provider failure |
| Startup or upstream inactivity before headers | 504 `upstream_timeout` | No cooldown from timeout alone |
| Late stream read failure | SSE error + DONE if writable; release resources | Classify actual error; timeout alone is neutral |
| Client cancellation or downstream write/flush failure | Stop waits/I/O/retries, release ticket/body/lease | No cooldown, disable, availability change, or failover |
| Provider parameter rejection | 400 `invalid_request`, or terminal SSE error | No rotation or account penalty |
| Model unavailable on one account | Cache that account/model result; try another before response commitment | No whole-account cooldown |
| All eligible model candidates unavailable | 404 `model_not_available` | Keep other model capabilities intact |
| Queue retries exhausted | 429 `upstream_busy`, `Retry-After` when applicable | Preserve provider rate policy; explicit concurrency is neutral for HTTP400/429 and early/late SSE |
| Provider 401 after refresh handling / 5xx | Existing auth-disable / provider cooldown policy | Apply only to genuine provider errors |

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
