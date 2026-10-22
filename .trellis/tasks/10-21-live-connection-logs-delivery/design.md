# Design

## Change boundary

The functional gaps live in model discovery/cache ownership (`upstream/models.go`, `server/handler.go`, `server/availability.go`), the embedded panel, and chat diagnostics. Address these owners directly. Preserve account leases, admission caps, retry budgets, OAuth/token persistence, shared refresh ownership, audit classification, authentication and persisted credential files. Keep one coordinated task because the model and connection APIs share a single embedded console and release acceptance; implementation ownership and dependencies below provide independent review boundaries.

## A. Account model discovery and refresh

### Follow-up: account-specific model casing and repeated busy responses

The user reports lowercase New API IDs, mixed-case CodeArts IDs and a log whose
requested `glm-5.3` already shows canonical `GLM-5.3`, alongside real provider
registration and concurrent-session rejections. The canonical label alone is not
evidence that the chosen account received its own correct ID or has entitlement.
The current global known-model index merges accounts and is reapplied by the
chat body builder; this allows another account's case to overwrite a selected
account's exact registration name.

Resolve case-insensitive identity from the captured account/auth catalog at the
outbound boundary, preserving the exact source ID and account-specific benefit
route. Preserve established static aliases, but do not guess new aliases, infer
IDs from display labels, change model versions or borrow an unrelated account's
dynamic registration. With no own evidence, preserve unknown input under the
existing seed/static-alias compatibility rules. Once selected, the resolved ID
must reach actual chat/probe JSON without another global rewrite. Keep cache
keys, scoped stale-negative recovery and requested/actually-sent audit metadata
consistent; a new login cannot inherit a removed login's dynamic mapping.

Expected product owners are `upstream/models.go` and client chat-body construction,
server chat/probe emission and model identity checks only where required. Parent
owns task/spec/docs/release. Changes to concurrent-session retry policy require
repository evidence first; do not increase capacity, retry counts or pretend a
provider refusal was solved by case conversion. Preserve lifecycle admission,
lease cleanup, timeout/cancellation, claim and upstream rights boundaries.

The independent `research/busy-retry-findings.md` confirms repeated same-account
queue retries precede rotation: three selected accounts can each receive three
attempts. It also demonstrates a classification mismatch: preflight SSE TM is
wrapped as HTTP 429 and currently causes whole-account soft cooldown, whereas
HTTP 400 TM is neutral. Correct explicit concurrency errors consistently for
HTTP 400/429 and early/late SSE, preserving real authentication rejection and
ordinary rate/TPM policies. Late-stream errors still end the existing response
without failover or regeneration.

Before a fixed same-account concurrency retry, prefer an untried eligible
candidate only if the generic rotation budget permits the next account. Use the
existing atomic lease acquisition to decide actual capacity; candidate membership
does not mean a slot is immediately free. Keep single-account/no-backup retries,
startup/cumulative slot-wait budgets and generic rotation cap. The minimal three
account all-busy sequence becomes A/B/CCC (five attempts) rather than AAA/BBB/CCC
(nine); this is not a new universal retry ceiling. Keep real attempt/tried audit
counts. The mapping implementation owner also owns these adjacent chat/handler
changes, avoiding concurrent edits to the same request loop.

Use real localhost HTTP tests with two accounts requiring different casing,
rotation, normal/benefit requests, probes, default/auto and refreshed credentials.
Assert upstream model ID, actual audit canonical name, alias-consistent rejection
cache and no discovery network work on the chat path. Review the independent
busy-retry research before altering that separate policy.
Concurrency tests must cover HTTP400, HTTP429 and first-SSE TM with a healthy
backup, all-busy finite attempts, one account, exhausted rotation budget, ordinary
rate/TPM behavior and late-SSE no-regeneration/neutral-health behavior.

### Follow-up: account benefit channel and ambiguous errors, 2026-10-22

The user's deployed panel reports benefit source `failed`, count/retained zero
and a generic HTTP error without an HTTP status. A localhost reproduction shows
HTTP-success/nonzero benefit business codes take exactly this path. Current
upstream protocol evidence also reports the old benefit channel can be disabled;
the existing documented `GET /v1/benefit-gateway-config` exposes `enabled`.

