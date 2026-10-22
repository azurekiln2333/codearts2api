# Research: source-aware model discovery and bounded manual refresh

- Query: Identify proven partial-refresh/routing defects, parser compatibility gaps, safe refresh hooks, and the smallest useful account/source freshness and negative-capability contract.
- Scope: internal; planning only, no provider requests or product edits.
- Date: 2026-10-21

## Findings

### Files found

| File | Responsibility |
| --- | --- |
| `internal/upstream/models.go` | Three-source discovery, merge, account catalogs, canonical IDs and benefit routing. |
| `internal/upstream/client.go` | Per-source backoff, signed GET, metadata/stream HTTP clients and auto-claim setting. |
| `internal/auth/auth.go` | Synchronized credential snapshot and refresh mutation. |
| `internal/server/handler.go` | Process-global display cache, `/v1/models`, background probes and route registration. |
| `internal/server/availability.go` | Account/model negative capability, expiry and active chat probes. |
| `internal/server/lifecycle.go` | Actual chat candidate exclusion from cached catalog/capability. |
| `internal/server/admin.go` | Overview currently calls `modelList()` and exposes only model IDs. |
| `internal/upstream/{models,partial_discovery}_test.go` | Existing discovery happy path and one narrow benefit-retention test. |
| `internal/server/{models,availability,capability_rotation,probe_ownership}_test.go` | Selection helpers, capability caches, actual route rotation and probe lease ownership. |
| `docs/reverse-engineering.md:134` | Historical repository protocol notes, including CodeArtsSpace 0.1.18 and benefit route semantics. |

### Proven partial-result defects

The following are consequences of the current code, independent of the unknown production response for DeepSeek V4.1 Flash.

1. **Agent success plus both other sources failing discards previous benefit metadata.** `FetchModels()` only calls retention when `builtinOK && !benefitOK` (`internal/upstream/models.go:303-327`). A nonempty agent result is enough to overwrite the complete account catalog even when builtin and benefit both failed. A previously discovered non-seed benefit model disappears, or becomes an ordinary model if agent lists it without a benefit flag. `IsBenefitModel()` then returns false (`models.go:203-211`).
2. **Agent presence is mistaken for builtin confirmation when removing a benefit flag.** Retention tests `ac.knows[key]` (`models.go:145-150`), but that set contains the already merged agent and builtin results (`models.go:131-139`). Thus builtin success plus benefit failure can remove an old benefit flag because agent listed the same ID, even if builtin did not list it. The comment says builtin explicitly registered the ID; the implementation does not retain source provenance to prove that.
3. **Historical benefit entries can resurrect after an authoritative removal.** Every replacement copies `prev.lastBenefit`, including `keepBenefit=false` (`models.go:126-129`), and a successful benefit refresh never replaces that historical map with just its current result. Example: first catalog contains benefit X; a later fully successful replacement contains only Y; a later partial refresh with builtin Y can append X again from `lastBenefit` (`models.go:145-153`). This conflicts with the documented whole-replacement semantics (`models.go:99-100`).
4. **Returned discovery results differ from the stored account catalog.** Retention appends entries only to `ac.infos` (`models.go:153`), while `FetchModels()` returns the original pre-retention `out` (`models.go:327-328`). `fetchDynamicModels()` builds the display union from that return value (`handler.go:571-580`). Consequently a retained account capability/benefit can be missing from the display union until the next cache cycle.
5. **Failed non-benefit sources have no preservation at all.** A successful benefit-only refresh replaces prior agent/builtin results; a successful agent-only refresh replaces prior builtin models. `FetchModels()` drops failed sources entirely before `MergeModels()` (`models.go:303-322`). A failed source is indistinguishable from an authoritative source that stopped listing a model, and the new account-wide timestamp masks the uncertainty (`models.go:119`).
6. **Failed accounts lose their display contribution despite the fallback comment.** When an account is stale and its complete fetch fails, `fetchDynamicModels()` skips it without appending its existing `AccountModels()` (`handler.go:565-574`). Other accounts can make the overall fetch succeed and replace the global union for an hour (`handler.go:589-593`), omitting the failed account's old models. If every account fails, the old global union is also not returned; the code returns nil and the caller uses the static fallback (`handler.go:583-587`, `430`).

