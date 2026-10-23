# Bounded local health-stall investigation

Date: 2026-10-23. Status: historical stalls remain unassigned. Two reviewer-run
idle control windows and one parent-orchestrated control window covering a real
18-request wave did not reproduce a one-second health timeout. The later wave
and immediate reuse succeeded; the earlier failed wave remains recorded below.
These bounded samples do not establish freedom from intermittent process stalls
or capacity for arbitrary sustained load.

## Scope and source path

No product source changed and no full test suite was repeated. The reviewer read
only source and explicitly authorized sanitized result files. No credentials,
runtime key/configuration, chat endpoint, provider endpoint or account action was
accessed by this investigation. The parent owns the concurrent real chat tests.

`internal/server/handler.go` registers `GET /healthz` directly, outside
`withAuth`. `Handler.ServeHTTP` directly invokes its initialized ServeMux. The
health handler writes HTTP 200 and the two bytes `ok`; it reads no account pool,
catalog, audit sink, conversation state or other application lock, and performs
no application disk, logging or upstream work.

`cmd/server/main.go` supplies this handler directly to `http.Server`, with only
`ReadHeaderTimeout: 30s` configured. No global server write timeout, connection
state/context callback or application-wide handler semaphore was found. Chat
read/write deadlines apply to their individual connections and are cleared by
their owners. Source search found no GOMAXPROCS/GOMEMLIMIT override, runtime/GC
tuning or explicit OS-thread locking in the serving path.

This rules out a direct health dependency on the chat pool lock or its admission
queue in the reviewed code. It does not exclude process suspension, runtime/GC
pauses, OS scheduling, loopback stack delays, a blocked client, or another
unobserved global effect. No process stack/ETW trace from a historical pause is
available, so those remain hypotheses, not diagnoses.

## Existing historical evidence

The sanitized `health-comparison-1792706119434598100.json` contains a prior
90-second two-thread stock-Python HTTP comparison of keep-alive and fresh TCP
connections to the gateway, without a separate control service:

| Mode | Samples | HTTP 200 / correct body | Timeouts | Maximum elapsed |
| --- | ---: | ---: | ---: | ---: |
| Keep-alive | 168 | 157 | 11 | 1030.27 ms |
| Fresh TCP | 168 | 156 | 12 | 1029.97 ms |

Both modes had temporally clustered failures. Their agreement alone does not
identify whether the gateway process or their shared environment/client stalled.
The parent also retained two health and two connection-monitor timeouts during
the real 38.907-second generation; those observations are not erased by the newer
successful windows.

Read-only review of `.local-test/health_comparison.py` confirms ordinary
`http.client.HTTPConnection(..., timeout=1)` against explicit loopback, one
independent connection per thread, reset on error, and no automatic retry.
`elapsed_ms` is recorded before `sleep(max(0, .5 - elapsed))`, so the scheduled
half-second sampling interval is not added to the reported request duration.
The old records do not distinguish connect/header/body timeout phases.

## Independent control and bounded samples

Created only ignored local diagnostics:

- `.local-test/health-control.go`: standalone standard-library Go server, no
  project imports/configuration, binds an ephemeral loopback port, identical
  direct health response and server read-header timeout, automatic lifetime
  capped at 90 seconds.
- `.local-test/health_probe_compare.py`: four health-only threads covering
  gateway/control with keep-alive/fresh TCP, 250-ms planned intervals, one-second
  per-request timeout. A separate collector samples process CPU, working set,
  private memory and thread count every 500 ms. It records scheduling lag and
  wall-clock request starts. No response content or headers are persisted.
- The script owns and terminates its control child in `finally`; both completed
  runs record `control_stopped=true`. The samples total 85 seconds, in separate
  55- and 30-second rounds. There are no further reviewer-run rounds.

| Round / mode | Samples / successes | Median | p95 | Maximum |
| --- | ---: | ---: | ---: | ---: |
| 55s gateway keep-alive | 220 / 220 | 1.021 ms | 61.448 ms | 167.999 ms |
| 55s gateway fresh | 220 / 220 | 16.997 ms | 61.529 ms | 168.109 ms |
| 55s control keep-alive | 220 / 220 | 0.980 ms | 58.620 ms | 167.691 ms |
| 55s control fresh | 220 / 220 | 15.674 ms | 63.193 ms | 168.577 ms |
| 30s gateway keep-alive | 120 / 120 | 0.837 ms | 52.570 ms | 65.620 ms |
| 30s gateway fresh | 120 / 120 | 16.050 ms | 58.606 ms | 95.157 ms |
| 30s control keep-alive | 120 / 120 | 0.743 ms | 47.475 ms | 120.082 ms |
| 30s control fresh | 120 / 120 | 14.758 ms | 54.911 ms | 120.086 ms |

