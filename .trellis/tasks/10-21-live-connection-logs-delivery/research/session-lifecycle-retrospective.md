# Bug analysis: valid models followed by repeated concurrency refusal

Date: 2026-10-23 Asia/Shanghai. This report records the implemented upstream
session boundary and the parent's published direct-account evidence. The author
did not access credentials or run actual provider requests. Full rebuilt-gateway
real validation was pending when this retrospective began. Final measurements
are recorded in `live-gateway-session-verification.md`; no production rollout is
claimed.

## 1. Root cause category

- **E — Implicit assumption:** the gateway assumed that receiving protocol DONE,
  draining HTTP EOF, closing the response and releasing its local account lease
  also released the provider's root session. The official current client has a
  separate busy/heartbeat/idle lifecycle identified by `user-session-id`.
- **B — Cross-layer contract:** local pool admission, HTTP transport lifetime,
  remote account occupancy, model registration and discovery are different state
  machines. A zero local active count does not establish zero remote occupancy;
  a discovered display label does not establish the ID accepted by inference.
- **D — Coverage gap:** finite SSE fixtures and slot counters exercised local
  cleanup but did not retain remote sessions until an explicit idle notification.
  One successful call could not reveal the successful-then-busy pattern.
- **A — Missing executable spec:** earlier lifecycle guidance covered transport
  and local ownership without the official remote identity and release protocol.

There are two independently evidenced defects, not one universal casing symptom:

| Boundary | Evidence | Consequence |
|---|---|---|
| Source ID versus display label | Real agent `model_parameters.model_id` and builtin `model_id` are `glm-5.3` / `deepseek-v4.1-flash`, while names are `GLM-5.3` / `DeepSeek-V4.1-Flash` | A display-merged ID could be sent and receive `InferHub.002002009.404`, later exposed as gateway `model_not_available` |
| Remote root-session ownership | Three fully drained unmanaged calls succeeded, then TM refused; ten managed calls with signed busy/inference/idle all returned content | Correct local release and exact model IDs alone could not prevent retained provider occupancy |

Some real explicit IDs remain uppercase (`GLM-5.2`, `Qwen3-VL-235B`). The repair
uses authoritative account/source IDs and explicit aliases; globally lowercasing
outbound IDs would introduce another registration defect. A closed legacy benefit
switch on these test accounts also does not imply normal inference is unavailable.

## 2. Why the earlier fixes were insufficient

1. **Transport and pool cleanup repaired genuine local failures.** Recognizing
   terminal SSE without EOF, bounding I/O and releasing exact leases prevents
   local hangs. Those checks cannot balance a remote entity that is identified
   through another header and released through another API. The parent's direct
   control drained the real response to actual EOF and still reproduced TM.
2. **Account-scoped casing did not separate display identity from inference
   identity within one account.** Richer metadata and uppercase display merging
   still overrode explicit lower IDs. Actual field capture, exact-ID calls and
   captured-wire HTTP regressions were needed to expose that narrower defect.
3. **Identity repair enabled actual replies but not repeated reliability.** The
   parent rebuilt the identity gateway and recorded eight content-bearing streamed
   successes and four TM refusals in twelve calls. This is positive evidence for
   the model repair and contrary evidence for treating it as a complete fix.
4. **Independent conversation IDs and a minimal body did not discriminate away
   the remote-lifecycle problem.** Both still produced three successes followed
   by TM. Existing `Chat-Id`/`Session-Id` and prompt-cache options were not proven
   substitutes for the missing official `user-session-id` protocol.
5. **Cross-request busy admission is protection, not remote release.** Shared
   bounded backoff prevents repeated hammering and preserves free backups. A
   twenty-second sleep, retry-budget change or larger local concurrency setting
   cannot establish that the provider freed a session.

The discriminating evidence is documented in
[live-account-findings.md](live-account-findings.md) and
[official-session-heartbeat-contract.md](official-session-heartbeat-contract.md).
The latter records the installed official 26.10.0 kernel artifact hash and source
offsets, plus the parent's real same-account control: ten immediate DeepSeek calls
returned actual content, each with busy/idle HTTP200, approximately 1.1–2.1 seconds.
That is a direct upstream result, not repaired full-gateway or 18-way acceptance.

