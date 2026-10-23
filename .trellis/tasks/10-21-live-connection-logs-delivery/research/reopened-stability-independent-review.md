# Independent reopened stability acceptance review

Date: 2026-10-23 (Asia/Shanghai). Active task: `10-21-live-connection-logs-delivery`.
Status: approved for the measured current regression only. Historical stall
attribution and the single earlier reasoning-only answer remain unresolved.

## Review boundary

Read the active PRD/design/implementation context, lifecycle/audit/discovery/release
contracts, previous integrated quality evidence, current safe Git diff and the
explicitly authorized sanitized result files. No credential/runtime/config/key
file was read; no real-provider or gateway traffic was sent by this reviewer.
The parent owns all live calls and process lifetimes. Product files remain
unchanged from`72e83fbed57d862d85e4d20b8b96f7a44e41019d`in the reviewed
`internal`, `cmd`, release scripts and build inputs.

The parent additionally assigned only ignored`.local-test/stall-sse-overlay/`
for a private structural diagnostic overlay. That work observes existing parser
and writer decisions without changing product behavior. This report is the only
task artifact this reviewer edits. Other agents own HTTP/runtime instrumentation
and their reports; do not treat their files as this reviewer's source changes.

The already documented full product test/race/vet/build gate applies to the
unchanged committed product. Repeating it without a source change would add no
coverage of the remaining live incident and could disturb concurrent measurements.

## Measured workload evidence

The mixed-model eighteen-request waves use the two actual target IDs, streaming,
`max_tokens=256`, no automatic client retries, per-account capacity3 and cumulative
account wait10seconds. Successful strict checks require useful content, DONE,
correlated successful audit, and no retained active request. The effective limits
are parent-controlled; this reviewer did not open the real config.

| Run | Scope | Strict result | Evidence boundary |
| --- | --- | --- | --- |
|`r13-a`|six consecutive mixed-model waves|108/108|Private HTTP/runtime diagnostic build; all one-attempt audits, no missing/dropped/write-failure record, each wave active0.|
|`r13-b`|cold2, long1, post-long18|cold and long pass; post-long17/18 useful answers|Keep overall`passed=false`; one DeepSeek produced reasoning and DONE with no content.|
|`r13-prod-a`|six consecutive mixed-model waves|108/108|Ordinary production build, SHA256`16fa2b4525aafcc4655da7529ea3fa07a98b43cdb3dbf4bbbcdb6daa1cb92fef`; every wave has18 one-attempt ok audits and active0. Maximum client duration5.921seconds.|
|`r13-prod-b`|cold2, long1, post-long18|all pass|Same ordinary build; both cold targets have useful replies. Long stream has7077content characters,5995reasoning characters,66.307seconds to finish, DONE, successful one-attempt audit and active0. Post-long18/18have useful content/DONE/audits and active0.|
|`r13-c`|six consecutive mixed-model waves|108/108|Combined private HTTP/runtime/SSE build. Structural evidence directly read below; ordinary-build acceptance is established separately.|
|`r13-d`|six consecutive DeepSeek-only waves|108/108|One final bounded reproduction attempt for the reasoning-only failure; no recurrence or conversion loss in complete structural evidence.|

The diagnostic waves' longer successful calls are not recurrence of the old
preparation stall by duration alone. In`r13-a`, wave3has a9.403-second streaming
phase; wave5has a7.860-second header wait. Preparation maxima across the six waves
are485,220,199,274,340and218milliseconds. The HTTP investigator separately owns
the transport-phase explanation. Neither long streaming nor written-to-first-byte
time is automatically local lock time or provider execution time.

The later r13-c maximum, 17.787 seconds, was also localized by the HTTP investigator:
2.323 seconds account waiting, 75.858 ms initial busy, then 15.003 seconds from
inference write to first response byte. It was a successful useful response, not
an initial-busy timeout. Current runtime/health sampling stayed responsive in
that window. This narrows the measured wait location without proving whether
network delivery or provider execution caused the tail.

Source result paths are ignored local artifacts under`.local-test/results/`:
`r13-a-summary.json`, six`r13-a-wave-N.json`files,`r13-b-extended.json`,
`r13-prod-a-summary.json`, six`r13-prod-a-wave-N.json`files,
`r13-prod-b-extended.json`, six`r13-c-wave-N.json`files, and six
`r13-d-wave-N.json`files. Counts were checked
against individual sanitized requests and audit timelines, not inferred solely
from a top-level success label. Actual-window Go/control coverage is documented
below and in the independently owned observer report.

