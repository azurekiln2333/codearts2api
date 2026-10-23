# Repair model discovery, simplify the console and add live connection logs

## Goal

Make the deployed gateway's admin console useful for operating real models and diagnosing slow requests: discover models seen in the official CodeArts client, refresh the catalog on demand, explain account/model exclusions, provide live connection diagnostics, and remove redundant presentation.

## Background and confirmed evidence

- The user approved task creation and continued implementation, and identified the previously supplied production site as their console. On 2026-10-21 public read-only checks initially found the old panel and 404 audit routes. The user subsequently rebuilt the image and reports that the redesigned panel is now present; the earlier public check is historical, not evidence of its current state.
- `internal/server/chat.go:523` generates the reported `model_not_available` 404 after cached candidate filtering and model-capability rejection. The response does not identify the actual account/source or reason for exclusion.
- `internal/server/availability.go:30` caches negative account/model conclusions for 30 minutes. Discovery and negative availability are separate caches.
- `internal/upstream/models.go:292` merges three model sources sequentially; individual failures are logged but not surfaced to the admin. Research confirms source failures can erase known catalog/benefit routes, removed benefit entries can resurrect, and returned results can differ from retained account data (`models.go:126-153`, `303-328`). Exact production source responses for DeepSeek V4.1 Flash are not available yet.
- `internal/server/handler.go:96` uses a one-hour global catalog cache and five-minute failure backoff. The panel's refresh fetches overview, which is distinct from forcing upstream discovery.
- `internal/server/admin.go:27` exposes hard-coded service/region labels. The panel displays these in its header, repeats branding/build details in a sidebar footer, and adds a redundant overview shortcut card (`panel.html:226`, `245`, `266`).
- `internal/server/handler.go:436-446` retains the original model list when filtering excludes every item, so the list can advertise models that chat routing excludes.

## Requirements

- R1: Correct discovery, source parsing/partial-result handling and per-account benefit routing wherever repository tests or current upstream evidence demonstrates a gap. The user's reported target is `deepseek-v4.1-flash`; no guessed alias, entitlement or substituted model is acceptable.
- R2: Provide a real authenticated catalog refresh operation and a model-directory button. It must actually refetch upstream sources, expose per-account/source results and freshness, bound time/concurrency and prevent overlapping refresh jobs. A browser refresh or static fallback is not a successful pull.
- R3: Make discovery versus callability clear. Show sanitized reasons for model exclusion, failed sources and cached negative conclusions. Successful discovery must not falsely mark a model callable; refreshing valid metadata must offer a bounded way to recover from stale negative capability without a 30-minute wait or container restart.
- R4: Remove the entire lower-left branding/build block, header service-region caption and overview shortcut card. Remove associated unused CSS, DOM updates and duplicate handlers. Build metadata stays available through the existing authenticated diagnostic API and unobtrusively in runtime settings.
- R5: Reduce rounded corners using a restrained MD3-inspired scale: controls/navigation 8 px, cards 12 px and major shells/dialogs at most 16 px. Avoid oversized pills, including on mobile, while preserving functional focus, layout and theme behavior.
- R6: Add a dedicated connection-log view displaying active request phase/age, actual upstream progress and bounded completed phase histories. Distinguish account waiting, credential waiting, upstream response headers, first SSE data and streaming/aggregation; do not label local chat-ID generation as an upstream session request or infer that a healthy long stream is stuck.
- R7: Produce a complete source release and reproducible read-only verification instructions for the actual deployed routes/UI. Do not overwrite existing credentials, configuration, data or the user-owned ZIP. Do not claim remote deployment without access and verification.
- R8: Accept New API model IDs regardless of case and resolve to the selected account's exact discovered upstream ID. Avoid cross-account casing contamination or a second global rewrite; keep requested versus actually sent names inspectable. Investigate repeated upstream concurrency-limit attempts separately from genuine model-registration rejection.

## Acceptance criteria

