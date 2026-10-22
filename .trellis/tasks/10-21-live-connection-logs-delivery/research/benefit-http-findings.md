# Research: Benefit catalog failure and HTTP/business error ambiguity

- Query: Why does the deployed benefit source report `current 0 / retained 0`, `upstream HTTP error`, and a retry time? Does the request differ from the recorded official protocol?
- Scope: Mixed; current repository code and historical reverse-engineering notes, plus an isolated localhost reproduction. No production API calls or credential files were read.
- Date: 2026-10-22
- Active task: `.trellis/tasks/10-21-live-connection-logs-delivery`

## Findings

Sections 0–4 retain the initial **pre-fix** inspection and original reproduction results. Their local line references identify that inspected version, before the backend worker added the gate. Section 5 records the later localhost comparison against the in-progress repair; it is not a production-account observation.

### 0. New primary implementation evidence: account channel gate was missing locally

The public CodeArts2API author's commit `e472961a69319a629f09a9518e060eff1d07bbe4`, dated **2026-10-21 08:08:49 UTC**, removes its benefit implementation. Its `docs/adr/0005-remove-benefit-channel.md:27–31` records one account returning a disabled snap gate, HTTP-success business code `4000` from the opengw endpoints, and a working builtin directory. The ADR explicitly acknowledges at lines 74–75 that these measurements cannot distinguish account-specific closure from global closure. This is strong primary evidence about that author's implementation and measurements, not an official provider announcement or the user's account result.

The parent commit `db37b8fff8e8a1e4bf11cc60cadc2a5b0cc26c42` provides the exact pre-removal gate protocol in `internal/codearts/benefit.go:42–49`: GET snap `/v1/benefit-gateway-config`, `Agent-Type: PromptCenter`, ordinary signed credit headers, no benefit route header. Its `ClaimDaily` checks that gate before attempting a claim (lines 120–128). Its `client.go:170–171` adds the benefit route header only for that channel.

Current pinned `e472961` code fetches the normal model directory using `FetchBuiltinModels` (`internal/codearts/client.go:249–253`). Its registry contains the exact `deepseek-v4.1-flash` identifier (`internal/registry/registry.go:44`). This corroborates a separate normal-directory route without proving the user's individual model entitlement.

The smallest evidenced local protocol repair is therefore a **per-account strict gate**, not global removal and not treating every `4000` as a global shutdown. Explicit false can authoritatively remove that account's benefit contribution and seed routing; explicit true retains its legacy behavior. A missing, malformed, or failed gate remains unknown and cannot establish closure. Do not automatically retry an upstream benefit refusal on a chargeable alternate route.

### 1. Confirmed defect: successful HTTP transport can be reported as an HTTP error

`internal/upstream/model_discovery.go:407` rejects a non-2xx response and always sets its real `HTTPStatus`. Separately, `benefitEnvelope` at line 572 accepts only `error_code == "0000"`. Any other business code is mapped to `Kind: "http"` (except the string codes `401`/`403`), without an HTTP status. Its four-value provider-code allowlist drops every unrecognized code.

`internal/server/panel.html:511` renders kind `http` as `上游 HTTP 错误`. Lines 514–519 append any available HTTP status and provider code. Therefore a failure label with neither a numeric HTTP status nor a code is consistent with **a successful HTTP response carrying an unrecognized business failure**, not necessarily a failing HTTP status. This is a conditional diagnosis of the user's current deployed code, not evidence of the exact production response.

The localhost reproduction `research/benefit-http-repro.go` exercises the real exported discovery and catalog publication boundaries with synthetic credentials:

| Synthetic benefit response | Published source result | UI implication |
|---|---|---|
| HTTP 200, `{"error_code":"benefit.synthetic.error"}` | `failed`, `count:0`, `retained:0`, `error:{"kind":"http"}`, retry in five minutes | Bare `上游 HTTP 错误`, matching the reported wording |
| HTTP 503, JSON error | `failed`, `error:{"kind":"http","http_status":503}` | Label includes `HTTP 503` |
| HTTP 200, `0000`, `result.models:[]` | `empty`, no error or retry | An authoritative empty catalog, not a source failure |

