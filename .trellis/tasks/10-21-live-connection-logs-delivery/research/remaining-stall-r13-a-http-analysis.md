# R13 HTTP-stage evidence: successful slow requests and diagnostic coverage

Date: 2026-10-23. This report uses private, sanitized HTTP JSONL only, plus the
parent's sanitized request/audit results for two matched r13-a requests. It
contains no credentials, headers, URLs, account identifiers, prompts or replies.
The parent owns actual traffic and runtime/control comparison. This worker made
no network requests and changed no product source.

## Conclusions and evidence boundary

The r13-a trace covers 108 inference attempts and their 216 session notifications.
All 339 outbound HTTP operations, including 15 other operations, returned
HTTP200. There are no recorded timeout/transport failures, negative remaining
deadlines or dropped diagnostic events. Initial session preparation completed in
at most 484.846 ms. The two slow successful end-to-end requests are explained at
the measured stage boundary: one spent 9.404 seconds after response headers in
stream consumption; the other spent 7.860 seconds between writing inference and
receiving its first response byte. These are different from the historical
multi-second initial-busy timeout symptom.

This run did not reproduce the historical notification timeout. It cannot prove
what caused that failure or that the cause was corrected. HTTP callbacks alone
cannot distinguish provider execution, delayed network delivery and reader/runtime
scheduling inside a long wait. The parent/reviewer own the independent runtime
and health-control evidence and the broader acceptance decision.

The r13-b final file is also complete and readable. An earlier directory-length
snapshot showed zero while the file was open; directly reading final content
found 393,177 bytes and 1,781 valid records. It was incorrect to infer an empty or
broken writer from that metadata snapshot. No writer malfunction was established.

## Inputs and version boundaries

- r13-a: ignored `stall-observer-run-r13-a/http-trace-55504-1792732729007972300.jsonl`,
  985,824 bytes read, 5,355 valid JSONL records.
- Request correlation: ignored sanitized `results/r13-a-wave-3.json` and
  `results/r13-a-wave-5.json`, checked only for timing/count/audit metadata.
- r13-b: ignored `stall-observer-run-r13-b/http-trace-52908-1792733019634043000.jsonl`,
  393,177 bytes read, 1,781 valid JSONL records.

r13-a was built before the session-source overlay mapping was added. It records
body conversion, marshal, coarse session preparation and inference signing, but
does not record notification-local ID/sign/Do/read/log boundaries, notification
kind/sequence or negotiated protocol. Initial busy is classified by a session
RoundTrip falling inside that call's `session_begin` to `session_ready` interval;
the later session operation is cleanup. Exactly one of each exists per call.
The absence of a protocol field is not protocol zero and establishes no HTTP/2
claim. r13-b includes the session mapping and protocol field.

Timings below are monotonic differences in milliseconds. P95 uses nearest rank;
medians use the ordinary middle-value mean for an even sample count. Per-hook
DNS/dial/TLS envelopes are observed operations, not additive request phases.

## r13-a totals and timing

| Operation | Count | Headers median | Headers P95 | Headers maximum |
|---|---:|---:|---:|---:|
| Inference | 108 | 931.607 | 1,524.443 | 7,860.161 |
| Session notification | 216 | 85.302 | 273.500 | 483.808 |
| Other upstream HTTP | 15 | 201.810 | 453.368 | 453.368 |

All 339 have a start, response-header return and body-close completion. The only
`error_class` observations are 231 normal EOF reads from drained session/other
bodies. Canceled-context callback records do not mean failed requests: inference
bodies are closed after protocol completion, and speculative dial callbacks can
finish after the operation has already used a different connection.

| Inference boundary | Count | Median | P95 | Maximum |
|---|---:|---:|---:|---:|
| GetConn to GotConn | 108 | 0.000 | 77.652 | 113.792 |
| DNS envelope | 31 | 0.000 | 1.075 | 1.086 |
| Connect envelope | 31 | 55.828 | 58.558 | 59.480 |
| TLS envelope | 31 | 64.163 | 117.731 | 118.508 |
| Request written to first byte | 108 | 918.222 | 1,522.122 | 7,860.161 |
| Headers returned to body-close start | 108 | 587.946 | 2,379.135 | 9,403.655 |
| Explicit transport close | 108 | 0.000 | 0.929 | 1.480 |

