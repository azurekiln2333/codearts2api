# Independent panel quality review

Reviewed on 2026-10-21 against the active task's PRD, design, execution plan,
check context, request-audit contract and embedded-panel spec. Product ownership
was limited to `internal/server/panel.html` and `panel.go`; all fixes below are
in `panel.html`. No backend, credential, data, configuration or reference-project
files were changed. Product files are frozen and the parent's final Chrome run
has passed.

## Findings (fixed)

- File: `internal/server/panel.html:579`
  - Issue: A failed filter change retained the old request rows, exclusive cursor
    and `hasMore`. The new controls could then page the old cursor and append a
    different query's records. Reproduced with an existing cursor of 99, a model
    filter change and a synthetic 503: old rows remained and More was enabled.
  - Fix: Track the query independently of its cursor. A different query clears
    rows, cursor, loaded state, pagination and old diagnostic text before loading.
    Failure shows an explicit error and cannot page old data. Retrying starts at
    the new query's first page. Same-query refresh failures preserve useful rows
    and cursor; existing version/epoch guards still discard stale responses.
- File: `internal/server/panel.html:649`
  - Issue: A header refresh awaiting overview could continue into audit loading
    and show completion/error feedback after logout or a different login.
  - Fix: Capture the authentication epoch and suppress the follow-up load and
    feedback when that epoch no longer owns the operation.
- File: `internal/server/panel.html:326`
  - Issue: `escapeAttr` removed backticks. New model copy controls therefore lost
    characters from identifiers containing backticks, although the visible ID
    retained them.
  - Fix: Encode backticks as `&#96;`; quoted attributes remain escaped while the
    browser receives the exact identifier.
- File: `internal/server/panel.html:530`
  - Issue: Missing schedule-hour metadata displayed as a known unconfigured
    schedule. A newly displayed keepalive window also came from a backend field
    that is hard-coded rather than the actual effective pool setting.
  - Fix: Missing/non-array hours show unknown, explicit empty arrays show
    unconfigured. With parent agreement, remove the new keepalive-window row;
    retain the valid returned keepalive interval and all prior controls/details.

## Findings (not fixed)

- Pre-existing backend diagnostic mismatch: `cmd/server/main.go:90` sets
  `WatchInfo.keepalive_window` to the literal `10m`, while
  `cmd/server/config.go:403` parses the configurable keepalive window used by the
  pool. Correcting that producer is outside this presentation task and reviewer
  ownership. The inaccurate row is no longer displayed. Recommendation: handle
  effective WatchInfo reporting and a non-default-window assertion in a separate
  backend task. This does not block the reviewed panel once the row is removed.
- No other confirmed product defects remain. The optional mobile horizontal
  scroll hint is a usability enhancement, not an acceptance blocker.

## Verification

- Lint: PASS, `go vet ./internal/server ./cmd/server`.
- TypeCheck/build: PASS, `go build ./...` with the designated Go toolchain.
- Tests: PASS, `go test ./internal/server ./cmd/server -count=1 -timeout 120s`
  (`internal/server` 7.934s; `cmd/server` 0.076s).
- Embedded JS: PASS, parse the exact script with Node `vm.Script`; verify unique
  DOM IDs and every literal `$()` target exists.
- Focused behavioral assertions: PASS, run the actual embedded functions against
  synthetic data for failed-query reset/retry, hidden pagination after failure,
  same-query refresh preservation, exclusive-cursor append, delayed stale query
  suppression, logout during pending header refresh, backtick attribute
  preservation, absent/empty schedule hours and removed window display.
- Diff: PASS, product-file diff check; parent also confirmed final whole-worktree
  `git diff --check` passes.
- Actual-browser checks: PASS, parent confirmed the final real-Chrome suite on
  the frozen product files, including failed-filter recovery, same-query
  preservation, logout during refresh and exact backtick model copying. Parent
  owns the durable harness and screenshots; this reviewer did not run or alter
  them.
- Spec sync: parent owns the executable spec and has been informed of the query,
  authentication, copy and schedule contracts above. No reviewer spec edits.

Reviewed paths preserve allowlisted hash/base routing, all account operations,
OAuth generation/timer cleanup, hidden-view keyboard behavior, safe rendering,
honest unknown fields, current account availability versus historic failures,
and actual HTTP status versus execution outcome. `panel.go` only removes obsolete
label substitutions; its embedding, auth/API boundaries and branding remain valid.
