# Repository and operational evidence, 2026-10-21

## Production status

The initial unauthenticated read-only check found `/panel/` serving the old panel (28,500 bytes), `/healthz` 200, `/admin/api/overview` 401, and `/admin/api/requests` plus `/admin/api/audit` 404. The evidence is in `%TEMP%/codearts2api-delivery-check-20261021.json`. This is historical: the user later explicitly reported that a new build revealed the redesigned console. No remote deployment, chat load test or administrator mutation was performed this session. Actual remote build/image remains unverified without server access.

## Model and UI findings

- `chat.go:523-525` returns the reported code/message. `lifecycle.go:192-217` filters healthy account candidates by negative availability and known catalogs. Seeing a model in the official UI does not establish this service's current per-account route/cache state.
- `availability.go:30` uses a 30-minute negative TTL. `upstreamUnavailableReason` classifies not-registered versus missing-benefit errors. It does not classify concurrency/transport timeout as model unavailability.
- `handler.go:539-602` uses one-hour global/account catalog caching and a five-minute failed-fetch cooldown. Refreshing overview is not a force pull; there is no admin catalog refresh route.
- `handler.go:436-446` only installs the filtered list if nonempty. If all entries are known unusable, the original list survives, disagreeing with actual chat routing.
- `upstream/models.go:292-340` merges three sources, logs individual failures and commits a partial merged result. Independent research records specific provenance/benefit-loss and return-value inconsistencies in `model-discovery-findings.md`.
- The request `deepseek-v4.1-flash` is absent from static seeds; dynamically returned exact IDs are supported. The source currently returning the model to the official UI has not been observed, so do not invent an alias or claim an account lacks permission solely from this 404.
- `admin.go:27-28` provides literal service/region metadata. `panel.html:226,245,266` show the redundant header caption, sidebar branding/build block and shortcut card targeted for removal. `renderOverview` and the duplicate `btnAddAccount2` listener must be updated with the markup.

## Live diagnostic hooks from the completed connection_hooks explorer

Admission (`chat.go:134`), body read (`151`), local preparation (`181-239`), account lease (`246-255`), validation (`308`), request preparation (`332-344`), explicit refresh (`360`), retry delay (`374-376`), stream forwarding (`443`), aggregation (`463`), nonstream write (`503`) and cleanup (`244`, `lifecycle.go:68-79`) have narrow instrumentation points.

Upstream HTTP headers wait is `client.go:579`; error-body read is `587-593`; accepted first SSE data is `613-618`; real read progress (`n > 0`) is `stream_body.go:114-120`. Validate's refresh-wait boundary is `pool.go:420` / `refresh.go:60-69`. Do not pass the observer into the shared background refresh goroutine at `refresh.go:72-78`.

There is no upstream session-creation RPC in the v2 chat path; local chat-ID preparation is not network latency. Existing `chat.go:426` firstData can be set after empty EOF/prefix-limit preflight; new diagnostic first-SSE data must use actual acceptance. `http.Do` includes DNS/TLS/connection acquisition, so describe its stage as waiting for upstream response headers. A long live stream is not itself evidence of a hang: show last actual progress separately. Finish only after owned resources are released, reuse the existing final classifier, ignore late updates and bound every list.

## Shape reference

Read the official Material Web shape token source, `material-components/material-web`, `tokens/v0_192/_md-sys-shape.scss` (version v0_192). Its small/medium/large corner values are 8/12/16 px. Use those restrained values as the requested MD3 reference; do not import a frontend dependency or increase the existing radii.

## Existing verification and release inputs

Go and Chrome/Playwright are available under the temporary toolchain locations recorded in the session. Reuse a copy of `.trellis/tasks/archive/2026-10/10-21-newapi-panel-redesign/research/browser-check.cjs` for current-task checks. The pre-existing ZIP matches the old local feature work but lacks Compose; preserve it and create a separately named complete source package. The Dockerfile embeds HTML into the Go binary, so code changes require a rebuild/recreate; `restart` alone keeps the same executable.