All 108 inference GotConn callbacks report a reused connection. The 31 DNS/dial/TLS
envelopes belong to raced connection establishment: 78 TLS completions across all
endpoints occurred after their GotConn, and 25 after their RoundTrip had returned.
They must not be added to already-served requests' critical paths. The largest
session dial envelope was 1,063.012 ms, although all session header returns were
under 484 ms; an overlapping late dial is not a one-second session stall.

Session DNS/connect/TLS envelope maxima were 5.437 / 1,063.012 / 575.311 ms.
Other-endpoint maxima were 13.125 / 57.567 / 179.916 ms. GetConn-to-GotConn includes
all connection acquisition work and is not a pure idle-pool queue measurement.

| Preparation or cleanup boundary | Count | Median | P95 | Maximum |
|---|---:|---:|---:|---:|
| Entire initial session preparation | 108 | 85.738 | 286.682 | 484.846 |
| Initial busy RoundTrip through body close | 108 | 85.738 | 286.682 | 484.846 |
| Final idle RoundTrip through body close | 108 | 85.996 | 263.640 | 355.388 |
| Body conversion | 108 | 0.000 | 0.000 | 0.514 |
| JSON marshal | 108 | 0.000 | 0.034 | 1.005 |
| Inference signing | 108 | 0.000 | 0.038 | 1.001 |

There is no gap over two seconds in the recorded pure local pairs or the entire
coarse initial-session interval. Four inference header waits exceed two seconds:
2,424.629 / 2,280.269 / 2,145.554 / 7,860.161 ms. Body reads also include a
7,789.020-ms wait. These waits include I/O and scheduling; labeling them pure
synchronous computation would be wrong. Work before `body_build_begin`, including
account admission and credential preparation, is outside this HTTP helper.

## Matched slow successful requests

These matches use the parent's audit start plus queue/preparation/header/cleanup
offsets, checked against JSONL wall timestamps and monotonic durations. HTTP
call_seq is a process-local counter, not the gateway request ID. Rounded audit
milliseconds and callback/write boundaries are not expected to be identical.
All wall times in this section are UTC on 2026-10-23.

### Wave 3, index 13, GLM: 11,825.99-ms client duration

Matched HTTP call 197, inference RoundTrip 201. Audit starts at
05:19:01.1166793; account queue consumes 1,387 ms. Body preparation begins at
05:19:02.503. Initial busy runs 05:19:02.504 to 05:19:02.632 (127.439 ms);
the coarse initial-session interval is 128.470 ms. Inference starts at
05:19:02.632, and headers return at 05:19:03.537 (905.666 ms). Body close begins
at 05:19:12.941: the post-header interval is 9,403.655 ms. Idle then completes at
05:19:13.016, another 75.125 ms.

The audit timeline independently reports offsets 1,387 ms prepare-upstream,
1,515 ms wait-headers, 2,421 ms first SSE/stream, 11,824 ms cleanup and
11,901 ms finished. Client delivery finishes before bounded idle cleanup, so its
11,825.99-ms duration is shorter than the 11,901-ms audit total. The client reports
DONE, two content characters, 560 reasoning characters and finish=stop; audit is
ok. This measured slow request is predominantly stream/read duration after
headers, with earlier account waiting. It is not a slow initial busy notification.

### Wave 5, index 1, GLM: 10,750.18-ms client duration

Matched HTTP call 361, inference RoundTrip 367. Audit starts at
05:19:18.764767; account queue consumes 2,374 ms. Body/initial-session preparation
begins at 05:19:21.139; busy completes at 05:19:21.478 (339.135 ms). Inference
starts immediately, but headers return only at 05:19:29.338: written-to-first-byte
and RoundTrip header duration are 7,860.161 ms. Send returns at 05:19:29.339;
body closes at 05:19:29.490; idle completes at 05:19:29.568 (77.377 ms).

