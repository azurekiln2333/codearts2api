# Model discovery and explicit catalog refresh

## 1. Scope / Trigger

Read this before changing `internal/upstream/models.go`, model-source discovery,
per-account benefit routing, server model snapshots/refresh, negative capability
recovery, the Models console view, or `/v1/models` filtering. Preserve Go 1.22
source compatibility and the request-lifecycle/account-auth contracts.

## 2. Signatures

- `(*upstream.Client).FetchModels(*auth.Auth)` remains a compatibility wrapper.
- Discovery accepts one bounded context through agent list/detail, builtin,
  optional configured benefit claim and benefit config requests.
- `AccountModelsForAuth`, `AccountCatalogStaleForAuth`,
  `AccountModelDiagnosticsForAuth` and `IsBenefitModelForAuth` take the captured
  `*auth.Auth`; server readers use them to hide a previous login's retained data.
- `AccountModelKnowledgeForAuth(accountID, model, owner)` atomically reads
  membership and authoritative completeness from one catalog generation.
- `ResolveModelForAuth(accountID, model, owner)` atomically resolves the selected
  account's exact ID and benefit flag. `ChatStreamWithExactModel` sends that ID
  without another union-wide lookup; legacy client entry points remain compatible.
- Server `modelAvailabilityForAuth(accountID, input, owner)` resolves the caller's
  ID/alias against that account/login before reading availability. Production
  writes use `markModelUnusable(acct, capturedAuth, actualWireID, reason)` and
  `markModelUsable(acct, capturedAuth, actualWireID)` under `Pool.WithCurrentAuth`.
- `(*server.Handler).StartModelDiscovery(context.Context)` attaches the service
  lifecycle and begins bounded metadata discovery.
- Authenticated `GET /admin/api/models` reads current diagnostics only.
- Authenticated `POST /admin/api/models/refresh` starts/joins a bounded job and
  returns HTTP 202 with its `refresh` object.
- Existing OpenAI `GET /v1/models` keeps its list envelope and supported aliases.

## 3. Contracts

### Source ownership and identity

Keep the latest successful contribution separately for agent, builtin and benefit
sources. Only a structurally valid, complete success replaces that source's
snapshot. Explicit empty arrays are authoritative removals; absent/unrecognized
collections, malformed bodies, read errors, oversized bodies/collections and
provider failures retain the prior contribution with visible stale/failure state.
The returned merged list must equal the list published for that account.

Before legacy benefit claim/config calls, read the account's signed Snap
`GET /v1/benefit-gateway-config` with `Agent-Type: PromptCenter` inside the same
source budget. Require HTTP 200 and an explicitly present boolean `enabled`: false is an
authoritative `disabled` source, with empty contribution, a real confirmation
timestamp and no error/backoff. Skip claim/config, drop retained benefit entries
and suppress seed benefit routing for that auth. True preserves legacy discovery.
Other HTTP statuses, a missing/null/nonboolean/duplicate field or cancellation are unknown/failed and
retains prior source evidence; never default a missing boolean to false. A closed
source counts as successful/current knowledge for job totals and completeness.
Its later failed refresh cannot resurrect the removed route. Do not infer closure
for other accounts or from a date/business code alone.

Benefit routing follows current or retained account-specific source evidence.
An agent entry alone cannot remove a benefit flag while benefit discovery fails.
A later partial failure cannot resurrect entries removed by an earlier successful
benefit response. Never copy union-wide entitlement back to every account. Keep
exact inference IDs; do not guess a mapping from human display names or substitute
an older model for a newly requested ID. Authoritative benefit-source removal
also overrides the cold-start seed route; later failures cannot revive that seed.

Read credentials through synchronized accessors after the account's auth snapshot.
Publish only if the pool still contains the captured account/auth object, so a
removed or same-UID replacement login cannot receive the old result. Any lock-held
publication callback performs bounded memory operations only, with consistent
lock order and no network, disk I/O or request-observer calls.

Retained source entries and fields have hard limits (256 entries/source, 192-byte
IDs/names, 512-byte descriptions). Reject over-limit authoritative collections
rather than interpreting their truncated prefixes as deletion. Prune no-longer
configured account snapshots; API response limits do not replace retention limits.

### Refresh ownership and observability

