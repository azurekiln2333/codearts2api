# Reopened live stability verification, 2026-10-23

## Scope and preservation

The user rejected closing the incident with an unexplained failed concurrency
trial. This follow-up reuses the authorized isolated localhost gateway and
credential copies. No production New API traffic or remote deployment is used.
Original credentials, user archives and earlier source releases must remain
untouched. Private diagnostics, keys, accounts and generated artifacts remain
under the Git-excluded `.local-test` directory.

The product source remains commit
`72e83fbed57d862d85e4d20b8b96f7a44e41019d`. A private Go overlay adds bounded
HTTP-phase and runtime observation without changing the delivered sources.
Ordinary-build verification is separate from overlay verification. Per-account
capacity remains 3, account waiting remains 10 seconds, and startup timeout
remains 60 seconds. Three supplied credential copies are eligible; an older
fourth copy is refresh-expired. This is not a statement about newer originals.

## Historical failure retained

The first historical cap-3 trial completed nine of eighteen requests and
rejected nine at the local account-wait deadline without upstream attempts.
The old stderr contains nine initial-busy notification timeout warnings around
2026-10-23 09:41:53–09:42:01 Asia/Shanghai. The successful requests spent roughly
3.007–11.450 seconds in preparation. These facts locate part of the symptom but
do not establish DNS, TLS, provider, logging or scheduler causation. Later
successful tests do not erase this trial or prove its root cause.

## Measured trials

| Run | Construction | Work | Strict content result | Cleanup |
| --- | --- | --- | --- | --- |
| r13-a | HTTP/runtime overlay | Six consecutive waves, 18 each | 108/108 useful content and DONE | 108 correct audits; active zero after every wave |
| r13-b | Updated HTTP/runtime overlay | Cold two-model nonstream | 2/2 useful content | Correct audits; active zero |
| r13-b | Updated HTTP/runtime overlay | GLM useful long stream | 61.178 seconds to completion, 7,902 answer characters, DONE | Correct audit; active zero |
| r13-b | Updated HTTP/runtime overlay | Post-long 18-request reuse | **Failed strict content gate:** 17 answers plus one reasoning-only response | All 18 HTTP 200/DONE and audit ok; active zero |
| r13-prod-a | Ordinary product build | Six consecutive waves, 18 each | 108/108 useful content and DONE | 108 correct audits; active zero after every wave |
| r13-prod-b | Ordinary product build, restarted | Cold two-model nonstream | 2/2 useful content | Correct audits; active zero |
| r13-prod-b | Ordinary product build | GLM useful long stream | 66.307 seconds to completion, 7,077 answer characters, DONE | Correct audit; active zero |
| r13-prod-b | Ordinary product build | Post-long 18-request reuse | 18/18 useful content and DONE | 18 correct audits; active zero |
| r13-c | Combined HTTP/runtime/structural overlay | Six consecutive mixed-model waves, 18 each | 108/108 useful content and DONE | 108 correct audits; active zero after every wave |
| r13-d | Same combined overlay, restarted | Six targeted DeepSeek waves, 18 each | 108/108 useful content and DONE | 108 correct audits; active zero after every wave |

Each mixed-model short wave uses nine `glm-5.3` and nine
`deepseek-v4.1-flash` requests; each targeted r13-d wave uses eighteen DeepSeek
requests. Output is capped at 256 tokens with no client retry. Ordinary build SHA256:
`16fa2b4525aafcc4655da7529ea3fa07a98b43cdb3dbf4bbbcdb6daa1cb92fef`.
The ordinary six waves had median client durations 2.214–3.099 seconds and
maximum duration 5.921 seconds. No audit drops or write failures were observed.

The r13-b empty response ended normally in about 2.825 seconds with 32 reasoning
characters, zero answer characters and finish reason `stop`. Its correlated
audit was `ok`, one attempt, approximately 1.856 seconds of account waiting,
and complete cleanup. It remains a failed useful-answer test. HTTP 200, DONE
and reasoning are not substituted for answer content. The available result
alone cannot distinguish an upstream empty answer from an unhandled SSE shape.
The completed bounded structural capture did not reproduce it, so the earlier
response's cause remains unknown.

## Independent observations

For r13-a, four independent Go probe modes (gateway/control, reused/fresh
connections) each succeeded 360/360 times over 90 seconds. Maximum gateway
health operation duration was 2.308 ms. Maximum request scheduling lag was
1.548 ms. Independent probe/control/gateway 100-ms timer lag maxima were
1.585/1.583/1.716 ms. Runtime metrics showed no long GC or scheduling histogram
bucket growth. These observations apply to that measured interval only.

For r13-b, the same four health modes again succeeded 1440/1440 in aggregate,
with maximum operation duration 3.050 ms. Internal runtime collection was
unavailable: observer startup occurred about 77 seconds after the gateway's
initialization, so the private 120-second listener lifetime and 90-second
flight-recorder window did not cover the entire experiment. That absence is
an instrumentation coverage limit, not a gateway hang finding.

Detailed runtime/control numbers and method limitations are maintained in
`remaining-stall-observation-plan.md`. Detailed HTTP operation attribution is
maintained in `remaining-stall-code-investigation.md` and
`remaining-stall-r13-a-http-analysis.md`.

The combined diagnostic runs captured 216 complete SSE conversions, with no
drops, missing ends, empty answers or builder/output mismatch. No input frame
had positive projected answer content that the unchanged parser ignored.
This supports absence of observed conversion loss for the inspected shapes in
those runs. It cannot reconstruct the earlier reasoning-only response, whose
structural input was not retained. The focused final series was bounded; no
indefinite repetition or unsupported parser fallback was introduced.

One r13-c client response took 17.787 seconds. Its audit and HTTP trace align:
2.323 seconds waiting for an account, about 75.858 ms initial busy notification,
then about 15.003 seconds between request write and first upstream byte. Health
and runtime/control sampling continued. This locates the measured wait after
the outbound write, rather than in notification/signing. Client-side HTTP trace
alone cannot distinguish provider execution from network/reader scheduling.

## Current gate state

Ordinary-build cold/long/reuse verification passed. The 2026-10-23T05:34:12Z
read-only snapshot showed three eligible accounts, zero errors, no cooling,
and zero active requests. The fourth older test copy remained disabled for
refresh expiry. All four original files matched the reopening baseline;
the user ZIP and previous verified source archive hashes also matched.

The final preservation snapshot at 2026-10-23T05:52:21Z again matched all four
original credential hashes and both archive hashes. Eligible test copies still
had zero errors, no cooling and zero active requests. The owned gateway and
independent controls were stopped after verification. Refreshed credential
copies and private diagnostic evidence are retained under `.local-test`.

Independent review approves the **bounded current regression**. The whole
incident's AC13 closure and task quality flag remain open because historical
stall causation and the one earlier empty answer remain unresolved. No new
product source was changed, and no new performance fix or ZIP is represented.
The existing commit-based source archive remains byte-for-byte verified against
its previous SHA256. There is no production deployment or unconditional
stability claim. See `reopened-stability-independent-review.md` for the separate
finite acceptance and historical-cause disposition.
