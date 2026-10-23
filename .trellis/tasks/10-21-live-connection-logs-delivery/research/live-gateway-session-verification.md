# Real gateway verification of identity and remote-session repairs

Date: 2026-10-23 Asia/Shanghai. Parent-operated, authorized local credential copies
and an isolated loopback server. No production gateway traffic, claims, raw
completion content, tokens or account identifiers are included here.

## Initial compiled gateway and discoveries

The first session-managed gateway binary had SHA256
`b475443664995edc381e7eaf67b49bf9f13bfb81dad20cbaf88c271f800e916c`.
Its first four case variants failed with 404 (six actual attempts). All startup
metadata sources had received HTTP400 using expired STS credentials, leaving an
empty account catalog. Display names were emitted unchanged without known seed
IDs; six wrong-wire negatives then blocked correct lowercase requests.

After the gateway's chat validator refreshed credentials, an explicit real
catalog POST obtained agent/builtin sources for three valid accounts, confirmed
their benefit channel disabled, and cleared six old negatives. The fourth test
login remained disabled due to its expired refresh token. Four nonstreamed case
variants then returned actual content/HTTP200, six actual attempts total.

Next, 24 sequential streamed GLM/DeepSeek requests alternating display/lower case
all returned actual content and DONE, with one upstream attempt apiece. This
demonstrates improvement over the earlier 8/12 completion result, but is not the
final acceptance run: client disconnect immediately after DONE exposed audit
misclassification during detached idle cleanup (23 canceled records, last still
cleaning up at the initial snapshot). Four one-second health polls also timed out
during a short local-host stall and subsequently recovered. Do not omit these
observations or represent this run as a complete resource/audit/health pass.

Ignored sanitized reports: `lifecycle-first-cases.json`,
`lifecycle-warm-nonstream.json`, `lifecycle-warm-stream-24.json` under
`.local-test/results/`. Each stores actual per-request content counts, completion,
status, ID, selected account alias, wire identity, attempts and sampled health.

## Additional repairs and deterministic regressions

- Verified exact GLM/DeepSeek normal IDs are available before metadata warm-up;
  genuinely uppercase model registrations remain uppercase.
- Negative availability belongs to exact wire ID and captured login. Non-seed
  discovery corrections and late old-wire results cannot poison a new identity;
  a real same-wire denial remains authoritative.
- Bounded startup/POST metadata workers validate shared credentials before use;
  failure/backoff belongs to the credential generation that actually failed.
  Admin cached GETs remain read-only.
- Sticky waiters reevaluate provider-busy state under the pool lock and select
  a compatible free backup. Late SSE/aggregation records busy before lease release.
- Successful flushed DONE is still success when a client closes during remote
  idle cleanup. A gate-controlled real HTTP fixture verifies cancellation of
  the server request before releasing idle, then an `ok`, non-canceled audit.
- First-data presence is independent of a zero-duration clock measurement.
  Existing successful/empty-data and remote-session route cases passed ten runs.

## Final rebuilt gateway: measured scope

The final parent-operated runs used the rebuilt full gateway, actual upstream
GLM/DeepSeek inference and an isolated localhost listener. These are gateway
measurements, not direct-provider substitutes or a production New API test. Three
credential copies were eligible; the fourth copy's refresh token had expired.
That authentication failure is separate from model registration/capability. All
three valid accounts reported the benefit channel closed. No real benefit-route
inference was tested or established by these results.

The requested variants were `GLM-5.3`, `glm-5.3`, `DeepSeek-V4.1-Flash` and
`deepseek-v4.1-flash`. Actual matched audit canonical IDs were `glm-5.3` and
`deepseek-v4.1-flash`. The cold nonstream run completed without a manual catalog
warm-up. The 18-request waves mix these **two models**, nine requests each; they
are not a test of 18 distinct models. Per-account local concurrency limits and
client concurrency are different: waiting requests remain visible as active
connections without all being sent upstream at once.

### Short inference, parallel waves and reuse

All times below are seconds. Client elapsed time ends after receiving the
response/completion; detached bounded idle cleanup may finish afterward and is
included in the separate audit duration. P95 is the nearest-rank sample
percentile, so it equals the maximum for these small cohorts except the 24-call
run. Slot wait is the largest measured audit `wait_ms`, not provider generation
time.

