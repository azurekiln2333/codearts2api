# Research: OAuth console flow and callback submission

- Query: Simplify the OAuth dialog to one primary action, clarify authorization-link copying and callback entry, and explain failure of automatic browser callbacks.
- Scope: internal; panel state machine, matching server DTOs and synthetic browser verification.
- Date: 2026-10-24

## Findings

### Files found

| File | Description |
|---|---|
| `internal/server/panel.html` | Embedded OAuth dialog markup, start/import/poll controls and lifetime ownership. |
| `internal/server/oauth.go` | Start, polling, validated loopback callback import, completion/continuation response shapes. |
| `internal/server/handler.go` | Direct callback route, completed-session publication and callback HTML. |
| `internal/upstream/client.go` | Authorization-link creation and loopback redirect_uri for code exchange. |
| `internal/server/oauth_test.go` | Real-route backend tests for imported code, direct callback and continuation. |
| `internal/server/oauth_callback_host_test.go` | Existing explicit-port callback-host compatibility expectations. |
| `research/browser-check.cjs` | Current broad Chrome fixtures; OAuth fixture misses real completion semantics. |
| `.trellis/spec/backend/admin-panel.md` | Existing operation reachability, focus, async ownership and browser-check contracts. |

### Confirmed behavior and defects

1. The dialog presents five actions at once: copy/open/import/poll/start. Copy
   says only 复制链接, while the instructions never identify a single action after
   pasting a callback (`panel.html:321-322`). The actual copy handler copies
   `oauthUrl`, meaning the authorization/continuation URL, never the callback
   (`panel.html:1055`). The requested label 复制授权链接 accurately describes it.

2. The same `oauthBusy` flag represents foreground start/import and silent
   polling. Every 2.5-second poll sets it true and calls updateOAuthControls,
   disabling all start/import/poll buttons until the request completes
   (`panel.html:981-985`, `1011`, `1033-1036`, `1048`). This causes unnecessary
   disabled-state changes while the operator is preparing a submission and can
   prevent the click when upstream ticket polling is slow.

3. There is a real success-contract bug. A successful code import returns
   `{ok:true,status:"done",message,account}` and deletes its session
   (`oauth.go:343-359`). The UI only checks `ok`, clears the callback, and schedules
   a new poll (`panel.html:1025-1028`). The next poll finds the deleted session and
   returns 404 (`oauth.go:259-262`), but silent poll errors are swallowed
   (`panel.html:1047`). A credential can therefore be saved while the dialog fails
   to show completion or refresh accounts.

4. Import has two legitimate results, not a single generic "received" result:
   final code import is `done`; an intermediate portal-secret callback is
   `continue` with an HTTPS portal `next_url` (`oauth.go:361-378`). The latter must
   keep the session, present/open the continuation URL, clear only the consumed
   callback and continue detection. Its returned redirect is validated against
   the configured portal host and ticket. Do not loosen those backend checks.

5. The browser fixture hid the defect: import responds only `{ok:true,message}`
   and then leaves a session that the mock poll completes
   (`research/browser-check.cjs:139-141`). Its test clicks the separate import
   button and accepts the intermediate text rather than requiring final success
   (`research/browser-check.cjs:366-373`). Backend tests already assert real import
   status `done` (`oauth_test.go:62-69`) and continuation `continue`/next_url
   (`oauth_test.go:234-242`). UI fixtures should mirror those DTOs.

6. Direct callbacks are supported when the browser can reach the gateway at its
   loopback callback URL: the handler saves the account and marks the session done,
   which polling observes (`handler.go:227-228`, `291-297`; `oauth.go:264-270`).
   On a remote deployment, the browser's `127.0.0.1` is the operator's own machine,
   not the deployed server. The current authorization protocol is loopback based;
   the exchange uses `http://127.0.0.1:{port}` (`internal/upstream/client.go:242`).
   The panel cannot obtain a failed cross-origin browser address automatically.
   Therefore the dialog must not promise remote auto-callback from polling alone.

7. Existing callback-host comments are misleading: `cmd/server/config.go:23-26`
   and `handler.go:59` imply a public callback host/automatic ticket fallback, but
   `oauth.go:217-220` only rewrites the port. The parallel callback researcher also
   identified that the session port is not rewritten alongside it. Treat backend
   corrections as separately owned and synchronize the final UI explanation with
   the actual supported contract; do not infer that a public-host configuration
   can redirect native loopback traffic to a remote machine.

### Recommended minimal state flow

| State | Primary action and behavior | Supporting controls and feedback |
|---|---|---|
| Idle / terminal failure | 发起授权 starts a fresh session; prevent duplicate starts while its request is pending. | Hide/disable callback entry and authorization-link controls until a link exists. |
| Awaiting browser / callback | Same primary button becomes 提交授权. It submits the pasted callback, not a second start and not an ambiguous "I authorized" signal. | 复制授权链接 and 打开授权页 remain clearly labeled. Auto-detect a reachable direct callback. Without a pasted URL, explain that direct completion needs no submission; for a failed loopback page, paste its entire address. |
| Foreground submission | 正在提交…; prevent duplicate import. | Preserve the input on recoverable validation/exchange error, restore 提交授权 and focus/error-help as appropriate. |
| Continue | Keep 提交授权 for a later callback; keep same session and poll. | Replace displayed authorization URL with returned next_url; say "回调已接收，请继续打开授权页。若再次停在127.0.0.1，粘贴新的完整地址后提交。" |
| Done from import or poll | One shared success finalizer stops timers, clears session/callback, renders success, refreshes account data once and dismisses consistently. | Do not poll a session that final import already deleted. |
| Session expired / terminal poll failure | Stop timers and return primary to 发起授权. | Clear stale URL/session and show why a new authorization is needed. |