Before benefit claim/config, request that signed Snap endpoint with
`Agent-Type: PromptCenter` within the same benefit source budget. Only HTTP 200
with a valid explicit boolean false authoritatively disables this source. Other
2xx statuses such as accepted/partial responses do not authorize removal. Publish source
status `disabled`, remove its prior contribution, suppress seed benefit routing
for that auth, skip claim/config, and count the confirmed channel state as a
successful discovery result. Explicit true preserves the existing benefit flow.
Failure or malformed/unknown switch state remains failed/unknown with retained
prior evidence; it must not infer channel retirement or revoke other sources.
No static date cutoff, guessed aliases, blind retry without the benefit header,
or inference from business code 4000 alone is allowed.

Keep HTTP failures and HTTP-success business failures distinct (`kind:business`).
Allowlist observed code 4000 with a fixed diagnostic label; continue omitting raw
provider message/body and arbitrary code values. Surface closed-channel and
skipped-claim state honestly in the panel. Existing exact ID discovery and account
negative reset identity/revision rules remain in force.

Change owners: implementation worker owns upstream const/discovery/models and
their tests plus necessary server integration fixtures; panel worker owns only
panel.html and task browser fixture. Parent owns specs/research/release and final
integration; independent checker owns final review. Cover disabled/reenabled
transitions, prior failure retention, no removed route resurrection, malformed and
failed switches, actual signed requests and actual normal-route V4.1 fixture chat.

Add context-aware discovery while preserving `FetchModels(*auth.Auth)` as a compatibility wrapper. Carry the same deadline through agent-list/detail, builtin, benefit claim and benefit config calls; use synchronized credential snapshots. The new explicit operation must not enter the chat path. Keep source provenance so a successful empty response is different from failure and stale retention is distinguishable from new discovery.

Use per-source snapshots for agent, builtin and benefit. A failed source retains its last successful contribution, annotated stale; a successful response replaces only that source's contribution, including intentional removal through a valid empty result. A malformed/missing expected envelope is failure, not an authoritative empty response. Benefit route flags follow the current merged account/source evidence. Do not infer that an agent entry has superseded a benefit route while benefit is failing, do not resurrect models removed by a successful benefit response, and return the same effective merged list that is committed to the account catalog. Preserve exact model IDs. Compatibility parsing needs fixture or primary-source evidence; never translate V4.1 Flash to an older model.

Selection consumes the atomic `AccountModelKnowledgeForAuth` membership/completeness read for the captured auth object. Only a complete current catalog can exclude an absent model; partial, failed, unknown or expired discovery leaves absence unknown while present entries retain preference. Legacy whole-catalog replacements remain complete. Existing actual provider denials still exclude the account independently. A cold partial catalog must not prevent the real chat path from attempting an otherwise eligible account, and this does not invent a benefit route.

Discovery status is bounded metadata: source name, status, last attempt/success timestamps, retry/backoff expiry, retained count, safe error kind, HTTP status and allowlisted provider code. Include configured auto-claim policy and its optional attempt/result separately; config discovery success is not claim success. Never silently enable claims. It does not include arbitrary upstream text, URLs with queries, credentials or bodies.

Retained per-source snapshots cap collections at 256 entries; IDs/names at 192 bytes and descriptions at 512 bytes. Validate read completeness and expected collection presence before accepting results; reject over-limit/incomplete collections as failed and retain the old snapshot rather than treating truncation as authoritative removal. Preserve exact IDs, so overlong identifiers are rejected rather than silently shortened. Prune snapshots for accounts no longer configured. Publication and negative invalidation require the same account/auth object identity captured at job start; an old login cannot publish over a same-UID replacement. Source deadlines may record timeout, while caller/service cancellation is separately classified and does not manufacture a provider denial.

### Authenticated API contract

`GET /admin/api/models` is a nonblocking cached snapshot; it performs no upstream I/O or chat probes. Its shape is:

```json
{
  "models": [{"id":"deepseek-v4.1-flash","origin":"discovered","catalog_accounts":1,"benefit_accounts":1,"blocked_accounts":0}],
  "accounts": [{"account":"account-name","eligible":true,"excluded_reason":null,"last_success_at":null,"sources":[{"source":"benefit","status":"failed","last_attempt_at":null,"last_success_at":null,"retry_at":null,"count":0,"retained":0,"error":{"kind":"timeout"}}],"claim":{"enabled":false,"attempted":false}}],
  "exclusions": [{"account":"account-name","model":"example-model","reason":"not_registered","recorded_at":"RFC3339","expires_at":"RFC3339"}],
  "refresh": {"id":"","state":"idle","started_at":null,"finished_at":null,"retry_at":null,"total":0,"completed":0,"failed":0,"skipped":0,"negative_reset_count":0},
  "truncated": {"accounts":0,"models":0,"exclusions":0}
}
```

