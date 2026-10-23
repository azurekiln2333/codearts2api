# Research: Normal-route model identity during live validation

Historical research snapshot, superseded by the controlled source/wire captures
in `live-account-findings.md`, the official session contract, and
`live-gateway-session-verification.md`. The previously missing identity comparison
was completed and the source-ID defect confirmed; tentative statements below
describe the earlier research boundary rather than the current implementation.

- Query: Can agent display-name precedence and model merging replace a builtin registration ID, and what is the smallest evidence-based fix?
- Scope: internal static source inspection only; live account observations are attributed to the parent session.
- Date: 2026-10-22

## Findings

### Conclusion and evidence boundary

The hypothesized mechanism exists in the current source: normal-route exact IDs inherit the display merge winner, while benefit-route exact IDs have a separate source override. An agent entry named `DeepSeek-V4.1-Flash` can therefore override a builtin entry whose ID is `deepseek-v4.1-flash`. This is a conditional code-path finding, not proof that this difference caused the real provider rejection.

The parent reports successful discovery for four accounts, confirmed disabled benefit channels, merged IDs `GLM-5.3` and `DeepSeek-V4.1-Flash`, and actual normal-route `not_registered` outcomes on three accounts. This researcher did not inspect credential files or live payloads, run tests, or make upstream requests. The missing evidence is the sanitized builtin/agent field relationship and the result of a controlled exact wire-ID comparison.

### Files found

| File | Responsibility |
| --- | --- |
| `internal/upstream/model_discovery.go` | Parses source fields, retains source snapshots, publishes merged catalogs, builds exact-ID index, chooses fresh negative-reset identities. |
| `internal/upstream/models.go` | Merges display metadata and resolves one account/auth's exact ID plus benefit route. |
| `internal/upstream/client.go` | Preserves the explicitly resolved ID in the actual chat JSON body. |
| `internal/server/chat.go` | Resolves selected-account identity immediately before emission; records the actually sent model in audit tracking. |
| `internal/server/availability.go` | Uses the same resolver for probes and removes only bounded pre-job negative entries. |
| `internal/upstream/model_discovery_test.go`, `models_test.go`, `model_resolution_test.go` | Protect parser compatibility, merge behavior, benefit-source ID precedence and account identity. |
| `internal/server/model_catalog_test.go`, `model_resolution_test.go` | Exercise real local HTTP chat/probe boundaries and route/account changes. |

### Exact code path

1. `wireModel` retains `model_name`, `model_id`, `model_alias`, and nested `model_parameters.model_id` (`model_discovery.go:482`). Builtin/benefit parsing chooses `model_id` then `model_name`; agent parsing deliberately chooses `model_name`, then top-level ID, alias, then nested ID (`model_discovery.go:519-524`). Agent context/max-token/description metadata comes from nested parameters. Parsed `Name` is set to the chosen ID (`model_discovery.go:529`), so no separate display-name-to-registration-ID relationship survives in `ModelInfo`.
2. Discovery merges sources in agent/builtin/benefit order (`model_discovery.go:23`, `:257-275`). `MergeModels` folds IDs to lowercase for equality (`models.go:292-305`). `betterModel` prefers the entry with any positive context window; otherwise it prefers an ID with uppercase letters over an all-lowercase ID (`models.go:345-350`). It does not compare source authority or the numerical context-window size. Other missing metadata is filled from the discarded entry (`models.go:323-342`).
3. `indexCatalog` initially assigns `ac.exact[lowercase(ID)] = merged.ID` (`model_discovery.go:180-190`). It then overrides exact IDs only from benefit-source entries when the effective route is benefit (`:191-198`). There is no corresponding normal builtin override. Thus a richer uppercase agent record wins normal inference even when a same-fold lowercase builtin record exists.
4. `ResolveModelForAuth` retrieves that same-fold exact ID from the captured account/auth snapshot (`models.go:224-243`). The real chat path records and passes it to `ChatStreamWithExactModel` (`server/chat.go:359-366`), which places the value directly in `body.model` (`client.go:486-498`). A gateway request differing only in input case cannot distinguish upstream casing: both requests resolve to the same `ac.exact` value. The legacy `ChatStreamWithOptions` still applies global `CanonicalModel` (`client.go:480-481`), so a controlled exact-ID comparison must use the exact-model boundary rather than that legacy wrapper.

### Smallest owner and fix options