No numerical priors were recorded before the investigation; this report does not
invent retrospective probabilities. Evidence moved the investigation from local
lease/EOF hypotheses to explicit source identity, then to remote ownership. The
successful managed control supports implementing the official protocol, while
provider capacity, long real streams and integrated scheduling still need their
own measurements.

## 3. Prevention mechanisms

| Priority | Mechanism | Concrete action | State at this report |
|---|---|---|---|
| P0 | Ownership architecture | Manage busy, renewal and detached bounded idle at common `sendChatV2`, covering chat and probes; own one unique remote ID per attempt | Implemented upstream |
| P0 | Cleanup order | Cancel renewal, close/cancel transport, perform bounded idle and join the worker before managed body cleanup returns/local lease release | Implemented; focused HTTP assertions pass |
| P0 | Failure isolation | Limit each notification to at most three seconds; always attempt idle even after busy failure; log fixed safe metadata without replacing generation outcomes | Implemented; notification HTTP/timeouts tested |
| P0 | Identity contract | Prefer explicit agent parameter/builtin inference IDs under account/auth/source ownership; preserve genuine uppercase IDs and safe explicit aliases | Implemented by the identity owner; see model discovery spec |
| P0 | Representative fixtures | Retain synthetic remote sessions until matching idle; exercise more than three repeats and an 18-request server-route wave with health/resource assertions | Full server fixture/validation owned by parent; do not claim passed here |
| P1 | Shared overload protection | Remember explicit provider busy per account/auth with bounded backoff, free-backup selection and honest 429; do not turn it into health/capability failure | Implementation owned by busy-admission owner; integrated check pending parent |
| P1 | Evidence gates | Require content and terminal completion, actual sent IDs, correlated audits and no retained local/remote state; distinguish direct, fixture, full gateway and deployed results | Captured in executable lifecycle spec; real full-gateway results pending parent |

Upstream focused actual-HTTP tests passed for seven sequential calls, same-chat
parallel isolation, signed matching identities, periodic renewal using a shortened
private test interval, terminal-without-EOF cleanup, preflight/transport/read errors,
timeouts, cancellation, an unread returned body, idempotent close and in-flight
renewal cancellation/join. The full `internal/upstream` race suite and vet passed.
These local results do not claim a real stream beyond the production 30-second
renewal interval, full project gate completion or deployment.

## 4. Systematic expansion

- **Similar paths:** background availability probes perform inference and must
  enter the same managed boundary. Each retry, credential-refresh retry and account
  rotation owns a balanced lifecycle; cleanup cannot use the next account's auth.
- **Parallel ownership:** conversation affinity and remote occupancy must remain
  separate. Reusing one remote root ID across independent parallel attempts lets
  an earlier idle release a still-running request. Unique attempt ownership avoids
  that race without changing prompt-cache conversation identifiers.
- **Operational limits:** remote session management does not remove the provider's
  account allowance or sessions consumed by official tools/other gateway replicas.
  Local caps, shared busy state and truthful responses remain necessary.
- **Review scope:** unit helpers, source presence, a green fixture or a single HTTP200
  cannot establish the user's repeated real-use outcome. Inspect new untracked
  tests as well as tracked diffs and revalidate the actual compiled artifact.
- **Evidence quality:** HTTP200 with empty content at too-small output limits was
  not counted as a useful reply. Provider auth expiry is kept separate from model
  registration, rate/TPM, explicit session saturation and local slot waiting.

## 5. Knowledge capture

- [x] Updated [request-lifecycle.md](../../../spec/backend/request-lifecycle.md)
  with exact heartbeat wire shape, default interval/bounds, constructor boundary,
  account/auth-scoped busy signatures, cleanup/error contracts and required tests.
- [x] Linked the official protocol report and preserved its direct-versus-integrated
  evidence boundary. [model-discovery.md](../../../spec/backend/model-discovery.md)
  separately owns inference-ID/display-ID/source authority conventions.
- [x] Checked template locations: this application has neither
  `src/templates/markdown/spec/` nor `packages/cli/src/templates/trellis/spec/`.
  No template tree is invented and no CLI/framework files were modified.
- [x] Parent: complete fresh full-gateway real sequential/streamed/long-generation
  and progressive concurrent measurements, integrated review and required full
  normal/race/vet/build checks; update acceptance using those actual results.
- [ ] Parent: explicitly stage/review owned sources, tests/spec/reports, commit the
  consolidated repair and verify its extracted source release. Credentials and
  diagnostic private files remain excluded; no production deployment claim.