Admin snapshot GETs do not fetch, claim or probe. Explicit refresh, startup and
stale-catalog background refresh share a flight; at most two account workers,
512 account snapshots and a 60-second total job budget. Repeated requests join
the current job rather than creating goroutines/wait queues. Keep at least 15
seconds between starts and expose retry time. A caller disconnect cannot cancel
another caller's shared job; service cancellation and the job deadline end work.
Chat selection remains cached-only and never waits for discovery.
Before contacting sources, bounded startup/background/explicit workers call the
pool's shared `ValidateContext` with the job context. Recheck the exact account
and auth object's discovery eligibility after validation, and retain guarded
publication. Canceling a worker releases its refresh wait promptly; it cannot
cancel persistence of another caller's shared token refresh. Disabled, removed
or replacement accounts cannot publish old work. Credential validation failure
counts failed account work without inventing source HTTP errors or attempts.
Each source also has at most 15 seconds within the shared budget, with optional
claim work limited to five seconds, so one source cannot starve all later ones.
The admin overview uses the same cached filtered model projection, without
starting discovery, claims or real probes. Only the explicit refresh, service
startup and existing public model-list path may request their documented work.

Source failure backoff belongs to the captured auth owner and exact signing
credential generation, represented only by a private digest. A refresh completed
by another caller before the catalog job starts must invalidate old-credential
backoff just as a refresh performed by the worker does. Keep same-generation
failures backed off for five minutes and preserve explicit force behavior.
Retain one generation with three source retry times per account; replace obsolete
generations and prune removed/relogged accounts. A late old-generation failure
or success cannot set or clear a newer generation's backoff. Do not expose the
digest, signed credentials or raw provider response in diagnostics.

The snapshot contains model summaries (exact ID, discovered/fallback origin,
catalog/benefit/blocked account counts), account/source freshness and safe errors,
negative exclusions with expiry, refresh progress and truncation counters. Source
attempt and last-success timestamps are distinct. Publish backoff expiry and
configured auto-claim policy/result separately. A successful config listing is
not proof of a successful claim or an actual model completion.

Model lists are capped at 2,048 summaries, accounts at 512 and exclusion summaries
at 512, with loss counts. Job completion distinguishes completed/partial/failed;
skipped or unattempted accounts cannot become successful work. Error messages use
fixed safe kinds, optional HTTP status and allowlisted bounded provider codes.
Do not expose raw responses, headers, URLs with queries or credential material.
HTTP-success benefit refusals use `business`, distinct from non-2xx `http` errors.
Keep observed code `4000` as a reviewed safe code with a fixed channel-invalid
description; it does not itself establish a disabled channel. Unknown codes and
arbitrary provider messages stay omitted. The claim's `enabled` remains user
policy, while `skipped_reason:channel_disabled` explains no attempt on a closed
channel without claiming success or failure of a nonexistent claim.

### Negative capability and list semantics

Resolve lowercase/mixed-case requests against the selected account and captured
auth, not the union-wide `CanonicalModel` index. The exact ID and benefit flag
come from the same snapshot. When benefit evidence governs the route, its exact
ID wins even if display merging prefers a richer differently-cased normal-source
entry. After confirmed benefit closure, use the surviving normal-source ID.
Unknown or replaced-login catalogs cannot borrow another account's dynamic ID;
only existing static aliases/seeds are cold-start fallbacks, otherwise preserve
the unknown input. Do not guess display aliases or interchange versions. A
bounded unchanged model_name explicitly paired with a source inference ID may
be an account-local alias only when unambiguous across all retained sources;
exact registered IDs win collisions. Compatibility SetAccountModels snapshots
do not manufacture aliases from arbitrary Name fields.

Cold-start normal seeds include the actually captured inference IDs `glm-5.3`
and `deepseek-v4.1-flash`, so first display-case requests work before metadata is
published. They are not benefit seeds, proof of entitlement or a reason to mark
an account usable. Keep uppercase genuine seeds (`GLM-5.2`, `Qwen3-VL-235B`).
Do not add guessed nearby model versions or borrow dynamic IDs from other accounts.

Agent model_parameters.model_id precedes model_name. Keep the display name in
Name; without that explicit parameter, retain legacy name-before-opaque-ID
fallback. For normal-route same-fold conflicts, builtin model_id governs the
wire ID even if richer agent metadata wins display merging. Benefit source
governs when benefit routing applies. GLM-5.3/DeepSeek-V4.1-Flash display names
can correspond to lowercase actual IDs, while GLM-5.2/Qwen3-VL-235B remain
uppercase actual IDs. Global lowercasing is therefore incorrect.

Use the same account resolution for actual chat, background probes, candidate
checks, public model filtering and capability cache reads/writes. Resolve caller
case and established aliases before querying; `snap-chat` and GLM-5.2 must reach
the same actual registration. Never run discovery on this path. The outbound body
must preserve the chosen ID instead of recanonicalizing globally.

