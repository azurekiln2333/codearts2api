# Embedded admin panel

## 1. Scope / Trigger

Read this before changing `internal/server/panel.html`, `panel.go`, panel navigation,
account controls, OAuth presentation or request-audit rendering. The panel is a
self-contained Go-embedded asset; use the existing admin API and request-audit
contracts. Reference the local `cnb2api-panel` login, connecting stage and console design,
using original standalone markup, styles and inline icons without importing its
Vue application or introducing external runtime assets. The R18 reference and
migration reports in the active task record the inspected source and contracts.

## 2. Signatures

`(*Handler).servePanel(http.ResponseWriter, *http.Request)` serves `/`, `/admin`,
`/panel` and `/panel/`. Existing branding placeholders are substituted before
delivery. The app uses the existing service-scoped API-key localStorage entry and
`Authorization: Bearer` API requests.

Navigation uses allowlisted hashes: `#overview`, `#accounts`, `#requests`,
`#connections`, `#models`, `#settings`. `data-page` controls select `data-view` sections. Preserve
operational element IDs or update their renderer, listener and verification
together. `/admin/api/overview` and `/admin/api/requests` keep their existing
response schema, filters and exclusive sequence cursor.

The Requests view consumes per-event `usage` and the response's bounded `usage`
summary under [Generation Token Usage](./token-usage.md). Preserve its
unknown/estimated/upstream distinctions and query/auth ownership; loaded rows
must never become an invented historical total. Account credential expiry is
labeled as credential time, not generation tokens.

## 3. Contracts

- Match the reference shell: 52px full-width header, 180px sidebar (64px
  collapsed), neutral canvas and independently scrolling inset content. Use its
  restrained blue selection, semantic status colors, compact controls, visible
  focus, reduced-motion support and a dismissible mobile drawer.
- Login uses the reference split composition with an original inline gateway
  illustration and a compact form. Mobile removes the decorative column. The
  full-viewport connecting stage says “正在连接控制台” and tracks actual initial
  or submitted authentication validation; never add an artificial delay.
- Authentication validates overview with a 15-second cancellable request. Success
  reveals the shell only for the current login epoch. Failure/timeout returns to
  a usable login. Initial no-key 401 is silent; submitted invalid keys retain a
  visible error. An obsolete response cannot reveal the shell or replace newer
  feedback. Preserve the initial proxy-auth attempt even without a stored key.
- Keep corners at 8 px for controls/navigation, 12 px for cards and no more than
  16 px for workspace/login/dialog shells. Sidebar branding/build footers, the
  service-region header caption and duplicate overview shortcut card are removed.
  Build metadata remains in authenticated runtime settings and API diagnostics.
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
- Keep all existing account actions and OAuth capabilities reachable. Use one
  start/submit primary, explicit authorization-link copy/open and automatic
  polling under [OAuth Console](./oauth-console.md).
  Sign-in and keepalive labels must describe their actual endpoints;
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
- Constrain the application shell to the dynamic viewport height. Page content
  scrolls inside the workspace; neither long source tables nor short mobile
  viewports may create document-level vertical overflow that scrolls away the
  header/navigation. Verify reachability of the final source table and local
  horizontal scrolling as well as absence of document overflow.
- Models reads `GET /admin/api/models`, and `重新拉取` posts `{}` to
  `/admin/api/models/refresh`. Report the actual job/source results, retained stale
  entries and scoped exclusions; never report overview reloading as a new pull.
  The backend DTO/limits are owned by [Model Discovery](./model-discovery.md).
- Benefit `disabled` means the account's upstream switch explicitly closed the
  channel; show it as confirmed closed, without source-failure warnings or retry.
  Explain skipped claim through `skipped_reason:channel_disabled` and point to
  normal Agent/builtin discovery. `business` errors and HTTP errors have distinct
  labels. Safe code 4000 may explain invalid channel/group, but cannot be presented
  as proven channel closure, expired credentials or missing account entitlement.
- Connections reads `GET /admin/api/connections`, showing active phase/age,
  actual upstream read progress and recent optional traces. Missing legacy
  traces stay unknown; accepted SSE data is not labeled as a generated token.
  HTTP response-header wait is not presented as exact TCP-connect duration.
- Each operational view has one cancellable GET and at most one future poll,
  scheduled about two seconds after completion. Connections polls only while
  authenticated, visible and active; Models also requires a running pull job.
  Navigation, visibility changes and logout abort/version out obsolete work;
  a late POST result cannot start a snapshot request in another login epoch.

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
| Token usage absent/partial/estimated | Honest unknown components/source/coverage; no invented zero or exact cost |
| Request pagination | Server filtered-window usage summary unchanged by loading more rows |
| 200 plus stream error | HTTP 200 retained alongside failed result/cause |
| Uncommitted disconnect | No fabricated status code |
| Malicious display text | Text escaped; no element/event handler executes |
| OAuth/drawer dismissed | Focus returns appropriately and temporary work stops |
| Model pull partially fails | Retained catalog plus visible per-source failure and job state |
| Connections GET fails | Preserve prior snapshot with visible error; no false empty success |
| Model/connection view hidden or logged out | Stop polling, ignore late results and clear metadata on logout |

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
Include real model-pull POST/duplicate suppression, source and exclusion tables,
connection trace expansion across polls, old events without traces and late
model GET/POST or connection GET after logout. Exercise visibility events as
well as navigation, and inspect source-result and expanded-trace screenshots.
Assert root width and height against the viewport on every mobile view, scroll to
the final source table inside the workspace, and exercise its horizontal scroll.
Render failed claims even when an omitted JSON `succeeded:false` leaves only the
safe `error` result.

For the R18 design, also run `research/cnb-browser-check.cjs` against the actual
embedded source and extracted release asset. Compare captured login/loading/shell
images with local synthetic reference screenshots; include mobile and dark mode.
Hold the validation response to inspect the real loading stage. Cover saved-key
restore, proxy auth without a saved key, failure recovery, bounded timeout, stale
completion and logout. A correct-looking screenshot alone cannot establish auth
state correctness.

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
