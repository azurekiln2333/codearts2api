# Benefit-source follow-up verification, 2026-10-22

## Scope and evidence

The user reported a failed benefit source with zero current/retained entries, a
generic HTTP error and a retry timestamp. Localhost reproductions confirmed two
local defects: discovery omitted the per-account benefit-channel switch, and an
HTTP-success business rejection was classified as an HTTP error. The displayed
zero count is not evidence of the user's account entitlement or a global channel
shutdown. Pinned protocol evidence and before/after results are in
`benefit-http-findings.md`; the original release report remains historical.

Production snapshot access was attempted once with an authenticated cache-only
catalog GET; that and public health returned HTTP 403. No account switch/code or
production completion was observed. This does not establish invalid credentials.
No production refresh, claim, chat, load test, deployment or push was performed.

## Implemented contract

- A signed Snap GET to `/v1/benefit-gateway-config`, using
  `Agent-Type: PromptCenter`, precedes legacy benefit claim/config calls within
  the existing source budget.
- Only HTTP 200 and an unambiguous boolean `enabled:false` authorize `disabled`.
  Closed sources remove their old contribution and seed routing for the captured
  account/auth, skip claim/config and clear failure backoff. Normal Agent/builtin
  entries retain their exact IDs and ordinary route.
- HTTP 202/206, unavailable/malformed/missing/duplicate switches and cancellation
  cannot revoke previous evidence. A failed query after confirmed closure cannot
  resurrect removed routes. Account/auth identity and scoped negative-reset
  revision safeguards remain in force.
- HTTP-success provider refusals use `business`; actual HTTP errors retain their
  numeric status even with invalid error bodies. Only reviewed provider codes,
  including `4000`, are retained. No arbitrary provider message/body is exposed.
- The panel labels confirmed closure, the confirmation timestamp and a skipped
  claim; it does not present closure as a failed pull or retry countdown. Business
  4000 and HTTP 503 remain distinct. Closure is not inferred from code 4000 alone.

## Regression evidence

Backend fixtures cover close/reopen/failure transitions, ownership replacement,
per-account isolation, contribution/seed removal, safe diagnostics, source/claim
deadlines and signed path/header behavior. Server-route tests first retain prior
benefit data, then confirm closure while returning the exact V4.1 Flash ID from
Agent or builtin discovery. A real localhost chat subsequently succeeds without
`maas_type`, clears only the old eligible rejection, and does not bypass a new
real provider rejection with an alternate route.

The complete real-Chrome suite passed with the new disabled/business/HTTP states,
desktop/mobile layout, existing account/OAuth/audit/connection actions, safe HTML
escaping, model refresh and polling/login isolation. Embedded JavaScript syntax
also passed. The parent visually inspected all four new screenshots:

- `screenshots/model-benefit-disabled.png`
- `screenshots/model-benefit-disabled-mobile.png`
- `screenshots/model-benefit-business-error.png`
- `screenshots/model-benefit-http-error.png`

The fixture regenerated older screenshots with current timestamps; those tracked
PNG-only changes were restored, retaining only the four relevant new captures.

## Bug analysis and prevention

1. Root category: implicit protocol assumption and cross-layer contract gap.
   Legacy benefit endpoints were assumed always applicable; business rejection
   was folded into transport failure, preventing useful diagnosis.
2. Previous fix limits: source-aware retention solved transient loss but did not
   discover the newer per-account switch. Safer retention alone cannot remove a
   route after a real authoritative closure.
3. Prevention: strict gate authority at the upstream owner, a shared successful
   source predicate for publication/totals/freshness, real HTTP regression tests
   and explicit backend/panel specs. Independent review also exercises accepted
   and partial HTTP responses so an incomplete switch cannot delete routes.
4. Expansion: claims and config share the source deadline; claims remain a
   separate policy/result. Similar parser failures retain last-known same-auth
   contributions rather than manufacturing an empty catalog.
5. Knowledge: `backend/model-discovery.md` and `backend/admin-panel.md` now state
   these executable boundaries and the business-versus-HTTP error contract.

## Check and delivery record

Final integration checks passed:

- `go test ./... -count=1`
- `go vet ./...`
- `go build ./...`
- Full upstream/server/pool test and race suites
- `python -B scripts/test_release_tools.py` (15 tests)
- Complete real-Chrome fixture and embedded JavaScript syntax

After the independent strict-status correction, the checker reran the complete
upstream normal/race suite, related server/pool normal tests and the new actual
benefit-route denial regression in both normal and race modes. The parent then
ran the final full `go test ./... -count=1`; all packages passed. Final
`go vet ./...`, `go build ./...`, embedded JavaScript syntax and context-manifest
validation passed. No product changes followed the final review.

Independent Trellis review ended with no remaining code blocker. All four new
screenshots were inspected by both panel/check workers and the parent. The
parent reviewed explicit owned text for service/private-key patterns and staged
only task-owned files, leaving bootstrap/platform files and ZIPs untouched.

Go checks use temporary Go 1.27.3 and MinGW; no Go 1.22-specific or Docker runtime
check is claimed. Remote rebuild and actual account/V4.1 results remain pending.

## Verified follow-up release

- Product commit: `709f63ede82f8f790a853a4550f9430a7e1b2cfc`.
- Artifact: `codearts2api-source-709f63ede82f.zip`.
- SHA256: `fd0be2f5910418c7144936d5f196d2fa98ba8743ee7ffe3b5bc0f54bcef9cbb6`.
- 107 ZIP members, including 106 hashed product/generated identity members.
- The source export includes the actual reviewed benefit gate, panel, tests,
  Compose, Dockerfile and configuration examples, excluding live configuration,
  credentials/data, platform/task files and prior archives.

`verify-source-release.py` verified every hashed member, extracted this final
archive into a temporary directory, compiled the actual server with its source
identity, started it with isolated empty credentials/data and ran the verifier
packaged inside the ZIP. Health, console, build, requests, audit, models and
connections all passed. It sent no chat, refresh or claim operation.

Both previous archives remain unchanged:

- User-owned `codearts2api.zip`: SHA256
  `7934b2e7827f5f4a685b9f2e367385752b4eb9aec063eea75b563f87c601a84c`.
- Prior verified `codearts2api-source-365f4828d019.zip`: SHA256
  `eeeeeed3037704008fda0ff015f8667ecca1275a72d68bc35b9994feb040ff7e`.

Rebuild/recreate from the new source in the original deployment directory,
preserving `.env`, `config.json`, `auths/`, `data/` and the same mounted paths.
Use the packaged `release.env` as documented in README. Then invoke Models ->
重新拉取 and inspect the actual per-account source results. A cache-only GET or
successful localhost fixture cannot establish the production account's switch,
provider code, exact Flash entitlement or real completion. Keep the operational
task open until that evidence exists; no deployment or push has been performed.