Recommended coherent fix: keep one last successful snapshot per **account and source**, with separate attempt/success/failure/backoff metadata. Replace only a source whose response is structurally validated and authoritative; preserve a failed source's prior data as explicitly stale. Merge those source snapshots once, and return the exact stored merged result. Benefit route flags come from the retained/current benefit source; do not infer a transition from one unrelated source being successful. A validated successful empty benefit source may remove old benefit flags. This is a bounded current snapshot, not an ever-growing historical union.

### Empty successes and parser compatibility

- Agent detail decodes only `gpts.models` and accepts its zero value as success (`models.go:344-376`). HTTP 200 `{}` or an unexpected envelope becomes an empty successful source. Builtin similarly decodes only `builtinModels`; a missing field or a business-error object still clears source failure and returns an empty success (`models.go:447-469`). Benefit checks `error_code=="0000"`, but absent `result` or absent `result.models` is accepted as empty success and clears its failure (`models.go:546-580`). If any other source returns one model, these empty successes can cause whole-catalog replacement and loss of prior metadata.
- A legitimate authoritative `models: []` must remain different from a missing/invalid field or unrecognized envelope. Use presence-aware validation. Do not make every empty result a transport error, since real removals/entitlement changes need to remove old entries.
- Agent ID preference is currently `model_name`, then top-level `model_id`, then `model_alias`, then parameter `model_id` (`models.go:364`). Builtin/benefit prefer `model_id` then `model_name` (`463`, `570`). This inconsistency is proven; **wrong agent precedence is not proven** by repository fixtures. The only agent fixture has `model_name:"GLM-5.2"` and no competing ID (`models_test.go:201`), and it asserts that model (`240-241`). No fixture or captured response with distinct simultaneous `model_name` and `model_id` was found. Changing agent precedence on assumption could substitute an opaque internal ID for the actual inference name.
- Add explicit parser fixtures for absent name with top-level ID, absent name/ID with parameter ID, alias-only, both name and ID present, empty IDs, malformed envelope, missing collection, valid empty collection, malformed numeric parameters and JSON business-error objects. The paired-field fixture must encode a documented/captured rule before changing current precedence. Preserve display name separately if supported by evidence; do not infer inference aliases from human labels.
- Builtin and benefit discard `io.ReadAll` errors (`models.go:442`, `541`), whereas `getSigned()` propagates them (`client.go:817-820`). A read failure should be a failed attempt preserving the prior source, even if the partial bytes happen to decode.
- Historical docs show `result.base_url`, and the parser reads it but does not use it (`models.go:549`). That is not evidence that current V4.1 Flash needs another inference URL. No endpoint change or model mapping is justified by the checked files.

### Display/callability inconsistency

- If every generated model entry is excluded, `modelList()` keeps the original entries because it assigns `entries = kept` only for `len(kept)>0` (`handler.go:435-445`). This re-advertises all known unusable models, while `chatCandidates()` excludes negatives unconditionally (`lifecycle.go:199-205`). An empty callable catalog should stay empty; an authenticated diagnostic catalog can still show excluded entries with reasons.
- A displayed union is not proof that a given account can call a model. Static fallback and cross-account union also cannot confer account entitlement. Do not copy union benefit flags back into per-account routes.
- Candidate catalog exclusion does not consult `AccountCatalogStale()` (`lifecycle.go:203-205`), so a stale/incomplete known catalog continues excluding IDs. Fixing source retention and exposing freshness is necessary; any change to stale-catalog eligibility should be deliberate and tested against the existing lifecycle contract.
- The reported 404 is generated by this gateway (`chat.go:523-525`), after cached exclusions and/or a provider capability refusal. The exact message does not prove that DeepSeek V4.1 Flash is globally unavailable in CodeArts. A pure catalog miss without any cached/provider capability refusal can instead end in the 503 branch (`chat.go:535-536`); do not conflate those cases.

