# Implementation and independent review — 2026-10-21

## Delivered

- Capability rejections no longer exhaust the generic rotation limit before eligible accounts are tried. A finite tried set, iteration bound and shared startup deadline still bound the request; generic provider failures retain their retry cap.
- The existing lifecycle admission, cancellation, owned account leases and timeout contracts are preserved. An independent real-HTTP 18-request mixed-model wave covers partial cooling, silent upstreams, queued rejection, cancellation, health responsiveness and full capacity reuse.
- Added bounded metadata-only request audit with nonblocking enqueue after request cleanup, one disk writer, restart restoration, retention, rotation and observable overflow/storage failures. Authenticated APIs and the embedded panel expose request IDs, actual transport status, execution outcome, safe causes, timings, account/model filters and sequence pagination.
- Account schedulability and historical failure metadata are separate. Successful recovery clears stale current failures once, preserving history, active cooldown and manual disable. Existing state JSON remains readable; auth JSON is untouched.
- Overview exposes effective runtime limits and explicit known/unknown build identity. Compose forwards all 31 application environment keys plus build identity; host/container/listen ports share CA2A_PORT. Existing credential/data/config mounts are preserved.

## Review and root-cause lessons

Pi at verified Herdr pane w1C:p4 implemented the assigned integration and independently reviewed the parent-owned audit sink, tests, configuration, Compose and HTTP wave. Its final read-only review found no reproducible confirmed defects and passed the targeted nonblocking storage/overflow regression. Parent reviewed integration and fixed independently reproduced audit boundary defects: startup expiry while queued, pre-canceled admission, downstream timeout classification, and arbitrary suffixes in provider error codes. Targeted regressions cover each boundary.

The original model false negative came from charging capability rejections against a generic retry budget. Error badges also conflated present schedulability with accumulated historical failures. Audit metadata needs actual response commitment and context cause; HTTP 200 alone does not mean a stream succeeded. These contracts are captured in `.trellis/spec/backend/request-lifecycle.md` and `request-audit.md`.

## Quality gate: passed

- Full `go test ./... -count=1 -timeout 120s`.
- Full `go test -race ./... -count=1 -timeout 120s` with the temporary Windows GCC toolchain.
- `go vet ./...`, `go build ./...`, changed-source gofmt and regular `git diff --check`.
- Meaningful audit limits/replay/ownership/overflow/storage tests, authenticated actual-handler audit checks, pool recovery compatibility and capability rotation regressions.
- Independent localhost HTTP wave: 18 requests, four accounts, three cooling, concurrency five; explicit bounded failures, canceled capacity released, health responsive, all 33 lifecycle tickets reusable and later requests successful.
- Official standalone Compose config verification: isolated fixture environment; all 31 env keys, default/21508 ports, spaced mounts, false/zero values, build args, shell precedence and missing/empty key rejection.
- Embedded panel JavaScript executed in a Node DOM fixture: real element IDs, build/config/concurrency/history fields, escaping, filtering and honest response status. Rerun passed after final labels changed. This is script verification, not a visual browser review.
- Actual freshly built executable on localhost with isolated empty auth/data/config: health, route authentication, build injection, effective limits, served panel, request-ID/audit correlation, absent metadata and fixture-secret exclusion. Linux amd64 build passed. Final rerun passed; temporary process and files were cleaned.

## Production boundary

The earlier authorized production wave was 18 requests across 15 model IDs plus three repeats: four successes, four model-specific 404s and ten diagnostic client aborts at 125 seconds without headers. Health remained responsive. Three of four accounts initially cooled; all explicitly allowed concurrency five. Legacy response templates suggest a stale or mixed deployment, but the actual running build is unknown. A 125-second non-stream request without headers alone does not prove a server deadlock, and concurrency five is not a proven provider limit. Sign-in credits do not establish entitlement to a particular model.

This task completes local implementation, independent review, regression validation and the credential-preserving deployment runbook. No new production traffic, administrator mutation, credential reset, push or deployment was performed during implementation. Production success must be verified after deploying the intended build and checking its reported identity and effective configuration. That separate operational verification is pending; no production-fix claim is made.

## Residual constraints

- The recent API window defaults to 512 records; retained older files are a bounded local archive. New records may evict the oldest page entries.
- Audit writer pruning has bounded per-file filesystem overhead; queue loss/storage failure counters make persistence pressure visible without blocking chats.
- Queue and wait duration currently report the same cumulative slot wait. Header/idle timeout remains a terminal request result under the existing contract.
- Shared New API service credentials do not identify individual downstream users; request bodies, generated content and keys are deliberately absent from audit.
