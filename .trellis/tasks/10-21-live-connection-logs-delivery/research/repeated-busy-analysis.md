# Repeated-request upstream busy investigation

Date: 2026-10-23 (Asia/Shanghai). Read-only product inspection; this report is the
only file owned by this researcher. No credentials, production requests or live
account metadata were accessed. The parent owns actual upstream verification.

**Superseding evidence:** subsequent official26.10.0 kernel inspection found
the signed remote chat-session heartbeat busy/idle protocol, and the parent
verified ten immediate managed-session DeepSeek replies on the previously
three-then-busy account. See `official-session-heartbeat-contract.md`. The earlier
absence-of-contract statements below describe the repository-only inspection;
they must not be used to dismiss this now-confirmed missing protocol.

## Evidence boundary

The supplied incident description has successful streams followed by repeated
`TM.00001041` / `concurrency_limit` / HTTP429. Some rows have the same final
account and overlapping request intervals. Those intervals alone do **not** prove
the precise upstream concurrency: `chat.go:50-57,99` starts the audit clock when
the handler receives a request, and the account field is the final selected
account. Slot wait, earlier account attempts and retry sleeps are included.
Use phase transitions and first-data times, or controlled outbound active counts,
to distinguish actual concurrent sends from requests queued for the same account.

## Findings ranked by relevance

### 1. Explicit configured concurrency can still admit five active requests per account

The config default is one (`cmd/server/config.go:102`) and the environment
override is `CA2A_MAX_CONCURRENT` (`:242-245`). A positive value supplied in an
existing JSON config is preserved. Initial, added and synced accounts all use
that configured value (`internal/pool/pool.go:123-130,170-186,340-356`).
`internal/pool/admission.go:165-191` enforces the configured local value rather
than provider quota. The example and README already explicitly state that an old
`max_concurrent:5` remains effective after rebuilding.

No root `config.json` or `.env` exists in this researcher's workspace snapshot.
Therefore the production effective value is **unverified**. Read it through the
parent's existing bounded admin snapshot or deployment configuration, rather
than assuming a default change modified an old installation.

This explains genuine overlapping upstream calls if production still permits
five, but no public/provider quota is established here. Existing comments saying
the provider permits three are not authoritative measurement.

### 2. Confirmed missing cross-request busy admission/backoff

`handler.go:788-789` deliberately treats explicit concurrent-session refusal as
health-neutral: it neither increments errors nor sets cooldown. This health
policy is correct, but there is **no separate temporary admission state** for an
upstream-busy account. `chatCandidates` and `AcquireAccount` can immediately
select it for a new independent request once the failed attempt releases its
local slot. Round-robin spreads calls but does not prevent repeated busy waves
across all eligible accounts.

Within one request, `chat.go:393-411` rotates to an untried account when possible,
otherwise closes/releases and retries the same account after the configured
delay. With defaults, three perpetually busy accounts yield A/B/CCC, five sends;
one account yields three sends. New independent requests repeat that full
sequence without remembering recent TM refusals. Returning `Retry-After:2`
only advises the external client; it does not govern other admitted handlers.

This is a concrete scheduler gap that amplifies an upstream limit or residual
session occupancy. It does not establish why the provider originally remained
busy. A safe repair should keep account health/capability intact while adding
bounded, account-scoped busy admission/backoff shared across requests. Include a
wake deadline: merely filtering busy accounts as unhealthy would produce false
503s, and queue waiters currently wake on pool mutations rather than an arbitrary
future backoff expiry. Ordinary rate/TPM policy must remain distinct.

Meaningful regression: a live local HTTP upstream accepts one generation and
retains simulated capacity briefly after DONE or caller cancellation; later
requests receive TM until that deadline. Send several gateway requests/waves,
assert bounded upstream send count during the busy interval, healthy/capability
neutral state, healthy backup selection, bounded waiting or truthful429, then a
successful request after recovery with all local leases/tickets released. This
tests cross-request memory absent from existing per-request retry tests.

### 3. No premature stream lease release found in the current implementation

