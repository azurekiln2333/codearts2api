# Generation token usage delivery

Date: 2026-10-24. Scope: R15 only; historical R13 causes remain open.

## Observed gap and implementation

Chat responses already included upstream usage or the existing text-length
estimate. Audit events discarded that metadata, so the console had no generation
counts to display. Added safe nullable input/output/total counters and an
allowlisted upstream/estimated source to successful audit events. Streams record
usage independently of the client's include_usage opt-in. Public response
serialization and its opt-in remain compatible.

Record/replay/page/completed-connection boundaries own copied metadata; malformed
optional legacy usage cannot discard a valid event. Failed/canceled/rejected and
older events remain unknown. Native partial or zero counters stay partial or
known zero; no invented zero or historical backfill is introduced.

Existing requests and audit APIs expose filtered retained-window summaries,
including component coverage, unknown/estimated counts and bounded account/model
groups. Pagination does not change totals. The Requests view renders those
server totals and per-request counts/source, with query/logout race guards,
escaped keys and local mobile scrolling. Credential expiry now says 凭证剩余.

Independent review identified a capacity/count mismatch: existing page.window
must remain capacity for compatibility, while usage.window must count actual
valid nonexpired retained events before query filters. Regression covers
capacity512/retained3/matched1, expiry and empty later pages. UI and fixtures now
distinguish the retained count from matching requests.

## Integrated validation

Final Go 1.27.3 checks after the window correction:

- Full `go test ./... -count=1`: PASS (audit0.586s, server7.542s).
- Full `go test -race ./... -count=1`: PASS (audit1.831s, server8.928s).
- Full `go vet ./...`, `go build ./...`, `git diff --check`: PASS.
- Actual HTTP regressions cover native/estimated/zero/partial/invalid usage,
  public stream compatibility, delivery failure/cancellation, remote DONE cleanup,
  replay, admin APIs, filtering/paging and disabled/nil audit.
- The existing full browser suite passed all prior six-view/model/audit/
  connection/OAuth/mobile behavior. Current screenshots were preserved at
  `.local-test/token-full-ui-639283771313735341/`; historical tracked screenshots
  were restored without replacement.
- Final token Chrome suite: PASS with distinct usage.window5/page.window512,
  screenshots `.local-test/token-ui-1792780600511/`. Desktop/mobile groups were
  visually reviewed. Covers native/estimate/unknown/partial/zero rows, filtered
  server summaries/paging, empty/legacy/disabled/error/overflow/truncation/loss,
  query/auth races, escaping, proxy prefixes and desktop/dark/mobile scrolling.

The independent final review and token-specific browser evidence are recorded in
`token-usage-quality-review.md`. Exact committed-source export, extraction,
compilation/start and actual embedded-page verification are recorded separately
after packaging in `token-usage-source-release.md`.

## Practical limits

This is the configured recent audit window, not all-time usage or provider
billing. Older records contain no counts. Estimates reuse existing behavior.
No live credential inference, production traffic, push or deployment is part of
R15. Original auth/config/data and existing archives remain outside the release.
Historical stability cause closure and actual production rollout stay open;
the parent task remains in progress and its whole-task quality flag stays false.