Executed successfully with the local Go 1.27.3 toolchain on 2026-10-22. All requests went to `httptest.NewServer`; the fixture never contacts Huawei Cloud or the user's service.

### 2. No evidenced endpoint, method, or signing mismatch

The current chain agrees with the repository's recorded CodeArtsSpace 0.1.18 benefit protocol:

| Boundary | Current code | Recorded evidence |
|---|---|---|
| Host | `BenefitHost = https://opengw.developer.huaweicloud.com`, `const.go:41` | `docs/reverse-engineering.md:140` |
| Catalog | GET `/api/v1/gateway/config`, `model_discovery.go:595` | docs lines 140–142 |
| Claim | POST `/api/v1/benefit/claim` with `{}`, lines 379–381 and 621 | docs lines 144–145 |
| Auth | AK/SK signing and `X-Security-Token`, lines 391–394 | docs line 141 |
| Agent-Type | Omitted because the argument is empty | docs line 141 explicitly says no Agent-Type |
| JSON envelope | `error_code:"0000"`, `result.models` | docs line 142 |

`modelRequest` also sets JSON content type and `X-Language: zh-cn` before signing. `signer.go:31` adds the date, host, security token, payload hash, and SDK-HMAC-SHA256 authorization, covering all current headers. The localhost fixture captured GET, the exact catalog path, absent Agent-Type, and the presence of both a security token and SDK signing; no credential values were printed.

`cmd/models/main.go:48` calls `FetchModels`, which now uses the same `DiscoverModels` chain; it is not an independent implementation proving the production protocol. The existing `TestFetchModelsMergesSourcesAndGatesClaim` (`internal/upstream/models_test.go:183`) verifies the same paths and three-source merge against a synthetic server. These tests establish local consistency, not current upstream acceptance.

One external historical difference needs separate treatment: the peer project's pre-removal `benefit.go:77` claims using GET, while this repository's earlier captured-client notes and implementation use POST. A current official-client capture was not available to resolve that version difference. The reported catalog failure is on GET config, so changing the claim method is not justified as the root fix.

There is no `internal/upstream/benefit.go`; benefit catalog/claim implementations are in `model_discovery.go`. `internal/upstream/checkin.go:65` uses the separate snap `/v1/ops/*` activity-credit protocol. Its integer `code:0` envelope must not be copied onto the token-benefit endpoint without new evidence (`docs/reverse-engineering.md:114`).

### 3. Zero retained items and the retry timestamp do not establish entitlement

`PublishModelDiscovery` retains only an existing successful contribution for the same account/auth owner (`model_discovery.go:240–253`). A first failed pull has nothing to retain, so both counts remain zero; this is not an authoritative statement that the provider offers no benefit models.

`client.go:133` defines a five-minute per-source failure cooldown, written at lines 207–213. The retry timestamp is generated locally after a failed attempt, rather than supplied by the benefit provider. `DiscoverModels` bypasses that cooldown when forced, so the explicit coalesced admin refresh is the correct retry boundary. Repeated ordinary cached reads are not retries.

The exact current official inference ID and the user's entitlement to V4.1 Flash remain unobserved. A metadata-diagnostic fix cannot honestly be described as proof that a real Flash completion now works. Forcing a benefit route or substituting an older model would lack evidence.

### 4. Existing test coverage missed the diagnostic distinction

`TestDiscoveryStrictEnvelopeAndModelParsers` at `internal/upstream/model_discovery_test.go:255` includes an unknown business-code sentinel. It only checks whether the source succeeded, not the error kind, real HTTP status, or actionable safe classification. The sentinel protects against accidental disclosure and should remain protected while adding transport-versus-business assertions.

### 5. Post-fix localhost comparison, 2026-10-22

The research-only `benefit-http-repro.go` now serves `{"enabled":true}` from the new snap gate by default, preserving the original three config-response comparisons. It also covers known business code `4000`, an explicitly disabled gate, and a gate response missing `enabled`. Synthetic request summaries contain method/path, safe header names/values, and credential-presence booleans only. Source-request counters distinguish a skipped config/claim from a failed request.

Executed command, using the unchanged local module and the backend worker's in-progress product edits:

```powershell
& 'D:\AppData\Local\Temp\codearts2api-toolchain-go1.27.3\go\bin\go.exe' run ./.trellis/tasks/10-21-live-connection-logs-delivery/research/benefit-http-repro.go
```

The 2026-10-22 12:19:26 Asia/Shanghai run exited successfully. The substantive results were:

| Synthetic scenario | Post-fix source result | Config calls | Claim calls |
|---|---|---:|---:|
| Gate true; HTTP 200 unknown business rejection | `failed`, `error:{"kind":"business"}` | 1 | 0 |
| Gate true; HTTP 503 | `failed`, `error:{"kind":"http","http_status":503}` | 1 | 0 |
| Gate true; HTTP 200, `0000`, empty models | `empty`, successful timestamp, no error/retry | 1 | 0 |
| Gate true; HTTP 200, `4000` | `failed`, `error:{"kind":"business","provider_code":"4000"}` | 1 | 0 |
| Gate false; auto-claim enabled | `disabled`, successful timestamp, no error/retry; claim not attempted with `skipped_reason:"channel_disabled"` | 0 | 0 |
| Gate `{}`; auto-claim enabled | `failed`, `error:{"kind":"parse"}`, no successful timestamp | 0 | 0 |

Every scenario made exactly one GET gate request with `Agent-Type: PromptCenter`, SDK signing, and a security token. Config requests, when made, remained GET with no Agent-Type. The unknown business-code and `error_msg` sentinels were not included in the diagnostic JSON.

The original pre-fix output in section 1 remains part of this record: its unknown business rejection had `kind:"http"`, whereas the current result has `kind:"business"`. The disabled result is produced only by the fixture's explicit false, and a missing switch does not become disabled. The initial three scenarios keep auto-claim off, so their zero claim counts are policy-driven; the disabled/missing-switch scenarios turn auto-claim on to demonstrate that the gate prevents the claim.

At this run, business-error JSON did **not** include an `http_status:200` field; HTTP 200 is known from the fixture response, rather than reported by that diagnostic field. This comparison documents the actual current code and does not claim an additional status-field contract was delivered.

All six outcomes are synthetic local observations. They do not establish the user's real gate value, actual `4000` response, account entitlement, model list, or successful production completion. No production request or credential access was involved. The script is a research comparison, while product regression coverage and changes remain owned by the backend/check workers.

## Minimal repair recommendation given before implementation

1. Before optional claim/config, fetch the account's bounded snap gate and require an explicit boolean. False authoritatively closes only that account's benefit source: skip opengw claim/config, clear prior benefit contributions and cold-start benefit routing, keep exact normal-directory models, and report closed rather than a failed pull. True preserves legacy behavior. A failed/unknown gate preserves prior evidence and does not imply closure. Keep it inside the existing job/source budgets and auth-owner publication guard.
2. Distinguish an HTTP success with a provider-declared failure from an actual non-2xx HTTP response. A fixed business/provider error kind and a corresponding Chinese label should not claim an HTTP transport failure. Preserve the actual HTTP status through benefit-envelope validation.
3. Add the evidenced `4000` business code to a bounded reviewed allowlist with a fixed channel-invalid category, while preserving secret-sentinel protection. Do not expose arbitrary `error_msg`, response bodies, signed headers, or credentials. `4000` alone is insufficient for clearing all benefit routes.
4. Give unknown provider refusals a truthful fixed fallback. Do not infer expired credentials, entitlement, rate limits, or deprecation solely from a generic business failure.
5. Add regressions for explicit false/true/unknown/malformed gate, closed-source cache/seed removal and later failure, retained normal models, no claim/config when closed, no cross-account closure, ownership replacement, business-vs-HTTP errors, malicious codes, separate claim outcomes, and retention. Assert real API JSON and panel labels.
6. Obtain the user's actual sanitized gate/status/code after the repair, or an authorized current official-client capture, before claiming the user's entitlement or real V4.1 completion was verified. No model substitution or automatic cross-route retry.

## Files found

