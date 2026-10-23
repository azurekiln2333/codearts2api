# Remaining intermittent-stall observation plan

Date: 2026-10-23. Owner: integrated quality reviewer. Historical health failures
remain unattributed; successful finite reruns do not close this investigation.

## Change boundary

The missing evidence is the state of the gateway, its client and an independent
control at the same incident, with connect/write/headers/body timing separated.
The current health handler has no direct chat-pool dependency. Neither its source
nor a later successful wave identifies why prior one-second probes failed.

This work creates only this report and new ignored `.local-test/stall-observer-*`
files. It does not alter product sources, task requirements, credentials, source
archives or production configuration. It does not issue chat/provider/account
requests. The parent owns starting a diagnostic gateway and any real workload.

The tools will comprise a standalone Go health observer/control binary and a
Go-build overlay adding a private diagnostic listener to a separately built
gateway. Build-time overlay files live entirely under `.local-test/`; the root
`cmd/server` directory is never written. Diagnostics remain disabled unless the
parent supplies the specific private diagnostic environment switch.

## Evidence already preserved

See `health-stall-investigation.md`: earlier health timeouts, the first failed
limit-three wave, the successful synchronized 18-request rerun and immediate
reuse are all retained. No assumption here assigns those failures to Windows,
Python, the Go runtime, upstream inference or a lock.

## Implemented private tools

`stall-observer-prepare.py --build` creates `stall-observer-overlay.json`, builds
`stall-observer-probe.exe` and `stall-observer-gateway.exe`, and checks that the
original `cmd/server/main.go` hash and absent virtual file remain unchanged.
The overlay maps a virtual new `cmd/server/stall_observer_private.go` to
`.local-test/stall-observer-overlay/observer.go`. It does not replace `main.go`
and can be merged with a separate `upstream/client.go` overlay by combining their
distinct `Replace` entries. No gateway is launched by this helper.

The observer CLI is:

```powershell
.local-test/stall-observer-probe.exe `
  --gateway http://127.0.0.1:56807 `
  --gateway-pid <owned-gateway-PID> `
  --duration 90s `
  --output .local-test/stall-observer-results-<unique-run>.json `
  --ready-file .local-test/stall-observer-ready-<unique-run>.json `
  --diag .local-test/stall-observer-run-<unique-run>/ready.json