Remove the separate 提交回调地址 and 我已授权 elements/listeners. Retain operation
reachability through the single primary and automatic polling, updating the spec
and tests with the changed operational IDs.

Suggested short instructions:

1. 点击「发起授权」，再打开授权页，使用目标账号登录。
2. 服务器能收到回调时，会自动完成；无需再次提交。
3. 若授权页停在127.0.0.1或显示无法访问，复制地址栏的完整地址，粘贴到下方并点击「提交授权」。

Input label: 授权回调地址. Help: 粘贴浏览器地址栏中的完整127.0.0.1地址，包含问号后的全部参数；不是上方的授权链接。
Keep exact callback content out of status/toasts/logs; it contains sensitive
authorization data. Existing textContent/escaping and URL scheme checks remain.

### Polling and ownership implementation guidance

- Separate foreground action state from background in-flight poll state so silent
  polls do not flicker-disable the primary button. Maintain one in-flight OAuth
  API operation, or explicitly queue a manual submit behind the current poll;
  never start simultaneous mutating ticket-poll/import operations accidentally.
- A manual submit can mark foreground pending immediately and suspend future
  poll scheduling. If a poll is already running, let its result settle; final
  success wins, otherwise submit the captured callback under the same generation.
  Do not report a skipped click merely because a silent poll was in flight.
- Prefer one completion-scheduled timeout rather than a fixed interval, with no
  catch-up parallel calls. Respect close/logout generation checks at every await.
- Silent transport failure should yield a concise recoverable detection message
  and keep a retry scheduled, rather than leave a permanent spinner or silently
  lose completion. A definite expired-session response is terminal. Do not let a
  background pending response overwrite an import validation error or foreground
  status that the user needs to act on.
- Keep the existing oauthGeneration checks, stopOAuthPoll cleanup, Escape/focus
  return and logout close (`panel.html:971-998`, `909`, `1056-1058`). A late start,
  import, poll or account refresh after close/logout must not open a popup, change
  another login's UI, restart polling or schedule a close timer.
- window.open is called after an awaited start/import (`panel.html:1004-1010`,
  `1023-1027`), so popup handling is browser-dependent. Preserve explicit
  打开授权页 as the reliable user-click fallback and make the text say so.

### Meaningful browser regressions

1. Initial primary is 发起授权; after start it is 提交授权. Removed button labels
   are absent; 复制授权链接 copies the displayed authorization URL exactly.
2. Import code returns actual `status:done`, invalidates the mock session, and any
   later poll would return404. Assert success, account refresh once and no later
   poll/import/start from that session.
3. Import secret/redirect returns `continue`/next_url. Verify correct displayed
   continuation, same session, continued detection and optional second callback.
4. Direct callback completion through polling needs no manual import; the primary
   is not required to signal "I authorized".
5. Hold a poll response while pasting/clicking submit. Assert the primary remains
   usable, one queued import at most, no request overlap and no lost click.
6. Validation/exchange error preserves input/session and displays actionable
   feedback; expired session resets to start. Recoverable detection errors keep
   retrying visibly without erasing import errors.
7. Late start/import/poll after close/logout cannot reopen a window, write status,
   refresh another login or schedule a new timer. Focus trap, Escape and focus
   return remain correct.
8. Actual desktop/mobile screenshots cover idle, pending, pasted callback,
   continuation and success/error; buttons fit with local dialog scroll and no
   document overflow. Test proxy base path and no browser errors/external assets.

### Related specs and external references

- `.trellis/spec/backend/admin-panel.md:49-54`: all OAuth operations reachable,
  close cleanup and keyboard behavior; update to allow the requested unified
  primary while preserving capabilities.
- `.trellis/spec/backend/admin-panel.md:63-68`, `88-92`: escaping, login ownership,
  request bounds and late-response suppression.
- `.trellis/spec/backend/admin-panel.md:125-148`: real Chrome, visual checks,
  relevant server/embed build and no production or credential fixtures.
- No external sources were required or used; findings are grounded in the current
  checked-out source and existing executable tests.

## Caveats / Not Found

- No credential/config secrets were read, no production access or real OAuth
  request was made, and no source/spec files were modified by this research.
- Remote native-loopback automation cannot be claimed from a UI cleanup. A local
  bridge/listener or different provider-supported redirect contract would require
  explicit architecture work beyond this narrow change.
- Backend callback-port behavior is being traced independently; use the final
  callback researcher findings before implementing a port fix or changing helper
  wording. Current report identifies the UI completion defect independently.
- This research does not close historical R13 stability attribution or establish
  deployed production behavior.