### Backoff, context and resource hooks

- `Client.srcFail` tracks only builtin/benefit, per `accountID|source`, for five minutes (`client.go:124-131`, `189-219`). Agent discovery has no source backoff. Only a boolean is exposed, so current code cannot return a retry timestamp/reason. Manual refresh must distinguish bypassing the global display TTL from deliberately retrying a source inside its backoff; otherwise a pull can perform no real fetch.
- `FetchModels()` has no context parameter. Agent list/detail use `context.Background()` (`models.go:340`, `383`); builtin, benefit and claim each create independent background 30-second contexts (`423`, `478`, `523`). `getSigned()` already accepts a context (`client.go:798`), so most changes can thread a caller/job context through existing helpers. Preserve compatibility with a `FetchModels()` wrapper if CLI callers need it.
- At default client timeout 60 seconds (`client.go:134-147`), the sequential request path can approach 210 seconds/account: agent list + detail (60 each), builtin (30), optional claim (30), benefit (30). Account iteration is also serial (`handler.go:560-581`). There is no overall catalog-fetch deadline. This is a structural upper-budget observation, not a measured production latency.
- Concurrent cache misses all pass the read lock, then perform network discovery independently (`handler.go:546-557`). The cache mutex protects stored data; it does not deduplicate work. Manual pull needs one shared job, plus a bounded worker count and total deadline. Ordinary GETs must not queue an unbounded set of refresh goroutines.
- `FetchModels()` directly reads mutable credential fields (`models.go:296-300`), whereas `Auth.UpdateCredentials()` changes them under a mutex (`auth.go:151-163`). Use `Auth.Credentials()` (`auth.go:144-148`) after `Account.AuthSnapshot()` and any `ValidateContext()` wait. Check the selected account object is still current before publishing results; a same-name/UID replacement must not receive stale results or a negative reset from an old login.
- Do not clear source-backoff maps globally. A manual forced retry should target the selected account/source set and remain bounded; keep other account/source failures intact. Avoid counting caller cancellation as a new provider failure/backoff. Report skipped cooling/disabled/removed accounts explicitly.
- Auto-claim is **on** by construction (`client.go:154`) and configured in `cmd/server/main.go:49`; comments saying default false/read-only are stale (`client.go:176-177`, `models.go:474-475`). A manual pull should preserve the configured auto-claim policy, expose claim outcome separately, and never silently enable it. A claim failure currently only logs and does not fail benefit discovery (`models.go:518-521`). A config success is therefore not a claim success or capability success.
- `GET /admin/api/overview` invokes `modelList()` (`admin.go:20`). That method both performs metadata discovery and starts chat probes (`handler.go:447-449`). A new diagnostic snapshot GET must read snapshots only. Returning a refresh result through `modelList()` would accidentally start probes and occupy inference capacity. Do not make a refresh button test every model.

### Smallest coherent API proposal (not an approved contract)

Use two authenticated routes, keeping existing overview and OpenAI envelopes compatible:

- `GET /admin/api/models`: cache-only diagnostic snapshot; no discovery, claim or probe. Include a bounded/paged display union **including excluded models**, account/source freshness and availability metadata.
- `POST /admin/api/models/refresh`: bounded optional body `{ "account": "name", "model": "exact-id", "reset_negative": true }`. Omitted account means eligible configured accounts; omitted model means the discovered IDs for that refresh scope. No arbitrary URLs or source credentials. Enforce small body/string limits and validate account/model scope.

Suggested essential fields (names can change before implementation):

