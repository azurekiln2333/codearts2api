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

## E. Live-evidenced inference identity correction

Actual builtin records return model_id glm-5.3 with model_name GLM-5.3,
model_id deepseek-v4.1-flash with model_name DeepSeek-V4.1-Flash, and
model_id glm-5.2-sft-harmony with model_name GLM-5.2-ArkTS-SPARK. Agent records
contain the same inference IDs in model_parameters.model_id and model_alias,
while model_name is the display name. GLM-5.2 and Qwen3-VL-235B remain uppercase
in builtin model_id, so globally lowercasing outbound strings would be wrong.
The actual account accepted glm-5.3 and returned content, while the prior actual
handler sent GLM-5.3 and received provider not_registered. This establishes the
previously unproven parser/source defect, independently from account entitlement.

Smallest behavior gap: model-name display merging must never replace explicit
inference identity. Its owner is upstream model parsing/catalog indexing, not a
hard-coded GLM alias in the chat handler. Prefer agent parameter model_id when
present; preserve the historical name-before-opaque-top-level-id fallback when
no explicit inference parameter exists. Retain display Name separately. Within
a normal-route same-fold collision, the builtin model_id owns sending; benefit
source remains authoritative when that route applies. Keep explicit source-backed
display-name compatibility only where its target is unambiguous; exact IDs win
collisions. Do not invent version aliases or add a second route attempt.

Candidate membership, negative cache, source refresh reset and actual audit IDs
must follow resolved inference identity. A successful agent refresh cannot clear
a denial if sending still depends on a retained failed builtin source. Preserve
same-auth retention, changed-auth isolation, bounded source parsing and no metadata
network on chat. No new scheduler policy is introduced during this identity fix.

Implementation owner: internal/upstream/{model_discovery,models}.go and focused
upstream tests. Independent HTTP regression owner: new server test file only.
Parent owns task/spec/reports, live credential operations and release verification.
If server implementation changes are needed, coordinate through the parent.

## F. Live-evidenced cross-request upstream saturation

The repaired identity gateway reproduced 8 completed and 4 TM concurrency
refusals in 12 sequential streams at max_concurrent=1. Direct normal-route calls
fully drained to actual HTTP EOF also accepted three successive requests and
then returned TM.00001041. Minimal bodies and independent conversation IDs did
not remove the refusal. These observations establish saturation outside local
lease occupancy, not its provider-side expiry or a required undocumented API.

Add transient account/auth-scoped busy admission independent of health cooldown,
errors and model capability. Record explicit concurrency refusals before retry
selection, prefer an immediately usable backup, and share bounded increasing
backoff across independent handlers. Wake bounded admission waiters at busy
expiry; preserve existing startup, cumulative wait, cancellation and ticket caps.
Report known provider saturation honestly rather than inventing model 404/health
503 or silently waiting forever. Ordinary rate/TPM penalties remain unchanged.
Retried acquisition must use the exact acquired account object and validated auth;
old-login results must not affect a replacement. Success resets transient busy
state. No hard-coded claim of a universal provider quota or release duration.

Owner: one coordinated implementation agent owns pool busy admission plus server
integration and meaningful regressions. Upstream identity code is stable and
belongs to its prior owner. Parent owns real calls, task/spec evidence and release.

## G. Confirmed official remote-session lifecycle

Current official 26.10.0 client source sends user-session-id with inference and
signed PUT /snap-manager/v1/chat-session/heartbeat?status=busy|idle at the same
inference origin, body {}, no benefit header. Busy starts immediately and repeats
every 30 seconds; idle covers completion, error and cancellation. Existing
Chat-Id/Session-Id are not substitutes for user-session-id. Parent reproduced the
protocol against a supplied account: ten immediate sequential DeepSeek calls
with matching busy/inference/idle all returned content and HTTP200 notifications.

Own this at the upstream inference HTTP/body boundary so actual chat and probes
share cleanup. Each attempt owns a unique tracked session; preserve conversation
and prompt-cache IDs separately. Start bounded busy work before sending inference,
sign matching user-session-id, and keep at most one heartbeat worker per owned
active attempt. Stop/join it before bounded detached-context idle cleanup, even
after caller cancellation. Close transport before releasing remote session/local
lease. Preflight/request failures must also finish the session. No inference
regeneration after content; cleanup cannot become unbounded or overwrite the
actual inference outcome. Test endpoint injection must remain entirely local.

Owner: upstream client/session lifecycle implementation and focused upstream plus
actual server HTTP regressions, coordinated with the pool busy owner. Parent owns
real traffic and final full-scope validation.