Source statuses are `ok`, `empty`, `disabled`, `failed`, `backoff`, `canceled`, `unknown`. Error kinds are a fixed safe enum (`timeout`, `canceled`, `auth`, `http`, `business`, `parse`, `empty`, `backoff`, `unknown`), with optional numeric HTTP status and bounded provider code. Counts and timestamps must describe actual attempts. Model origin distinguishes dynamically discovered data from static fallbacks. Catalog membership is not called verified usability. Disabled benefit sources represent an explicit per-account false switch and count as authoritative successful metadata; claim `skipped_reason:channel_disabled` explains why a configured automatic claim was not attempted.

`POST /admin/api/models/refresh` accepts an empty JSON object, starts one bounded server-owned discovery job and returns HTTP 202 with the current `refresh` object. While running, repeated callers receive that same job, not new background work. GET polls the job snapshot. States are `idle`, `running`, `completed`, `partial`, `failed`. A minimum 15-second interval between job starts prevents repeated upstream fanout; early requests return the existing job and a truthful retry hint. The job has a 60-second total deadline, at most two account workers and no more than 512 account snapshots. Response lists are capped at 512 accounts, 2,048 model summaries and 512 exclusion summaries, with truncation counts; per-source response bodies retain existing bounded read limits. Deadline exhaustion reports partial/failed and skipped accounts, not success. Background startup discovery shares serialization with explicit discovery so requests cannot create overlapping account pulls. The caller's disconnect only cancels that caller's wait, not another caller's shared job.

An explicit refresh bypasses relevant discovery backoff once, inside that coalesced job. On successful authoritative rediscovery, remove only pre-job negative entries for exact returned account/model pairs whose source governing the effective route was actually refreshed. A fresh agent listing plus a retained stale benefit route is insufficient; retained entries from failed sources are not a fresh authorization assertion. Check the entry timestamp/revision and captured account/auth identity before deletion so a new denial from a concurrent chat or replacement login is preserved. Clearing means `unknown` and permits the next real call; it does not call `markUsable` and does not touch other account/model negatives. Expose the reset count and keep current account cooldown/disable intact.

The model list must allow an empty filtered result; never keep an all-unusable original merely because filtering produced zero rows. Preserve the OpenAI `/v1/models` envelope and supported aliases. Explain 404 using the diagnostic catalog view, rather than inventing provider entitlements or concealing genuine denials.

## B. Console cleanup and catalog operation

Remove the sidebar footer (all branding/subtitle/build lines), header service/region caption, overview shortcut card and duplicate authorization button. Remove their CSS rules, DOM writes and listeners at the same time. Keep one reachable account authorization control and all original account/OAuth/audit operations. Build metadata remains in authenticated runtime diagnostics/settings; do not move deleted marketing content elsewhere.

Use a compact MD3-inspired shape scale: 8 px controls/navigation, 12 px cards, at most 16 px workspace/dialog/login containers; small badges/icons may use 4 px. Preserve a fully round status dot and progress bar where geometry has meaning. Desktop and mobile must use the same scale. Official token reference: Material Web `tokens/v0_192/_md-sys-shape.scss` (8/12/16 px small/medium/large). Do not introduce Material dependencies or larger decorative radii.

Constrain the root shell to the dynamic viewport and let the workspace own vertical scrolling. Source tables own horizontal scrolling. Mobile verification checks both root dimensions and that the last source table can be reached without scrolling the entire application offscreen.

The Models view adds `重新拉取` and a last-attempt/job-state line, consumes the cached models API, shows origin and catalog/benefit/blocked account counts and exposes safe source/exclusion details. Reuse the existing API wrapper, BASE prefix, login epoch, escaping and notification functions. Disable/merge duplicate local clicks. Poll the catalog job only while it is running, authenticated, visible and on the Models view; use one in-flight GET and stop on navigation/logout. Distinguish refresh success, retained stale data, partial failure and complete failure. A refresh failure cannot clear a previously visible catalog or claim success.

