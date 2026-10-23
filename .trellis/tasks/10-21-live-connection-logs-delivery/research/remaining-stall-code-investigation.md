# Remaining preparation/health stall: source investigation and capture plan

Date: 2026-10-23, Asia/Shanghai. This investigation continues after the user
rejected closing the incident with unexplained failed trials. A later successful
wave is recovery evidence, not a diagnosis of an earlier stall.

Scope: task manifests, PRD/design/implementation notes, request-lifecycle
contract, relevant product source and the installed Go 1.27.3 standard-library
source. No credentials, runtime/key/server configuration or raw completion/log
files were read. No real HTTP request was sent by this investigator. The parent
performed the bounded fixed-pattern log check described below. Product source
was not changed; only this report and ignored private build-overlay files were
written. Runtime sampling/mux instrumentation belongs to the separate reviewer.

## Evidence that needs an explanation

The first per-account-cap-3 wave had nine content/DONE completions and nine local
queue timeouts with zero upstream attempts. The successful holders spent
3.007-11.450 s in `prepare_upstream`; their local account-slot and credential
validation phases were zero at the recorded precision. See
`health-stall-investigation.md` for the actual phase table and prior health
deadline failures.

The parent's bounded regex extraction found nine fixed `busy`/`timeout` session
log records in that failed wave. Eight were timestamped around 09:41:53-54 local
time, approximately three/four seconds after its 09:41:50 start; the last was
timestamped around 09:42:01, approximately eleven seconds after start. The
successful later controlled wave had no session-error record. These are new
facts about initial busy-notification failure, not proof of a provider response,
an eleven-second network call, or the cause of delayed cancellation.

The successful repeat cannot explain that difference. Historical one-second
health/monitor deadlines remain part of the evidence and still require
correlation with an actual failing interval.

## Exact source boundaries

Locations refer to unmodified product files, not the longer overlay copies.

| Source | What runs in the measured interval | What existing telemetry misses |
| --- | --- | --- |
| `internal/server/chat.go:358-375` | Starts `prepare_upstream`; reads account/auth snapshots, credentials and cached model resolution; updates the tracker/traffic/keepalive; calls the selected account client | No timestamp around these individual synchronized operations |
| `internal/pool/pool.go:154-160`, `internal/auth/auth.go:145-148`, `internal/upstream/models.go:232-251`, `internal/server/availability.go:198-205`, `internal/pool/pool.go:676-686` | Account/auth/catalog/availability/pool locks, copied metadata and keepalive timestamp updates | Lock acquisition/hold time is not separately measured; `PingKeepalive` itself performs no network request |
| `internal/upstream/client.go:472-474`, `:559-594` | Converts messages/builds body, marshals JSON, creates the request, constructs the managed session and signs inference | All precede the `WaitHeaders` phase |
| `internal/upstream/session_lifecycle.go:34-60` | Creates a random owned session ID, builds context/endpoint and synchronously performs initial busy before launching renewal | The first random ID is generated before the notification timeout context exists |
| `internal/upstream/session_lifecycle.go:112-155` | Creates the at-most-3-second context, constructs notification, creates its trace ID, signs, calls short HTTP client, drains bounded body, closes and logs fixed failures | Request preparation, random/signing work, Do, body drain, deferred close and logger are not individually timed |
| `internal/upstream/client.go:595-621` | Sets `wait_headers`, executes inference Do, then enters first-SSE preflight | `wait_headers` is not a TCP-connect measurement; initial busy has already returned |
| `internal/upstream/client.go:736-746`, `internal/upstream/signer.go:31-60` | Crypto randomness, local HMAC/header canonicalization and synchronous standard logger | These do not accept an operation context or expose timing events |

`internal/telemetry/context.go:42-86` carries only bounded phase/progress hooks.
`internal/audit/connections.go:145-153` takes the tracker mutex and records the
phase time after acquiring it. These existing phases locate the coarse boundary
but cannot identify a DNS lookup, a busy-response body stall or a blocked logger.
The tracker mutation path performs no I/O and takes no pool/account/stream lock.
None of this proves a lock caused the observed delay.

## Transport and deadline facts

