# Implementation plan

- [x] Resolve the user's local reference and inspect the existing panel/API contracts.
- [x] Record user authorization to create the task and continue implementation.
- [x] Complete reference research; converge PRD/design and curate context manifests.
- [x] Activate the task and dispatch one Trellis implementer with explicit ownership of panel.html/panel.go.
- [x] Replace the shell/styles; organize overview/accounts/requests/models/settings with responsive navigation and functional controls.
- [x] Preserve existing API/OAuth/audit/account semantics and remove misleading served-label rewrites if needed.
- [x] Run isolated actual-browser functional checks and inspect desktop/mobile screenshots, including dark and empty/error states.
- [x] Dispatch a separate Trellis checker, fix confirmed defects and run appropriate Go/embed/script/build/diff checks.
- [x] Update the panel's executable spec, commit only task-owned files and archive/record the session.

## Commands and ownership

Parent owns task docs, browser fixtures/screenshots, final spec and git bookkeeping. Implementer owns `internal/server/panel.html` and the minimal `panel.go` label correction; no other product writes without coordination. Checker may fix confirmed local defects after implementer handoff. Preserve all unrelated pre-existing untracked Trellis/platform files.

Go tool: `D:/AppData/Local/Temp/codearts2api-toolchain-go1.27.3/go/bin/go.exe`. Browser: installed Chrome with Playwright under an isolated temporary directory, not repository dependencies. Use synthetic localhost APIs only; no real auth/config/persistent files or production operations.

Primary checks: browser fixture in this task's research directory, `go test ./internal/server ./cmd/server -count=1 -timeout 120s`, `go build ./...`, `git diff --check`; broaden only for actual cross-layer changes or a confirmed concern. The existing archive's VM-only fixture may need adaptation to actual browser DOM APIs; do not distort product code solely to satisfy its limited mock DOM.