## H. Credential validation before bounded model discovery

The parent observed cold startup metadata use expired STS and leave all sources
in HTTP failure/backoff, while later chat validation successfully refreshed the
same account. Source eligibility is account health/identity, not credential
freshness. Bounded catalog workers therefore reuse `Pool.ValidateContext` before
any metadata request; shared refresh remains cancellable for that job waiter and
bounded independently for provider-rotated token persistence. Check exact
account/auth eligibility again after validation and keep `WithCurrentAuth` on
publication. Initial disabled accounts remain skipped. Credential validation
failure counts failed account work, without fabricating errors or attempts for
model sources that were never contacted. Source failure backoff itself is owned
by the captured auth and a private digest of its exact signing tuple, so a refresh
already completed by another caller also isolates the new generation. Keep one
generation and three retry timestamps per account, replace obsolete generations,
and ignore late old-generation updates. Same-generation five-minute failure
backoff and explicit force semantics remain. Existing cached admin GETs,
job/worker bounds, API shape, claim policy and chat's cached-only selection remain.

Admin catalog negative projection uses the same exact wire/auth predicate as
routing. Copy candidate negative entries under availability lock, then resolve
current wire IDs after releasing it; never take a catalog lock while holding
availability. Old-login/wrong-wire entries cannot contribute exclusions or blocked
counts, while literal unknown IDs retain current-login provider authority.

Owner: catalog worker and snapshot implementation in `server/model_catalog.go`,
upstream source-backoff state, dedicated auth/rotation/backoff regressions and
the bounded credential research note. Parent
owns integrated real-account validation, specs, task acceptance and release.

## H. Live-discovered cold-start identity and expired catalog credentials

The rebuilt gateway's first test exposed another concrete boundary: startup
metadata used expired STS credentials, all sources returned HTTP400, and the job
backed off for five minutes. Chat's later ValidateContext refreshed credentials,
but the catalog remained empty. A display-case request then went out unchanged,
was denied, and its folded negative key blocked the correct lowercase wire ID.
An explicit catalog refresh after chat refreshed credentials retrieved the real
IDs and cleared all six old negatives; four nonstreamed gateway calls then
completed with actual content. This is evidence of the boundary, not a substitute
for repairing it or final cold-start validation.

Keep chat metadata reads cached and make the bounded discovery job refresh
expired/expiring credentials through the existing shared cancellable validator.
Preserve account/auth publication guards and disabled/replaced-account policy.
Metadata refresh failures must not be invented model-capability failures; cached
admin GETs must remain side-effect-free. Add the two source-captured exact GLM
and DeepSeek IDs to normal known seeds. Preserve uppercase real IDs and avoid
inventing benefit routing. Availability must retain the actual rejected wire
identity, so a source-authorized exact-ID correction does not inherit wrong-wire
negatives, including a denial arriving late from the old attempt. Matching-wire
denials remain authoritative and discovery is never proof of callability.

Owners: the pool/busy owner handles model_catalog.go and separate auth regressions;
the upstream identity owner handles known seeds, identity-aware availability and
its callers; the independent reviewer owns cold-start dynamic HTTP regressions.
Parent owns actual calls and final cold-start/progressive concurrency evidence.

## I. Eligibility outcome classification

The final chat fallback currently consults negative availability independently
from candidate construction. A healthy account excluded by a complete current
model catalog can therefore disappear from candidates without establishing a
negative availability entry, and the request falls through to the generic
no-healthy-account error. Reuse the existing selected-account/auth model knowledge
and eligibility rules when classifying the exhausted candidates. Authoritative
model exclusion from otherwise healthy accounts belongs to the existing
model-unavailable status/error/audit path. Empty, disabled, cooling or unusable
account pools remain pool-unavailable; concurrency waiting/busy and request
deadlines retain their dedicated paths. No synchronous discovery, new cache,
guessed alias, relaxed entitlement or credential mutation is introduced.

Actual HTTP regressions must preserve unknown/partial/stale/replaced-auth evidence
as nonauthoritative and assert status, error code and audit outcome. The parent
owns deployment evidence and sanitized reports; the implementer owns the narrow
server eligibility helper and route regressions, followed by independent review.

## J. Generation token usage across audit and console

The public response already exposes native usage or a text-length estimate, but
the request event drops it. Preserve the existing response serializer and stream
include_usage opt-in while observing a successfully completed generation for
audit independently. Never collect prompts/completions into audit metadata.

