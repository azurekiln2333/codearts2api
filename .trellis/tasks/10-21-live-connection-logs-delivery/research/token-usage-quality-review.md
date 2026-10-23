# R15 token usage independent quality review

Reviewed on 2026-10-24 in `fix/deepseek-concurrency-20261021` by the dispatched
`trellis-check` reviewer. The review covered the integrated, initially untracked
token source/tests as well as tracked diffs. Product code is frozen after the
changes below; the reviewer did not commit, deploy, read credentials or run
production inference.

## Findings (fixed)

- File: `internal/audit/audit.go`
  - Issue: the new `usage.window` inherited `Page.Window`, which is the configured
    recent-ring capacity. The console's "已保留" label therefore claimed 512
    retained records even when only three valid, unexpired records existed.
  - Fix: count the copied ring's valid, unexpired records before applying query
    filters and pagination. `usage.window` reports that actual retained count;
    `page.window` retains its existing capacity semantics. Timestamp parsing is
    performed once per copied event.
- File: `internal/audit/token_usage_test.go`
  - Issue: the original retention assertion reinforced the capacity/current-count
    confusion and did not distinguish an underfilled ring from its capacity.
  - Fix: adjust the expiry expectation and add
    `TestUsageSummaryWindowCountsRetainedEventsBeforeFilters`. It distinguishes
    capacity 512, retained count 3 and filtered match count 1, excludes an expired
    event, and preserves the retained count for empty filters and cursor pages.
- File:
  `.trellis/tasks/10-21-live-connection-logs-delivery/research/token-browser-check.cjs`
  - Issue: the browser fixture used 512 both as capacity and retained count, so it
    could not expose the integration mismatch.
  - Fix: supply `usage.window: 5` alongside `page.window: 512` and assert both
    rendered values. The real Chrome fixture passes with these distinct values.

## Reviewed contracts

- Successful native/estimated usage is captured independently from streaming
  `include_usage`; public usage opt-in and native response serialization stay
  compatible. Usage is attached only to an `ok` audit event. Failed terminal
  delivery and cancellation retain unknown usage; post-DONE cleanup cancellation
  preserves a completed generation's usage.
- Normalization retains known zero and partial native evidence, safely derives
  only an absent total, rejects unsafe/invalid counters and provider source text,
  and clones nullable counter pointers at publication boundaries. Optional
  malformed replay metadata does not discard the underlying event.
- Summaries, page rows and completed-connection copies use bounded retained data;
  filters and retention agree, cursors/page size do not change the summary, and
  grouped counters expose coverage, truncation and numeric saturation.
- Existing authenticated request/audit routes expose honest nil/disabled
  summaries. The panel consumes server totals, clears query/auth state, ignores
  late responses, escapes group keys, distinguishes estimate/native/unknown, and
  keeps mobile table scrolling local.
- Parent synchronized the executable token spec and design with the distinction
  between configured capacity, actual retained count and filtered request count.

## Findings (not fixed)

No further defect was identified within R15 after the local corrections and
verification. The following evidence limits remain outside this feature review:

- This review does not resolve the historical R13 upstream stall attribution or
  reasoning-only-answer investigation. The parent task remains in progress and
  its overall quality gate must not be closed solely by this token feature.
- No production deployment or live provider token billing was verified. The
  displayed totals describe known successful-generation observations in the
  retained recent window, including explicitly identified estimates. Legacy and
  failed records are unknown, not backfilled zero usage or an all-time ledger.
- Final committed-source export, packaged-server replay/API checks and embedded
  packaged UI verification are parent-owned and were pending when this reviewer
  froze source. They are not claimed as independently executed here.

## Verification

The reviewer independently executed the following after the audit-window fix.
`go` and `gofmt` used
`D:/AppData/Local/Temp/codearts2api-toolchain-go1.27.3/go/bin/`.
Race checks used `CGO_ENABLED=1` and
`CC=D:/AppData/Local/Temp/codearts2api-race-compiler/mingw64/bin/gcc.exe`.

| Check | Result |
| --- | --- |
| `go test ./internal/audit -count=1` | PASS, 1.442 s |
| `go test ./internal/server -run 'TestTokenAudit\|TestChatCompletionsUsage\|TestRemoteSessionLifecycleDisconnectAfterDoneRemainsSuccessful\|TestChatAudit' -count=1` | PASS, 1.573 s; the separate `TestAudit` run below covers the existing audit suites |
| `go test -race ./internal/audit ./internal/server -run 'TestUsage\|TestTokenAudit\|TestChatCompletionsUsage\|TestRemoteSessionLifecycleDisconnectAfterDoneRemainsSuccessful\|TestChatAudit' -count=1` | PASS, audit 2.633 s, server 2.333 s |
| `go test ./internal/server -run 'TestAudit' -count=1` | PASS, 1.211 s |
| `go test -race ./internal/server -run 'TestAudit' -count=1` | PASS, 2.289 s |
| `go vet ./internal/audit ./internal/server` | PASS |
| `go build ./internal/audit ./internal/server` | PASS; Go type checking |
| `gofmt -l` over all changed Go source/tests | PASS, no output |
| `git diff --check` | PASS; only repository LF/CRLF notices |
| Parse the single embedded panel script with Node `vm.Script` | PASS |
| `node --check .trellis/tasks/10-21-live-connection-logs-delivery/research/token-browser-check.cjs` | PASS |
| `node .trellis/tasks/10-21-live-connection-logs-delivery/research/token-browser-check.cjs` | PASS after the fixture correction |

The independent real Chrome run covered native/estimated/unknown/partial/zero
rows, server summary coverage, paging, filters, failures, reset, query/logout
races, legacy/invalid/empty/disabled states, overflow/truncation/loss, escaping,
desktop/dark/mobile local scrolling, credential-expiry wording, proxy prefixes,
and absence of external requests or browser errors. Screenshots are in
`.local-test/token-ui-1792780600511/`; the reviewer opened and visually inspected
`tokens-desktop.png` and `tokens-mobile-groups.png`.

Separately, the parent reported a final post-fix full-repository gate:
`go test ./... -count=1`, `go test -race ./... -count=1`, `go vet ./...`,
`go build ./...` and `git diff --check` all PASS. Reported package timings include
normal audit 0.586 s/server 7.542 s and race audit 1.831 s/server 8.928 s. These
are parent-executed results, not additional independent reviewer runs.
