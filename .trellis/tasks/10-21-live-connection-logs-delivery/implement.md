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

## Benefit-source follow-up

- [x] Reproduce HTTP-success business failure with no HTTP code matching the user's panel.
- [x] Find documented live benefit-channel switch and current upstream protocol evidence.
- [x] Gate legacy claim/config by a strictly validated channel state; publish authoritative disabled state and retain on uncertain state.
- [x] Distinguish business and HTTP errors without exposing raw provider data.
- [x] Show closed-channel/skipped-claim diagnostics and verify actual normal-route model chat.
- [x] Independent review, appropriate Go/browser/release checks, specs, committed follow-up and verified source artifact.

The follow-up is committed as `709f63ede82f8f790a853a4550f9430a7e1b2cfc`.
`codearts2api-source-709f63ede82f.zip` passed checksum verification, extraction,
actual server compilation/start and every packaged GET-only deployment check.
See `research/benefit-followup-verification.md`. Operational step 8 and real
account/model evidence remain pending; this local release does not imply rollout.

## Model-casing and busy-retry follow-up

- [x] Trace selected-account model resolution, actual payload, aliases/probes, negative cache and audit names.
- [x] Implement account/auth-scoped case resolution with exact outbound IDs and meaningful real HTTP regressions.
- [x] Confirm the reported 9-attempt busy behavior from real retry loops and correct only evidenced scheduling gaps.
- [x] Complete independent review, relevant normal/race/vet/build checks, spec sync, committed source release and extracted-source verification.

The model identity/concurrency follow-up is committed in `b9b318ccc026426f593af09cc66eaea62cd19479`.
`codearts2api-source-b9b318ccc026.zip` passed full member hash/exclusion checks,
extraction, actual server compile/start and all packaged GET-only route/revision
checks. See `research/model-retry-verification.md`. Production deployment and
real account/model entitlement remain pending; keep operational step 8 open.

## Real-credential identity and repeated-busy follow-up

- [x] Reproduce display-ID provider rejection and capture explicit source IDs.
- [x] Demonstrate exact GLM/DeepSeek content, then repair source identity owner.
- [x] Add full HTTP discovery/chat/probe/negative-reset regressions.
- [x] Test repaired gateway sequential streams; reproduce later TM refusals.
- [x] Compare independent IDs, minimal body and full HTTP EOF consumption.
- [x] Add shared transient busy admission and account-replacement-safe retries.
- [x] Repeat actual gateway calls and progressive bounded concurrency with health,
  actual content/DONE, audit attempts and zero retained local requests.
- [x] Independent full-scope review, full tests/race/vet/build and spec updates.
- [x] Commit only owned files, produce and verify the actual source artifact.

Do not represent provider busy as model absence or promise that local admission
can remove an external account quota. Preserve supplied originals and refreshed
test copies. Production deployment remains unverified.

- [x] Inspect official 26.10.0 source and confirm user-session-id/busy/idle contract.
- [x] Direct real protocol comparison: ten consecutive managed DeepSeek replies,
  each initial busy and final idle HTTP200, on the same supplied account.
- [x] Integrate official lifecycle at shared upstream HTTP/body ownership boundary.
- [x] Verify cancellation/error/long-stream cleanup using actual local HTTP fixtures.
- [x] Repeat real full-gateway success, progressive concurrency and release checks.

## Cold-start boundary discovered during real gateway verification

- [x] Validate account credential freshness in bounded catalog workers, retaining
  cached GET semantics and exact account/auth publication guards.
- [x] Register source-captured normal exact IDs in known cold-start seeds and
  prevent wrong-wire negatives from poisoning corrected dynamic identities.
- [x] Actual HTTP regressions for cold startup, non-seed correction/late rejection,
  same-wire authority, cancellation, disabled/replaced accounts and shared refresh.
- [x] Rebuild and restart the real isolated gateway; verify without manual pull,
  then run progressive 1/3/18 requests, actual content/DONE, audit and cleanup.

## Final real-account measurement state, 2026-10-23

The rebuilt full gateway was tested cold, sequentially, streamed, for a
content-bearing 38.907-second generation, and with 18 requests at effective
per-account limits 1, 2 and 3. The first limit-3 wave had nine local account-wait
timeouts (zero upstream attempts), while a synchronized-control repeat passed
18/18 followed by 4/4 reuse. All trials returned active tracking to zero, and
all actual completions had successful correlated audits. Three valid test
accounts ended with zero errors and no cooling; the fourth older test copy was
disabled for refresh expiry. Do not infer that a newer original login is expired.

The latest 55-second loaded comparison had 220/220 successful health checks on
each of four independent gateway/control connection modes. Older one-second
timeouts remain unexplained; the later success does not erase those results.
See `research/live-gateway-session-verification.md` and
`research/health-stall-investigation.md`. The owned gateway and all control
processes were stopped. Final source-release verification follows the work
commit; operational deployment step 8 stays open.

Work commit `72e83fbed57d862d85e4d20b8b96f7a44e41019d` was exported as
`codearts2api-source-72e83fbed57d.zip` (SHA256
`2825880698989ece2683fb3d6a02df4638839dcd2af04ad1c30e91d40ff42a65`).
The actual archive passed member hashes/private-file and credential-value
exclusion checks, extraction, compilation, startup and all packaged read-only
health/console/build/requests/audit/models/connections checks. Current-round
original credential hashes matched, and the current user ZIP matched the
pre-release baseline after generation. No production deployment or push occurred.


## Reopened stability gate R13

- [x] Instrument a private diagnostic build without changing product files or exposing diagnostics publicly.
- [x] Run aligned independent Go health/control sampling and capture HTTP phase/runtime scheduling evidence under cap3 / wait10s.
- [ ] Investigate every reproduced failure, make only evidenced source corrections, and verify the affected HTTP boundary.
- [x] Pass six consecutive 18-request waves plus cold-start and >30s useful long-stream checks with truthful audit and complete cleanup.
- [ ] Independently review source changes, run appropriate checks, capture cause/spec, and commit/package the actual tested source.

The previous release remains historical and is not proof of this reopened gate. Do not raise timeouts or lower concurrency to satisfy these checks.

The ordinary build passed the finite workload; HTTP/runtime and structural SSE
evidence were independently reviewed. No new product defect was demonstrated,
so no new product patch or source ZIP is asserted. The existing product source
and verified archive remain unchanged. The earlier one-answer strict-content
failure remains failed and unassigned. Historical cause closure and actual
production verification stay open; keep the task active and its whole-task
quality flag false while recording the bounded regression pass separately.

## R14: no-eligible-account error classification

- [x] Trace the exact 503 fallback and record model-filter versus health evidence.
- [x] Reproduce a healthy complete-catalog exclusion through the chat route;
  fix only the demonstrated misclassification using existing eligibility rules.
- [x] Cover unavailable model, unhealthy/empty pools and nonauthoritative catalog
  boundaries, with actual HTTP status/error and correlated audit assertions.
- [x] Independent full-scope lifecycle/model/audit review, full normal/race tests,
  vet/build and diff checks; update specs and commit only owned files.

Current GET-only production snapshots identify three eligible accounts, zero
catalog support for the failing distinct `glm-5.3-flash` static entry, and old
running revision `709f63e`. They are adjacent observations rather than an atomic
capture of the request. The repair shares the existing cached predicate across
selection, terminal classification and list filtering. Twelve new actual-HTTP
subcases, independent full-scope affected-contract review, full Go normal/race,
vet/build and whitespace checks pass. Commit/release verification is recorded
after the work commit. This does not close historical R13 causes or change
provider entitlement, concurrency limits, refresh policy or model mapping.
