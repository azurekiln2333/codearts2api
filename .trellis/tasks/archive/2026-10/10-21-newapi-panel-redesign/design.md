# Design

## Boundary and architecture

The behavior gap is a crowded glass/gradient single-column panel where every operational area appears at once. The requested result is the local New API workspace layout and visual treatment. Presentation and view routing live in `internal/server/panel.html`; `panel.go` embeds it and performs branding substitution. Implement there while reusing the existing API/auth/action/audit/OAuth functions. Keep a self-contained HTML asset and the current Go build/deployment path.

Expected product changes: `panel.html` for the new shell, tokens, semantic views, navigation and UI controls; `panel.go` only to remove stale label substitutions that currently rename sign-in actions as keepalive despite calling sign-in APIs. The actual APIs, gateway scheduling and storage are unchanged. Reference project files are read-only. The parent owns task/spec/verification artifacts and commits; the implementer owns product files until handoff.

## Reference and visual system

Follow research/reference-style.md. Confirmed reference targets: 48 px full-width header; 208 px desktop sidebar, optional 44 px collapsed rail, 272 px mobile drawer; inset workspace with small outer gutters; compact 18 px page title; 32 px controls. Use neutral white/charcoal surfaces, thin gray borders, clear text hierarchy, a restrained blue navigation selection and small consistent radii. Remove decorative gradients, blur, lift-on-hover and heavy shadows. Use system fonts and inline line icons, with no CDN, external font or copied image asset.

Support light/dark themes with readable semantic colors; default to the OS preference and allow a simple persistent toggle. Theme state must be separate from the existing service API-key storage.

## Information architecture and interaction

The authenticated app has a full-width header, grouped left navigation and a scrollable content workspace. Views are overview, accounts, requests, models and settings, using allowlisted hash routes (`#overview`, `#accounts`, `#requests`, `#models`, `#settings`) so server routes and reverse-proxy base paths remain compatible. Unknown/empty hashes land on overview. Use one route owner to update active navigation, page title, visibility and mobile drawer state. Repeated navigation cannot add refresh timers or duplicate API listeners.

- Overview: real account/schedulability/concurrency/model summary, pool state and clearly scoped recent-request information if used, plus working shortcuts/connection information. No fake traffic chart, latency or entitlement claims.
- Accounts: contextual add-account control and account operations; readable compact rows, search/status filters if added, concurrency, current status and historic failure details. Retain each original action and stable account identity.
- Requests: compact toolbar with outcome/model/account filters, full metadata table, actual HTTP status versus execution outcome, safe provider reason and loss/archive/pagination diagnostics. Outcome choices are real keyboard-operable controls.
- Models: searchable model inventory using the actual discovered list, with copyable identifiers if added. Discovery is not evidence of individual account entitlement.
- Settings: effective runtime limits, audit limits, build identity, schedule and OpenAI connection information as legible groups. Display-only settings do not imply server-side editing support.

Keep existing element IDs for operational controls when practical. New views use `data-view`; navigation uses `data-page`. Use explicit accessible names, focus styles, field labels, table headers and empty/loading/error states. Mobile navigation must support opening/closing, Escape and navigation dismissal. OAuth remains a modal with its existing callback/import/poll/copy/open flow; restore focus after dismissal. No new nonfunctional decorative control.

## Data and compatibility

Reuse overview and request APIs with the existing Authorization header, local key store and BASE logic. All external strings still pass through existing escaping/textContent; hash/filters never become executable HTML. Keep unknown values honest, account failure history distinct from availability, active cooldown/manual disable intact and committed-200 failed-stream visible as failure. New summaries are derived from returned data and labeled as a bounded recent window when applicable.

No auth or state migration, backend feature, build dependency, runtime network asset, additional production traffic or deployment. Rollback is the prior embedded panel; existing credential/data/config mounts remain usable.

## Verification

Run the embedded script in a real installed Chrome via an isolated localhost fixture server with synthetic APIs. Verify login/logout, allowlisted view/hash routing, account actions, OAuth modal/callback states, request filters/paging, theme/mobile controls and escaping. Inspect desktop/mobile screenshots and check document overflow and browser errors. Validate empty/disabled/unknown states. Run relevant Go handler tests and build because the HTML is embedded. Parent independently reviews the visual output; a separate Trellis checker reviews the final diff and fixes local defects before commit.