- [x] AC1 / R1: Synthetic source fixtures include a dynamically returned V4.1 Flash model. Exact ID and per-account route metadata survive merging and refresh; partial source failure cannot silently erase previously confirmed usable metadata.
- [x] AC2 / R2: Clicking the catalog pull button performs upstream discovery once, reports loading and completion/partial failure, and updates models/freshness without server restart. Concurrent clicks/requests share bounded work; authentication, cancellation and deadlines are tested.
- [x] AC3 / R3: A cached rejection is inspectable by account, reason and expiry. A refresh/recheck can recover stale negative state without globally clearing unrelated capability history. Actual provider denial remains an honest failure.
- [x] AC4 / R4-R5: Deleted content is absent after login, reload, refresh and navigation. Desktop/mobile screenshots show the reduced radius scale, no leftover blank card or JavaScript errors, and working account/OAuth/audit flows.
- [x] AC5 / R6: Stalled upstream fixtures remain visible while running and leave accurate completed traces after resource cleanup. Live tracking, phase points and polling are bounded; logs contain no message bodies, headers, keys or credentials.
- [x] AC6 / R7: Relevant Go tests, race tests for backend changes, vet, build, browser checks and diff checks pass. Release includes Compose, Dockerfile and example configuration, excludes live auth/data/secrets, and identifies the actual source revision.
- [x] AC7 / deployed benefit follow-up: A strictly validated per-account channel switch gates legacy benefit calls. Explicit closure removes old contributions/seed routing, skips claims/config and appears as confirmed closed; unknown/failing state retains prior evidence. HTTP-success business failures remain distinct from real HTTP errors. Actual normal-route chat, channel transitions, source retention, account isolation and safe diagnostics are verified with localhost fixtures; production account results remain pending.
- [x] AC8 / model casing follow-up: Lowercase/mixed-case inputs, including GLM-5.3 fixtures, use the selected account's exact ID on actual chat/probe HTTP requests. Different accounts/captured auths cannot rewrite each other's IDs; aliases, default/auto, benefit flags, negative capability and audit canonical names remain consistent. Unknown names are not guessed or silently substituted. Provider concurrency rejection remains distinct, bounded and accurately counted.

## Scope and compatibility

Retain the Go-embedded panel, API-key authentication, existing account/OAuth actions, audit API and credential/data mounts. Keep Go 1.22 source compatibility. Do not rewrite the frontend framework, add decorative dashboard content, fake model availability, bypass account authorization, run another production 18-request load test, or alter unrelated scheduling policy.

## Deferred operational evidence

Server/deployment access is not available. Remote deployment verification and the exact official-client response for V4.1 Flash require operational evidence; local implementation and deterministic checks do not depend on that access. The final scope was presented and the user's subsequent 2026-10-22 direction authorized execution. No further user product choices are required for the listed scope.

## Live-credential follow-up authorization and acceptance

The user supplied four local credentials and explicitly authorized actual model
calls followed by bounded progressive concurrency testing. This supersedes the
prior lack of account evidence. Testing uses an isolated localhost server and
credential copies, not the deployed New API gateway. Existing claims policy and
credential originals must be preserved; no credential/raw-response content is
committed. Source release archives remain immutable.

- R9: Parse actual inference IDs separately from provider display names, using
  the real agent model_parameters.model_id and builtin model_id evidence. Keep
  account/auth/source ownership, truthful negative-cache resets and safe explicit
  display-name compatibility; never lowercase every model or guess another version.
- [x] AC9: Reproduce the observed GLM/DeepSeek display-ID failure on actual HTTP
  fixtures, correct parsing/resolution, and demonstrate real lowercase GLM and
  DeepSeek completion on a supplied valid account. Test streams/repeated calls
  and progressively bounded concurrency while observing health and cleanup.
  Clearly separate refresh-expired credentials from model capability failures.

- R10: Reproduce successful-then-busy requests with supplied credentials and
  prevent independent requests from repeatedly hammering a recently rejected
  account. Preserve health/capability neutrality and distinguish provider
  saturation from local slot waiting, authentication and model registration.
- [x] AC10: Account-scoped transient busy admission is shared across handlers,
  bounded and cancellable, wakes on recovery, selects a free backup immediately,
  and cannot transfer old-login state onto a replacement account. Actual repeated
  and concurrent calls report content, statuses, attempts, health and cleanup;
  upstream capacity/release behavior remains an explicit measurement boundary.

- R11: Implement the official current user-session lifecycle for inference and
  availability probes: matching signed user-session-id, initial busy notification,
  periodic busy heartbeat for long generations, and bounded idle notification on
  success, provider failure, cancellation and timeout. Closing HTTP alone is
  insufficient. Keep cleanup scoped to the session this attempt owns.
- [x] AC11: Actual-HTTP fixtures reproduce three retained remote sessions without
  idle and demonstrate repeated recovery with the lifecycle. Verify long streams,
  every failure/close path, bounded heartbeat/cleanup, unique attempt ownership,
  signed matching headers and no heartbeat loop after cleanup. Repeat real
  gateway GLM/DeepSeek sequential/streamed and 18-request bounded concurrency.