## Aligned independent Go and control evidence

Reviewed the ordinary-build section of `remaining-stall-observation-plan.md`.
The two 90-second ordinary runs contain 2880/2880 successful health observations
across gateway/control and keep-alive/fresh TCP, with no timeout or sample at
least 50 ms. Gateway HTTP maximum is 2.1481 ms in prod-a and 3.6619 ms in prod-b.
Planned-start lateness is recorded separately from operation duration. All six
ordinary wave windows lie inside actual sampling and contain 508/508 successful
health requests. The prod-b actual audit windows for cold replies, long stream
and post-long reuse contain 1208/1208 successful requests, with complete window
coverage. This is observed overlap, not an inference from tool readiness messages.

Both runs retained independent observer/control clocks and native process samples.
No near-one-second timer gap, PID reuse or timeout was seen. The 14.2646 ms
control-only timer maximum in prod-a was not a slow HTTP result. No gateway
internal-runtime claim is made for an ordinary binary without that overlay.

The independently written r13-c report also records 1440/1440 successful Go health
requests, gateway maximum 3.5155 ms and control maximum 2.3432 ms. Its actual
health envelope covers all six waves; 748 samples start in the outer wave
windows and 736 in reconstructed server windows. Gateway/observer/control timer
lag maxima are 1.8663/1.7297/1.5806 ms, with no capture. Runtime starts slightly
after the first audit: first timer tick is 23.157 ms late and first metric
snapshot 523.221 ms late. The later five waves have full runtime coverage; do
not describe the first setup fraction as captured. This evidence supports a
responsive measured window, not an explanation of a historical pause.

The final r13-d report was also read directly: 1440/1440 Go health observations
succeeded, with complete six-wave overlap (372 outer-window and 368 server-window
samples). Gateway maximum is 2.3917 ms; control maximum is 2.9950 ms. There is no
timeout or 50 ms sample. Runtime collection succeeded; gateway/observer/control
lag maxima are 1.7593/1.7355/1.8139 ms. First tick and first metric start 13.949
and 514.135 ms after the first audit, respectively, so the small first-wave
prefix has the same stated internal-observation limitation. Later waves are
fully covered. No capture was triggered.

The earlier private r13-b missed its final internal-runtime snapshot because
the diagnostic listener's bounded lifetime and experiment start were misaligned.
Its successful health probes still stand, but no GC/scheduler diagnosis can be
made from an absent runtime snapshot. The later complete r13-c collection and
ordinary build runs address current coverage without inventing old evidence.

## Reasoning-only failure and structural discrimination

Retained`r13-b`request`965ec69791ddc37a`is HTTP200, six choice events,
seven SSE events,32reasoning characters, zero content characters, finish`stop`
and DONE. It took2.825seconds at the client, had one actual attempt and a
successful audit after cleanup. Its audit is protocol/outcome evidence, not a
promise that the generated answer contains useful text. The strict harness
correctly rejected the wave even though its generic`completed`counter is18.

Actual source review covered`decodeSSEEvent`, `deltaFromChunk`, `applyEvent`,
`StreamCaptureWithUsage`, `AggregateRaw`and`preflightSSE`:

- Standard first-choice delta content/reasoning is appended, then forwarded.
  A nested terminal delta is applied before termination; `finish_reason`alone
  does not stop reading. Preflight replays its prefix and buffered remainder.
- Some other shapes are not accepted: top-level text/content on named terminal
  frames, unknown event names, first-choice message content rather than delta,
  non-string content, or top-level content combined with an early usage-only
  branch. These are possible discrimination points, not demonstrated CodeArts
  protocol defects. No product change is justified without actual shape evidence.
- The Python downstream consumer counts every output choice's nonempty string
  delta content. Standard gateway output content would therefore be counted.

The private SSE overlay records only fixed event classes, field type/byte-length
projections, before/after builder lengths, successful output byte totals and
DONE/end counts. It stores no raw field/event names, bodies, semantic IDs, errors,
headers or credential values. The generated local numeric stream sequence is not
a request/provider ID. Matching begin/end and zero drops are required for a
per-stream interpretation. Byte lengths differ from Python Unicode character
counts; compare byte totals to byte totals.

