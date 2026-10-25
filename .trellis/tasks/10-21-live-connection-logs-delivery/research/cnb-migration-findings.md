# Research: cnb2api console migration preservation and ownership

- Query: Inspect the current login, boot, console and browser/release fixtures; identify behavior that the requested cnb2api-panel visual migration must preserve and propose implementation ownership.
- Scope: internal; reference-project visual research is delegated separately.
- Date: 2026-10-25

## Findings

### Source inventory and current authentication

- `internal/server/panel.html:217` defines `bootView`, initially visible, with a status announcement and “正在连接服务…”. `loginView` follows at line 218; `loginForm`, `keyInput`, `loginError`, `loginBtn` are operational IDs. `appView` starts at line 227. These are useful stable migration boundaries.
- `internal/server/panel.go:10` embeds this single asset into the Go binary. `servePanel` at line 13 accepts `/`, `/admin`, `/panel`, `/panel/`, substitutes service name/title/logo/accent and sends `Cache-Control: no-store`. Source HTML changes require a rebuild/recreate to reach the deployed binary.
- `panel.html:327–330` defines service-specific storage keys `codearts2api_panel_api_key` and `codearts2api_panel_theme`, and computes the proxy base by stripping terminal `/panel` or `/admin`. Preserve saved credentials and theme across the redesign.
- `panel.html:446–466` implements key storage and the JSON fetch wrapper. Requests send `Authorization: Bearer` when a saved key exists, preserve the reverse-proxy prefix, and expose HTTP status on thrown errors. Keep API routing independent from visual shell markup.
- `panel.html:881` `showApp` increments `authEpoch`, disables submission, and validates by loading `/admin/api/overview`. Only a matching epoch may reveal appView. On failure it clears the key, returns to login, and distinguishes a 401 from connection failure.
- `panel.html:893` `showLogin` invalidates epochs, stops both polling owners, resets catalog/connection/request/token data, closes OAuth and drawer, clears authenticated content, and focuses the API-key field. A new loading screen must preserve this cleanup.
- `panel.html:914` saves the submitted key before validation. Logout at line 915 clears it. Initial `showApp()` at line 1117 deliberately tries the overview once even without a locally stored key, allowing a reverse proxy to supply authentication. Do not replace startup with a “no key means show login immediately” shortcut.
- Existing boot markup is displayed on initial connection; `showApp` does not currently re-show bootView on a submitted login. A reference-style “正在连接控制台” stage should explicitly transition among login/loading/app for each validation attempt, recover on error, and suppress stale completion using the existing epoch.

### Shell and six-page functionality

`panel.html:331–339` defines the six-page allowlist; navigation and rendering at lines 416–444 use hash routes, `data-page`, and `data-view`.

| Page | Behavior and data that must remain |
| --- | --- |
| overview | Account-pool metrics/current health, model count, API base URL and exact copy controls; no redundant shortcut panel. |
| accounts | Search/status filtering, account detail/history, validate/keepalive/disable actions, claims, OAuth addition and pending-action suppression. |
| requests | Account/model/outcome/HTTP filters, query-owned cursor, bounded recent window, per-request input/output/total tokens, server summary/coverage and explicit unknown values. |
| connections | Active phases, recent completion outcomes, expanded traces, real HTTP versus provider error codes, retention/loss notices and manual refresh. |
| models | Search/exact model copy, explicit pull POST and progress, duplicate suppression, partial/error retention, source attempt/success/claim diagnostics and account-model exclusion information. |
| settings | Effective limits/configuration, automated-task state, authenticated build identity, API base URL. |

Operational anchors include `reqBody`/`requestCard` (line 284), `connActiveBody`/`connRecentBody` (lines 291–292), `btnModelsRefresh`/`modelRefreshStatus` (296–297), `modelSourceDetails`/`modelExclusionBody` (301–302), and OAuth IDs at 319–323. Keep these IDs or update renderers, listeners and fixtures atomically.

Do not restore the previously removed sidebar build/branding block, service-region caption or “工作台快捷入口”. Build details belong in authenticated settings. The request to use cnb2api-panel supersedes the previous New API visual reference, but does not imply dropping operational pages or changing backend contracts.

### Async ownership, semantics and accessibility

- `authEpoch`/overview version prevents prior-login responses from rendering; late mutation continuations must not start reads in a new session.
- `catalogState` and `connState` (`panel.html:341–342`) have a version, timer and AbortController. Retain cancellable snapshots and completion-scheduled polling with at most one future timer. Connections poll only while authenticated, active and visible; models poll only while a refresh runs. Navigation, visibility changes and logout stop obsolete work.
- Request rows and cursor belong to the current query. Filter changes reset old rows/cursor; only same-query refresh errors may retain rows with visible error. Summary comes from the server, never a sum of loaded pages.
- Token source distinguishes native `upstream` from `estimated`; native zero is known zero, absent/invalid counts unknown. Keep recent-window scope, matching count, coverage, truncation/overflow and pagination independence. Credential expiry wording is “凭证剩余”.
- Existing `escapeHtml`/`escapeAttr` (`panel.html:348–351`) and exact clipboard values protect dynamic account/model/provider/error content, including backticks.
- Drawer focus/inert handling at `panel.html:383–414` and route focus behavior at 416–444 must remain. Hidden views cannot expose tabbable descendants. Preserve Escape, focus trap/return, short-mobile modal scrolling, local horizontal table scrolling, reduced-motion behavior, and light/dark mode independent of auth.
- OAuth (`panel.html:973` onward) has a generation, one polling promise, explicit busy/completion states and focus return. Keep a single primary “发起授权 → 提交授权” action; direct `done` finalizes without polling a deleted session; `continue` retains the session/link. Submission queues behind a poll, direct completion wins, close/logout invalidate late callbacks, retryable callback input survives errors. Only explicit user actions open popups. Never echo/store sensitive callback contents in feedback.