```

`--gateway-pid` and `--diag` are optional. With `--diag`, the supplied PID must
match the diagnostic metadata PID. Targets must be explicit HTTP IPv4 loopback
with a port, without a path, credentials, query or fragment; the only target
request path is `GET /healthz`. The binary reads no project runtime/config/auth
file. Output and ready files must be new `stall-observer-*` paths beneath the
binary's `.local-test` directory. Never point it at the production URL.

Four goroutines probe gateway/control with keep-alive/fresh TCP at 250-ms planned
intervals. The control is a separate hidden child process of the same standalone
binary, not another server inside the observer process. Both observer and control
record their own 100-ms timer lag and goroutine counts. The child is owned and
bounded to the observer window plus two seconds; it is waited or killed on exit.
No prior gateway process is killed by this tool.

The health client uses explicit connections and `http.ReadResponse`, not a Go
Transport with implicit GET retries. Each result records wall-clock start,
monotonic elapsed duration, planned-tick lag, reused/fresh connection, status,
success, failing phase, timeout classification, and numeric syscall errno where
available. Separate connect/write/headers/body phase budgets are each one second;
the total operation may therefore exceed one second. It records no HTTP body,
header, arbitrary error text or model/account data. The expected two-byte body is
checked only in memory. Missed sampling ticks are skipped, avoiding catch-up load.

Native read-only Windows collection runs every 500 ms for explicitly supplied
gateway PID, owned control and observer. It records GetProcessTimes cumulative CPU,
GetProcessMemoryInfo working set/private bytes/page-fault count, collection lag and
duration, and aggregate numeric IPv4 TCP-state counts. It does not record local or
remote addresses, URLs, process command lines, environment, handles or memory
contents. Process-creation FILETIME detects PID reuse. TCP counts cover IPv4 only;
an unavailable/empty aggregate is not proof that there are no connections.

## Diagnostic gateway overlay

The overlay requires the private Go 1.27 toolchain. This is an instrumented local
build only; the regular product's Go 1.22 source compatibility is unchanged.

The parent enables it only in the owned isolated gateway child's environment:

```text
CA2A_STALL_DIAG_RUN=stall-observer-run-<unique-run>
CA2A_STALL_TRACE_SECONDS=90
```

The binary must reside directly in `.local-test`. The run name must be a basename
with the shown prefix and its directory must not already exist. With no run switch
the overlay performs no diagnostic startup. It binds only `127.0.0.1:0` using an
independent explicit ServeMux. It imports no `net/http/pprof`, never registers on
DefaultServeMux and adds no route to the public product handler. It writes private
`ready.json` with PID, ephemeral loopback URL and a random 192-bit diagnostic key;
the key is used only as `X-Stall-Observer-Key` on private diagnostic requests and
is never printed or copied into observer output. File modes request owner-only
access; actual Windows ACLs inherit from the local workspace.

Private routes, all requiring that diagnostic key:

| Route | Purpose |
| --- | --- |
| `GET /diag/healthz` | Independent mux's minimal response inside the gateway process |
| `GET /diag/runtime` | Bounded timer/goroutine/GC/scheduler ring; no disk/upstream reads |
| `POST /diag/capture` | At most two bounded stack/runtime/optional flight snapshots |

The private listener closes after 120 seconds without stopping the gateway.
Diagnostic ready is emitted before ordinary gateway startup and explicitly says
`gateway_ready=false`; the parent must separately wait for the public health route
before workload. There is no publicly reachable pprof/cmdline/heap/profile route.

Every 100 ms the overlay records its own timer lag and NumGoroutine into a ring of
at most 900 ticks. Once per second it samples fixed runtime metrics: GC cycles,
heap live/objects/allocations, cumulative runnable-scheduler latency histogram,
GC and non-GC STW pause histograms, and estimated GC/total runtime CPU classes.
Histograms retain bucket boundaries and counts; compare successive count deltas,
not cumulative percentile values. Runtime CPU-class estimates are not Windows
process CPU times and must not be directly subtracted from those measurements.
No `runtime.ReadMemStats` or forced GC is called by the sampler.

The optional trace switch arms Go's FlightRecorder for at most 90 seconds, asking
for ten seconds of history and an 8-MiB moving window. MaxBytes is explicitly a
runtime hint, not a hard memory or history guarantee. Each saved snapshot has a
hard 16-MiB output cap; `trace_complete=false` means the file may be unusable.
Trace is off by default. On expiry it stops and drops its in-memory window.
It neither starts an unbounded trace nor forces a process crash.

Capture writes an all-goroutine stack snapshot capped at 2 MiB, a bounded runtime
ring, timestamps and the optional flight trace into its new private run directory.
It allows one capture at a time, at least ten seconds apart, at most two total.
The observer requests capture asynchronously on gateway health failure or elapsed
time at least 250 ms, with a four-second private request deadline. Failed/429
capture responses cannot corrupt the final observer JSON. The parent may also
request capture with the same private token; account for the shared two-capture
budget. Diagnostic artifacts are private evidence, never source-release members.

All-goroutine stacks and flight-recorder flushing perturb the runtime. Their
recorded start/finish interval must be marked as diagnostic activity and excluded
from any claim about a spontaneous pause. A snapshot requested after a stall can
arrive only when the gateway runs again. The prearmed flight recorder and timer
history provide pre-trigger context; a post-recovery stack alone cannot prove
where the gateway was blocked during the stall. Stack/trace files can contain
source paths, function names and numeric arguments/pointers; inspect and sanitize
before sharing them. No process-wide profiling signal, debugger attach, dump,
`GOTRACEBACK=crash`, service kill or remote pprof exposure is used.

## What the existing Python results do and do not establish

Read-only source review covered `health_comparison.py` and
`health_probe_compare.py`. Both use stock `http.client.HTTPConnection(timeout=1)`.
They record request elapsed before cadence sleep, so the 0.5-second/0.25-second
poll schedule is not simply added to response duration. A real TimeoutError is
not created just because the loop starts late. However, Python socket timeout is
an operation-level blocking budget and includes wall time while the process or
thread cannot run. It is not a measurement of time spent executing the handler.
An HTTP operation contains several socket operations and interpreter transitions.

The old records combine request/send, getresponse and body read into one try block
with only exception class. They do not identify the failing socket/HTTP phase.
The comparison's four threads also share one Python process, GIL, resource collector
and scheduler. Its pre-call `scheduler_lag_ms` does not measure a pause occurring
inside a socket operation, or the interval between socket readiness and resumed
Python execution. Therefore matching gateway/control times cannot by themselves
assign a host pause, and successful scheduled-start measurements cannot eliminate
a probe pause. They remain genuine observed client failures with missing cause.

The Go observer removes the shared Python client from the health path and adds
separate control-process timing. Its four clients still share one Go observer
runtime; its independent control timer and gateway timer are required to distinguish
that remaining ambiguity. One run cannot prove that every process on Windows
paused. Only the three measured processes, and any externally captured scheduler
trace, are observable.

## Incident interpretation and next-step evidence

| Synchronized observation | Supported conclusion / next evidence |
| --- | --- |
| Gateway timer shows a gap; observer/control timers remain timely | Gateway-specific execution delay is supported. Compare pre-trigger flight, scheduler histogram deltas and GC/STW deltas; inspect stacks with their capture timing. |
| Only observer timer shows a gap; control and gateway timers remain timely | Observer-process scheduling/runtime delay is supported. A simultaneous pair of client slow results alone is insufficient evidence against the gateway. |
| All three independent timers show matching gaps | Shared host scheduling/suspension/CPU pressure is supported, without proving every process paused or excluding coincident GC. OS scheduler evidence is needed for cause. |
| Both health targets slow, process timers timely, timeout in connect/headers | Client socket/loopback/accept/readiness path needs investigation. Compare fresh versus reused sockets, TCP states and external scheduler/network metadata. |
| Public health stalls but private gateway health/runtime stays timely | Public listener/request-path difference is supported. Collect both listeners' actual timing and owned-connection state; do not blame all runtime activity. |
| Gateway GC/STW histogram gains a bucket covering the same gap | A runtime pause becomes a candidate; verify time-bounded trace and exclude diagnostic capture intervals. Cumulative GC totals alone do not locate the incident. |
| High process CPU and scheduler runnable latency grow during gateway-only gap | CPU/Go scheduling starvation is supported as a hypothesis; flight trace or OS CPU samples identify the responsible work. |
| Healthy timers, no CPU, many waiting goroutines/connection states | Waiting I/O or synchronization may explain a particular request. Trace actual health-path stacks/connection phase rather than inference waiters alone. |
| Python reports timeout while independently sampled Go health/timers succeed | Monitor-local behavior is a candidate. Different requests are not identical evidence; retain the failed Python request until phase/OS data explain it. |

If ambiguity remains, the parent can separately authorize and prearm a short
Windows Performance Recorder/ETW recording containing CPU sampling, context
switch/ready-thread, DPC/ISR and TCP connection metadata, without packet payloads.
Keep the ETL local, bound its recording period and correlate by PID/process start
and the incident timestamps. Do not use an unreviewed broad collection profile or
capture command lines/payloads as a substitute for this narrower plan. The reviewer
does not start ETW, elevate, attach to a service or change machine scheduling.

The parent's workload orchestration should await the Go observer ready file and
the gateway health response in the same process that starts the finite wave,
stop additional chat requests on the first failure, preserve the existing request
audits and wait for bounded cleanup. It must record actual request windows and
compute their intersection with probe timestamps. Do not infer coverage from tool
messages. The reviewer does not own or run this real workload.

## Verification and primary references

Private checks completed with Go 1.27.3:

- Both binaries compile, including the virtual added overlay source; no real
  gateway was launched. `main.go` hash and absent virtual-file checks passed.
- Synthetic actual-HTTP fixtures verify healthy reuse, header/body timeout phase
  separation, nonloopback/nonhealth target rejection and empty capture rejection.
- Overlay fixtures verify private-key enforcement, no DefaultServeMux registration,
  JSON-safe histogram buckets, stack byte cap, capture cooldown and capture count.
- A two-second synthetic-only observer smoke ran four modes with 8/8 success each,
  retained separate control/observer clocks and four native process samples,
  including CPU/private memory, and stopped its owned control. The standalone
  fixture was also stopped. No credentials or real provider/gateway was used.
- Private Go vet, gofmt and helper Python syntax checks pass. The unchanged
  product's full suite is not rerun for this ignored diagnostic tool work.

Primary API contracts were checked against the installed Go 1.27.3 source,
particularly `src/runtime/trace/flightrecorder.go`,
`src/runtime/metrics/description.go` and `src/cmd/go/alldocs.go` (`-overlay`).
Further official references for the parent:

- `https://pkg.go.dev/runtime/trace#FlightRecorder`
- `https://pkg.go.dev/runtime/metrics`
- `https://pkg.go.dev/runtime#Stack`
- `https://docs.python.org/3/library/socket.html#socket.socket.settimeout`
- `https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getprocesstimes`
- `https://learn.microsoft.com/en-us/windows/win32/api/psapi/nf-psapi-getprocessmemoryinfo`
- `https://learn.microsoft.com/en-us/windows/win32/api/iphlpapi/nf-iphlpapi-getextendedtcptable`
- `https://learn.microsoft.com/en-us/windows-hardware/test/wpt/windows-performance-recorder`