`chat.go:314-315` retains the actual acquired lease in `chatAttempt`.
Streaming consumption runs to `StreamCaptureWithUsage` return and then cleanup
(`chat.go:471-474`); aggregation similarly closes after `AggregateRaw`
(`:492-494`). Deferred cleanup covers early exits (`:256-257`).
`lifecycle.go:69-82` closes the response body **before** releasing the lease.
`Lease.Release` is idempotent and releases the exact object
(`internal/pool/admission.go:30-44`). Retries close/release before sleeping.

Both response consumers stop at recognized protocol completion rather than EOF
(`upstream/sse.go:93-103,475-476,643-658`).
`upstream/stream_body.go:126-132` cancels the actual HTTP request and closes its
body. Thus there is no evidence here for “stream starts, local slot immediately
frees” or “DONE waits forever for socket EOF” in current code.

Local request cancellation/Close does not itself prove the remote provider has
already relinquished its session count. The parent should measure immediate
sequential requests separately from delayed sequential requests, including full
DONE consumption versus early cancellation. That distinguishes normal-stream
release from cancellation/residual occupancy without inventing a stop API.

### 4. A separate real retry/account-replacement ownership bug

At `chat.go:411`, a retry reacquires an account **by name**. It then updates only
`current.lease` (`:427`), while `acct` still points at the originally selected
object. If administration removes and re-adds the account during the retry
sleep, acquisition can reserve the replacement object but the subsequent send
still reads the old object's `AuthSnapshot` and `Client` (`:351,365`). It also
records provider health against a name that now identifies the replacement.

The availability-probe path already checks `lease.Account != acct` and abandons
this stale work (`availability.go:209-216`). Chat retry lacks the corresponding
identity handling and does not validate the reacquired object again.

Meaningful regression: first outbound call returns TM; during retry delay remove
the old account and re-add the same UID with different synthetic credentials and
a distinct test endpoint; retry must not send with the old credentials/client or
apply its result to the replacement. Assert exact lease capacity and no leak.
This is not established as the cause of the reported ordinary repeated calls.

## Background activity and session protocol

- Model discovery (`model_catalog.go:186-195`) fetches metadata/optional claims;
  it does not issue inference and intentionally has no inference lease.
- Availability probes **do** issue inference. They check recent traffic and busy
  state, acquire a real lease, use a 45-second bound and close it on exit
  (`availability.go:201-246,261`). Their global concurrency is one. At default
  per-account capacity one, an existing probe can briefly make a chat wait; at a
  larger configured cap it can coexist with chat. There is a check/acquire race
  in the idle heuristic, but the atomic lease still enforces total local cap.
- Probes read at most six lines and cancel instead of waiting for full DONE
  (`availability.go:262-281`). No `max_tokens` is supplied in this probe body.
  If provider cancellation release is delayed, probes could contribute residual
  remote occupancy despite correctly releasing local capacity. This is a
  **measurement hypothesis**, not proof; a bounded tiny-output full-completion
  probe would be more representative, but must have a real completion budget.
- Repository endpoint constants include old start/chat/feedback/record routes;
  there is no implemented stop/delete/release-session call. The reverse-engineer
  note labels `/v1/chat/management/event` as feedback, not session release. Its
  original local client-source directory is absent. No inspected provider
  contract proves another cleanup call is required. Do not add an invented API.
- Stable Chat-Id is derived from the first user message
  (`request.go:292-315`), and Session-Id from Chat-Id
  (`upstream/client.go:555-558`). Identical independent first turns therefore
  share upstream identifiers despite fair local account selection. Whether the
  provider interprets that as occupancy is unproven; test unique versus repeated
  IDs only if controlled sequential/concurrent evidence warrants it.

## Existing regression limits

`concurrency_limit_test.go` proves per-request rotation/retry counts, neutral
health, late error behavior and local resource reuse. It intentionally expects
all-busy A/B/CCC and no busy state, so it does not prove a later request avoids
recently upstream-saturated accounts. `production_wave_regression_test.go` uses
configured concurrency five on one schedulable account and proves local bounds,
timeouts and recovery; it does not establish provider capacity or remote session
release. No tests were executed by this read-only researcher; the parent owns
the current integrated test runs and real measurements.
