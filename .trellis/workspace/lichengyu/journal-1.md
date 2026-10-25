# Journal - lichengyu (Part 1)

> AI development session journal
> Started: 2026-10-17

---



## Session 1: Fix DeepSeek timeouts and account concurrency
<!-- trellis-session: v=2 fp=e722f7872c5481ca -->

**Date**: 2026-10-21
**Task**: Fix DeepSeek timeouts and account concurrency
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

Fixed cancellable startup and upstream idle budgets, SSE completion, fair bounded account admission, retry lifecycle permits, lease cleanup, and refresh/reload safety. Existing Pi reviewed through Herdr and fixed background probe ownership. Full tests and race, vet, build, Linux compilation, and before/after reproductions passed. Credential schema and persistent deployment paths remain compatible. User approved the local commit; no production deployment or push.

### Git Commits

| Hash | Message |
|------|---------|
| `3d87a5c` | fix: bound chat lifecycle and concurrent account admission |

### Status

[OK] **Completed**


## Session 2: Production concurrency repair and request audit
<!-- trellis-session: v=2 fp=961068a52a252ddf -->

**Date**: 2026-10-21
**Task**: Production concurrency repair and request audit
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

Implemented capability-aware bounded failover, metadata audit and truthful account diagnostics with Pi; completed independent review and local verification.

### Main Changes

- Added authenticated request audit APIs/panel, bounded nonblocking storage, safe failure causes and request-ID correlation.
- Preserved lifecycle leases, credential mounts and state compatibility; forwarded Compose env/port and build identity.

### Git Commits

| Hash | Message |
|------|---------|
| `3fefda8` | fix: improve capability failover and add request audit |

### Testing

- [OK] Full Go test/race, vet, build and diff/format checks passed; independent 18-request localhost wave restored all capacity.
- [OK] Actual Compose config, embedded JavaScript fixture, isolated executable/auth/API verification and Linux amd64 build passed.

### Status

[OK] **Completed**

### Next Steps

- Deploy intended build with existing auth/data mounts, verify reported build/effective limits, then evaluate production traffic; no push or deployment performed.


## Session 3: New API inspired admin panel redesign
<!-- trellis-session: v=2 fp=3f0b6997ddd546d6 -->

**Date**: 2026-10-21
**Task**: New API inspired admin panel redesign
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

Rebuilt the embedded admin panel as five workspace views with mobile navigation and light/dark themes. Preserved account, OAuth and audit contracts; independent review fixed failed-filter cursors, logout refresh races, exact model copying and schedule unknown states. Final synthetic localhost Chrome suite, targeted Go tests, vet/build, script checks and diff checks passed. UI task archived; no push or deployment.

### Git Commits

| Hash | Message |
|------|---------|
| `42fc19f` | feat: redesign admin panel with New API inspired workspace |

### Status

[OK] **Completed**


## Session 4: Local model discovery and live connection delivery
<!-- trellis-session: v=2 fp=773cbab6485bb791 -->

**Date**: 2026-10-22
**Task**: Local model discovery and live connection delivery
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

Completed local model discovery, bounded pull, panel cleanup and live connection diagnostics. All local acceptance criteria and independent review passed. The verified source release targets 365f482; task stays in progress for production rebuild and exact live V4.1 evidence. No deployment or push was performed.

### Main Changes

- Retained source ownership and benefit routing, atomically distinguished partial/expired absence from complete absence, preserved concurrent provider denials and made overview GET cache-only.
- Removed redundant console chrome, applied 8/12/16 px corners, added real source/exclusion pull diagnostics and bounded live phase/progress logs.

### Git Commits

| Hash | Message |
|------|---------|
| `3d10b4c` | fix: repair model discovery and add live connection diagnostics |
| `365f482` | fix: verify the actual audit statistics response |

### Testing

- [OK] Full Go test/race/vet/build, JavaScript syntax, real Chrome desktop/mobile checks and 15 release tests passed.
- [OK] Final codearts2api-source-365f4828d019.zip hashes checked, extracted and compiled; actual isolated server and every packaged deployment check passed. Original user ZIP and credential/data mounts preserved.