All 1360 samples in these two windows returned HTTP 200 with the expected body.
At 41.500 seconds
in the first round, all four modes simultaneously measured approximately
168 ms. That particular delay is not unique to the gateway. The same Python
driver and resource collector are shared, so the observation cannot distinguish
OS/network/Go effects from client-side scheduling or instrumentation overhead.
It does not explain the old one-second timeouts, which were not reproduced.

Resource ranges, using CPU percentages relative to one logical core:

| Round / process | CPU time gained | Peak CPU | Working set range | Threads |
| --- | ---: | ---: | ---: | ---: |
| 55s gateway | 0.04688 s | 3.128% | 22.95-25.92 MB | 22-23 |
| 55s control | 0.03125 s | 6.243% | 7.82-10.27 MB | 6-8 |
| 30s gateway | 0.10938 s | 3.132% | 21.28-21.82 MB | 17 |
| 30s control | 0.06250 s | 6.247% | 7.77-9.76 MB | 7-9 |

The probe process itself used 6.14062 CPU seconds in the first round and
2.20312 in the second. Maximum collector start lag was 1.464/1.602 ms; this does
not measure every request thread's scheduling during an in-flight operation.
Private memory remained bounded (gateway 59.27-62.22 MB and 56.80-57.36 MB;
control 47.06-49.71 MB and 46.94-49.46 MB). System CPU peaked at 50.5%/80.0% on
24 logical CPUs. These sparse aggregate measurements do not identify a stall's
cause or capture historical process state.

The first round began after the parent's per-account-2 wave had already ended.
The second round was intended to cover the per-account-3 wave, but cross-tool
readiness coordination failed; it must not be reported as a loaded-wave control.
Actual second-round request window was UNIX
`[1792719511.053402, 1792719540.8042955]`. The sanitized per-account-3 report starts
at `1792719710.480` and ends at `1792719725.427`: intersection is zero seconds,
and the wave began 169.676 seconds after the last control request completed.

For subsequent parent-owned orchestration, the comparison script now exposes a
ready file and top-level `started_unix` / `finished_unix`, calculated from actual
first request and last completion. Existing samples already retain their exact
`unix_time` and elapsed duration, so the first two windows remain reconstructible.
No claim of wave/control overlap should rely only on a tool-turn message.

## Read-only phase breakdown of the later per-account-3 wave

The separate sanitized `final-per-account-3-concurrency-18.json` contains nine
HTTP-200 content/DONE successes, each one actual attempt, and nine local account
wait timeouts with zero actual attempts. It is outside both control windows.

For the nine successful calls, account-slot and credential-validation phase
durations were zero milliseconds in the recorded precision. The time was spent
before useful data, not primarily in streaming or final cleanup:

| Phase | Minimum | Median | Maximum |
| --- | ---: | ---: | ---: |
| `prepare_upstream` | 3007 ms | 4220 ms | 11450 ms |
| `wait_headers` | 1737 ms | 9274 ms | 10291 ms |
| `wait_first_sse` | 0 ms | 1 ms | 24 ms |
| `stream` | 82 ms | 405 ms | 1550 ms |
| `cleanup` | 88 ms | 97 ms | 214 ms |

Under the actual `sendChatV2` path, `prepare_upstream` includes initial signed
busy notification and local request preparation before `WaitHeaders` is emitted.
The 11450-ms entry belongs to request index 17, followed by only 1737 ms in
`wait_headers`. It cannot honestly be labeled entirely as provider generation
latency. The nominal heartbeat timeout is at most three seconds; a phase this
long requires further evidence about local scheduling, notification timing and
other work in the measured interval. Wall-clock phase gaps alone cannot prove
which one was responsible.

The nine queued requests never reached upstream. Their approximately 10-second
account-wait budget expired before the successful holders completed. Raising
concurrency does not itself guarantee these request deadlines or provider
throughput. This failed wave also contained one health timeout. It is retained
as a failed observation, with its health-stall cause unassigned, rather than
being replaced by the later successful run.

## Parent-orchestrated loaded control and immediate reuse

The parent then launched the 55-second control comparison and, after its ready
signal, the real chat wave in the same orchestration. The reviewer only read
these explicitly authorized sanitized outputs; no new reviewer traffic or
product edits were made:

- `health-control-cap3-wave.json`
- `final-per-account-3-controlled-concurrency-18.json`
- `final-per-account-3-reuse.json`

Actual timestamps establish coverage independently of the ready signal:

| Window | UNIX start | UNIX finish | Control intersection |
| --- | ---: | ---: | ---: |
| Control requests | 1792720748.9994652 | 1792720803.7704792 | 54.771 s |
| 18-request wave | 1792720749.115 | 1792720755.569 | 6.454 s, entire wave |
| Four-request reuse | 1792720755.757 | 1792720759.166 | 3.409 s, entire reuse |

The wave ran at `2026-10-23T01:59:09.115Z` through `01:59:15.569Z`. It submitted
nine requests each for lowercase `glm-5.3` and `deepseek-v4.1-flash`, with client
concurrency 18, streaming enabled, `max_tokens=256` and no automatic retries.
All 18 returned HTTP 200, nonempty content and DONE, and all 18 matched final
`ok` audits with one actual attempt apiece. Client completion durations ranged
from 1140.73 to 6263.52 ms. Final active and matching-active counts were zero;
audit missing, dropped and write-failure counts were zero. The wave's own seven
health and seven connection-monitor polls all succeeded, with maximum elapsed
15.89 and 21.85 ms respectively.

The immediately following four calls also returned HTTP 200, content, DONE and
one-attempt `ok` audits. Final active counts again reached zero. Its four health
and four connection-monitor polls all succeeded, with maxima 16.03 and 16.71 ms.
This demonstrates bounded post-wave reuse and no persisting deadlock in that
sample. The probes and chat settings do not establish capacity for 18 distinct
models, long outputs, or unlimited concurrent callers.

The independent control comparison recorded 220/220 correct HTTP-200 results in
each mode, 880/880 total, with `control_stopped=true`:

| Mode | Full-window median | p95 | Full-window maximum | Samples starting inside 18-request wave | Wave maximum |
| --- | ---: | ---: | ---: | ---: | ---: |
| Gateway keep-alive | 0.800 ms | 45.691 ms | 161.445 ms | 26 / 26 successful | 41.485 ms |
| Gateway fresh TCP | 16.068 ms | 58.840 ms | 136.785 ms | 26 / 26 successful | 42.059 ms |
| Control keep-alive | 0.754 ms | 43.397 ms | 161.394 ms | 26 / 26 successful | 41.629 ms |
| Control fresh TCP | 15.385 ms | 57.365 ms | 199.734 ms | 26 / 26 successful | 55.101 ms |

The reuse interval contained another 13/13 successful requests in each mode;
its gateway maxima were 42.860 ms with keep-alive and 61.989 ms with fresh TCP.
The loaded-wave subset therefore contains 104/104 successful control-comparison
requests, including 52 directly against the gateway. Subset membership uses
each request's actual start timestamp. No one-second failure was reproduced.

During the complete control window the gateway gained 0.296875 CPU seconds,
peaked at 9.38% of one logical core in the 500-ms samples, used 23.44-37.15 MB
working set and 61.60-75.42 MB private memory, and had 23-25 threads. The control
used 7.75-10.23 MB working set and 6-8 threads; its CPU-time delta rounded to
zero at the available process-clock precision. The probe used 5.421875 CPU
seconds. System CPU peaked at 99.9% on 24 logical CPUs; maximum collector start
lag was 4.430 ms. These sparse measurements describe the successful window and
cannot identify the cause of earlier pauses.

The successful wave's recorded phase durations also differ from the failed
one. Local slot wait was 0-2763 ms, `prepare_upstream` 68-319 ms (median 223),
`wait_headers` 607-4348 ms (median 1055), streaming 127-2157 ms, and cleanup
68-297 ms. Queued requests obtained slots within their budget in this sample.
It does not retrospectively explain the
earlier 3007-11450-ms preparation phase or prove why that interval was long.

Final conclusion: the corrected gateway completed this real 18-request wave
with working queueing, observable audits, responsive sampled health and reusable
resources. The earlier nine queue timeouts, historical health timeouts and long
generation's monitor timeouts remain part of the evidence. Their intermittent
health-stall cause is unresolved; a successful later wave cannot assign it to
the OS, the client, the provider or the gateway. No further traffic was run by
the reviewer, and the already-passed integrated product quality gates were not
repeated for these diagnostic/report-only changes.
