# OAuth console and loopback callback

## 1. Scope / Trigger

Read before changing console OAuth controls, callback-port configuration or
start/import/poll completion. Preserve PKCE/DPoP, session expiry, credential
persistence, portal/ticket validation and account refresh ownership.

## 2. Signatures

- Authenticated `POST /admin/api/oauth/start` with `{}` returns
  `{ok,session_id,auth_url,expires_in}`.
- `POST /admin/api/oauth/import-callback` with `{session_id,callback_url}` returns
  `{ok:true,status:"done",message,account}` or
  `{ok:true,status:"continue",message,next_url}`.
- `POST /admin/api/oauth/poll` with `{session_id}` returns
  `{ok,status:"pending"|"done"|"error",message}`; absent session is HTTP404.
- `GET /oauth/callback` handles a browser-reachable direct native callback.
- `OAuthCallbackHost` / `CA2A_OAUTH_CALLBACK_HOST` preserve legacy naming;
  only an explicit configured port overrides the listener's callback port.

## 3. Contracts

The native redirect is `http://127.0.0.1:{port}/oauth/callback`. A remote
browser's loopback is the operator's machine, not the deployment host. Never
promise public-host redirect or automatic capture of a failed browser URL.
Keep authenticated import as the remote bridge; reachable direct callbacks
still complete through server observation and panel polling.

Choose the effective callback port before constructing the authorization URL.
Use it for URL port, OAuth session, legacy pending login, imported callback
validation and exchange redirect_uri. No explicit override means listener port;
the configured host does not replace the fixed loopback hostname.

Use one primary button: 获取授权链接 before a session, 提交授权 afterward. Starting
creates a session and displays its URL without opening a browser. The operator
chooses whether to copy the link or open it using an explicit user action.
Submission imports the pasted full callback; no separate import/poll buttons.
Automatic detection preserves polling. Copy/open labels explicitly identify
authorization or continuation URLs. Tell operators to copy the complete failed
loopback URL, including all query parameters, paste it and click 提交授权.

Import done may already have deleted the session. Immediately share poll-done
finalization: stop timers, clear session/callback/link, refresh accounts once,
show success and dismiss without waiting for the refresh to finish. Report a
refresh failure so the operator can retry it, unless a newer overview request or
login session has superseded that refresh. Bound that refresh to 15 seconds and
report timeout as failure. Never poll that deleted session.
Continue retains the session, presents validated next_url and explains a possible
second callback. An intermediate portal secret is not completed authorization.

Background polling cannot disable or discard submit clicks. Serialize a queued
callback behind a current poll and suppress future polls during mutation. A poll
success wins; otherwise submit under the same session/generation ownership.
Avoid overlapping intervals. Preserve actionable validation/continuation feedback
across generic pending results. Expiry resets to start; recoverable errors retain
input. Close/logout version out every late response/follow-up. Preserve focus,
Escape, proxy paths, mobile scroll and reduced motion. Never echo callback data
into status/toasts/logs/localStorage. Open links through a reliable user gesture.

## 4. Validation & Error Matrix

| Input/state | Required behavior |
|---|---|
| Idle/start pending | One primary start, no duplicate mutation |
| Link acquired | No automatic browser open; explicit copy and open actions |
| Session and pasted callback | Same primary submits callback |
| Import done/session deleted | Finish once; no later poll/import |
| Account refresh pending or failed after done | Dismiss without waiting; report failure or 15-second timeout |
| Older refresh fails after a newer refresh | Ignore the stale failure notification |
| Continue | Same session, next link and clear instructions |
| Direct callback | Poll completes without manual submission |
| Submit during poll | One queued import; no lost click/overlap |
| Invalid callback/exchange error | Visible retry, preserve input |
| Input edited after empty/validation warning | Clear the obsolete warning, identify the submit action; do not override in-flight/terminal feedback |
| Expiry | Clear stale session/link, offer start |
| Close/logout | No late popup/refresh/restarted timer |
| Listen7866, override8443 | URL/session/pending/import/exchange all8443 |

## 5. Good / Base / Bad Cases

- Good: imported code returns done and immediately refreshes the saved account.
- Base: reachable direct callback completes normally through polling.
- Bad: change URL port alone, ignore done or promise remote loopback automation.

## 6. Tests Required

Actual HTTP start/import/exchange fixtures assert effective ports, PKCE/DPoP and
saved credentials. Preserve direct callback/continuation tests. Real Chrome
synthetic fixtures cover primary labels/copy and explicit-only opening, done with
deleted session, dismissal despite a pending/failed refresh, stale refresh
failure suppression, direct completion,
continuation/second callback, poll-submit serialization, duplicates,
validation/expiry/transport errors, close/logout races, focus, proxy and mobile.
Fixtures return real status DTOs; new screenshots stay outside historical tracked
directories. Run relevant normal/race/vet/build and JS checks. No real account or
production OAuth traffic is required.

## 7. Wrong vs Correct

Wrong: ignore done, poll a deleted session and leave multiple ambiguous actions.

Correct: one start/submit primary, one completion owner, clear remote/manual and
reachable direct-callback behavior, and consistent port identity.
