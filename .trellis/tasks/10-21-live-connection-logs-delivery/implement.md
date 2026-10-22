# Implementation plan

## Required phase gate

- [x] User approved the existing task and clarified that the target was production.
- [x] User reports a rebuild revealed the redesigned UI; retain the older check only as historical evidence.
- [x] Capture the subsequent explicit UI-removal, restrained MD3, true catalog refresh and V4.1 Flash requirements.
- [x] Inspect source owners, existing regression tests and cache/route boundaries.
- [x] Complete research and curated implementation/check manifests and validate them.
- [x] Present the final planning summary.
- [x] The user's subsequent 2026-10-22 response directs immediate execution of the presented scope; proceed without another process confirmation.

## Execution order

1. Start the approved task. Inject its PRD/design/implementation manifest into each Trellis worker, with child-side loading as fallback.
2. Dispatch model-backend and live-connection backend owners with nonoverlapping responsibilities from `design.md`. Dispatch the panel owner against the fixed API contracts. Model discovery fixes and tests precede declaring the model catalog operational; UI cleanup can be reviewed independently.
3. Integrate API route registration and check that background discovery and explicit refresh share bounded work. Resolve any shared-file changes sequentially. Preserve the existing API/auth/cancel/lease semantics.
4. Extend the current task's copy of the previous real-browser fixture with removal/radius assertions, the Models pull flow and connection visibility/polling controls. Do not modify archived verification scripts.
5. Dispatch release-script work when capacity is available. Include source manifest, build identity and a read-only deployment verifier. Preserve `codearts2api.zip`.
6. Run the appropriate Go checks and browser/release checks. Ask an independent Trellis checker to review actual diffs/new files, specs, boundedness, API/UI consistency and privacy. Fix evidenced local failures, then rerun only affected checks.
7. Update relevant specs, commit only owned changes after staged diff/secret review and produce the release artifact containing the actual work commit. Record test results and the operational state in the journal.
8. If server access becomes available, preserve mounts, rebuild/recreate remotely and verify the expected revision and features. Otherwise keep remote verification pending and give exact operator commands; never archive the task as production-delivered while that acceptance remains unverified.

## Validation commands and evidence

- Use the installed temporary Go toolchain when `go` is not on PATH; discover its compiler before race tests. Commands are `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...`.
- Meaningful catalog tests: independently failing sources; all failure versus authoritative empty; historical benefit removal; exact new-model ID/benefit header; stale negative recovery; concurrent fresh denial retention; overlapping refresh clicks; cancellation/deadline; authentication; `/healthz` during a stalled refresh; zero eligible models.
- Meaningful connection tests: admission rejection, waiting for slot/refresh/response headers/first data, long healthy stream, blocked downstream, disconnect, active cleanup, bounded trace/storage/overflow and no sensitive content.
- Run task `research/browser-check.cjs` through installed Node/Chrome/Playwright using synthetic localhost APIs. Check every old view/action, new catalog and connections states, safe strings/clipboard/base paths, login epochs, mobile overflow/focus and light/dark screenshots.
- Verify extracted inline JavaScript syntax. Run `git diff --check` and review untracked owned source separately.
- Verify release members and absence of `.env`, real config/auth/data, existing ZIPs and platform files. Verify deployment-check failures on old UI, 404 routes, unauthorized calls and missing/wrong build.

## Risk and rollback points

- Do not overwrite old catalog snapshots on failed or malformed sources. Do not clear fresh concurrent model denials or unrelated cooldown/disable state.
- Do not add discovery to the chat critical path or hold audit callbacks under locks. No per-read goroutines or unbounded active/poll lists.
- Update each deleted panel node's renderer/listener together to avoid breaking login refresh with null DOM access.
- Keep restored completed audit history compatible and preserve actual HTTP commitment versus execution outcome.
- Roll back the code/image only; credential and state mounts remain usable. Existing ZIP and unrelated untracked files are user-owned and excluded from staging.

## Delivery state, 2026-10-22

Execution steps 1–7 are complete. Product work is committed in `3d10b4c`, with the
real audit-statistics verifier corrected in `365f482`. The final source release
was extracted, compiled and started with isolated empty auth/data; its packaged
GET-only verifier passed every route and actual build identity. All local PRD
criteria pass; see `research/verification.md` for exact evidence and artifact hash.
Step 8 remains an operational follow-up: no server entry point or Docker runtime
is available, and the exact live official V4.1 Flash response remains unobserved.
Keep the task in progress for that evidence rather than claiming deployment.
