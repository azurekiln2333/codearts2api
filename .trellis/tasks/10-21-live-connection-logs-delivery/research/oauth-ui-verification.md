# OAuth dialog implementation verification

Date: 2026-10-24. Scope: R16 embedded-panel behavior and synthetic browser tests.

## Changes

- One primary `oauthStart` action changes from 发起授权 to 提交授权 for an active
  session. Separate import/poll buttons and their listeners are removed. Copy
  and open controls explicitly identify the authorization/continuation link.
- The dialog has two operation steps, a short remote-loopback explanation and
  callback-specific helper text. An empty/invalid-input notice updates when the
  operator edits the input; busy, continuation and unrelated server feedback
  remain owned by their operation. Auxiliary text has a scoped modal-help style.
- Import `status:done` shares poll completion: stop timers, clear sensitive
  callback/link/session fields, refresh account data once, show success and close.
  It never polls the session that the successful backend import deleted.
- Import `continue` keeps the session and explicitly exposes the next browser
  step. It explains that a second callback may need to be pasted. Links open only
  through a user click; start/import no longer attempt asynchronous popups.
- Polling uses one completion-scheduled timeout and one in-flight promise.
  Background detection does not disable the input/primary action. Submission
  captures one click, suppresses future polls and waits for the current poll;
  direct completion wins if that poll succeeds. No concurrent import/ticket
  exchange is introduced. Generic pending results preserve actionable feedback.
- Expiry clears stale state and offers a new start. Recoverable submission errors
  retain input, without reflecting callback contents into feedback. Close/logout
  generation guards suppress late responses, refreshes, popups and new timers.

## Executed checks

| Check | Result |
| --- | --- |
| Parse embedded script with Node `vm.Script` | PASS |
| `node --check research/browser-check.cjs` and new OAuth fixture | PASS |
| Full original Chrome fixture | PASS; `.local-test/oauth-full-ui-1792786076274/` |
| Existing token Chrome regression | PASS; `.local-test/token-ui-1792786076344/` |
| Final OAuth Chrome fixture after helper/input refinement | PASS; `.local-test/oauth-ui-1792786435460/` |
| `go test ./internal/server -run 'TestOAuth\|TestPortOfCallbackHost\|TestPanel' -count=1` | PASS, 0.155 s |
| Final `go build ./cmd/server` | PASS |
| `git diff --check` | PASS; only repository LF/CRLF notices |

The broad and token browser runs preceded the final modal-helper styling and
input-notice refinement. The final dedicated Chrome run and embedded-server
build cover those final refinements. No generation-token behavior changed.

The new `research/oauth-browser-check.cjs` covers initial/started labels, exact
clipboard contents, no ambiguous controls, real done DTO/session deletion and
one account refresh, direct completion without import, continuation/second
callback, queued poll-submit and a direct-completion winner, duplicate start/
import suppression, validation errors/transport recovery/expiry/retry, callback
feedback privacy, close and logout races, explicit-only popup, proxy prefixes,
desktop/mobile/short viewport local scrolling, keyboard focus and zero external
requests or browser errors. A synthetic server measures at most one overlapping
OAuth operation. Historical tracked screenshot directories were not overwritten.

Visually inspected final `oauth-pasted-desktop.png`, `oauth-idle-mobile.png` and
`oauth-continue-mobile.png`; the completed field no longer displays the prior
empty-field error, supporting text is visually subordinate and the three actions
fit in the narrow dialog. The dialog itself scrolls at short mobile heights.

To verify an extracted release asset, set `UI_PANEL_SOURCE` to the absolute
`internal/server/panel.html` path from that extraction and run the same script.
Optional `UI_OUTPUT_DIR` chooses a new scratch screenshot directory. By default
the fixture reads the repository panel and creates a unique `.local-test/` path.

No real credential/config files were read or modified. All API/OAuth traffic was
synthetic and loopback-local; no real authorization, production inference or
deployment was performed. Remote native-loopback automation remains unsupported
without a separate local bridge or provider-supported redirect contract. Parent
owns final independent review, full integration gates, commit and release export.