| Run | Local cap per account | Client concurrency | Completed / sent | Client median / P95 / max | Max slot wait |
| --- | --- | --- | --- | --- | --- |
| Cold nonstream, four case variants | 1 | 1 | 4 / 4 | 1.907 / 2.484 / 2.484 | 0 |
| Sequential streams, four case variants | 1 | 1 | 24 / 24 | 1.452 / 2.783 / 2.949 | 0 |
| First parallel stream step | 1 | 3 | 6 / 6 | 1.673 / 2.436 / 2.436 | 0.241 |
| Mixed stream wave | 1 | 18 | 18 / 18 | 5.658 / 11.841 / 11.841 | 9.516 |
| Mixed stream wave | 2 | 18 | 18 / 18 | 3.420 / 7.141 / 7.141 | 3.972 |
| Post-wave display-name reuse | 2 | 2 | 4 / 4 | 1.778 / 3.867 / 3.867 | 0 |
| First cap-3 mixed stream wave | 3 | 18 | **9 / 18** | 12.027 / 14.789 / 14.789 | 11.359 |
| Coordinated cap-3 mixed stream repeat | 3 | 18 | 18 / 18 | 2.817 / 6.264 / 6.264 | 2.763 |
| Immediate post-repeat reuse | 3 | 2 | 4 / 4 | 1.531 / 1.942 / 1.942 | 0 |

The first six reports contain 74 requests: all returned HTTP200, actual content and
completion; all 70 streamed requests also received DONE. All 74 matched audits
were `ok`, not canceled, with exactly one upstream attempt, no missing audit,
no dropped record and no write failure. No client automatic retry was enabled.
Each run ended with active and matching-active counts both zero, no connection
overflow and no truncated connection trace. The 24 sequential streams shared
work evenly across the three eligible accounts (8 each). The cap-1 18-request
wave used them 7/6/5 times; the cap-2 wave used them 6/6/6 times. Counts identify
distribution only, not account identity or a provider quota.

All 79 sampled health checks and all 79 sampled connection reads in these six
runs returned HTTP200. Maximum observed times were 18.88 ms for health and
28.28 ms for connection reads. The sequential-stream monitor briefly saw two
active handlers despite client concurrency one; completed delivery can overlap
the preceding request's bounded remote idle cleanup. Final zero active counts
and the successful following requests distinguish this from a retained lease.

The first cap-3 wave, starting at 2026-10-23 09:41:50 Asia/Shanghai, did **not**
pass all requests. Nine returned HTTP200, actual content and DONE with one
attempt each; nine returned local HTTP429 `queue_wait_timeout`, with zero
upstream attempts and `queue_wait` audit cause. Those nine are not provider TM
concurrency refusals or model-registration failures. Successful requests took
12.590-14.789 s client elapsed with zero slot wait. Rejected requests took
10.026-11.464 s, with 10.001-11.359 s measured slot wait. The table's latency
percentiles for this row include both response classes. Every request had a
matched, non-canceled audit; missing/dropped/write-failure counts remained zero,
and final active/matching-active counts were zero. Health and connection polling
each missed one one-second deadline out of 15 samples. This demonstrates bounded
rejection and cleanup, not acceptable completion of the full wave. Attribution
of the slowdown is not established by the phase timings alone.

The coordinated repeat started at 2026-10-23 09:59:09 Asia/Shanghai with the same
cap of three per valid account and 18 client requests. All 18 returned actual
content/DONE, HTTP200 and one-attempt `ok` audits. The immediately following
four reuse requests also succeeded with content/DONE and one attempt each.
Missing/canceled/dropped/write-failure counts were zero, and both runs ended with
active/matching-active zero. Their 7+4 health and connection samples all returned
HTTP200 (health maximum 16.03 ms, connection-read maximum 21.85 ms). Success in
this repeat establishes recovery/reuse; it does not erase the first cap-3
failure or identify what delayed it.

Across all nine final short-inference reports, 114 requests produced **105
content completions and nine local queue-timeout rejections**. The 101 successful
streamed requests received DONE; four successful requests were nonstreamed. All
114 audits matched, with 105 actual upstream attempts: one per completed call,
zero for each local queue rejection. Adding the separate long generation below
gives 115 observed gateway requests, 106 completed responses and nine local
rejections, rather than an all-requests-pass result. No automatic client retry
was enabled in these runs.

Sanitized ignored evidence under `.local-test/results/`:
`final-cold-nonstream.json`, `final-sequential-stream-24.json`,
`final-concurrency-3.json`, `final-concurrency-18.json`,
`final-per-account-2-concurrency-18.json` and
`final-per-account-2-reuse.json`; the failed first cap-3 wave is
`final-per-account-3-concurrency-18.json`, followed by
`final-per-account-3-controlled-concurrency-18.json` and
`final-per-account-3-reuse.json`.