## C. Live connection diagnostics

Use a small neutral context observer package for fixed phase/progress callbacks. The audit sink owns at most 512 active records and at most 32 phase points per request. Reuse the bounded recent completed audit window with additive optional trace fields; old JSON remains readable. Existing event sanitization and 16-KiB encoded event ceiling still apply. Tracking overflow/truncation counters are explicit. Audit disabled/nil yields a truthful disabled response and no allocated stranded tracker.

Phases: `admission`, `read_body`, `prepare`, `wait_account`, `validate_account`, `wait_refresh`, `prepare_upstream`, `wait_headers`, `read_error`, `wait_first_sse`, `retry_delay`, `stream`, `aggregate`, `write_response`, `cleanup`, `finished`. A stage point is `{phase,at_ms,attempt,account?}` with offsets from request start. Track actual last upstream read progress (`n > 0`) separately, and set first SSE data only when preflight accepts an actual data event. Do not call it first token. Do not attach request observers to shared background token-refresh work. No disk access, network I/O, per-read goroutines, unbounded lists or callbacks under pool/account/stream locks.

Authenticated `GET /admin/api/connections` returns `{active,recent,disabled,capacity,overflow,trace_truncated}`. Active entries contain request ID, start time, requested/canonical model, nullable account, stream flag, attempt, phase, phase age, total age, nullable last upstream progress age and bounded timeline. Recent entries reuse actual audit event fields plus optional trace; no fabricated history for older records. Finish diagnostics after body/lease/admission cleanup using the existing final outcome/status/cause classifier. Late observer calls after finish are ignored. Healthy streams retain caller cancellation and idle/write deadlines and are not killed due to age alone.

The new `#connections` view follows existing navigation semantics and polls roughly every two seconds only while active, visible and authenticated. Enforce a single request, epoch/version guards, visible error/disabled/empty/loss states and immediate stop on logout/navigation.

## D. Release and actual deployment evidence

Create a separate source release after the work commit. Include Go module/source, Dockerfile, `.dockerignore`, Compose, `.env.example`, example config, README, license and relevant scripts. Exclude actual config/env/auth/data, Git/Trellis/platform metadata and existing ZIPs. Identify the source commit and build args without showing noisy unknown-build UI chrome. A read-only verifier checks health, current UI markers and authenticated audit/model/connection APIs, failing clearly on missing routes and unknown/mismatched expected revisions. Preserve the user-owned ZIP and all mounted credentials/data.

The user's successful rebuild supersedes the historical old-panel finding. It does not by itself validate new work. Remote redeployment requires an actual server entry point; do not claim it happened based on local tests or public health alone.

## Ownership and dependencies

1. Discovery implementer: `upstream/models.go`, new discovery helpers/tests, new `server/model_catalog.go` and its route tests, targeted `availability.go`/model-list fixes; coordinate any `handler.go` edits. Does not edit `panel.html`, audit, chat or telemetry.
2. Connection implementer: neutral observer, audit extension/tests, `chat.go`, `lifecycle.go`, `upstream/client.go`/`stream_body.go`, caller-only pool refresh hooks and connection API tests. Does not edit model discovery or panel.
3. Panel implementer: `panel.html` only, against the exact API contracts above. Cleanup/radius work is independently reviewable; catalog and connection rendering depend on integrated backend routes.
4. Parent integrates route registration, maintains task/spec/research and browser verification. Release-script work is dispatched after a slot is available; an independent checker reviews all integrated work. No worker reverts another's edits. If a public contract must change, parent updates this design and informs every dependent owner before implementation proceeds.

## Validation and rollback

Use localhost source fixtures for partial/malformed/empty responses, benefits, cancellation, refresh coalescing, source backoff bypass, concurrent chat rejection, exact new model IDs, health responsiveness and model filtering. Add lifecycle active/final trace assertions to meaningful stall/refresh/long-stream/disconnect tests. Run full Go tests/race/vet/build for backend changes and real-browser desktop/mobile/light/dark checks using synthetic metadata. Review actual screenshots and JavaScript errors. Test release inclusion/exclusion and verifier failure modes. Rollback code/image independently; the data/auth format remains additive and credentials require no migration.