### Browser fixtures and release coupling

- `research/browser-check.cjs:174` launches real Chrome with reduced motion and screenshots. It covers six routes, account/model operations, request queries, provider-error escaping, polling/lifecycle, theme, mobile drawer, proxy prefixes and no external assets/page errors. Lines 196–199 assert removed elements and compact corners; the radius check currently assumes `.workspace`, `.stat`, `.nav-item`, `.btn`, `.login-box`, `.modal` exist and are at most 16px. A visual redesign may need a deliberate selector update while retaining the user’s compact-corner requirement.
- `research/browser-check.cjs:18–19` can use a supplied panel URL or checked-in HTML. `research/oauth-browser-check.cjs:15` accepts `UI_PANEL_SOURCE` for extracted-archive HTML. `research/token-browser-check.cjs:10` currently reads only checked-in HTML; add an equivalent override if packaged token regression proof is desired.
- `research/oauth-browser-check.cjs:263` summarizes extensive direct/imported/continue/queued completion, late callback, proxy and short-mobile keyboard regressions. It verifies actual serialized OAuth activity; retain it rather than replacing with cosmetic smoke tests.
- `research/token-browser-check.cjs:129–170` tests held requests, stale filtering/logout and desktop/mobile/short viewport behavior. Its server summary checks are essential to avoid fake frontend aggregation.
- `scripts/verify-deployment.py:20–24` declares required operational IDs and removed IDs. `check_panel` at line 99 parses six views and removed classes/text. Preserve meaningful feature checks; adding a design revision marker alone does not establish behavior or visual parity.
- `scripts/test_release_tools.py:116` builds fixture HTML; line 206 tests obsolete/removed UI rejection and line 266 checks current asset markers. If markers change, update both implementation and fixture with semantic assertions.
- `research/verify-source-release.py` extracts and verifies archive checksums, builds an isolated Go binary, uses empty auths and localhost only, then verifies the served panel/routes/build. It is the appropriate final embedded-asset proof. `scripts/release-source.py:23–24` includes the panel in the source archive.

Recommended expanded login/boot scenarios: no saved key/401, valid saved key, manually submitted valid/invalid key, delayed overview with loading visible, connection error recovery, proxy-injected auth with no key, repeated login/logout, delayed old-session success/failure, and no app flash before validation. Assert exactly one visible auth surface and no permanently blocking overlay. Capture actual reference and implemented login/loading/app at desktop/mobile/light/dark, and visually inspect screenshots. Existing behavioral fixtures alone cannot establish the requested reference fidelity.

### Implementation split without write conflicts

1. Give one implementer ownership of `internal/server/panel.html`: shell/style/markup plus the small boot/auth transition changes. CSS, markup IDs and auth surfaces are tightly coupled; parallel whole-file rewrites risk silently losing existing logic.
2. Give a separate verifier ownership of browser fixtures and deployment-verifier tests after agreeing preserved/new DOM anchors. This work can run alongside implementation; use mocked localhost metadata only.
3. Parent owns design/PRD/spec alignment, final rendered review, integration, release and session bookkeeping.
4. If two implementers are necessary, split by literal source regions and deliver the behavior change as a sidecar patch or integrate sequentially. Do not concurrently rewrite the HTML from stale snapshots.
5. Reproducing appearance does not require a new frontend framework/build pipeline. The current embedded, self-contained HTML can carry the reference design while preserving the backend and offline assets.

## Related specs and workflow

Read `.trellis/workflow.md`, `.trellis/spec/backend/admin-panel.md`, `.trellis/spec/backend/oauth-console.md`, `.trellis/spec/backend/token-usage.md`, and current task PRD/design/implementation notes. Admin-panel visual-reference language should be aligned to the new user instruction by the parent/spec owner. Researcher did not read implementation/check JSONL context or edit source/specs.

## External references (docs, versions)

None required for these repository-specific findings. The reference UI project is being inspected by a separate research agent; this report makes no unsupported claims about its structure or exact design values.

## Caveats / Not Found

- Read-only inspection used no credentials and contacted no production service. Browser fixtures were inspected, not executed by this researcher.
- Historical R13 empty-answer/stall causes and production rollout are separate unresolved work; a UI release cannot claim to close them.
- Current API wrapper has no explicit timeout, so a newly branded loading surface alone does not guarantee bounded login during a truly hung overview. An implementer may add bounded/cancellable validation if required, keeping epoch ownership and error recovery intact; this finding does not establish a reproduction.
- There was no dedicated panel Go test identified by targeted panel-test symbol search; browser and deployment tooling provide the principal UI regression coverage.