### Status

[OK] **Completed**

### Next Steps

- Rebuild and recreate the actual deployment with existing mounts, verify expected revision 365f4828d0194fdfb7878ffc7fffb29f88a15052, then inspect live V4.1 source and actual call. Docker runtime and server access were unavailable locally.


## Session 5: Local benefit channel repair and verified source release
<!-- trellis-session: v=2 fp=cfc76285c5bd7050 -->

**Date**: 2026-10-22
**Task**: Local benefit channel repair and verified source release
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

Fixed the missing account benefit gate and misleading HTTP errors; local code and reviewed source release pass. Production account/model evidence and rollout remain pending.

### Main Changes

- Strict HTTP 200 boolean authority; confirmed closure removes old benefit routes and skips claims/config while unknown state retains evidence.
- Distinct safe business errors and closed-channel UI, with signed source/admin/chat/browser regression coverage.

### Git Commits

| Hash | Message |
|------|---------|
| `709f63ede82f8f790a853a4550f9430a7e1b2cfc` | fix: respect account benefit channel availability |

### Testing

- [OK] Final full Go test/vet/build, affected race suites, embedded JS, real Chrome and 15 release-tool tests passed; independent review has no remaining code blocker.
- [OK] codearts2api-source-709f63ede82f.zip verified, extracted, built and started with empty auth/data; packaged GET-only checks all passed. Previous archives preserved.

### Status

[OK] **Completed**

### Next Steps

- Rebuild the actual service with unchanged mounts, inspect real per-account switch/code and V4.1 Flash source/call; no production deployment or push was performed.


## Session 6: Account model identity and bounded busy retries
<!-- trellis-session: v=2 fp=b4e82724927f6b68 -->

**Date**: 2026-10-22
**Task**: Account model identity and bounded busy retries
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

Fixed selected-account wire casing and explicit concurrency retry/health behavior; reviewed code and verified source release complete locally, production remains pending.

### Main Changes

- Resolve exact IDs and benefit route from selected account/auth; align chat, probes, aliases, negative cache and actual audit canonical names.
- Prefer eligible backups within the existing rotation budget; explicit HTTP400/429/early and late SSE concurrency stays health-neutral without late regeneration.

### Git Commits

| Hash | Message |
|------|---------|
| `b9b318ccc026426f593af09cc66eaea62cd19479` | fix: resolve account model identities and bound busy retries |

### Testing

- [OK] Full Go normal/race/vet/build, formatting/diff checks and independent Trellis review passed; old 709f63e fails the new actual wire-ID regression while repaired source passes.
- [OK] codearts2api-source-b9b318ccc026.zip passed hashes/private-file exclusions, extraction, actual compile/start and all packaged GET-only checks; all three prior archives preserved.

### Status

[OK] **Local code and source release complete; production verification pending.**

### Next Steps

- Rebuild the actual deployment using release.env and existing env/config/auth/data mounts, refresh model catalog and verify real account registration and concurrency results. No deployment or push performed.


## Session 7: Live model and remote session repair with bounded concurrency evidence
<!-- trellis-session: v=2 fp=393c024884c9b7b9 -->

**Date**: 2026-10-23
**Task**: Live model and remote session repair with bounded concurrency evidence
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

Committed and packaged verified model identity, managed remote sessions, credential-fresh catalog and bounded busy admission repairs. Real gateway trials recorded 106 completions and nine local queue rejections; production and historical health-stall attribution remain open.

### Main Changes

- Preserve exact inference IDs by account/auth/source; sign independent remote sessions with busy renewal and bounded idle cleanup; isolate busy and metadata backoff; preserve truthful completion audits.

### Git Commits

| Hash | Message |
|------|---------|
| `72e83fbed57d862d85e4d20b8b96f7a44e41019d` | fix: balance upstream sessions and repair live model discovery |

### Testing

