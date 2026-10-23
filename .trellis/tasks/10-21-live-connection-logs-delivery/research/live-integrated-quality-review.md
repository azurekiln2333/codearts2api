# Integrated live-follow-up quality review

Date: 2026-10-23 (Asia/Shanghai)

Task: `10-21-live-connection-logs-delivery`, follow-up requirements R9-R12.

Reviewed the current working changes and new untracked source/test files across
`internal/upstream`, `internal/pool` and `internal/server`, rather than treating
the earlier source release as the repaired implementation. Read the task PRD,
design, implementation context, model-discovery, lifecycle and audit contracts,
and the sanitized account/session research supplied by the parent session.

The reviewer did not read local credentials or test-runtime directories, make
real-provider calls, change credential files, modify source archives, commit or
deploy. Implementation owners made the coordinated production changes below;
the reviewer reproduced boundary failures and added independent regressions.

## Findings (fixed)

### Queued sticky requests could ignore a healthy backup

- Files: `internal/server/lifecycle.go`, `internal/pool/admission.go`,
  `internal/pool/upstream_busy.go`.
- Reproduction: pin a request to locally occupied account A, queue it, then
  record an explicit provider concurrency refusal on A while compatible B is
  idle. The original path returned HTTP 429 after its wait with no attempt on B.
- Fix: retain compatible candidates, mark the sticky candidate, and re-evaluate
  its eligibility atomically during acquisition and waiter dispatch. Local slot
  occupancy preserves affinity; remembered provider busy permits the backup.
- Regression: `TestChatStickyWaitReevaluatesNewUpstreamBusy` plus
  `TestStickyWaitKeepsLocalAffinityButReleasesItOnProviderBusy`.

### Late SSE concurrency errors were recorded after releasing the lease

- File: `internal/server/chat.go`.
- Reproduction: a first request emitted data, then a concurrency error while a
  second request waited for its slot. Releasing first allowed the second actual
  upstream request to succeed before the refusal became shared admission state.
- Fix: record the explicit busy result before `current.close()` can release the
  lease and dispatch a waiter. Apply to streaming and aggregate paths; retain
  cancellation neutrality and the existing no-regeneration policy after data.
- Regression: `TestChatLateBusyRecordedBeforeLeaseRelease` covers both modes.

### Cold model identity and old-wire availability could block corrected IDs

- Files: `internal/upstream/models.go`, `internal/server/availability.go`,
  `internal/server/handler.go`, `internal/server/model_catalog.go`, and chat/
  lifecycle integration.
- Issue: display-case cold requests could send a rejected wire ID. A folded
  negative cache key then remained applicable after ordinary discovery corrected
  the exact ID; a late old-wire result could overwrite a newer genuine denial.
- Fix: use only the two captured normal-route cold IDs; keep genuine uppercase
  registrations. Resolve account-local source IDs without a global rewrite.
  Store exact wire ID and captured auth with each availability entry. Guard
  writes by current account/auth and current exact resolution, and use the same
  predicate in routing, public filtering and cached admin diagnostics.
- Reviewer regressions: `TestColdIdentityCapturedDisplayNamesBeforeDiscovery`,
  `TestColdIdentityDynamicWireCorrectionIgnoresOldCaseDenial`, and
  `TestColdIdentityLateWrongCaseCannotOverwriteCorrectWireDenial` exercise actual
  HTTP, content, exact outbound IDs and truthful audit attempts. Dynamic tests
  use a nonseed ID and nonforced discovery, preserving unrelated/same-wire
  denials. Additional owner tests cover display collisions, source/benefit
  authority, cached projections and replacement login outcomes.

### A refresh completed by another caller left old source backoff active

- Files: `internal/upstream/model_source_backoff.go`,
  `internal/upstream/model_discovery.go`, `internal/upstream/client.go`, and
  `internal/server/model_catalog.go`.
- Reproduction: make three metadata requests with expired fixture STS, refresh
  successfully through another pool caller, then start ordinary discovery. The
  temporary worker-local before/after comparison saw no rotation, produced a
  failed job and sent no metadata requests with the new credentials.
- Fix: bind source backoff to the captured auth owner and a private digest of
  the exact signing tuple at its owning upstream boundary. Retain one generation
  and three retry times per account. Old results cannot set or clear another
  generation's backoff. Preserve five-minute same-generation failures and the
  explicit force operation; remove the worker's temporary force-on-rotation rule.