Audit owns `TokenUsage` with nullable int64 `prompt_tokens`, `completion_tokens`,
`total_tokens` and allowlisted `source` (`upstream` or `estimated`). Unknown is
absent usage/null counters; known zero is zero. Decode finite integral nonnegative
JSON-safe numbers only. Derive an absent total only when both components are
known and their sum is safe. Preserve incomplete native evidence rather than
labeling missing components exact or silently replacing it with estimates.
Sanitize/clone this metadata at record, snapshot and replay boundaries.

`Page.Usage` serializes as `usage` on requests. `UsageSummary` contains `scope`
(`recent_window`), `window`, `disabled`, `from`, `to`, totals, `by_account` and
`by_model`. Each group uses `key` and the same totals: `requests`, `with_usage`,
`upstream_requests`, `estimated_requests`, `unknown_requests`, `prompt_tokens`,
`completion_tokens`, `total_tokens`, `prompt_known`, `completion_known`,
`total_known`, `overflowed`. Known counters aggregate without fabricating unknown
zeros; known-count fields explain partial coverage. Group by final canonical
model (fallback requested model) and final selected account (empty key unassigned).
At most 100 account and 100 model groups are returned deterministically, with
`accounts_truncated` / `models_truncated` counts. Overall totals still cover all
matches. Saturate sums to JSON-safe integer bounds with visible overflow flag.

Keep existing top-level `page.window` as configured capacity. `usage.window`
counts actual valid, nonexpired retained events before query filters;
`usage.requests` counts filter matches. Capacity 512 with three retained events
and one match must serialize 512/3/1 respectively, including empty later pages.

Summarize the same immutable copied ring and retention/filter rules as request
paging, before applying `before`/`limit`. Thus loading older pages does not
multiply totals. No new all-time accumulator, disk scan, lock-held I/O, unbounded
maps, separate endpoint or additional poller is introduced. Add `usage` to the
existing request and audit API responses; nil/disabled sinks return honest empty
disabled summaries. Partial/failed generations remain unknown in this scope;
these counters are successful-generation observations, not provider billing.

The Requests view adds per-event input/output/total and source; its summary and
account/model tables consume the server's `usage` object, not loaded-page sums.
Label the filtered recent-window range and coverage. Legacy records/old servers,
disabled audit, invalid values, losses and grouping truncation stay visible.
Clear summaries with query/auth changes, preserve same-query error feedback,
escape all keys and keep tables locally scrollable. Rename credential expiry
wording to distinguish it from generation tokens. Follow existing MD3 geometry.

Ownership: audit implementer owns `internal/audit` schema/sanitization/replay/
summary/tests; chat implementer owns server chat capture/admin envelope/tests;
panel implementer owns embedded HTML and UI fixtures/checks. Parent owns shared
contracts, specs, integrated tests, independent review and source delivery. No
credentials, production mutations, model substitution or scheduler changes.

## K. OAuth primary action and callback correctness

The remote browser's loopback address is its own host, while the gateway listens
on the deployment host. Native CodeArts authorization uses a fixed loopback
redirect URI; do not claim polling can obtain an unreachable authorization code
or infer a supported public redirect host. Explain this in the dialog. Preserve
the authenticated existing import endpoint as the remote bridge and direct
callback/poll completion for same-host deployments.

Use one primary button: start when idle, submit the pasted callback when pending.
No separate import/poll buttons remain. Copy explicitly means authorization URL;
opening that URL stays available. Native/legacy continuation URLs may require a
second browser round, with exact instructions and no assumed success. Process
import status done with the same completion owner as poll done: stop timers,
clear sensitive/session fields, refresh accounts, show success and close. Never
poll a session deleted by a successful import. Separate background-poll in-flight
state from primary mutation state; serialize or queue submission against a poll
so an entered callback is not silently dropped. Terminal status, expiry, malformed
input and retry remain visible. Dialog generation/login ownership gates every
late result and follow-up.

When an explicit OAuth callback-port override exists, decide the effective port
before building the authorize URL and storing either session. Use that same port
for imported callback validation and code exchange. Do not change PKCE/DPoP,
credential persistence, upstream entitlement or the fixed loopback host contract.

## L. Compose .env delivery

Use env_file: .env for optional application settings. Keep explicit environment
overrides only for required CA2A_API_KEY, TZ default and CA2A_LISTEN derived from
CA2A_PORT. Thus the .env deployment contract has one port setting; native
CA2A_LISTEN remains supported without changing application config precedence.
Retain Compose-level interpolations for port mapping/build/mounts/name/restart.
Document that arbitrary shell-only optional application values are no longer
automatically forwarded by a removed YAML list; place them in the service .env
or use an explicit runtime override. Do not load local dotenv into native code.
Keep source exports free of actual .env/config/auth/data and existing ZIPs.