## Independent review of parent experiment r13-a

The parent reports six successive 18-request waves, 108/108 completed with correct
audits and active counts returning to zero. This reviewer independently read only
the authorized `stall-observer-results-r13-a.json` and listed the non-ready files
in `stall-observer-run-r13-a`; no credential, ready-file key, network request or
additional workload was accessed. The numerical results below are telemetry
verification, not an independent recheck of the parent's 108 chat/audit records.

The observer's configured sampling period was 90 seconds. Its report window is
`2026-10-23T05:18:49.969706Z` to `05:20:21.971722Z`; the final two seconds include
waiting for the owned control child's bounded completion. Actual health requests
started between approximately `05:18:49.969706Z` and `05:20:19.720015Z`. The control
exited successfully, private runtime GET returned HTTP 200, and no automatic
capture was requested. No stacks/flight snapshot exists for this run. The final
HTTP-trace JSONL file is 985,824 bytes; the separate HTTP investigator reports
5,355 records, 339 round trips, all HTTP 200 and no drops. An earlier intermediate
directory listing showed zero bytes before final trace publication; that listing
is superseded and is not evidence of an instrumentation defect. The reviewer
rechecked the final size without reading the diagnostic ready/key file.

| Health mode | Successful / total | Median | p95 | Maximum | Maximum planned-start lag |
| --- | ---: | ---: | ---: | ---: | ---: |
| Gateway keep-alive | 360 / 360 | 0.000 ms | 0.7540 ms | 2.2795 ms | 1.4976 ms |
| Gateway fresh TCP | 360 / 360 | 0.6116 ms | 1.5507 ms | 2.3080 ms | 1.5477 ms |
| Control keep-alive | 360 / 360 | 0.000 ms | 0.7039 ms | 2.2795 ms | 1.5477 ms |
| Control fresh TCP | 360 / 360 | 0.5909 ms | 1.4151 ms | 2.6346 ms | 1.5477 ms |