- `internal/upstream/model_discovery.go`: Bounded shared source request helper, benefit-envelope parser, benefit catalog and claim implementations, source publication.
- `internal/upstream/const.go`: Hardcoded production service domains and paths.
- `internal/upstream/signer.go`: SDK-HMAC-SHA256 signing implementation.
- `internal/upstream/client.go`: HTTP transport, test endpoint fields, five-minute source backoff.
- `internal/upstream/models.go`: Account catalogs, compatibility `FetchModels`, benefit routing.
- `internal/upstream/models_test.go`: Synthetic three-source merge and claim-policy boundary coverage.
- `internal/upstream/model_discovery_test.go`: Strict parser, retention, backoff, and secret-sentinel tests.
- `internal/upstream/checkin.go`: Independent activity-credit protocol, not token-benefit discovery.
- `cmd/models/main.go`: CLI entry point sharing `FetchModels` and configured auto-claim.
- `internal/server/panel.html`: Maps source error kinds to Chinese labels and displays optional HTTP status/code.
- `docs/reverse-engineering.md`: Historical protocol record, including CodeArtsSpace 0.1.18 and older CodeArts Agent 26.7.0 evidence.
- `research/benefit-http-repro.go`: Synthetic localhost transport/business/empty reproduction.

## External references and related specs

- Peer project commit metadata, read over the public GitHub HTTPS API without Git operations: `https://api.github.com/repos/2951461586/CodeArts2API/commits/e472961a69319a629f09a9518e060eff1d07bbe4`. Parent is `db37b8fff8e8a1e4bf11cc60cadc2a5b0cc26c42`.
- Pinned account-measurement/decision record: `https://raw.githubusercontent.com/2951461586/CodeArts2API/e472961a69319a629f09a9518e060eff1d07bbe4/docs/adr/0005-remove-benefit-channel.md`.
- Pinned legacy gate implementation: `https://raw.githubusercontent.com/2951461586/CodeArts2API/db37b8fff8e8a1e4bf11cc60cadc2a5b0cc26c42/internal/codearts/benefit.go`.
- Pinned legacy header/channel implementation: `https://raw.githubusercontent.com/2951461586/CodeArts2API/db37b8fff8e8a1e4bf11cc60cadc2a5b0cc26c42/internal/codearts/client.go`.
- Pinned current normal-directory implementation: `https://raw.githubusercontent.com/2951461586/CodeArts2API/e472961a69319a629f09a9518e060eff1d07bbe4/internal/codearts/client.go`.
- Pinned current registry identifiers: `https://raw.githubusercontent.com/2951461586/CodeArts2API/e472961a69319a629f09a9518e060eff1d07bbe4/internal/registry/registry.go`.
- Current unpinned `docs/UPSTREAM.md` was also read. GitHub HTML and raw-host responses showed differing older/newer content during this research; use the pinned commit above for reproducibility.
- Historical repository primary-evidence attribution: CodeArtsSpace 0.1.18, 2026-09-06; CodeArts Agent 26.7.0, 2026-08-03. The source artifact path named by the latter is currently unavailable.
- Official signer package named by `signer.go`: `@huaweicloud/huaweicloud-sdk-core`, AKSKSigner. No exact package version is recorded here, so no claim of current official-package parity was newly verified.
- Public searches produced no new current **official-provider** reference for the exact opengw endpoint/error semantics. The primary peer code/measurements above must not be presented as official API documentation or proof of global closure.
- `.trellis/spec/backend/model-discovery.md`: Source-aware retention, bounded refresh, safe error contracts, and no guessed IDs/entitlements.
- `.trellis/spec/backend/admin-panel.md`: Safe escaping and truthful console state.
- `.trellis/spec/backend/request-lifecycle.md`: Account/auth ownership and synchronized credential access.

## Caveats / Not Found

- `D:\CodeArts Agent\resources\app` does not exist in the current environment. No current official client assets were inspected.
- No real production status, business code, raw response, or provider request ID was captured. The user's wording alone cannot identify the provider's actual refusal reason.
- The peer author's `4000` result strongly explains the same local error shape, but the user's actual gate/code remains uncaptured. Per-account strict false is the authoritative runtime boundary.
- No production calls or real credential reads were performed.
- Git history comparison is intentionally left to the coordinating session: research-role scope forbids Git operations. Likewise `implement.jsonl` and `check.jsonl` were not loaded because research context is role-isolated; PRD, design, implementation plan, workflow and relevant specs were read.
- This research changes no product code, source specs, task metadata, or release artifact.
