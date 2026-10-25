# R18 browser verification

Date: 2026-10-25. Verification used actual current `internal/server/panel.html`, real headless Chrome, synthetic localhost APIs and isolated browser contexts. No real credentials or production traffic.

## Source changes owned by verification worker

- Added `research/cnb-browser-check.cjs`. Supports `UI_PANEL_SOURCE` and `UI_OUTPUT_DIR` for actual extracted release verification.
- `research/browser-check.cjs` now supports `UI_PANEL_SOURCE`. Its compact-login radius assertion targets `#loginForm` instead of removed `.login-box`; all operational assertions preserved.
- `research/token-browser-check.cjs` now supports `UI_PANEL_SOURCE` and `UI_OUTPUT_DIR`. No behavioral assertion removed.
- Existing `oauth-browser-check.cjs` unchanged.

## Passed commands

```
node .trellis/tasks/10-21-live-connection-logs-delivery/research/cnb-browser-check.cjs
node .trellis/tasks/10-21-live-connection-logs-delivery/research/browser-check.cjs
node .trellis/tasks/10-21-live-connection-logs-delivery/research/oauth-browser-check.cjs
node .trellis/tasks/10-21-live-connection-logs-delivery/research/token-browser-check.cjs
```

All four returned `passed:true` against the final independent-reviewed source. Syntax checks also passed. Final source SHA256 before and after all four runs: `d3f9ac3a35460962ac92199a35c0f1cb4cea7c9f209e580f1b360440fa139f47`. Focused result.json records the same hash.

## Evidence directories

- Focused reference design/auth: `.local-test/cnb-ui-1792926649134/`
- Full console: `.local-test/oauth-full-ui-1792926673062/`
- OAuth: `.local-test/oauth-ui-1792926696099/`
- Tokens: `.local-test/token-ui-1792926718457/`

The focused result JSON is `.local-test/cnb-ui-1792926649134/result.json`.

Focused assertions cover actual held initial/submitted connection stages, no authenticated content before validation, proxy attempt without local key, wrong-key handling, saved key, 15s timeout/late-response recovery, 52px header/180px sidebar/64px collapse, six pages desktop/mobile, dark persistence, drawer Escape and automatic dismissal, short-mobile login, absent redundant chrome, no external requests and no browser exceptions.

Existing regressions additionally cover account mutations/filters/history, catalog pull partial/error/source diagnostics, exact model clipboard values, connection trace/poll cancellation, request query/paging/cursors, HTML escaping, token nullable/estimated/unknown/partial/zero/server-total semantics, OAuth done/continue/direct callback/serialization/error/expiry/close/logout races, keyboard focus and mobile modal scrolling.

## Visual inspection

Opened actual screenshots with image viewer: login-desktop.png, overview-desktop.png, login-mobile.png, boot-desktop.png in focused evidence directory. Split login illustration, restrained input/button corners, inset content/compact sidebar, mobile form and centered connecting row render cleanly. Boot shows exact 正在连接控制台 text. No visible clipping/overflow in inspected screenshots.

The initial no-key401 feedback observation from early evidence is superseded: final implementation handles `hadKey=false`401 silently while preserving initial proxy authentication and explicit bad-key feedback. Focused suite now asserts that first blank login has no error, actual wrong-key submission has visible feedback, reduced-motion spinner stops, and hidden/pre-auth controls cannot enter visible focus order. Final source-review handoff received; all four suites rerun and passed. Latest login-desktop.png has no spurious initial error; bad-key assertion passes separately. Latest dark-shell screenshot also inspected with image viewer, with clean content and navigation styling.

An initial focused test run failed because the design intentionally uses separate desktop `#sidebarCollapse` and mobile `#sidebarToggle`; the focused test now uses the actual desktop control. No product code was changed for this test issue.

Release verification remains parent-owned; run all four scripts with extracted `UI_PANEL_SOURCE` and unique `UI_OUTPUT_DIR` against final packaged source. Current evidence does not claim production deployment or fix historical R13.