- [OK] Independent full Go normal/race/vet/build and release-tool tests passed. Real cold/repeated/long GLM/DeepSeek and per-account 1/2/3 waves measured; initial cap3 9/18 followed by synchronized 18/18 and four reuse successes, all active counts zero.
- [OK] Source ZIP 72e83fbed57d passed hashes, private exclusion, extraction, actual compile/start and every packaged GET-only route/revision check; original credential current-round hashes and pre-release user ZIP baseline matched.

### Status

[OK] **Completed**

### Next Steps

- Rebuild production from the new source release with existing env/config/auth/data mounts, verify actual revision and runtime limits; retain unexplained historical health misses and first cap3 local queue timeouts as measurement limits. No push or deployment performed.


## Session 8: Reopened stability verification with historical causes open
<!-- trellis-session: v=2 fp=a608c42a37ea6f83 -->

**Date**: 2026-10-23
**Task**: Reopened stability verification with historical causes open
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

Bounded current regression passed: 474 actual calls, 473 useful answers and one retained reasoning-only failure. Whole incident remains in progress; historical stall and empty-answer causes are unresolved.

### Main Changes

- Recorded bounded HTTP/runtime and structural evidence without changing product source 72e83fb; no new performance fix or ZIP is claimed.
- Retained whole-task quality false and AC13 historical closure open; added bounded_regression_passed separately.

### Git Commits

| Hash | Message |
|------|---------|
| `835181b4617f5bfaf1c28055940da5b1d3a5735b` | chore(task): record bounded live stability regression and open causes |

### Testing

- [OK] Ordinary build: 108/108 mixed concurrent replies, two cold replies, 66.307-second useful stream and 18/18 post-long replies; audits correct and active zero.
- [OK] Independent Go/control health: 8640/8640; complete structural streams: 216 with zero observed whitelist-shape conversion mismatch or drop.
- [OK] Original credentials and both archives matched current-round hashes; owned gateways and controls stopped. Explicit nine-file secret/diff checks passed.

### Status

**In progress.** Bounded current regression passed. Historical stall and the
earlier reasoning-only answer remain unresolved; AC13 and the whole-incident
quality gate stay open. The task was not archived.

### Next Steps

- Capture new discriminating evidence for the historical preparation/health timeout or reasoning-only answer; retain existing failures and avoid claiming historical cause from later success.
- Verify actual production revision and account behavior when deployment evidence is available; no push or remote deployment performed.


## Session 9: Eligibility 503 classification and verified source delivery
<!-- trellis-session: v=2 fp=890920e13ef2d2f1 -->

**Date**: 2026-10-23
**Task**: Eligibility 503 classification and verified source delivery
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

R14 locally repaired and source-packaged; production remains old709f63e. Whole task remains in progress because historical AC13 causes and deployment are still open.

### Main Changes

- Shared account/auth cached eligibility now distinguishes model404 from unhealthy503 and filters unsupported public fallback entries without model substitution.

### Git Commits

| Hash | Message |
|------|---------|
| `4622f3ef2f4b02ed311ca5cffb617e148c1bb4eb` | fix: classify authoritative model exclusions consistently |
| `86b228a` | chore(task): record verified eligibility repair source release |

### Testing

- [OK] Twelve actual-HTTP subcases, independent review, full Go normal/race/vet/build and diff checks passed.
- [OK] New4622f3e source ZIP passed member/private-value checks, extraction, actual compilation/start and all seven packaged GET-only checks. Original credentials and existing archives preserved.

### Status

**In progress.** R14 is locally repaired, reviewed and source-packaged. Historical
R13 causes and production deployment remain open; the task was not archived.

### Next Steps

- Update production source before rebuild/recreate with existing mounts; verify actual deployed revision. Historical R13 causes remain open; no push or deployment performed.


## Session 10: Token usage audit and console source delivery
<!-- trellis-session: v=2 fp=f7f31d232e014cd5 -->

**Date**: 2026-10-24
**Task**: Token usage audit and console source delivery
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

R15 locally implemented, independently reviewed and source-packaged. Production rollout and historical R13 causes remain open; parent task remains in progress.

### Main Changes