`internal/upstream/client.go:141-165` constructs one `http.Transport` per client
and shares it between the short client and the stream client. The short client
has a total timeout; the stream client has no total timeout and uses the
separate progress-aware wait owner. Dial, TLS and response-header waits use the
configured timeout, usually longer than the notification context. Busy/idle
notifications still carry the independent shorter context.

The transport sets 20 global idle slots, four idle slots per host and a 90-second
idle lifetime. It does not set `MaxConnsPerHost`. The installed standard library
documents this zero active-connection cap as unlimited; four **idle** slots must
not be described as a four-active-request limit. The transport's Proxy is nil,
so this constructor does not select `ProxyFromEnvironment`. Do not introduce an
environment-proxy explanation without another evidenced caller/transport.

The constructor uses a custom DialContext and does not opt into
`ForceAttemptHTTP2`. The inspected Go 1.27.3 transport conservatively omits
automatic HTTP/2 in that case (`src/net/http/transport.go:480-499`). The private
trace records numeric response protocol to check the actual run rather than
assuming HTTP/2 multiplexing behavior. This investigation does not propose a
transport-policy change.

`context.WithDeadlineCause` schedules cancellation with `time.AfterFunc` in the
installed standard library (`src/context/context.go:633-657`). It sets a
cooperative cancellation deadline, not a goroutine preemption guarantee. A
process/scheduler pause or arbitrary synchronous work can delay observation and
return. An expired context alone cannot interrupt random-byte generation,
signing, logging or an unrelated mutex. The observed 11.450 s phase also begins
before this timeout is created, so subtracting three seconds from the whole
phase does not establish a network overrun.

The standard logger captures its timestamp early, then formats and takes
`outMu` before synchronous writer I/O (`src/log/log.go:201-245`). Thus an existing
stderr timestamp is not a log-write-completion time or a context-timer callback
time. The new pre/post log markers can distinguish a long logging call from a
long interval before it; neither is inferred from the old records alone.

## Smallest useful diagnostic hook

A server-side test middleware can add `httptrace.WithClientTrace` to the inbound
request context without product changes. `chat.go:60`, session-derived contexts,
`newStreamWait` and detached idle preserve its context values. A trace attached
only to the external caller's request traces its connection to the gateway and
does not cross that HTTP boundary. An inbound telemetry Observer would be
replaced by the handler's own Observer; the separate standard httptrace value
survives.

One inherited ClientTrace is insufficient for reliable per-operation attribution:
initial busy, inference, renewal and idle share it, and later callbacks can
overlap. Account clients have private HTTP fields, explicitly constructed
transports, and upstream test helpers are unavailable to imported server tests.
Replacing `http.DefaultTransport` therefore does not reach this client.

The implemented ignored overlay wraps the shared short/stream transport once and
adds one fresh ClientTrace per outbound RoundTrip. It leaves the original
transport, signing and request body ownership in place. Contexts are derived
with value-only tracing; caller cancellation/deadline and existing hooks are
preserved. It also adds fixed local stamps to copied client/session files to
cover the non-network intervals identified above. This is temporary evidence
collection, not a new product audit system.

Files: `.local-test/stall-http-overlay/{client.go,session_lifecycle.go,http_trace.go,
prepare_overlay.py,overlay.json,overlay-tests.json,http_trace_test.go,README.md}`.
The three production-build Replace entries are the client copy, session copy and
one added virtual upstream helper. They do not overlap the reviewer's cmd/main
runtime observer replacement and can be merged by the parent.

Enablement is explicit through `CODEARTS2API_PRIVATE_HTTP_TRACE_DIR` at process
start. Its pre-existing real path must be under the resolved workspace
`.local-test` root. Each process-local call/notification/HTTP operation receives
independent numeric sequence metadata; no real account/session/request identifier
is used. The sink has a 4,096-record nonblocking channel, one asynchronous writer,
100-ms flushing and a 100,000-record cap. Full queues drop and count records.
No callback performs synchronous diagnostic I/O; original product logging is
left intact so its delay remains observable.