- R12: Cold startup must not replace the verified GLM/DeepSeek inference IDs with
  their display casing. A negative result belongs to the exact wire identity that
  failed; discovery of a corrected identity must not inherit an old wrong-wire
  rejection. Bounded background and explicit catalog jobs must obtain valid
  account credentials before fetching metadata; cached admin GETs stay read-only.
- [x] AC12: Actual HTTP cold-start and dynamic non-seed identity regressions cover
  a request before catalog warm-up, automatic recovery after exact-ID correction,
  a late old-wire denial, same-wire denial retention, and account/auth isolation.
  Expired credential metadata refresh shares the existing bounded refresh and
  respects cancellation, disabled/replaced accounts and safe publication. Verify
  a freshly rebuilt real gateway without requiring a manual catalog warm-up.

AC9–AC12 are supported by the independent integrated quality report and final
real-gateway verification report dated 2026-10-23. Acceptance includes truthful
bounded rejection and cleanup, not a guarantee that every load trial succeeds:
the first per-account-3 wave completed 9/18 and rejected nine locally at the
10-second account-wait limit; a synchronized control rerun completed 18/18 and
four subsequent calls. Historical one-second local health timeouts remain
unattributed. Production deployment, provider capacity beyond these finite
trials, and real benefit inference on an enabled channel remain unverified.

## Reopened stability acceptance, 2026-10-23

The user rejected closing this work with an unexplained per-account-3 failure.
The prior finite success and source archive remain historical deliveries, not
completion of this newly explicit stability gate. No new task or authorization
prompt is needed.

- R13: Diagnose the observed pre-header/preparation stalls and local queue
  failures using aligned independent-process controls and captured runtime /
  network-phase evidence. A later successful wave cannot explain an earlier
  failure. Keep the effective account wait at 10 seconds and per-account cap at
  3 while comparing; do not hide the issue by changing timeouts or reducing load.
- [ ] AC13: Capture discriminating timing/runtime evidence for any reproduced
  stall, implement the evidenced correction if it belongs to the gateway, and
  verify at least six consecutive 18-request waves (GLM/DeepSeek, useful content,
  DONE, correct audits, no retained activity). Repeat cold startup and a useful
  generation beyond 30 seconds. Health sampling must include an independent Go
  client and minimal HTTP control in the same time window. Report scheduling
  delay separately from handler/network duration. Any failed acceptance wave
  remains failed until investigated and the corrected sequence is rerun; no
  unconditional stability or production-deployment claim follows from finite
  tests.

### Current measured regression milestone

The unchanged ordinary product build passed six consecutive mixed-model waves
(108/108 useful replies), two cold nonstream calls, a 66.307-second useful long
stream, and eighteen subsequent streamed replies. Each correlated audit was
successful and every case returned active tracking to zero. Separate combined
diagnostics verified 216 complete streams with matching parser/output lengths.

The earlier diagnostic post-long wave still failed its useful-answer gate for
one reasoning-only DeepSeek reply. It was not reproduced in the final focused
108-request capture. The historical preparation/health stall and this empty
answer remain causally unresolved. Keep AC13's whole-incident closure unchecked,
while recording `bounded_regression_passed=true` separately. The task stays
in progress; current finite regression success is not a claim that the old
incident's cause was explained or that production has been deployed.

## Reported no-eligible-account 503 follow-up

- R14: Explain the reported `no_healthy_account` using the actual eligibility
  branches. A healthy account excluded by authoritative current model knowledge
  must not be misreported as an unhealthy account pool. Preserve distinct model,
  pool-health, concurrent-capacity, local-wait and timeout outcomes. Do not infer
  the live account state from the generic error text or old local snapshots.
- [x] AC14: Actual chat-route regressions demonstrate healthy current complete
  catalogs lacking the requested model produce model-unavailable, while empty /
  disabled / cooling / unusable-auth account pools retain no-healthy-account.
  Partial, stale and replaced-login catalog evidence remains nonauthoritative.
  Existing bounded busy/wait/timeout behavior and audit classifications hold.
  Verify the repair independently and preserve credentials and private files.

R14 local acceptance passed on 2026-10-23: the zero-attempt 503 was reproduced
before the fix, twelve actual-HTTP subcases and independent boundary review
passed afterward, as did full Go normal/race/vet/build checks. Current GET-only
production evidence matches the catalog-exclusion defect and identifies old
revision `709f63e`; production rollout and the separate R13 historical causes
remain open. See `research/production-eligibility-findings.md` and
`research/eligibility-quality-review.md`.