All 1440 observations were successful; none reached 50, 100, 250 or 1000 ms.
Zero recorded submillisecond values reflect measurement granularity and must not
be interpreted as zero execution time. Scheduled-start lag is distinct from the
elapsed request duration. Across the two gateway modes, maximum measured connect,
write, headers and body phases were 1.7345, 1.0362, 1.5848 and 1.0820 ms. There was
no failed phase or timeout to diagnose.

Independent 100-ms process timer histories:

| Process | Ticks retained | p95 lag | Maximum lag | Largest interval between ticks |
| --- | ---: | ---: | ---: | ---: |
| Observer | 899 | 0.9170 ms | 1.5854 ms | 101.3681 ms |
| Standalone control | 919 | 1.0509 ms | 1.5834 ms | 101.2774 ms |
| Diagnostic gateway | 900 | 1.0836 ms | 1.7162 ms | 101.3977 ms |

No sampled timer history contains a near-one-second execution gap. Gateway
goroutines ranged from 25 to 105, ended at 25, and ranged from 25 to 31 over the
last 20 seconds. Observer goroutines remained nine; the control ranged two to four.
The 180 native-resource samples had maximum collection lag 1.5854 ms and maximum
collection duration 4.6552 ms; no process identity changed or CPU sample was missing.

Runtime counters were available in 85 snapshots from `05:18:50.188786Z` through
`05:20:19.088727Z`. Differences below apply to that metric subwindow:

- GC cycles increased by 39. GC STW histogram counts increased by 78; their
  largest nonempty incremental bucket was `[1.048576, 1.310720)` ms. No GC pause
  count increment was in a bucket at or above 100 ms.
- Runnable-scheduler latency counts increased by 5316; their largest nonempty
  incremental bucket was `[1.310720, 1.572864)` ms, with no increment at or above
  100 ms. Non-GC STW histogram count did not increase.
- Cumulative heap allocations increased 325,987,584 bytes. GC-live heap went from
  5,730,888 to 8,012,824 bytes, peaking at 15,540,248; current heap objects went from
  6,723,528 to 8,802,520 bytes, peaking at 22,931,072. Allocation totals are traffic
  volume, not retained-memory totals or evidence of a leak.
- Estimated runtime GC CPU increased 0.4209096 CPU-seconds. This runtime-class
  measure is kept separate from the Windows process CPU accounting below.

Native process measurements, decimal MB:

| Process | Windows CPU gained | Peak CPU relative to one core | Working set first / last / peak | Private memory first / last / peak |
| --- | ---: | ---: | --- | --- |
| Gateway | 2.125000 s | 21.88% | 21.94 / 36.18 / 52.27 MB | 60.01 / 73.71 / 91.82 MB |
| Control | 0.015625 s | 3.13% | 8.24 / 11.33 / 11.33 MB | 47.06 / 50.33 / 50.33 MB |
| Observer | 0.015625 s | 3.13% | 9.80 / 15.12 / 15.13 MB | 49.35 / 54.79 / 55.00 MB |

Windows process CPU accounting is coarse in this sample; the small child/observer
totals should not be interpreted as an exact accounting of every probe's work.
Over the final approximately 20 seconds the gateway gained only 0.03125 CPU
seconds, its working set ranged 36.18-43.80 MB and private memory 73.71-83.34 MB.
Its peak memory declined by the end. These short instrumented measurements do not
prove a retained-memory leak or long-term stability.

Gateway aggregate established IPv4 TCP entries peaked at 39 and went from four
to eight between the first and last sample; listen entries stayed two. Other
transient connection states were observed. These are process-wide socket counts,
including diagnostics, keep-alives and upstream connections; they are not active
chat counts and do not contradict the parent's `active=0` request result. Remote
destinations and payloads were not recorded. IPv6 was not sampled.

The private tools and overlay were frozen at the parent's request before this
read-only analysis. A native TCP query failure currently yields an empty aggregate,
so an empty map must be interpreted as unavailable-or-empty rather than a proven
zero. Runtime histograms cover only their retained metric subwindow and report
bucket bounds, not an exact pause duration. The observer/control clocks are useful
independent-process observations, but a no-failure run does not tell which process
caused a historical failure. No one-second incident occurred to trigger stacks or
a flight snapshot in r13-a. Parent cold-start, long-stream and ordinary production
binary validation are separate and were still in progress when this section was
written.

## Independent review of parent experiment r13-b

The parent reports two successful cold calls, a 61.178-second generation with
7,902 content characters, and a subsequent 18-request wave. All 18 later calls
had HTTP 200 and DONE, but one DeepSeek call had zero content characters, 32
reasoning characters and finish reason `stop`. The parent's strict content gate
therefore failed. This health review does not convert that reasoning-only result
into a successful content reply; the root session owns investigating it and
checking the ordinary production binary.

The reviewer read only `stall-observer-results-r13-b.json` and non-ready artifact
file metadata. The configured 90-second sampling began at
`2026-10-23T05:24:56.954438Z`; the report completed at `05:26:28.957681Z`, including
the bounded control-child completion interval. Again the owned control exited
successfully and no automatic capture occurred.