Recorded values are fixed endpoint/event/error/status enums, sequence numbers,
Unix and monotonic times, deadlines, numeric HTTP status/protocol, connection
reuse/idle flags and safe counts. Raw URLs, query strings, host/address values,
headers, signed credentials, message/response bodies, certificates, arbitrary
errors and remote IDs are never encoded. The DNS/TLS hooks explicitly ignore
their sensitive arguments. Inspect trace completeness/drop counts before using
the intervals.

## Falsifiable hypotheses and required observations

| Hypothesis to test | Observation supporting that interval | Observation that rejects/narrows it |
| --- | --- | --- |
| Delay before the upstream client | Coarse `prepare_upstream` begins long before `body_build_begin` while process-local timers continue normally | Body-build stamp appears promptly; no pre-call gap |
| Randomness or signing stalls before HTTP | `session_id` / `notify_id` / `notify_sign` or inference-sign marker pair has the long gap, before RoundTrip/GetConn | Marker pairs remain short and delay occurs later |
| DNS lookup delay | Per-operation DNSStart-to-DNSDone envelope occupies the delay; dial follows late | The connection is reused or DNS completes quickly before the delayed region |
| Dial/TLS establishment delay | Connect or TLS envelope occupies the delay with fresh connection acquisition | GotConn is prompt/reused and connect/TLS finish before the gap |
| Transport acquisition/internal wait | GetConn-to-GotConn is long without a corresponding DNS/dial/TLS envelope | GotConn is prompt; delay begins after request write or outside RoundTrip |
| Network/provider response wait | Request-written-to-first-response-byte interval is long while runtime/control sampling remains responsive | No request-write event yet, notification Do already returned, or the process itself has a matching pause |
| Notification body drain or close | First-byte/Do return is timely but read/close marker pair is long | Read and close pairs are short and the gap is elsewhere |
| Synchronous logger blocks | `notify_log_begin` to `notify_log_done` contains the gap | Log markers are close and the delay precedes/follows them |
| Process-wide/runtime scheduling pause | Process-local ticker lag, independent runtime/GC/OS scheduling evidence and multiple otherwise unrelated operations stall over the same interval | Timers/control threads and unrelated in-process work progress throughout a specifically located network/local wait |

These are location hypotheses, not mutually exclusive root-cause findings.
GetConn-to-GotConn includes connection construction and cannot be called pool
queue time by subtraction alone. DNS/dial hooks may overlap or finish after
RoundTrip; do not zip/sum overlapping starts and ends blindly. A reused socket
can have no DNS/TLS callbacks. First-response-byte marks headers at the client,
not provider generation start. Without server timing or kernel-arrival evidence,
network versus provider execution versus delayed client read cannot be fully
separated by that one interval.

The runtime observer must independently retain a monotonic scheduling-lag series,
GC/pause counters and process/thread context over the same real failed wave.
The external control distinguishes a shared environment window from one
process's delay; a healthy control alone cannot rule out a gateway-process
pause. If the trace only shows a late return after a deadline, the next capture
needs actual context-done callback timing and a bounded runtime/thread trace,
not a larger timeout or an asserted provider fault. No raw heap, header or packet
dump is needed for this first capture.

## Validation and current limit

- Five focused overlay tests passed, using only fake RoundTrippers/in-memory
  sinks: header/body ownership, deadline/context propagation, hook composition,
  request/response/error-string exclusion, nonblocking bounded drops, directory
  restriction and timeout notification/log-marker coverage.
- A full `cmd/server` diagnostic overlay build passed with the installed Go
  1.27.3 toolchain. No diagnostic gateway was started by this investigator.
- The wrapper's concrete type is unknown to net/http's optimized transport
  recognition (`src/net/http/client.go:322-421`), so the short HTTP client's
  legacy timeout watcher can also be installed. Existing context/cancel/body
  semantics are preserved, but capture overhead is not zero; a trace binary is
  not bit-for-bit timing-equivalent to the source release.
- Product tests previously passed for the product repair. These private
  diagnostic checks do not prove that the remaining intermittent stall is fixed.

Next evidence is the parent-operated failed-or-successive real wave with the
merged HTTP/runtime overlay and overlapping control. A successful capture
without recurrence establishes only its measured healthy window. The incident
cannot be closed on that basis while the failed interval remains unexplained.
