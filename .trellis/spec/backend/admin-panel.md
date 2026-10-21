# Embedded admin panel

## 1. Scope / Trigger

Read this before changing `internal/server/panel.html`, `panel.go`, panel navigation,
account controls, OAuth presentation or request-audit rendering. The panel is a
self-contained Go-embedded asset; use the existing admin API and request-audit
contracts. Reference New API for general workspace styling, using original markup,
styles and inline icons without importing its frontend dependency stack.

## 2. Signatures

`(*Handler).servePanel(http.ResponseWriter, *http.Request)` serves `/`, `/admin`,
`/panel` and `/panel/`. Existing branding placeholders are substituted before
delivery. The app uses the existing service-scoped API-key localStorage entry and
`Authorization: Bearer` API requests.

Navigation uses allowlisted hashes: `#overview`, `#accounts`, `#requests`,
`#models`, `#settings`. `data-page` controls select `data-view` sections. Preserve
operational element IDs or update their renderer, listener and verification
together. `/admin/api/overview` and `/admin/api/requests` keep their existing
response schema, filters and exclusive sequence cursor.

## 3. Contracts

- Keep shell/header/sidebar distinct from the active page. Use neutral surfaces,
  restrained blue selection, semantic status colors, compact legible controls,
  visible focus, reduced-motion support and a dismissible mobile drawer.
- Hash routing must not replace the API base path. Root panel/admin aliases use
  root APIs; `/proxy/panel/` uses `/proxy/admin/api/...`. Unknown hashes resolve to
  overview. Navigating/reloading/deep-linking cannot duplicate event handlers,
  refresh timers or OAuth polling.
- Associate audit rows and sequence cursors with their filter query. Changing
  filters clears old rows/cursors before loading; a failed new query cannot offer
  pagination from the previous query. A failed refresh of the same query may
  preserve its existing rows, with explicit error feedback.
- Theme preference is independent of service authentication. No external fonts,
  icon CDN or runtime asset dependency is required to render any view.
- Keep all existing account actions and OAuth start/copy/open/import/poll flows
  reachable. Sign-in and keepalive labels must describe their actual endpoints;
  server string substitution must not silently rename them.
- Hidden views have no tabbable descendants. Mobile navigation and OAuth dialogs
  support Escape and focus return. Dialog polling stops on close. Labels and
  semantic buttons/forms must make filtering work with a keyboard.
- Render only returned data or explicit derived summaries. Catalog discovery does
  not prove an account has model entitlement; a bounded request window is not an
  all-time traffic metric. Empty/unknown values stay honest. Missing schedule-hour
  metadata is unknown; an explicit empty array is unconfigured. A diagnostic
  known to be hard-coded must not be labeled as an effective runtime setting.
- Escape every dynamic model/account/provider/error field. Current availability
  and historic failure are separate; committed HTTP 200 and failed execution
  remain separately visible. Preserve audit loss/archive/disabled diagnostics.
- Attribute escaping must preserve exact identifiers, including backticks, for
  copy buttons. An in-flight operation from a previous login epoch cannot render
  new metadata, start follow-up requests or overwrite logout feedback.
- Tables own necessary horizontal scrolling; long data must not widen the whole
  page on mobile. API failures should have visible feedback, and pending requests
  must not enable duplicate destructive controls.

## 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| No valid key | Reachable login form, meaningful API error, no metadata leak |
| Unknown hash | Overview shown with matching navigation |
| Prefixed panel path | Requests preserve the prefix exactly once |
| Narrow viewport | Operable drawer, local table scroll, no document overflow |
| Empty accounts/models | Clear empty state and reachable relevant action |
| Audit disabled/unavailable | Honest disabled/error state with no stale success claim |
| Filter change fails | Old query rows/cursor cleared; no mixed-query pagination |
| Same-query refresh fails | Existing rows may remain with visible error feedback |
| 200 plus stream error | HTTP 200 retained alongside failed result/cause |
| Uncommitted disconnect | No fabricated status code |
| Malicious display text | Text escaped; no element/event handler executes |
| OAuth/drawer dismissed | Focus returns appropriately and temporary work stops |

## 5. Good / Base / Bad Cases

- Good: select Requests, filter an account and inspect status, safe cause and
  timings; inspect account history separately from its present capacity.
- Base: initial overview and direct `#accounts` reload work without changing the
  deployed routes or credential/data mounts.
- Bad: a new sidebar must not hide an old operation, duplicate IDs/listeners,
  invent a traffic chart or make all table columns overflow the phone viewport.

## 6. Tests Required

Use the task's `research/browser-check.cjs` with installed Chrome/Playwright and
synthetic localhost APIs. Verify login/logout, every view/deep link, mobile
navigation/keyboard focus, relevant account and OAuth flows, audit cursor/filters,
safe rendering, light/dark state, empty/unknown/disabled/error views and base-path
aliases. Inspect actual desktop and mobile screenshots and browser errors; a VM
DOM stub alone cannot validate layout, visibility, focus or CSS overflow.
Include exact identifier clipboard copying and logout during an in-flight refresh
so asynchronous responses cannot cross authentication state.

Run relevant Go server tests and a build because the HTML is embedded. Check JS
syntax and `git diff --check`. Broaden Go/race checks when the change actually
affects concurrency/backend behavior; presentation-only changes do not warrant
repeating unrelated stress suites. Never use real credential files, production
traffic or administrator mutations for UI fixtures.

## 7. Wrong vs Correct

Wrong: copy an entire frontend application, replace account data with attractive
fake charts, or add a styled clickable span that cannot receive keyboard focus.

Correct: compose the existing server data/action functions into original static
views, derive honest summary values and use semantic controls with real-browser
verification of both function and appearance.
