# Local delivery verification, 2026-10-22

## Result and scope

Local implementation and all six deterministic acceptance criteria passed. Work
commits are `3d10b4cf90005c9699f50993be87cc4066e031fd` (product repair) and
`365f4828d0194fdfb7878ffc7fffb29f88a15052` (actual audit-statistics verifier).
The task remains open for operational verification. No production deployment,
production load test or live V4.1 Flash completion is claimed.

## Acceptance evidence

| Criterion | Result | Evidence |
|---|---|---|
| AC1: source-aware model and benefit discovery | Pass | Local source/admin/chat fixtures preserve exact dynamic IDs, retain failed sources, honor empty/removal and isolate account/login ownership |
| AC2: actual bounded model pull | Pass | API and Chrome tests cover authenticated POST, coalescing, source/whole-job deadlines, worker limits, cancellation and partial/error UI |
| AC3: honest negative capability and recovery | Pass | Scoped expiry/reason UI, timestamp plus revision protection, concurrent denial retention, source-governed reset and all-excluded list tests |
| AC4: removed UI and restrained shapes | Pass | Real Chrome desktop/mobile/light/dark screenshots; footer/caption/shortcut absent; 8/12/16 px geometry, internal scrolling and prior actions preserved |
| AC5: bounded live connection logs | Pass | Actual stalled/refresh/stream/disconnect lifecycle tests; 512 active/32 points, first accepted SSE and actual read progress, cleanup, replay and metadata-only boundaries |
| AC6: checks and complete verifiable source | Pass | Full Go checks, Chrome suite, 15 release tests, staged whitespace checks and final extracted-source build/start/verifier |

## Independent review corrections

- Partial or expired catalog absence stays unknown. Atomic membership/completeness
  reads prevent cross-generation exclusion. The real cold-partial route regression
  first returned 503 with zero chat attempts, then passed with 200 and one actual
  fixture chat attempt, without inventing a benefit header.
- Fresh-route identity uses the same case-insensitive comparison as merge/cache,
  preserving the original inference ID.
- Admin overview is cache-only: cold and cached GETs cannot start discovery,
  claims or real inference probes.
- Failed claims render correctly when JSON omits `succeeded:false`.
- The mobile shell stays within the viewport. Workspace/source-table scrolling
  remains usable; the document itself does not scroll vertically or horizontally.
- Final source extraction found the initial verifier fixture had confused request
  history with audit statistics. The actual `/admin/api/audit` response is
  `audit.enabled`; the verifier and its malformed/disabled regressions were fixed
  in the follow-up commit before regenerating the final release.

The independent Trellis checker reviewed the integrated product and the final
four-file verifier/spec follow-up. Both reviews ended with no blocking finding.

## Executed checks

- `go test ./... -count=1`: pass.
- `go test -race ./... -count=1`: pass, including audit/pool/server/upstream.
- `go vet ./...` and `go build ./...`: pass.
- Embedded UTF-8 JavaScript syntax: pass.
- `python -B scripts/test_release_tools.py`: 15 tests pass after the verifier fix.
- `research/browser-check.cjs` in real Chrome with synthetic localhost APIs: pass.
  Includes six views, real pull POST/duplicate suppression, retained partial data,
  source/exclusion details, connection phase expansion across polls, legacy traces,
  off-view/hidden/logout and late-result isolation, exact copying, escaping,
  account/OAuth flows, request filters/cursors and proxy base paths.
- Mobile root dimensions and scroll reachability: pass; workspace and table
  scroll offsets advance while root scroll stays zero. Screenshots were captured
  as viewports to match the fixed application shell and inspected visually.
- `git diff --check` and staged whitespace checks: pass. Reviewed owned text
  files have no long service-key/private-key patterns; no generated Python caches
  were included. Real auth/config/data were not staged.

Go checks used temporary Go 1.27.3 and MinGW for race. The source retains its
Go 1.22 contract; an independent run under Go 1.22 was not performed. Docker is
unavailable here, so no Docker image build/runtime result is claimed.

## Final release

- Artifact: `codearts2api-source-365f4828d019.zip`, 104 members.
- Source identity: `365f4828d0194fdfb7878ffc7fffb29f88a15052`.
- SHA256: `eeeeeed3037704008fda0ff015f8667ecca1275a72d68bc35b9994feb040ff7e`.
- Includes committed product sources, embedded panel, Compose, Dockerfile,
  example configuration and release/deployment scripts, with generated
  `release.env` and per-member hashes in `release.json`.
- Excludes real `.env`, `config.json`, auth/data, Git/Trellis/platform files and
  old ZIPs. Existing deployment mounts require no migration.

Executed `research/verify-source-release.py` against that final ZIP. It verified
all 103 hashed members, extracted into a temporary directory, compiled the actual
server, started it with isolated empty auth/data and a synthetic service key, then
ran the packaged verifier. Health, cleaned console, exact running build, requests,
audit statistics, models and connections all passed. No chat or refresh was sent.

The original `codearts2api.zip` remains unchanged at SHA256
`7934b2e7827f5f4a685b9f2e367385752b4eb9aec063eea75b563f87c601a84c`.
The earlier generated release candidate was rejected and removed after its
verifier-schema failure; only the verified new source release is delivered.

## Operational follow-up

Update the original deployment directory with the product source while retaining
`.env`, `config.json`, `auths/`, `data/` and the same mounted paths. Import
`release.env`, build and recreate the service as documented in README, then run
the read-only verifier against the actual service expecting the release SHA.
After deployment, use Models → 重新拉取 to inspect real per-account source and
exclusion results for the exact V4.1 Flash ID, and Connections/Requests for a real
call. Successful local fixtures establish implementation behavior, not the
production account's provider entitlement or actual source response.