- Persist safe nullable generation usage independently of stream include_usage and display per-request source/counts plus filtered retained account/model summaries.

### Git Commits

| Hash | Message |
|------|---------|
| `4f0ceff` | feat: persist generation token usage and display audit summaries |
| `c6e8593` | chore(task): record verified token usage source release |

### Testing

- [OK] Full Go normal/race/vet/build, independent review, full and token Chrome suites passed; final source ZIP extracted/compiled/started with seeded replay/API and actual embedded desktop/mobile checks.
- [OK] All 131 member hashes and private credential exclusion checks passed; original credentials/config and prior archives preserved; owned servers stopped.

### Status

**In progress.** R15 is locally implemented, reviewed and source-packaged.
Historical R13 causes and production deployment remain open; the parent task
was not archived and its overall quality gate remains false.

### Next Steps

- Update production source and rebuild with existing mounts; verify deployed revision. Historical R13 causes remain open; no push or production deployment performed.


## Session 11: OAuth primary action and Compose env source delivery
<!-- trellis-session: v=2 fp=c35c6a475d7c929c -->

**Date**: 2026-10-24
**Task**: OAuth primary action and Compose env source delivery
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

R16/R17 locally implemented, independently reviewed and source-packaged. Historical R13 and production rollout remain open; parent task stays in progress.

### Main Changes

- Unify OAuth start/submit, handle import done immediately, serialize poll/submit, align callback port; load optional application settings through service env_file with one deployment port.

### Git Commits

| Hash | Message |
|------|---------|
| `7a9bc5b` | fix: simplify OAuth authorization and Compose env delivery |
| `b7c6f7e` | chore(task): record verified OAuth and Compose source release |

### Testing

- [OK] Full Go normal/race/vet/build, final HTML build and independent final OAuth Chrome passed; official synthetic Compose matrix and 15 release tests passed.
- [OK] Verified the 131-member final source ZIP by hashes/private exclusions, extraction, compile/start, seven routes, usage replay/embedded tokens and extracted OAuth Chrome; original credentials and prior archives preserved.

### Status

**In progress.** R16/R17 are locally delivered. Historical R13 causes and actual
production rollout remain open; the parent task was not archived and its overall
quality gate remains false.

### Next Steps

- Update deployment source and rebuild using existing .env/config/auth/data mounts, then verify actual revision. No real OAuth, push or deployment performed; historical R13 causes remain open.


## Session 12: CNB reference console and verified source delivery
<!-- trellis-session: v=2 fp=a43ea722040c1e18 -->

**Date**: 2026-10-25
**Task**: CNB reference console and verified source delivery
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

Rebuilt login, connecting stage and all console views from local CNB reference; verified committed source ZIP, preserved credentials and kept historical R13 open.

### Main Changes

- Split login, real connecting/auth timeout recovery, reference shell and six-page responsive dark styling.

### Git Commits

| Hash | Message |
|------|---------|
| `6623d00818abe770d94344515e787475dfeee535` | feat: redesign console with CNB reference layout and login states |

### Testing

- [OK] Independent review; final four Chrome suites; Go server tests/vet/build;15 release-tool tests; actual extracted compile/routes/replay/embedded UI and all4 browser suites.

### Status

[OK] **Completed**

### Next Steps

- User rebuild/recreate production with existing auth/data/config mounts; production rollout and historical R13 remain unverified.


## Session 13: R19 image deployment and GitHub CNB releases
<!-- trellis-session: v=2 fp=72693c2e1005a18f -->

**Date**: 2026-10-26
**Task**: R19 image deployment and GitHub CNB releases
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

Four existing Pi delivered image Compose, scoped auto-updates, gated GitHub/CNB release pipelines and three complete READMEs. Actual140-member export verified;85 Python tests, Go normal/race/vet/build, two Linux architectures, actionlint and Compose matrix pass. No remote publication/deployment; parent R13 open.

### Git Commits

| Hash | Message |
|------|---------|
| `32f33db` | feat: add image deployment and GitHub CNB release pipelines |

### Status

[OK] **Completed**
