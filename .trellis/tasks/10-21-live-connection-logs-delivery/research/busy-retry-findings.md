# Explicit upstream concurrency refusals: retry and health findings

Date: 2026-10-22. Scope: repository inspection and localhost fixtures only.
The user reported `TM.00001041 / upstream_busy`, 14.9 seconds and nine attempts,
separately from `InferHub.002002009.404 / model_not_available`, four attempts.
Neither report identifies the actual production account sequence or configuration.

## Original retry amplification

The defaults permit three queue attempts per account, a two-second retry delay
and three generic account rotations. In `internal/server/chat.go`, the inner
loop exhausted retries on the current account before considering another one.
With three selected accounts continually refusing concurrency, this yields
`AAA BBB CCC`: nine actual upstream calls and six sleeps (12 seconds), plus
network and other work. This fits the reported duration but does not establish
that production used those accounts or defaults.

The counter increments immediately before the outbound chat call; it does not
count local selections, validation or slot waits. A successful 401 token refresh
separately permits another attempt, so nine is not a universal bound over mixed
failure classes. Startup and cumulative slot-wait budgets still apply.

## Confirmed health-classification defect

`internal/upstream/client.go` normalizes an HTTP200 first-SSE concurrency refusal
to `ApiError.Status=429`. The original `handleUpstreamError` applied soft cooldown
to every 429 before its neutral concurrency branch, which only matched HTTP400.
Thus HTTP400 plus TM was neutral, while HTTP429 plus TM and first-SSE TM cooled
the entire account (60 seconds by default). Audit independently recognized TM,
so a correct `concurrency_limit` label did not prove correct scheduling behavior.

Late SSE failures use `*upstream.UpstreamError` and could fall through to
`Pool.NoteError`. They must also be health-neutral when explicitly concurrent,
but still end the response: no regeneration or failover after first data.
Actual 401 authentication rejection retains precedence; ordinary rate/TPM
penalties must not be weakened by classifying every 429 as concurrency.

## Capability is a different failure

`InferHub.002002009.404` / `not registered` records an account/model negative
conclusion without whole-account cooldown. Capability failures do not consume
the generic rotation budget. Four attempts can therefore mean four different
accounts each refusing once, rather than four same-account retries.

The rejection establishes that the provider rejected the actual wire ID. It
alone cannot distinguish wrong casing/route from real entitlement. The separate
model-case findings reproduce a repository casing defect without claiming the
cause of the particular production request or substituting another model.

## Ownership and boundedness

No lease leak was found in this retry path. Attempt cleanup closes its body and
releases the acquired object before sleeping. A failed reacquisition owns no
lease; release remains idempotent across account removal/reload. The request
retains one pool-wide lifecycle ticket through all retries, preventing unlimited
retry sleepers. That ticket is released when handler cleanup completes.

`chatCandidates` reads health, catalog knowledge and negative availability.
Eligibility does not mean that a local slot is free. `Pool.AcquireAccount` makes
the atomic capacity decision, with bounded pending requests and cumulative wait.
Process-local capacity does not establish upstream occupancy or coordinate other
instances, website sessions or delayed provider session release. Those possible
production contributors were not measured; increasing default concurrency is
not supported by this evidence.

## Minimal repair and regression boundaries

For explicit concurrent-session refusal only, exit the inner retry loop early
when an untried eligible account exists and generic rotation budget permits
another selection. Release the current attempt through existing cleanup and let
the normal atomic acquisition choose the next slot. With no backup or remaining
rotation, retain bounded same-account retries. Ordinary429/TPM policies stay
unchanged. No additional shared state or scheduling redesign is required.

With defaults and three always-busy selected accounts, the expected sequence is
`A/B/CCC`, five actual calls and two sleeps. A single account or `MaxRotate=1`
retains at most three queue attempts. Five is not a universal cap, especially
when separately bounded auth retries are involved.

Required real HTTP checks cover HTTP400/429/first-SSE A-to-B success; single,
all-busy and exhausted-rotation sequences; ordinary429/TPM penalties; late-SSE
termination with no retry; actual audit attempts/tried/final account/cause/code;
Retry-After and complete lease/admission capacity reuse. Existing lifecycle
tests continue to cover cancellation, failed reacquisition, shared handlers,
repeated retry waves and account removal/re-addition.

## Evidence and affected owners

- `internal/server/chat.go`: actual attempts, retries, rotation and completion.
- `internal/server/{handler,audit_cause,lifecycle}.go`: classifications and cached candidates.
- `internal/pool/{admission,pool}.go`: lifecycle tickets, exact leases and cooldown.
- `internal/upstream/{client,sse}.go`: HTTP/first-SSE normalization and late errors.
- `cmd/server/{config,main}.go`: effective queue defaults and MaxRotate injection.

Before implementation, eight existing tests passed (server 0.149 seconds):
`QueueRetryDefaultBounded`, `ChatQueueExhaustionDoesNotSleepAfterFinalAttempt`,
`ChatCompletionsRetriesEmbeddedCodeArtsQueueLimit`,
`ChatCancellationDuringQueueRetryDoesNotRotate`,
`ChatFailedReacquisitionCannotReleaseAnotherRequestSlot`,
`ChatRetrySleeperWavesRespectSharedLifecycleBound`,
`AuditCapturesRetryAfterHeader` and `ModelErrorDoesNotCoolAccount` (all prefixed
with `Test`). These establish prior bounds, not the new multi-account behavior.

The follow-up `concurrency_limit_test.go` adds 12 actual-handler scenarios. Its
targeted normal suite passed in 1.050 seconds and race suite in 2.155 seconds.
The parent also passed full integrated normal/race/vet/build checks; the final
verification report records independent review and source release status.

The research worker changed only this report; the parent translated/consolidated
it after handoff. No production calls or credentials were inspected. Public
search for the precise error codes found no authoritative account/model/cross-
instance quota specification; no numeric provider capacity is asserted.
