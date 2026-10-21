# Timeout investigation evidence

Date: 2026-10-21. Repository baseline: `8d1e43d` on `main`.

## Reproduction

Run from the repository root:

```powershell
& 'D:\AppData\Local\Temp\codearts2api-toolchain-go1.27.3\go\bin\go.exe' run ./.trellis/tasks/10-21-deepseek-timeout/research/repro
```

The native environment had no Go executable. A temporary official Go archive was downloaded from the stable release manifest and SHA256-verified; no system installation or repository dependency change was made. The WSL Ubuntu distribution could not start because its virtual disk was missing. Toolchain directory: `D:\AppData\Local\Temp\codearts2api-toolchain-go1.27.3\go`.

Observed output before product changes:

```text
aggregate / OpenAI DONE: READ PAST TERMINAL: a live connection would block here
stream / OpenAI DONE: <nil>
aggregate / CodeArts event done: READ PAST TERMINAL: a live connection would block here
stream / CodeArts event done: <nil>
aggregate / CodeArts JSON DONE: READ PAST TERMINAL: a live connection would block here
stream / CodeArts JSON DONE: READ PAST TERMINAL: a live connection would block here
SSE preflight with constructor timeout 25ms: STILL BLOCKED after 150ms
SSE preflight cleanup after explicit cancel: context canceled
already canceled request while account busy: STILL WAITING after 150ms (configured wait 180s)
pool health after canceled request: healthy=0 cooling=1 (expected healthy=1 cooling=0)
first account busy, second account free: STILL WAITING on first account after 150ms (configured wait 180s)
```

The terminal-reader probe fails immediately on an extra read; a real open HTTP connection would wait there. The cancellation case intentionally uses a supported error threshold of 1 to make account punishment visible after one request; the normal threshold is 3. The harness does not read stored credentials and does not contact provider endpoints.

## Baseline verification

`go test -timeout 90s ./...` passed before changes for cmd/login, cmd/server, internal/pool, internal/scheduler, internal/server, and internal/upstream. Other commands have no tests. Existing tests mostly use finite readers or mock servers that close the response, and route helpers are tested separately from the real HTTP handler. Green baseline tests therefore do not cover the reproduced open-connection/cancellation cases.

## Confirmed fault chain

1. A slow or silent upstream can leave a request in preflight or body reading without a body-idle limit (`internal/upstream/client.go:144`, `:596`). A completed CodeArts stream can also remain occupied because its JSON completion marker is not terminal to the streaming loop; non-stream aggregation always waits for EOF (`internal/upstream/sse.go:406`, `:560`).
2. Later requests queue for up to 180 seconds behind the first healthy account even when another account has capacity (`internal/server/handler.go:728`, `:737`). The polling admission helper ignores caller cancellation (`internal/pool/pool.go:519`).
3. Queue/TPM responses trigger up to 30 attempts and 10-second sleeps per account, plus 30-second reacquisition waits and account rotation (`internal/server/handler.go:788`, `:819`). There is no shared startup deadline.
4. When a gateway/client times out and cancels, errors can be attributed to account health. Non-stream read failure immediately cools an account for 10 minutes; startup/stream failures increment the error counter (`internal/server/handler.go:837`, `:876`, `:895`). Repeated timeouts can therefore make previously working accounts unavailable.

These code defects provide a plausible amplification mechanism for the reported production symptoms. They do not establish whether the initial trigger was provider throttling, a model-specific format, networking, or a gateway timeout.

## Additional code findings

- The exposed `upstream.timeout_seconds` field is normalized but never passed to the chat clients. Header timeout is fixed at 120 seconds. Constructor timeout only applies to the short-request client (`internal/upstream/client.go:132`).
- A failed queue reacquisition releases no slot, but the error exit still calls `ReleaseLock`; the pool counts active requests by account rather than lease identity. This can decrement another request's active count (`internal/server/handler.go:819`, `:828`).
- The model-aware `pickAccount`/`accountCanServe` helpers exist, but chat dispatch currently bypasses them. Simply calling `pickAccount` from the handler is insufficient: its lazy discovery uses provider calls without the chat context. Restore capability-aware selection while keeping discovery bounded or out of the hot path.
- Example config and README document concurrency 5 while Go defaults to 1. Old comments about a provider concurrency limit of 3 are historical observations, not current verified provider policy.
- Searches did not find a production `config.json`, state file, logs, or deployment credentials in the workspace. The current upstream model ID corresponding to the user's shorthand is unverified; do not silently rewrite it to the seeded historical model ID.

## Review constraints

- Use fake credentials and loopback upstreams in regressions; leave user auths and production services alone.
- Stop at actual terminal markers, not at `finish_reason`: OpenAI can send usage after a finish-reason chunk and before `[DONE]`.
- Check embedded terminal errors before classifying a JSON DONE as success; preserve usage, tools, snapshots, and reasoning.
- Use caller cancellation for admission, refresh waiting, retry, HTTP work, and cleanup. Cancellation is not an account failure.
- Active streams must survive longer than an idle interval when data continues arriving; avoid a total `http.Client.Timeout` on streaming.
- Honor slot ownership across every release/reacquire branch, including exhaustion, cancellation, missing/disabled accounts, and downstream write failures.