The audit independently reports offsets 2,374 ms prepare-upstream,
2,714 ms wait-headers, 10,574 ms first SSE, 10,725 ms cleanup and
10,815 ms finished. The client reports DONE, two content characters, zero reasoning
characters and finish=stop; audit is ok. The dominant delay is inference response
arrival after writing, plus prior account waiting. HTTP evidence alone does not
attribute the 7.860-second gap to the provider versus network/reader scheduling.

## r13-b notification and long-generation addendum

Direct parsing finds 77 outbound operations: 21 inference, 44 session and 12 other.
All returned HTTP200 with protocol_major=1. All 77 bodies have a recorded close;
there are no recorded transport/timeout errors, negative remaining deadlines or
drops. The only error-category records are 56 ordinary EOF observations.
HTTP200 is transport evidence, not proof that every completion contains a useful
answer; semantic reply verification remains the parent's separate result.

The 44 notifications comprise 23 busy and 21 idle for 21 inference attempts.
Long call 25 has initial busy at 05:25:00.135, renewals at 05:25:30.442 and
05:26:00.442, and final idle at 05:26:01.479. Its inference starts at
05:25:00.441 and closes at 05:26:01.479, approximately 61.038 seconds later.
This is direct metadata evidence of two roughly 30-second renewals and idle for
that owned long attempt, not evidence that every possible cleanup path works.

Busy whole-notification maximum is 498.923 ms; idle maximum is 697.693 ms.
Inference header maximum is 1,749.267 ms. Maximum measured preparation details:
session ID generation 0.529 ms, inference signing 0.544 ms, notification ID
generation 0.000 ms at the clock's observed resolution, notification signing
0.556 ms, notification Do 697.693 ms, body drain 371.551 ms and close 0.137 ms.
The 21 coarse initial-session intervals max at 499.430 ms. None of those
notification/local pairs exceeds two seconds. No notification log boundary
appears because the recorded notifications did not enter an error-log branch.

The earlier zero file-length observation is superseded by readable final content.
Whether that stale observation arose specifically from Windows open-file metadata
refresh was not independently proven; it is not evidence of a writer failure.

## Private helper startup validation

The original helper's five tests exercised memory sinks but not the actual
environment/once/constructor/file-writer path. A new isolated subprocess test
uses only a fake RoundTripper and temporary files. Before any helper change,
that test passed and flushed request/close events in approximately 0.15 seconds.
There was no reproduced disk-writer bug to repair.

The ignored helper now adds immediate `sink_started` and approximately one-second
`sink_pulse` records on its sole writer goroutine. They contain fixed metadata
and safe queue/enqueued/written/drop counters, with no request identifiers.
Encode/flush failures emit only a fixed stderr category. These are diagnostic
observability changes, not a gateway product fix. Callbacks remain nonblocking;
the 4,096-event channel and 100,000-record process cap remain. Before another
experiment, the parent can require a directly read started/pulse within two
seconds, instead of trusting a directory file-length snapshot.

Six focused fake-only tests pass, including that real disk startup/pulse and fake
request flush. The standalone diagnostic overlay compiles `cmd/server`. No server
was started by this worker. The helper, copied source mappings and product files
are frozen; subsequent combined-build execution belongs to the parent.

## r13-c and r13-d directly read HTTP follow-ups

The parent separately owns useful-content, SSE structure, audit and runtime/control
verification. The following are transport-stage results only. Record counts are
snapshots because diagnostic pulses may continue while the service is alive.

| Direct-content snapshot | r13-c | r13-d |
|---|---:|---:|
| Last record UTC, 2026-10-23 | 05:45:39.130 | 05:47:34.383 |
| Bytes read | 2,284,975 | 2,242,900 |
| Valid JSONL records | 8,395 | 8,166 |
| Started / pulse records | 1 / 296 | 1 / 114 |
| Inference / session / other RoundTrips | 108 / 216 / 12 | 108 / 216 / 12 |
| HTTP200 returns | 336 | 336 |
| Busy / idle operations | 108 / 108 | 108 / 108 |
| Recorded drops / negative deadlines | 0 / 0 | 0 / 0 |