- Reviewer regression: `TestCatalogRefreshAfterOtherCallerRotatesCredentials`
  failed before the fix and passes in the final full normal/race suites.
  Upstream HTTP regressions additionally cover each changed tuple component,
  unchanged-generation backoff, late old success/failure, replacement auth,
  explicit force, successful reset and bounded pruning.

### Successful DONE delivery could be audited as canceled during cleanup

- Files: `internal/server/chat.go`, `internal/server/lifecycle.go`,
  `internal/server/remote_session_test.go`.
- Issue: detached bounded idle cleanup extends handler lifetime beyond protocol
  DONE. A client closing after receiving the flushed completion canceled that
  context and incorrectly changed the delivered success into cancellation.
  A separate coarse-clock boundary confused a real zero-duration first-data
  observation with an absent observation.
- Fix: preserve successful stream delivery when no stream/write error occurred;
  keep real interrupted delivery and nonstream cancellation distinct. Track the
  first-data presence flag independently of its duration.
- Regression: `TestRemoteSessionLifecycleDisconnectAfterDoneRemainsSuccessful`
  receives DONE over actual HTTP, holds idle cleanup and the upstream transport,
  closes the client, confirms context cancellation, then requires `ok`,
  `Canceled=false`, HTTP 200 and complete resource reuse. Existing audit tests
  retain successful first-data presence and null timing when no data arrived.

## Other verified boundaries

- Managed inference signs a unique attempt-owned `user-session-id`, initial
  busy, periodic busy and final idle using the captured signing credentials.
  Conversation IDs stay separate. Notification bodies and query/header
  signatures are checked by actual localhost HTTP fixtures.
- Cancellation, timeout, provider/transport/SSE failure, explicit close and an
  unread canceled body close the owned transport, stop/join the single renewal
  worker and perform bounded detached idle before releasing the local lease.
  Notification failures preserve the actual inference outcome.
- The actual gateway fixture reproduces unmanaged success-then-saturation and
  verifies managed repeated calls and an 18-request wave, content/DONE, one
  attempt per completion, responsive health, balanced remote sessions, empty
  active tracking and reusable local admission/leases.
- Bounded catalog workers validate credentials through the existing shared
  refresh, respect job cancellation/deadlines, recheck account/auth eligibility
  and guard publication. Cached admin GETs perform no refresh/discovery I/O.
  Credential failures do not fabricate model-source HTTP errors.
- Lifecycle, model-discovery and request-audit specs now document sticky waiter
  re-evaluation, remote ownership, exact-wire/auth availability, credential-bound
  source backoff and successful delivery before detached cleanup completes.

## Findings (not fixed)

No remaining concrete source-level finding was identified in the reviewed
follow-up changes after the coordinated fixes and final verification.

Real provider capacity, a real generation beyond the production 30-second renewal
interval, final rebuilt-gateway concurrency/health measurements, deployment and
release acceptance remain the parent session's separate evidence. Local fixtures
and these quality checks do not establish those operational results. No new UI
changes were part of this follow-up diff, so earlier browser acceptance was not
repeated by this backend reviewer.

## Verification

Final checks ran against the stable integrated product code after the fixes,
including the reviewer regressions, with Go 1.27.3 on Windows. Race used the
isolated MinGW GCC toolchain and process-local `CGO_ENABLED=1`.

| Check | Result |
| --- | --- |
| `go test ./... -count=1` | PASS; server 7.301 s, upstream 1.810 s |
| `go test -race ./... -count=1` | PASS; server 17.886 s, upstream 12.224 s; no race report |
| `go vet ./...` | PASS |
| `go build ./...` | PASS |
| `gofmt -l` on changed and new internal Go files | PASS; empty result |
| `git diff --check` with repository line-ending configuration | PASS |
| Explicit `git diff --no-index --check` for 17 new Go source files | PASS; CR-at-EOL accepted consistently with this Windows workspace |

Earlier intermediate red runs exposed outdated case/alias fixture expectations
and the genuine zero-duration first-data boundary; they are superseded by the
final full green runs, not reported as current failures. CPU-heavy local checks
finished before the parent resumed final real health/concurrency measurements.