```json
{
  "generation": 12,
  "refresh": {"running": false, "started_at": "...", "finished_at": "...", "partial": true},
  "accounts": [{
    "account": "configured-name",
    "eligible": true,
    "excluded_reason": null,
    "sources": [{
      "source": "benefit",
      "state": "error",
      "attempted_at": "...",
      "succeeded_at": "...",
      "retry_at": "...",
      "retained": true,
      "count": 1,
      "cause": "http_error",
      "http_status": 503
    }],
    "models": [{
      "id": "deepseek-v4.1-flash",
      "benefit": true,
      "sources": ["benefit"],
      "stale": true,
      "availability": {"state": "unusable", "reason": "model_not_registered", "checked_at": "...", "expires_at": "..."}
    }]
  }],
  "negative_reset_count": 0,
  "truncated": false
}
```

The model above is an intentionally synthetic ID taken from the user's report, not a verified provider entry. Source states need `unknown`, `ok`, `error`, `backoff`, `skipped`; `ok` plus zero count means a validated authoritative empty list. Keep last success distinct from last attempt. Model stale derives from its contributing source snapshots. Use fixed safe cause codes and bounded HTTP/provider code tokens, not raw provider messages or payloads. Snapshot arrays must have explicit limits/pagination and truncation counts; never claim a complete refresh of unattempted/truncated accounts.

For the smallest synchronous implementation, elect one refresh flight immediately, use a fixed worker count (e.g. two accounts), one total deadline (e.g. 45 seconds), and prevent queued/overlapping jobs. Joining callers may cancel their own wait independently; a shared job needs its own bounded lifetime and service shutdown cancellation. An already running job should be shared or return an explicit in-progress result, not be duplicated. A short post-job minimum interval can prevent repeated clicks continually bypassing source backoff. Exact limits belong in the final design.

Negative recovery must be explicitly **invalidate-to-unknown**, not mark-usable:

1. `reset_negative=true` affects only the selected account/model IDs confirmed by a validated fresh source during that same job; retained-only metadata and failed/unattempted accounts cannot trigger a reset.
2. Reset only negatives that predate job start (or match a captured entry revision). A real rejection arriving during refresh must survive. Preserve positive history and unrelated models/accounts.
3. Return which entries were invalidated/count and label them unknown/awaiting a real call. A subsequent real chat uses the existing bounded lease/deadline path and can mark usable or unusable. No bulk chat probe is necessary to refresh metadata.
4. If a real manual probe is added later, use `probeModel`'s exact owned lease pattern and context propagation; its current background-only/idle-gated signature is not a suitable synchronous admin API. Existing probe ownership tests must stay green.

### Existing coverage and meaningful additions

Existing coverage:

- `upstream/models_test.go:183` exercises three successful sources, one agent selection shape, benefit signing and auto-claim toggling. It does not cover partial HTTP-source failure or parser envelopes.
- `upstream/partial_discovery_test.go:7` calls `setAccountModels()` directly and tests only benefit failure with builtin success plus later promotion; it bypasses `FetchModels()` and cannot catch missing retention triggers or return/store divergence.
- `server/models_test.go:15` covers the compatibility picker; actual chat uses `chatCandidates()`, so helper-only coverage is insufficient. `:68` covers static/alias wrapping.
- `server/availability_test.go:37`, `:55`, `:69` cover scope/expiry/all-account aggregation; `:145` checks no health penalty for capability errors.
- `server/capability_rotation_test.go:69`, `:110` exercise the real chat route across five accounts and enforce truthful 404/existing rotation limits.
- `server/probe_ownership_test.go:21`, `:87` cover owned slots across removal/re-add and recording for the proper account.
- `server/native_tools_test.go:224` calls `/v1/models` with an empty pool and tests static models; it is not a source-refresh test.

Recommended new tests, all using synthetic localhost HTTP boundaries:

1. Real three-source refresh after a seeded full success, then each failed-source combination; assert return equals stored catalog, per-source stale state is true, exact non-seed V4.1 fixture ID survives, and the account's actual chat request retains the correctly signed benefit header.
2. Fresh successful removal/empty source followed by partial failure must not resurrect deleted benefit models. Agent-only presence cannot masquerade as fresh builtin confirmation. Genuine authoritative benefit removal/promotion must still remove obsolete route flags.
3. Table-driven parser shapes listed above; distinguish absent/malformed collections from explicit empty; propagate body-read failure and preserve previous snapshots; demonstrate both-field ambiguity without inventing a mapping rule.
4. Authenticated real GET/POST routes: missing/wrong key gives 401 and no provider calls; malformed/oversized scope is rejected; snapshot GET performs zero provider calls; normal refresh bypasses display TTL; forced source retry behavior and reported retry time are explicit.
5. Concurrent refresh requests use one flight and the worker cap, source stalls respect a total deadline, canceled waiters return promptly, health/chat remain responsive, and another bounded refresh can run after cleanup. An account removed/relogged during fetch must not receive old credential results. Run with race detection.
6. Initial real capability denial -> cached 404 without another provider call -> scoped refresh/reset -> next actual chat succeeds after synthetic provider capability changes. Preserve a concurrent newer denial, another account's negative, another model's negative and positives; retained-only/failed metadata cannot reset any negative.
7. Actual `/v1/models` route with every advertised entry known unusable returns an empty data list, while admin catalog still explains exclusions. Also test a failed account's stale model contribution when another account refresh succeeds. Do not merely assert selector helper output.
8. Secret-sentinel responses/credentials never appear in admin refresh diagnostics; sources expose only safe classified cause/status/code and bounded identifiers.

### Review against design section A

The parent's written `design.md` section A agrees with the recommended source snapshots, validated-empty handling, identical returned/stored merge, cache-only GET, coalesced server-owned asynchronous job, bounded workers/deadline, exact account/model negative resets and empty filtered model lists. Its 202-plus-poll job contract supersedes the synchronous option above if accepted. These details should be explicit before implementation:

1. **Route-provenance guard:** a fresh agent listing plus stale retained benefit data must not count as a fresh benefit route for resetting a negative. Require freshness for the source governing the effective chat route, as well as an older negative timestamp/revision. A same-UID removed/relogged account requires account/auth object identity checking before publishing or resetting.
2. **Retained-data limits:** the 4-MiB body limit and 2,048 union-summary response cap do not by themselves limit retained per-source model counts/string lengths. Set per-source entry and field caps before retention. If a collection cannot be fully interpreted within those limits, classify it as a failed/oversized source and retain the old snapshot; do not accept a truncated collection as authoritative absence and erase unseen entries. Likewise report how many accounts were not attempted because of the account cap/deadline.
3. **Backoff/claim visibility:** include the source retry timestamp when state is backoff, and record optional claim outcome separately from successful config listing. Preserve the configured auto-claim policy. Without those fields the UI still cannot explain an immediate retry suppression or discovered-but-unclaimed benefit.
4. **Cancellation classification:** timeouts/provider failure can update source failure state, but caller cancellation or service shutdown should not overwrite source success or create a misleading provider denial. Async waiters are detached from the server-owned bounded job; its work must end on total deadline/service shutdown.

### Related specs and versions

- `.trellis/spec/backend/request-lifecycle.md`: cached-only chat selection, owned leases, cancellation/refresh and real-route checks.
- `.trellis/spec/backend/request-audit.md`: bounded safe metadata, account identity and no fabricated capability/build claims.
- `.trellis/spec/backend/admin-panel.md`: actual-data rendering, base paths, auth epoch isolation, model discovery vs entitlement and browser checks.
- `go.mod:3`: Go 1.22 compatibility.
- External references: none fetched for this internal code investigation. The repository's historical protocol notes are supporting context, not current official provider proof.

## Caveats / Not Found

- No production credentials, credential files or public provider/model endpoints were used. No deployment or actual provider availability test was performed.
- No captured current official-client payload for DeepSeek V4.1 Flash, no verified alias relation and no paired agent name/ID fixture were found. Production source failure, changing upstream schema, entitlement, incorrect routing and a stale negative are plausible causes; the current evidence cannot select one as the production root cause.
- The API/limits above are design recommendations for parent review. This research did not implement them, alter task status or change existing tests.