Availability's case-folded account/model key is an index, not an inference
identity. Each record stores the exact actual `wireID` and captured auth owner.
A cached state applies only when the current account/login resolution returns
that exact string and the owner still matches. Therefore a valid ordinary
non-forced discovery correcting `Dynamic-Wire-ID` to `dynamic-wire-id` immediately
makes the former wrong-wire denial inapplicable without treating metadata as a
successful call or resetting a genuine denial for the unchanged exact ID.
Compatibility ownerless helpers exist for older fixtures; production must use
the auth-aware read/write wrappers.

At write time, validate the still-configured account/auth with `WithCurrentAuth`
and confirm resolving the actual attempted wire ID still yields the same exact
ID. Discard a late old-mapping or old-login outcome, whether success or denial,
rather than overwriting newer effective-wire evidence under the shared fold key.
The cached admin projection must apply the same exact-wire/auth predicate after
copying entries outside the availability lock; it cannot display stale wrong-wire
exclusions or blocked-account counts after mapping correction. Never acquire pool
or catalog locks while holding the availability lock.

An absent model excludes an account only when discovery is complete and current.
Partial, failed, unknown or expired discovery leaves absence unknown; retained
present entries still improve selection priority. Read membership and completeness
under the same catalog lock, with the captured auth identity, so a publication
between separate reads cannot produce a false authoritative exclusion. Explicit
legacy `SetAccountModels` snapshots retain their whole-catalog semantics. A real
cached provider denial remains authoritative even when discovery is incomplete.

The availability cache has a 30-minute TTL. For an unchanged effective wire
identity, an explicit
refresh may invalidate an older negative to unknown only for an exact rediscovered
pair whose effective route's source was freshly and authoritatively refreshed.
Fresh agent metadata plus a retained failed builtin ID or stale benefit route
is insufficient. Use the same effective source authority for exact sending and
negative-reset authorization. Guard
the negative's timestamp and monotonic revision, plus account/auth identity: a concurrent newer
denial or replacement login must survive. Do not clear unrelated models/accounts,
mark discovery as usable, or change cooldown/manual-disable state. Revision guards
are required because a new denial can share a coarse system-clock tick with job
creation; a timestamp comparison alone cannot distinguish them safely.
Fresh-source pair lookup uses the same case-folded registration grouping as the
merge, while preserving the exact effective ID used for inference and record
applicability. A richer display label cannot suppress a valid scoped reset or
revive an inapplicable wrong-wire denial.

An empty filtered `/v1/models` result stays empty. Never restore all excluded
entries merely because the filtered result has length zero. The diagnostic admin
catalog may still show excluded models with safe reasons. A gateway 404 based on
cached candidates is not proof that a model does not exist in the provider's UI.

### Console consumption

The Models view has a real `重新拉取` action, shows current job state/freshness and
source/exclusion details, and distinguishes retained stale data from successful
new discovery. Poll a running job only while authenticated, visible and on the
Models view. At most one snapshot request is in flight. Abort and guard by login
epoch/version so navigation/logout/old responses cannot restart polling or replace
new state. Keep BASE prefixes and existing escaping/clipboard behavior.

## 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| One source fails | Retain that source; show failure/staleness, merge other successes |
| Explicit valid empty source | Remove its old contribution; do not later resurrect it |
| Account benefit switch explicitly false | Disabled, skip legacy calls, remove old routes/seeds, no retry error |
| Benefit switch unavailable or malformed | Unknown failure, retain same-auth evidence, no inferred closure |
| Benefit switch HTTP 202/206 with false | Failed/incomplete response, retain routes; only HTTP 200 may revoke |
| HTTP-success nonzero business code | Business error with reviewed safe code; no invented HTTP failure |
| Missing/malformed/oversized collection | Fail source and retain old data; no invented empty success |
| Benefit fails, agent lists same ID | Preserve proven benefit route until authoritative change |
| Concurrent refresh starts | One bounded job; same job ID/progress |
| Metadata source hangs | Job deadline, partial/failed result, responsive chat/health |
| Catalog credentials expired | Share bounded credential validation, then fetch with refreshed STS |
| Credential validation fails | Failed account work; no fabricated source HTTP error |
| Another caller refreshed STS before the catalog job | Old-generation source backoff cannot suppress fresh metadata calls |
| Late old-generation metadata result | Preserve current generation's source retry state |
| Removed/relogged account | No stale publication/reset over the replacement |
| Fresh chat denial during refresh | Preserve new denial; only older scoped negatives may reset |
| Cold partial catalog omits requested model | Attempt eligible account; absence is unknown, no guessed route |
| Complete current catalog omits requested model | Account may be excluded by authoritative absence |
| Every advertised model excluded | OpenAI data array empty; admin explains exclusions |
| Lowercase/mixed-case request | Send this account/route's exact discovered ID |
| Cold display-case GLM-5.3 or DeepSeek-V4.1-Flash before discovery | Use captured lowercase normal seed IDs; no benefit header or positive capability claim |
| Valid non-forced discovery changes effective exact wire ID | Old wrong-wire availability becomes inapplicable immediately; unchanged exact-wire denials remain |
| Late result from old mapping or old login | Discard it; preserve current exact-wire/auth result |
| Other account or previous login has different casing | Do not rewrite the selected account's wire ID |
| Normal display entry is richer than same-fold benefit entry | Benefit route uses benefit source's exact ID |
| Unknown ID with no account evidence | Keep input or established static alias; no guessed dynamic registration |
| No/wrong admin key | Existing 401 envelope; no provider call or diagnostic leak |