| Option | Owner / change | Preconditions and risks |
| --- | --- | --- |
| Preserve source-specific builtin exact IDs for normal routes | Upstream model-discovery owner: after building merged metadata, select the matching builtin snapshot's exact ID for normal routing; preserve benefit override and agent-only fallback. Keep `MergeModels` as display metadata aggregation. | Recommended only if sanitized source fields and controlled calls establish builtin ID authority for the normal route. Match established same-fold IDs only; never map arbitrary display labels or model versions. Retained builtin evidence should survive failure and disappear after authoritative empty/removal. Keep account/auth ownership and static/legacy snapshot compatibility. |
| Prefer another agent field | Upstream parser owner: change source-specific field interpretation only with protocol evidence. | A blanket `model_id`-first change is not safe: `TestDiscoveryStrictEnvelopeAndModelParsers` explicitly protects `model_name:"inference-name"` with `model_id:"opaque-internal-id"` (`model_discovery_test.go:267`). Nested IDs or aliases may also be opaque. It would alter every agent-only model and catalog membership, not just the conflicting pair. |
| Change display merging or lowercase all IDs | Cross-cutting model-list/resolution change. | Not recommended for this evidence. Display richness and uppercase preference are not proof of inference registration. Global lowercasing would corrupt valid case-sensitive IDs and cross-account behavior. Changing the display ID alone could also affect `/v1/models`, union indexes and diagnostics unnecessarily. |

The minimal coherent fix is likely within `model_discovery.go` plus tests, with a parent-owned spec update. Existing `ResolveModelForAuth` and exact chat/probe boundaries already consume the account index; no new chat fallback or retry policy is needed if source authority is the issue.

### Freshness and negative-cache caveat

Source precedence and negative-reset authority must stay consistent. `PublishModelDiscovery` currently accepts any successful same-fold normal source when collecting fresh IDs (`model_discovery.go:278-299`), while benefit routes require benefit success. If builtin becomes the governing normal source, a fresh agent listing plus failed/retained builtin snapshot must not automatically refresh that builtin route's negative capability. Select and retain the governing source along with the exact ID, or otherwise derive the same source consistently when generating `fresh`.

The server consumes those fresh identities only during explicit refresh (`server/model_catalog.go:192-194`). `invalidateCatalogNegatives` already protects account/model identity, job timestamp and revision (`availability.go:121-130`); it should not need broad clearing. Preserve those guards and do not mark rediscovered models usable. Changing input case also cannot bypass existing case-folded capability state.

### Existing coverage and the missing regression

- `models_test.go:104-135` checks metadata merging and stable output, but its overlapping source records have identical IDs; it does not check normal-source identity authority.
- `model_discovery_test.go:189-211` covers a richer uppercase agent versus lowercase benefit entry, including stale-benefit negative-reset behavior. It does not cover an equivalent normal builtin conflict.
- `upstream/model_resolution_test.go:12-40` covers builtin versus benefit exact-ID selection and benefit closure, with an empty agent source.
- `server/model_catalog_test.go:337-406` checks normal routing with benefit disabled, but tests agent and builtin individually and uses the same exact lowercase ID.
- `server/model_resolution_test.go:17-58` verifies two accounts' different wire IDs and audit canonical identity; `:160-207` verifies builtin/benefit source switching and auth replacement. Neither combines conflicting agent and builtin normal entries for one account.

If a fix is justified, add a real local source-to-chat regression with a richer uppercase agent record, same-fold lowercase builtin ID, and benefit disabled. Request lowercase, uppercase and mixed case; assert the builtin wire ID, no benefit header, requested versus sent audit names, and zero leaked leases. Cover builtin failure retaining its exact route, fresh agent not clearing a stale builtin denial, builtin authoritative removal falling back to agent, benefit override/closure, agent-only opaque-ID compatibility, and isolation from another account or replacement auth. Keep the existing model resolver, source-retention and actual wire-boundary tests.

### Related specs and external references

- `.trellis/spec/backend/model-discovery.md`: preserve exact inference IDs, distinguish display labels from aliases, use selected-account/auth source evidence, and require fresh governing-source evidence for scoped negative reset. It already separates merged display preference from benefit wire identity; normal source precedence is not yet specified.
- Task `prd.md` R1/R3/R8 and `design.md` section A require evidence-based discovery, honest callability, selected-account exact wire IDs and no guessed substitutions.
- `.trellis/workflow.md` was consulted for research persistence and role boundaries. No implement/check JSONL manifests were read because research context is role-isolated.
- No external documentation was needed or consulted for these static code findings. The backend spec requires Go 1.22 source compatibility. Current upstream behavior remains a parent-run operational evidence question.

## Caveats / Not Found

- Successful catalogs and a false benefit switch establish metadata and route policy, not model callability. `not_registered` can still represent provider registration/entitlement differences or another protocol condition.
- A true casing comparison must preserve account, route/header policy, endpoint and request shape while changing the actual wire ID. Do not infer it from two differently cased gateway inputs or from the model-list display alone.
- Within one source, parser-level `MergeModels(out)` already collapses same-fold duplicate IDs (`model_discovery.go:531`). A builtin-only casing conflict would require additional evidence and a separate ambiguity policy.
- No product changes, tests, git operations, credential-file reads or upstream traffic were performed by this researcher. Only this research file was written.