Direct content read of the completed`r13-c`SSE file found4186valid JSONL records,
108begins,108ends, zero incomplete/empty/failed streams, and dropped maximum0.
Every stream's final content/reasoning builder byte lengths equal its successful
downstream emitted totals. Zero input frames have a positive projected content
field with an unchanged content builder, including streams that end with content.
This excludes observed content loss for the inspected shapes in those108calls;
it cannot retrospectively explain the different`r13-b`call.

The final bounded `r13-d` DeepSeek-only series has 2812 valid JSONL records,
108 begins and 108 ends, zero drops, incomplete/empty/failed streams or
builder/output mismatch. Its 1298 input frames are all unnamed; exactly 108
frames have positive first-choice delta content. No positive projected content
field occurs with an unchanged content builder. This did not reproduce the
earlier empty answer, and does not establish that answer was provider-caused.
No further identical real trials are recommended without a new discriminating
question or newly reproduced failure.

The helper uses a separate nonblocking1024-entry channel, one writer,20000records,
8MiB encoded-file cap, five-minute lifetime and100msflush. A cap/exit can omit
the tail or latest drop count; missing end is incomplete evidence. Observation
adds allocation/CPU and first-use file setup, so ordinary-build verification
remains necessary. Aggregate-mode end's failure flag covers parsed upstream
errors; the focused live investigation uses stream mode, whose existing
`streamErr`is observed correctly. Do not generalize that aggregate flag to every
early reader error.

## Gate interpretation

Six successful diagnostic waves alone are insufficient to identify the historical
09:41preparation failure or one-second health timeouts. Ordinary-build six-wave,
cold-start, useful long-stream and reuse success establish the finite current
workload's behavior. They do not turn old failed trials into passes, prove a new
performance fix, establish production rollout, or guarantee unlimited capacity.

AC13has both a finite verification part and a diagnosis condition for reproduced
stalls. Keep the finite results and historical causal status separate. No new
stall requiring a gateway correction has yet been evidenced by the reviewed
current runs; current success cannot be presented as the old cause's explanation.
The reasoning-only strict failure is a distinct unresolved observation and must
remain visible while the structural follow-up is interpreted. Do not silently
weaken useful-content acceptance to HTTP200/DONE or erase it with a later pass.

Approve the bounded current regression: the unchanged ordinary build passed the
planned six-wave, cold-start, useful long-stream, audit, cleanup and aligned
health/control checks. Diagnostic structural checks found no content loss in
the 216 completely observed r13-c/r13-d streams. No new product performance
fix is established or claimed by this review.

Keep the overall incident task `in_progress`, AC13's historical-diagnosis closure
unchecked, and whole-task `quality_gate_passed=false`. Add a separate explicit
`bounded_regression_passed=true` result so the successful measured verification
is not confused with a product test failure or erased. Existing source quality
checks remain passed. The user rejected premature full closure; neither an
unknown historical preparation stall nor the reasoning-only answer should be
silently reclassified by changing the criterion. No extra source ZIP or invented
performance-fix commit is needed for unchanged product code. Production rollout
remains unverified.

Report the measured pass and the two residual unknowns directly. Do not infer
impossible retrospective evidence from new instrumentation, attribute the old
incident to Windows/Python/Go/provider/gateway without evidence, or extend
identical success sampling indefinitely. Investigation can resume from a newly
captured failure with the prepared bounded tools; retaining that diagnostic
boundary is more accurate than claiming all historical symptoms are fixed.

## Verification

- Product lint/type-check/tests: existing independent full Go tests, full race,
  vet and build evidence reviewed; not rerun for unchanged product/report files.
- Private overlay: five fake/in-memory tests pass. They cover valid and terminal
  delta preservation, unchanged ignored-terminal behavior with accurate structural
  localization, payload immutability, text/key/model/error sentinel exclusion,
  failed-write accounting, nonblocking drops and directory containment.
- Private overlay standalone server build and Go vet pass with Go1.27.3.
- No product source fix or public interface change was made in this reopened
  diagnostic review. No real traffic was issued by the reviewer.