| Health mode | Successful / total | Median | p95 | Maximum | Maximum planned-start lag |
| --- | ---: | ---: | ---: | ---: | ---: |
| Gateway keep-alive | 360 / 360 | 0.0000 ms | 0.7583 ms | 3.0500 ms | 2.9340 ms |
| Gateway fresh TCP | 360 / 360 | 0.6101 ms | 1.7965 ms | 3.0500 ms | 1.9365 ms |
| Control keep-alive | 360 / 360 | 0.0000 ms | 0.7771 ms | 2.0976 ms | 2.9340 ms |
| Control fresh TCP | 360 / 360 | 0.5896 ms | 1.7498 ms | 3.0500 ms | 1.9365 ms |

All 1440 health observations succeeded, with no sample reaching 50 ms and no
transport timeout. The largest gateway connect/write/headers/body phase values
were 2.0037 / 1.0234 / 3.0500 / 1.0044 ms. Observer timer lag peaked at 1.8978 ms
over 899 ticks; control timer lag peaked at 2.6920 ms over 919 ticks. Their largest
inter-tick intervals were 101.6607 and 101.9437 ms. The collector's 180 samples had
maximum start lag 1.3367 ms and maximum collection duration 4.2019 ms.

Native gateway CPU increased 1.0625 seconds, peaking at 18.74% of one logical core
over the 500-ms sample intervals. Working set went from 27.07 to 29.43 MB, peaking
at 40.10 MB. Private memory went from 65.15 to 64.01 MB, peaking at 74.98 MB. No
PID reuse or missing CPU sample was recorded. The control's CPU delta rounded to
zero and the observer's was 0.015625 seconds at the available process-clock
precision. These finite measurements do not establish an allocation leak.

This run has an important diagnostic-coverage limitation:
`diagnostic_runtime_status=0` and `diagnostic_runtime=null`. No gateway timer,
goroutine or GC/scheduler histogram data was retained in the observer's final
result. Consequently this reviewer makes no assertion about r13-b gateway GC or
internal timer delays. The health requests themselves remained successful.

The gateway's IPv4 listen count changed from two to one. The non-ready trace
artifact's filename contains startup timestamp `1792733019.634043`, whereas the
observer started at `1792733096.954438`, about 77.320 seconds later. This sequence
is consistent with the private listener's 120-second lifetime expiring before the
observer's final runtime request; it does not by itself prove that cause. The
parent was notified to correlate its already-owned child-start timestamps. No
ready-file keys or credential files were read to make this comparison. The final
non-ready HTTP-trace file was 393,177 bytes when listed.

The optional 90-second flight-recording window also begins at gateway init,
rather than observer readiness. A delayed workload/observer start can therefore
exhaust the useful trace interval. For another authorized experiment the parent
should place gateway start, public readiness and observer start in one immediate
orchestration, leaving less than 30 seconds between diagnostic init and a full
90-second observer run. It can instead explicitly obtain the bounded runtime
snapshot before private-listener expiry. The reviewer did not change frozen
helpers or extend their lifetimes during these real tests.

Two independent-health runs now contain 2880/2880 successful results. They do not
explain the old health failures, and r13-b's missing internal runtime snapshot
narrows its causal coverage. The reasoning-only post-long response remains a
separate failed content criterion. Production-binary acceptance is still owned
by the root session.

## Ordinary production-binary experiments r13-prod-a and r13-prod-b

The parent next used the ordinary production binary, identified in the sanitized
extended-result metadata as SHA256
`16fa2b4525aafcc4655da7529ea3fa07a98b43cdb3dbf4bbbcdb6daa1cb92fef`.
The reviewer independently read the two authorized observer results, six prod-a
wave result files and prod-b extended result metadata. No private runtime overlay
was present. `diagnostic_runtime=null` and status zero are therefore expected,
not collection failures or evidence of a gateway stall. No gateway GC/internal
timer claim is made from these ordinary-binary runs.

Each run sampled for 90 seconds, with four modes at 360/360 success each:

| Run / health mode | Successful / total | p95 HTTP duration | Maximum HTTP duration | Maximum planned-start lag |
| --- | ---: | ---: | ---: | ---: |
| prod-a gateway keep-alive | 360 / 360 | 0.5474 ms | 2.0001 ms | 1.6692 ms |
| prod-a gateway fresh TCP | 360 / 360 | 1.3147 ms | 2.1481 ms | 1.6692 ms |
| prod-a control keep-alive | 360 / 360 | 0.5486 ms | 2.0001 ms | 1.6692 ms |
| prod-a control fresh TCP | 360 / 360 | 1.1690 ms | 3.0006 ms | 1.6692 ms |
| prod-b gateway keep-alive | 360 / 360 | 0.6128 ms | 2.3383 ms | 1.8964 ms |
| prod-b gateway fresh TCP | 360 / 360 | 1.4052 ms | 3.6619 ms | 1.3300 ms |
| prod-b control keep-alive | 360 / 360 | 0.5334 ms | 2.3383 ms | 1.8964 ms |
| prod-b control fresh TCP | 360 / 360 | 1.4347 ms | 3.6619 ms | 1.5277 ms |