All observed RoundTrip protocol-major fields are 1. The only error-category
records in either run are 228 normal EOF reads. These direct reads show the
startup/pulse diagnostics really reached disk and provide finite HTTP coverage;
neither run reproduces an initial-busy timeout or transport error.

| Measured maximum, milliseconds | r13-c | r13-d |
|---|---:|---:|
| Coarse initial-session preparation | 1,303.543 | 1,307.974 |
| Session ID generation | 0.007 | 0.997 |
| Inference signing | 1.000 | 0.884 |
| Notification ID generation | 0.510 | 0.015 |
| Notification signing | 1.030 | 0.999 |
| Notification Do | 1,303.535 | 1,307.974 |
| Notification response drain | 265.990 | 16.129 |
| Notification response close | 0.522 | 0.136 |
| Inference headers | 15,003.598 | 2,736.240 |
| Inference request written to first byte | 15,003.340 | 2,736.240 |
| Inference headers to body-close start | 3,142.530 | 1,870.764 |

No recorded body-build, marshal, session-ID, signing or notification stage pair
exceeds two seconds. r13-c inference header median/P95 is 1,014.480 / 1,605.670 ms;
r13-d is 825.404 / 1,530.241 ms. Their maxima are tail waits, not the median path.
Inference GetConn-to-GotConn maxima are 106.434 / 96.471 ms; measured inference
DNS/connect/TLS envelopes max at 1.204 / 60.445 / 122.437 ms for r13-c and
1.110 / 60.549 / 117.960 ms for r13-d. The earlier raced-dial caveat still applies.

The r13-c 17,787.43-ms client maximum matches wave 4 index 15, GLM, HTTP call 405
and inference RoundTrip 408. The independently sanitized audit starts at
05:41:01.4267625 UTC, records 2,323 ms of account waiting and enters preparation
at offset 2,324 ms. HTTP body/initial-busy preparation starts 05:41:03.751;
busy finishes 05:41:03.827 (75.858 ms). Inference is written at 05:41:03.828,
but first byte arrives at 05:41:18.831, 15,003.340 ms later. The headers return
within 0.258 ms of that byte; body closes at 05:41:19.214 and idle finishes at
05:41:19.300. Audit offsets agree: wait-headers 2,401 ms, first-SSE 17,404 ms,
cleanup 17,786 ms and finished 17,875 ms. The client reports DONE, two content
characters, 222 reasoning characters and finish=stop, with audit ok.

This particular tail is specifically bounded to the written-request/first-byte
wait, preceded by account queueing. It supplies no evidence for a 15-second
initial-busy, signing or DNS/TLS stall. It still cannot separate provider/network
delay from scheduling of the response reader without the separately owned
runtime/control evidence. The earlier failed acceptance wave is not retroactively
explained or converted to a pass by these later successes.

## Remaining limits

- No timeout was reproduced in these HTTP traces. The earlier initial-busy
  timeout lines remain historical failures requiring independent attribution.
- Successful later waves do not identify a historical defect or justify changing
  the account wait, per-account cap or upstream timeout to hide it.
- r13-a cannot split notification random/sign/Do/log internals; r13-b supplies
  that coverage only for its own successful operations.
- Runtime scheduling and OS suspension are not measured by HTTP callbacks alone.
  Wall time is used for matching, monotonic time for intervals; no assumption of
  cross-process clock-perfect equivalence is required.
- The diagnostic wrapper changes net/http concrete transport recognition and
  adds bounded overhead, including a possible legacy short-client timeout
  watcher. A diagnostic success is not bit-identical release timing evidence.
- These files contain stage metadata, not SSE payloads. Any content-loss question
  requires the separately owned SSE structural trace, not inference from HTTP200.