The final cached runtime snapshot (`final-cached-runtime.json`) showed three
enabled accounts with error counts zero, no cooling and zero active leases;
their local caps were three. The fourth copy was disabled for authentication.
The 12-model catalog included both target IDs as discovered on all three valid
accounts, each with zero blocked and zero benefit-route accounts. Live tracking
was enabled with zero active requests, overflow and truncated traces. Its
read-only effective settings reported a 10-second account-wait budget.

### Real generation beyond the 30-second renewal interval

`long-stream-1792706068205792100.json` records one actual `glm-5.3` streamed
gateway call starting at 2026-10-23 05:53:49 Asia/Shanghai. With `max_tokens=4096`
and a 120-second client deadline, it returned HTTP200, 5,652 content characters,
1,170 reasoning characters, 1,070 SSE events and DONE. Headers arrived at 1.323 s,
first content at 5.119 s, completion at 38.693 s and DONE at 38.907 s. The finish
reason was `length`, matching the requested output bound. It was not a transport
timeout or a regenerated response.

The audit was `ok`, not canceled, canonical `glm-5.3`, one attempt and 39.034 s
including cleanup. Upstream progress reached 38.902 s and active connections
returned to zero. This demonstrates a real generation surviving beyond the
production 30-second heartbeat interval. The sanitized gateway report does not
record individual real heartbeat HTTP statuses/counts. Signing, matching session
ownership and renewal behavior are separately established by the official
protocol evidence and actual localhost HTTP regressions; do not invent additional
provider notification telemetry from this run.

### Health observations and synchronized control comparison

The long call's 38 monitoring cycles recorded health 36/38 and connection reads
36/38 successful. There were four one-second `TotalDeadlineExceeded` results:
two health requests and two connection reads. Around 05:54:09-05:54:13
Asia/Shanghai, one connection read timed out, then both probes timed out, then
one health request timed out while the connection read recovered at 699 ms.
Observed maxima were 1,014.96 ms for health and 1,009.86 ms for connections;
subsequent polls recovered while inference continued to its successful DONE.
These monitoring deadlines are retained as operational observations, not hidden
by the successful inference/audit result.

The independent investigation is documented in `health-stall-investigation.md`.
An older 90-second health-only comparison had 11 keep-alive and 12 fresh-TCP
one-second timeouts without a separate control service. The first two new
reviewer-controlled windows recorded 1,360/1,360 HTTP200 samples across four
gateway/control and keep-alive/fresh-TCP paths, with no one-second timeout.
They did not overlap a real wave, so they are not loaded-wave controls. A shared
approximately 168 ms pause across all four paths in the first window establishes
only that this particular short delay was not unique to the gateway. Historical
one-second timeouts, including the first cap-3 wave's health/connection misses,
remain unassigned.

The parent subsequently ran the control and successful cap-3 repeat from the
same orchestration. Actual timestamps, independently of the ready signal,
establish that the 54.771-second control window fully covered the 6.454-second
18-request wave and its immediately following 3.409-second reuse run. All four
control-comparison paths returned the correct HTTP200 body in 220/220 samples
each (880 total); the control process stopped cleanly. Samples starting inside
the wave were 104/104 successful, including 52 against the gateway; another
13/13 on each path started during reuse. During the wave, gateway keep-alive
and fresh-TCP maxima were 41.485 ms and 42.059 ms. No one-second deadline failure
was reproduced in this genuinely overlapping control.

Source review found that `/healthz` directly writes its response without pool,
catalog, audit or upstream work. This rules out a direct application-lock
dependency on chat admission in that handler; it does not exclude process-wide
pauses or establish whether earlier delays arose in the gateway, client, host
or provider. The failed cap-3 successes spent 3.007-11.450 s in
`prepare_upstream`, including initial busy notification/local preparation,
versus 0.068-0.319 s in the successful repeat. The recorded phase gap alone
cannot identify its cause or be labeled entirely as generation latency.
Successful recovery and control samples do not retrospectively diagnose these
intermittent stalls. No historical process-stack/ETW evidence was captured.

## Quality and remaining delivery boundary

The independent source review is recorded in
`live-integrated-quality-review.md`: the stable integrated code passed full
normal/race tests, vet, build, formatting and tracked/new-source diff checks.
The actual operational results above supersede that review's then-pending real
long/concurrency measurements; they do not change its source-level findings.

The failed first cap-3 wave and unassigned historical health stalls prevent an
unconditional all-concurrency-pass or all-runtime-issues-resolved claim. The
later full-wave success establishes observed recovery, queue/resource reuse and
responsive sampled health in that window. All four original credential files
matched the parent-captured snapshot for this current verification round.
Final source artifact identity/extraction and deployment checks belong to the
parent session's final delivery verification. The initial binary hash above is
historical, not the final release identity. Local results do not claim a
production deployment, unlimited upstream capacity or successful real
benefit-model inference.