All 2880 independent health results succeeded. Neither run had a 50-ms-or-longer
HTTP sample, timeout or automatic capture. Planned-start lateness remains a
separate measurement from each request's elapsed HTTP duration. Maximum gateway
phase durations in prod-a were connect 2.0001, write 1.3317, headers 1.2358 and
body 0.6504 ms; in prod-b they were 1.9499, 1.0426, 1.9929 and 1.2764 ms.
Owned control children exited successfully in both runs.

### Actual load-window coverage

Actual health-request envelopes, computed from first request start through last
request completion, were:

- prod-a UNIX `[1792733318.890778, 1792733408.6415846]`, approximately
  `2026-10-23T05:28:38.890778Z` through `05:30:08.641585Z`.
- prod-b UNIX `[1792733497.240125, 1792733586.9906828]`, approximately
  `2026-10-23T05:31:37.240125Z` through `05:33:06.990683Z`.

The observer result's final timestamp includes about two additional seconds
waiting for the control child; that tail is not counted as health sampling.

All six prod-a wave orchestration windows are fully inside actual sampling:

| Wave | Start UTC on 2026-10-23 | Finish UTC | Entire-window intersection | Health samples inside window per mode |
| --- | --- | --- | ---: | ---: |
| 1 | 05:28:38.910 | 05:28:44.562 | 5.652 s | 22 / 22 successful |
| 2 | 05:28:44.565 | 05:28:50.650 | 6.085 s | 25 / 25 successful |
| 3 | 05:28:50.653 | 05:28:55.661 | 5.008 s | 20 / 20 successful |
| 4 | 05:28:55.663 | 05:29:00.960 | 5.297 s | 21 / 21 successful |
| 5 | 05:29:00.963 | 05:29:06.052 | 5.089 s | 20 / 20 successful |
| 6 | 05:29:06.055 | 05:29:10.863 | 4.808 s | 19 / 19 successful |

These six windows contain 508/508 independent health observations, including
254 gateway requests. Actual server request windows reconstructed separately
from each audit's start and total duration also sit within the same coverage.
All six result files independently satisfy the strict streamed-content criterion:
108/108 HTTP 200, nonempty content, completed protocol and DONE, matching `ok`
audits, one actual attempt each, and active zero after every wave. Audit matched
counts total 108, with zero missing, dropped or write-failure counts.

Prod-b's outer harness began about 81 ms before observer sampling. To avoid
treating that setup as model traffic, case windows below are reconstructed from
the actual server audit start through completion/cleanup:

| Case | Server window UTC on 2026-10-23 | Entire-window intersection | Health samples inside window per mode |
| --- | --- | ---: | ---: |
| Two cold nonstreamed replies | 05:31:37.268044–05:31:40.542044 | 3.274 s | 13 / 13 successful |
| Long streamed reply | 05:31:40.620645–05:32:47.225645 | 66.605 s | 266 / 266 successful |
| Eighteen post-long reuse calls | 05:32:47.315991–05:32:53.211793 | 5.895802 s | 23 / 23 successful |

These server windows are fully inside actual sampling and contain 1208/1208
independent health observations. The long request alone contains 1064/1064,
including 532 directly against the gateway. Sample membership uses actual request
start timestamps, not only nominal polling cadence.