Ownership: OAuth UI implementer owns panel and browser fixtures; callback
implementer owns server OAuth port selection/tests; Compose implementer owns
Compose/.env.example and only the relevant deployment README paragraphs. Parent
owns shared specs/task docs and final verification/export. Workers do not touch
real credentials/config/env or historical screenshots.


## R18: reference design and state boundary

Gap: current generic login/old shell does not match cnb2api-panel. Design lives in internal/server/panel.html; backend APIs and persistence need no redesign. Reference App.vue and styles.css are read-only; research/cnb-reference-findings.md records exact component/style locations, research/cnb-migration-findings.md records operational contracts. Reproduce layout with original standalone HTML/CSS/inline SVG and existing Go embedding; prefer system fallbacks over introducing font/build dependencies.

Ownership: one trellis-implement worker owns panel.html only, including small auth validation timeout and loading state changes where necessary. A separate worker owns new focused browser verification and updates existing browser fixtures only as required by changed presentation. Parent owns shared specs/task/release metadata. An independent reviewer owns fixes after implementation handoff.

State: initial and submitted authentication enter boot; overview response alone authorizes app; epoch invalidation suppresses old completions. Bound initial overview using AbortController without changing other API contracts, recover to login with usable error/retry, clear timeout handles. Preserve proxy-supplied auth attempt even with no saved key, storage keys, reverse-proxy prefixes and logout cancellation. No arbitrary spinner delay.

Visuals: reference split 1.08fr/1fr login, 320px form, SVG gateway scene; centered 30px blue braces mark plus 20px spinner/loading label. Auth shell 52px header/180px navigation (64px collapsed); #f2f4f7 canvas, #3169df accent, #cfddf2 navigation selection with dark 3px marker. Content16/card12/control8 radii. Adapt operational tables, forms, drawers and dialogs consistently in light/dark and mobile. Do not copy reference branding slogans or removed unrelated product chrome. No new chart/metric fabrication.


## R19: release/deployment architecture and ownership

Gap lives in deployment/build/release files: current Compose always builds, only Go CI exists, no registry publication/update service. Use reference docker-compose.yaml/docker-compose.build.yaml/.github/workflows/build-and-release.yml/.cnb.yml read-only. Default image ghcr.io/azurekiln2333/codearts2api:latest derives from the actual origin; CA2A_IMAGE may select the CNB registry image published for the actual CNB_REPO_SLUG_LOWERCASE. Do not assume an undeclared CNB repository exists. Version v* tag is the release boundary; robust validated SemVer, stable latest only. CI helper code should share version/metadata/checksum logic rather than incompatible shell copies.

Four existing Pi in live discovered workspace w1C (inherited workspace ID is stale): p4 compose/Dockerfile/.env/.dockerignore; p7 GitHub/CNB CI plus new release metadata helper; p6 release exporter/tool tests plus new deployment contract tests; pA README/deployment docs and research/independent integrated review after handoff. Parent owns shared task/specs, integration, final checks, commits/release evidence. They are not alone and must preserve other files/credentials; no recursive implement/check delegation or commits/push/deploy.

Default Compose has image only, bounded log rotation and Watchtower label/scope isolation. Automatic updating enabled in standard deployment as requested, with explicit documented disabling (profile or env-supported reference-compatible mechanism). Local build overlay sets local image/pull_policy build and disables updater targeting; default updater disabled in local build command. Watchtower requires Docker socket; document succinctly and avoid insecure auto-login/config rewrites. Preserve all original mounts/paths and direct .env semantics. Docker image cross-platform build uses supported Go toolchain and CGO-disabled Go binaries; no runtime source/secrets copied.

Validation: official Compose config matrix uses synthetic .env/config only; inspect resolved images/ports/mounts/updater filters/build overlay. Unit tests execute shared release metadata against stable/prerelease/invalid tags and committed export; static YAML parsing plus actionlint where available, actual Go build/tests, extracted source validation. Docker/registry runtime and remote workflows must be reported unverified if unavailable. No production traffic required.

R19 multilingual ownership: Pi pA owns all three README translations plus deployment docs; Pi p6 adds all three to required release inputs and tests navigation/command equivalence. Standard documented image-deployment command should enable automatic updates as requested, with an explicit opt-out/local-build path.