## 5. Good / Base / Bad Cases

- Good: after a source outage, pull metadata once, inspect the retained benefit
  entry and source failure, then retry the exact model after authoritative recovery.
- Base: a new model returned by the source appears with its exact ID and correct
  per-account route; no static seed update or service restart is required.
- Bad: treat `{}` as a successful empty list, overwrite the whole catalog, remove
  the benefit header, cache the resulting denial, and keep advertising the model.

## 6. Tests Required

Use actual localhost source/admin/chat boundaries, not only selection helpers.
Cover all source failure combinations, validated empty/removal followed by partial
failure, malformed/oversized/read-error bodies, identical returned/stored merges,
per-account signed benefit routing and an exact dynamically supplied new-model ID.
Test the signed benefit switch path/header, exact HTTP 200 authority versus
202/206 partial/accepted responses, true/false/unknown states and all
disabled/re-enabled/failure transitions. Confirm false sends neither legacy claim
nor config calls, drops old source/seed routes, and keeps other accounts isolated.
Use a real normal-route chat for exact builtin/agent IDs with no benefit header.
Contrast HTTP 503 and business 4000; retain malicious-code redaction coverage.
Test real chat/probe wire IDs with two accounts requiring different casing,
normal/benefit source conflicts, closure, auth replacement, unknown inputs,
default/auto and static aliases. Assert the actual last-sent audit name, alias
denial consistency, no global second rewrite and concurrent catalog publication.
Test coalescing/worker limits/deadline/service cancellation, responsive health,
authentication, stale-negative recovery, preservation of concurrent fresh denials,
same-UID replacement and unrelated state. Secret sentinels must not appear in
admin JSON. Verify zero filtered models through the actual OpenAI route.
Reproduce cold partial discovery with an agent success and failed benefit source;
the real chat route must reach an otherwise eligible account for an unlisted model.
Cover complete absence, expired snapshots, present retained entries, replacement
auth and preservation of actual negative capability.

Exercise a genuinely cold account through actual HTTP chat before metadata:
display-case GLM-5.3 and DeepSeek-V4.1-Flash, stream/nonstream, exact lowercase wire,
content and audit. For a nonseed dynamic model, first reject the preserved unknown
case, publish a valid explicit lower inference ID through non-forced discovery,
then require another actual HTTP send using the corrected ID. Assert old wrong-wire
exclusions disappear from cached admin/public model views, unrelated and genuine
unchanged-wire denials remain, and no forced-refresh reset is needed. Block an
old-wire HTTP attempt across publication; let the corrected wire obtain a genuine
denial first, then release the old result and verify it cannot overwrite or bypass
the corrected wire's denial. Cover old-login success/denial after replacement.
`cold_identity_test.go`, `wire_availability_test.go` and upstream
`cold_seed_test.go` own these boundaries.

`model_catalog_auth_test.go` and `catalog_rotation_boundary_test.go` cover expired
STS through actual localhost sources and the shared token endpoint, refresh
coalescing, cached admin GET no-I/O, job/service cancellation, disabled/removed/
replacement publication guards, and validation-failure source diagnostics.
`model_source_backoff_test.go` covers changes to each signing credential field,
same-generation 5xx/force/recovery, late old-generation success/failure, replacement
auth with identical STS, bounded generation replacement and account/expiry pruning.

Run Go tests/race/vet/build after integration. The real-browser fixture verifies
pull loading/completion/partial/error states, retained data, no duplicate jobs,
off-view/logout polling stops, exact identifier copying and proxy prefixes.

## 7. Wrong vs Correct

Wrong: a cache mutex protects reads but every concurrent miss independently
fetches all accounts; a Refresh button merely refetches overview and says success.

Correct: one bounded metadata job owns network work, publishes source-aware
snapshots, and exposes actual progress/results to cache-only admin GETs.