The two cold JSON replies have nonempty content and matched successful audits.
The long reply has 7077 content characters and 5995 reasoning characters, HTTP 200,
DONE, one-attempt `ok` audit and zero active afterwards. Its provider completion
arrived at 66.30689 seconds (the parent's rounded 66.307 seconds), DONE/client
elapsed was 66.53591 seconds, and audit completion including cleanup was 66.605
seconds; those are distinct timestamps. Finish reason was `length`. The long
case's own 67 health and connection-monitor polls all succeeded, with maxima
18.12 and 28.49 ms respectively. All 18 post-long calls have nonempty content,
DONE and successful one-attempt audits, no missing/dropped/write-failure counts,
and active zero. This production-binary reuse trial passed the strict content
gate; the earlier instrumented r13-b reasoning-only reply remains a failed
historical observation.

### Independent clocks and resource observations

Observer/control timer-lag maxima were 3.2091 / 14.2646 ms in prod-a and
1.7567 / 2.8917 ms in prod-b. The largest control inter-tick interval in prod-a
was 113.0475 ms. This small control-only timer delay did not become a slow health
request and does not resemble the old one-second incident. No near-one-second
gap appears in these independent timer histories. Both runs contain 899 observer
ticks, 919 control ticks and 180 resource samples. Maximum resource collector
durations were 3.2363 and 5.4529 ms respectively.

| Production run | Gateway Windows CPU gained | Peak relative to one core | Working set first / last / peak | Private memory first / last / peak |
| --- | ---: | ---: | --- | --- |
| prod-a | 1.359375 s | 28.13% | 20.16 / 26.82 / 40.98 MB | 55.98 / 62.48 / 78.91 MB |
| prod-b | 1.515625 s | 25.01% | 20.34 / 38.05 / 38.05 MB | 56.17 / 76.88 / 76.88 MB |

No PID reuse was observed. Listen counts remained one as expected for the ordinary
server; established IPv4 entries went from two to six in prod-a and two to five
in prod-b. Those aggregate process-wide sockets include keep-alive/upstream
connections and must not be equated to retained active chat requests. The finite
memory samples provide no proof of long-term retention behavior or a leak.

## Timestamp-only check of the old cap-three failure

The older failed wave is recorded in the research at
`2026-10-23T01:41:50.480Z`–`01:42:05.427Z`, which is **09:41:50–09:42:05 on
October 23, 2026, Asia/Shanghai**. Its JSON file's current creation/write times
are `01:42:05.439944Z` / `01:42:05.442061Z`, consistent with publication just
after that wave. This timestamp check did not reread any credentials or run code.

Available local metadata does not demonstrate overlap with the documented full
quality gate:

- `live-integrated-quality-review.md` was last written at
  `2026-10-22T21:32:07.461660Z`, about 4 h 9 min 43 s before the failed wave.
  Its text states CPU-heavy checks finished before subsequent real measurements.
- The then-existing `.local-test/server.exe` was last written at
  `2026-10-22T21:19:06.324680Z`, about 4 h 22 min before that wave.
- The independent `health-control.exe` was built/written at
  `2026-10-23T01:17:49.630495Z`, about 24 minutes before it.

File times are artifact publication/last-write times, not a process CPU or start/
stop trace. They cannot exclude another unrecorded build, test, tool process or
OS activity. No process-history evidence was found here that justifies assigning
the old health timeout or long preparation interval to quality checks. Neither
the original failure nor its cause is overwritten by these later successful
ordinary-binary experiments.

Current disposition: private diagnostics and the two finite ordinary-binary
experiments have produced successful health evidence with verified load overlap.
The historical stall cause remains open; it is not assigned to Python, Windows,
Go GC, upstream inference or gateway synchronization by these results. The root
session owns the overall real-load acceptance and any further user-facing action.

## Final aligned diagnostic experiment r13-c

The parent started the diagnostic gateway and observer together. This reviewer
read only the sanitized observer result and six completed wave result files;
no ready/key file, credential or response content was read, no traffic was
started, and the frozen helpers/product sources were unchanged.

Each of the four health modes returned HTTP 200 on 360/360 observations:

| Health mode | p95 HTTP duration | Maximum HTTP duration | Maximum planned-start lag |
| --- | ---: | ---: | ---: |
| Gateway keep-alive | 0.6349 ms | 2.3432 ms | 1.3382 ms |
| Gateway fresh TCP | 1.4907 ms | 3.5155 ms | 1.4801 ms |
| Control keep-alive | 0.5724 ms | 2.3432 ms | 1.6872 ms |
| Control fresh TCP | 1.3165 ms | 2.0044 ms | 1.6872 ms |

All 1440 observations succeeded, with no timeout, failing phase, or HTTP duration
of 50 ms or longer. Automatic HTTP retries were zero. Gateway phase maxima were
connect 2.3222, write 0.7841, headers 1.2921 and body 0.9985 ms. Scheduling lag
remains distinct from these actual network-operation durations.

Actual health sampling ran from `2026-10-23T05:40:43.362063Z` through
`05:42:13.112908Z`, UNIX `[1792734043.362063, 1792734133.1129084]`.
All six outer wave windows and independently reconstructed server audit windows
are fully inside that envelope:

| Wave | Outer window UTC on 2026-10-23 | Entire-window overlap | Health starts per mode, outer / server-audit window |
| --- | --- | ---: | ---: |
| 1 | 05:40:43.385–05:40:50.522 | 7.137 s | 28 / 28 |
| 2 | 05:40:50.524–05:40:55.847 | 5.323 s | 21 / 21 |
| 3 | 05:40:55.850–05:41:01.419 | 5.569 s | 23 / 22 |
| 4 | 05:41:01.422–05:41:19.387 | 17.965 s | 72 / 71 |
| 5 | 05:41:19.390–05:41:25.264 | 5.874 s | 23 / 23 |
| 6 | 05:41:25.266–05:41:30.140 | 4.874 s | 20 / 19 |

This yields 748/748 health observations inside outer wave windows, including 374
gateway observations; the tighter server audit windows contain 736/736, including
368 gateway observations. Counts use actual probe start timestamps.

The six waves independently satisfy the strict streamed-content gate: 108/108
HTTP 200 with nonempty content, completed protocol, DONE and matching `ok` audit.
All 108 audits record one attempt. Missing/dropped/write-failure counts and every
wave's final active/matching-active/overflow/trace-truncated counts are zero.
Maximum recorded local account wait was 3032 ms. The waves' own health and
connection monitors also completed without exceptions.

The longest request in wave 4 was GLM, 17787.43 ms client elapsed. Its audit
separates 2323 ms waiting for an account, 77 ms preparing upstream, 15003 ms in
`wait_headers`, 381 ms streaming and 89 ms cleanup. First content arrived at
17538.64 ms; it did return content and DONE. The phase label locates this delay
before response headers, but does not by itself identify a provider, network or
transport-internal cause. The independent health and timer records stayed fast
through this interval. It is not evidence of a process-wide gateway freeze or an
explanation of the historical failed wave.

### Internal runtime coverage and independent clocks

Unlike r13-b, the final runtime snapshot succeeded with HTTP 200. Its ring retains
900 ticks from `05:40:43.415367Z` through `05:42:13.315721Z`; total tick count is
906. Its first retained tick is 23.157 ms after the first actual server audit
start (`05:40:43.392210Z`). The 85 retained metrics snapshots span
`05:40:43.915431Z`–`05:42:12.615572Z`, starting 523.221 ms after that first audit.
Thus health coverage is complete, while gateway ticks and metric deltas omit a
small prefix of the first wave. They fully enclose the remaining five waves.

Gateway, observer and minimal-control maximum timer lags were respectively
1.8663, 1.7297 and 1.5806 ms. Maximum inter-tick gaps were 101.3874, 101.1664 and
101.2702 ms against their approximately 100-ms cadence. No near-one-second gap
appears. Gateway goroutines peaked at 97 and ended at 18. No capture was triggered,
so there was no stack/trace capture pause to interpret or causally attribute.
The owned control child stopped and exited successfully.

Histogram conclusions use last-minus-first retained counts, rather than lifetime
totals: 49 GC cycles, 98 GC pause observations and 6213 scheduler observations
were added. Both the largest newly occupied GC-pause bucket and the scheduler
bucket were `[1.310720, 1.572864)` ms. No negative counter delta or newly recorded
pause/delay of 100 ms or longer appears; non-GC stop-the-world pause count did not
increase. These are histogram bounds, not exact individual pause times, and do
not cover the omitted first 523 ms. `ReadMemStats` was not used.

Across 180 native process samples, gateway Windows CPU gained 2.421875 seconds,
peaking at 21.88% of one logical core over the sampled intervals. Working set
first/last/peak was 22.49/40.68/50.05 MB; private memory was
61.64/78.74/91.06 MB. No PID reuse or missing CPU sample was recorded. Control
and observer CPU deltas were 0.031250 and 0.140625 seconds at the available
process-clock resolution. Collector work peaked at 5.4161 ms. Runtime CPU-class
estimates are not substituted for Windows process CPU. Aggregate TCP entries
are not treated as active-chat counts, and this finite memory series does not
establish long-term retention behavior.

## Targeted DeepSeek control experiment r13-d

The final authorized targeted replay used the same combined diagnostic binary.
Independent parsing of its six wave files confirms 108/108 DeepSeek streams with
nonempty content, completed protocol, DONE, successful matched audits and one
attempt each. All final active/overflow/trace-truncated counts and all audit
missing/dropped/write-failure counts are zero. Each wave is 18 concurrent requests
with `max_tokens=256`; no additional traffic was run by this reviewer.

The actual health envelope was `2026-10-23T05:45:40.726588Z` through
`05:47:10.476791Z`, UNIX `[1792734340.726588, 1792734430.4767911]`:

| Wave | Outer window UTC on 2026-10-23 | Entire-window overlap | Health starts per mode, outer / server-audit window |
| --- | --- | ---: | ---: |
| 1 | 05:45:40.751–05:45:45.165 | 4.414 s | 17 / 17 |
| 2 | 05:45:45.168–05:45:48.343 | 3.175 s | 13 / 13 |
| 3 | 05:45:48.346–05:45:52.227 | 3.881 s | 15 / 15 |
| 4 | 05:45:52.229–05:45:55.785 | 3.556 s | 14 / 13 |
| 5 | 05:45:55.795–05:45:59.959 | 4.164 s | 16 / 16 |
| 6 | 05:45:59.962–05:46:04.362 | 4.400 s | 18 / 18 |

All wave/server windows are fully covered. They contain respectively 372/372 and
368/368 successful health observations. Across the full run, all four modes
succeeded at 360/360 each, 1440/1440 total, with no timeout, failing phase or
50-ms HTTP sample. Gateway keep-alive/fresh maxima were 1.8988/2.3917 ms;
control keep-alive/fresh maxima were 2.9950/2.3917 ms. Planned-start lag never
exceeded 1.3650 ms. Automatic retries and captures were zero; the control child
stopped and exited successfully.

Runtime collection again returned HTTP 200. Gateway/observer/control timer-lag
maxima were 1.7593/1.7355/1.8139 ms, with largest inter-tick gaps approximately
101.4 ms. The runtime ring's first retained tick is 13.949 ms after the first
actual server request; its 85-metric delta interval begins 514.135 ms after that
request. The later five waves are fully enclosed by both intervals. New GC
cycles/pauses were 34/68, with highest occupied GC-pause bucket
`[1.048576, 1.310720)` ms. The largest new scheduler bucket was
`[0.917504, 1.048576)` ms. There were no negative histogram deltas or new non-GC
stop-the-world pauses. No new timing or control concern appears in this run.

### Bounded final disposition

The aligned r13-c and r13-d diagnostic runs add 2880/2880 successful independent
health observations with verified actual-load overlap. Ordinary production-binary
evidence remains the separately reviewed prod-a/prod-b results above, including
cold replies, a useful generation beyond 30 seconds and successful post-long
reuse. These finite evidence records are approved for reporting within those
boundaries. The old cap-three failure and r13-b reasoning-only content failure
remain failed historical observations; neither is erased by a later pass. The
historical one-second health stall still has no discriminating captured failure
and no established root cause. This report does not approve marking that cause
fixed, making an unconditional stability claim, or claiming production deployment.
