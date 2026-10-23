# Official CodeArts session lifecycle: confirmed protocol and direct verification

Date: 2026-10-23 Asia/Shanghai. Researcher inspected public official assets and
the installed official client only. The parent owns real-account measurements.
No credentials, account inference or product edits were performed by researchers.

## Result

HTTP EOF is not the entire official CodeArts session lifecycle. The official
26.10.0 HC client reports a root session as busy, periodically renews it, and
reports idle when finished, errored or canceled. The inference request and the
heartbeat both identify that root session with **`user-session-id`**.

The current gateway sets Chat-Id and Session-Id but omits this header and the
session lifecycle API. Those identifiers are not proven substitutes.

The parent then performed the decisive real-account test: signed busy HTTP200,
inference with the same user-session-id HTTP200 and actual content, then signed
idle HTTP200. Ten immediate sequential DeepSeek calls on account-3 all returned
actual content, with all busy/idle responses HTTP200, approximately 1.1-2.1s each.
Sanitized parent evidence is ignored
`.local-test/results/official-managed-session-repeat.json`.

The earlier control on that same account had three successful fully drained
HTTP-EOF requests followed by TM.00001041. A minimal inference body and removal
of Chat-Id/Session-Id/cache options did not eliminate that pattern. This control
and the successful managed-session sequence support implementing the official
lifecycle, rather than treating a twenty-second cooldown as the root repair.
They do not yet establish end-to-end repaired gateway or concurrency success.

## Primary sources and provenance

1. Official download page: `https://codearts.huaweicloud.com/download.html`.
   At inspection it linked official CodeArts Agent26.10.0 packages, including
   `https://codearts-agent-obs-cdn.huaweicloud.com/codearts/ai/latest/codearts-agent-26.10.0.zip`.
   Selected official Mac extension bundles were retrieved by bounded HTTP Range
   requests into ignored `.local-test/public-research/official-26.10.0/`, without
   downloading or running the entire application. They confirm the HC provider
   base route and current package version; the decisive lifecycle implementation
   is embedded in the kernel below.
2. Installed official Windows CodeArts Agent26.10.0, discovered through desktop
   shortcut/installation metadata at
   `D:/ProgramFiles/CodeArts Agent/resources/app/extensions/vscode-codebot/`.
   `agentkernelServer-win32-x64.exe` was **read**, never executed.
   SHA256: `0cf0f9f1611cf4688a85be4e3fe04b7b50283de940ac9b34f7e4dfd7dc19ab41`.
   Eleven bounded source segments and byte offsets are stored only in ignored
   `.local-test/public-research/official-win-26.10.0-session-lifecycle.json`.
   No vendor bundle or binary belongs in the source commit/release.
3. Official Huawei user guide, updated2026-09-17:
   `https://support.huaweicloud.com/intl/zh-cn/usermanual-codeartsagent/codeartsagent_ug_0017.html`.
   It specifies at most three parallel sessions for the experience tier and five
   for basic/professional, shared by the user's IDE/VS Code and other tools.
   This corroborates the account-level nature of the limit; it does not alone
   explain lifecycle release and does not authorize a gateway concurrency cap
   greater than the account's actual allowance.

## Exact remote heartbeat request

- Method: **PUT**.
- Origin: `origin(inferhubBaseUrl)` (same provider origin as the selected
  account's inference endpoint; no `/api/v2` prefix remains).
- Path: **`/snap-manager/v1/chat-session/heartbeat`**.
- Query: **`status=busy`** or **`status=idle`**.
- Body: JSON **`{}`**.
- Header **`user-session-id`**: exactly the root session ID also sent on inference.
- Standard official HC request headers include `Content-Type:application/json`,
  `Accept:application/json`, `x-client-type:kernel`, `X-Language:zh-cn` and a
  fresh `x-snap-traceid`.
- Uses the official HC credential client and temporary AK/SK signing with the
  security token. In the gateway, set relevant headers and final query before
  calling the existing `signRequest(req, raw, cred)`.
- No `maas_type` is needed for heartbeat. Normal and benefit inference use the
  same root-session lifecycle; benefit differs by its inference maas_type header.

Binary evidence offsets (decimal):

| Segment | Start | What it establishes |
|---|---:|---|
| inferhub_headers |105242915|Inference root/user-session-id mapping; normal/benefit headers|
| heartbeat_endpoint_and_request |107334722|Remote path, status query and root session header|
| heartbeat_plugin |107336470|Immediate busy,30s renewal, idle cleanup|
| signed_put_wrapper |110960659|PUT with JSON body, query and HC signed client|
| hc_config_helper |111911282|HC scenario/environment predicate|
| root_session_resolver |112635147|Parent-root lookup and current-session fallback|
| error_idle |116845730|Provider error/abort transitions to idle|
| runner_idle_cancel |116891820|Completion/cancellation transition to idle|
| plugin_registration |107374870|Heartbeat plugin registered in HC environment|
| event_dispatch_is_async |115218396|Event dispatch does not await each async hook|
| prompt_busy_order |116919850|Prompt sets session busy before running work|

## Ownership and ordering boundaries for implementation

The official root resolver uses parentID when present, with the current ID as the
root fallback. For a gateway attempt, inference and its busy/idle notifications
must share the same explicit identity and selected-account/auth ownership. Do
not substitute an unrelated derived Session-Id or use the next rotated account's
credentials to clean up the previous account.

The official event bus invokes async hooks without awaiting their promises;
static source therefore does not prove the busy HTTP request has completed
before inference starts. The parent's diagnostic explicitly awaited busy and
proved this ordering works. A gateway may choose deterministic ordering within
the existing startup bound; that is an intentional implementation choice.

Finish/error/cancel must reach bounded idle cleanup even when the downstream
request context is canceled. Keep cleanup ownership explicit and do not release
the local account lease before required remote cleanup. Use a separate bounded
cleanup context with synchronized credential snapshots. Cancellation, failed
heartbeat and provider errors must not create unbounded goroutines, requests or
retry loops. Preserve real response errors; cleanup errors should be visible in
bounded diagnostics without replacing successfully delivered content.

Thirty-second busy renewal belongs to an active managed session and must stop
on every terminal path. Real long generation tests must verify renewal and
idle resource cleanup, while repeated short calls must verify more than three
successes. Account rotation/retry needs its own balanced lifecycle per attempted
session. Do not let late idle from one concurrent handler mark another active
handler's shared upstream session idle: either unique attempt/session ownership
or shared-reference ownership is required.

## Required next verification

- Full repaired gateway: lowercase and display-case model inputs, stream and
  nonstream, actual content, more than three repeated calls, accurate audit IDs.
- Cancellation, timeout, preflight capability refusal, TM and token-refresh
  paths: bounded idle calls, no local slot/ticket/timer/goroutine leak, later reuse.
- Long successful stream beyond30s: renew busy without truncating output, stop
  renewal, send idle at terminal completion.
- Concurrent distinct calls at the configured per-account cap: distinct or
  correctly reference-counted root ownership; one completion cannot free another.
- Progressive actual concurrency with health responsiveness. Official session
  management prevents leaked upstream occupancy; it does not make account quota
  unlimited. Keep bounded upstream-busy admission as a separate scheduler concern.
