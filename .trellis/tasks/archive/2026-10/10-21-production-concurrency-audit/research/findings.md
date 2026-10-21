# Live reproduction — October 21, 2026

## Method
The user authorized the supplied HTTPS origin and key. TLS verification remained enabled; redirects were not followed. The key was sent only to that origin and retained in memory. No administrator state-changing endpoint was called. These are direct service tests, not measurements from inside New API.

Baseline: deepseek-v4-flash-0731 returned HTTP 404 in 1157 ms: InferHub.4004.200 benefit not found.

Wave start: 2026-10-21 16:33:44 UTC+8. Eighteen concurrent non-streaming requests used 15 advertised IDs plus repeated glm-5.2, deepseek-v4-flash-0731 and openpangu-2.0-flash. Each used a unique harmless prompt, max_tokens=8, and a 125-second diagnostic-client deadline.

| Result | Count | Elapsed |
|---|---:|---|
| HTTP 200 | 4 | 2.339s, 64.154s, 70.044s, 79.349s |
| HTTP 404 model failures | 4 | 0.897–1.632s |
| Client deadline without headers | 10 | 125.012–125.013s |

Ten health samples remained HTTP 200, taking 233–1864 ms. Initially 3/4 accounts were already cooling; every account's max_concurrent was 5. This limits conclusions about full-pool throughput. A later snapshot showed natural cooldown expiry and four schedulable accounts; no second load wave was run.

## Deployment fingerprint
Observed templates:
- model <id> is not available on any configured account
- model <id> is not available: <upstream error>

Both exist in git show 3d87a5c^:internal/server/handler.go and neither is emitted by current chat.go. The repaired route uses is not available on eligible configured accounts. The old route has a 180-second account wait and 30-second retry acquisition. This strongly suggests an old request path or mixed deployment, not an exact verified build. The earlier repair was committed locally; this session has not pushed or deployed it.

## Actual model failures
DeepSeek Flash and GLM Flash returned InferHub.4004.200 benefit not found. One OpenPangu request returned InferHub.002002009.404 The model is not registered. These are capability/benefit errors, not proof of broken login credentials. Sign-in credits and model-specific benefits are separate features.

## Account and audit findings
internal/pool/pool.go:454 increments errors and replaces their cause with consecutive errors at threshold (defaults: 3 errors, 10-minute cooldown). Cooldown expiry does not clear the count/reason. NoteSuccess at line 474 clears the count but not lastErr. internal/server/panel.html:273 calls every non-disabled/non-cooling account healthy. The live test observed err_count=0 with stale consecutive errors, matching this defect.

There is no queryable request audit route or panel. Standard chat logs record a summary but not enough classified upstream detail to reconstruct these historical errors. A context canceled reason was present before the test; the version/caller that produced it is unknown.

Exact deployed image, runtime timeout/retry overrides, production container logs, and New API channel settings remain unverified. Per-request records and samples are in live-reproduction.json, with no key or credential contents.
