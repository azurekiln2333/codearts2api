# P0 research: production build identity and mixed-model latency

Read-only planning research by the independent Pi reviewer. No product code was
changed, no task activated, no production traffic sent. Evidence sources:
`../10-21-production-concurrency-audit/research/{findings.md,live-reproduction.json}`
(content previews have since been removed from that file), source at HEAD `1b98ff0`
(repair `3d87a5c`), and the localhost harness in `research/harness/main.go`
(fake credentials, loopback only).

## 1. Verdict in one paragraph

One thing is proven about the deployment: the HTTP error bodies the live service
returned cannot be produced by current code, so the **deployed request path is older
than `3d87a5c`** (or a materially different path). The exact running identity (image,
commit, effective limits) is **not** verified and cannot be inferred from the
collected evidence. The 125 s no-header aborts are *consistent with* the pre-repair
path but are **not** proof by themselves: on current code a non-streaming request may
legitimately keep aggregating while the upstream keeps producing data or heartbeats,
so a slow-but-alive generation can also produce no downstream headers for 125 s.
Independently of the deployment question, current code has **one proven defect**
(`MaxRotate` can report a model unavailable without trying every eligible account),
which is reproducible on localhost.

Scope alignment: this report supports P0-R1 (build/limits verification), P0-R3
(bounded waves, S2/S4/S5) and P0-R5 (capability failover, §3.1). It deliberately does
not restate P0-R2 (lifecycle ownership is already implemented and covered by existing
regressions), does not propose a total generation deadline, and defers the runtime
`effective`/`build` payload and its panel to the P1 child.

## 2. Deployment evidence, by strength

### 2.1 Strong: live 404 bodies cannot be produced by current code

Current `internal/server/chat.go` emits exactly one "not available" text for models:

- `model <id> is not available on eligible configured accounts` (404 `model_not_available`,
  `chat.go:364`), reached only when `modelKnownUnusable(model) ||
  upstreamUnavailableReason(lastErr) != ""` (`chat.go:363`)

(Param rejections use a different text, `upstream rejected request params: ...`,
400 `invalid_request`, at `chat.go:260` and `chat.go:317`.)

`git show 3d87a5c^:internal/server/handler.go` line 709 and line 840 contain:

- `model <id> is not available on any configured account`
- `model <id> is not available: <truncated upstream error>`

The live wave recorded (verbatim response bodies in `wave.requests`):

```
deepseek-v4-flash-0731  -> model deepseek-v4-flash-0731 is not available on any configured account
glm-5.3-flash           -> model glm-5.3-flash is not available: ... InferHub.4004.200 benefit not found
openpangu-2.0-flash     -> model openpangu-2.0-flash is not available: ... InferHub.002002009.404 ...
```

No current code path can emit either string. Conclusion: **the process that answered
those requests was not running HEAD.** What it *was* running (image digest, commit,
config) remains unverified — that is exactly what the runbook in §6 exists for.

### 2.2 Weak: `last_error: "context canceled"`

Current code never writes a raw error into `Account.lastErr`: all writers use fixed
text (`pool.go:435-445` `Cooldown` reasons come from `handler.go:838/844`;
`pool.go:454-465` `NoteError` `"consecutive errors"`; `pool.go:487-496` `Disable`;
`pool.go:415-424` `disableAuth`), and cancellation returns early at `handler.go:820`
(`isParamInvalidError(err) || isChatTimeout(err) || errors.Is(err, context.Canceled) ||
DownstreamWriteError`).
`3d87a5c^:internal/server/handler.go:895` used `aerr.Error()` as the cooldown reason,
which is how `"context canceled"` gets recorded.

But `lastErr` and `coolUntil` are persisted in `data/state.json`
(`pool.go:857 saveState`, `pool.go:823 loadState`), so a string written by an older
binary survives an upgrade and would still be displayed by a repaired build.
**Consistent with a stale deployment; cannot date the running process.**

### 2.3 Unusable as evidence: overview key sets

Live `/admin/api/overview` → `schedule.watch` has 7 keys and no `max_pending`;
current `cmd/server/main.go:74-83` emits 8 keys including `max_pending`. However the
evidence file clearly projects payloads (its `account_samples` use a reduced,
different field set than `Pool.List()` at `pool.go:196`), and the diagnostic file has
since been sanitised again (content previews removed). Key absence may be an artefact.
Do not use it.

### 2.4 Consistent, not proof: the timing profile

Pre-repair defaults were a 180 s `AcquireLockWait`, a hard-coded 120 s response-header
timeout, a 10 s queue-retry delay and 30 attempts, with no startup budget. Repaired
defaults are a 10 s cumulative account wait, 60 s upstream idle/header, 2 s retry
delay, 3 attempts per account and a 90 s startup budget
(`internal/server/handler.go:132-155`, `cmd/server/config.go` defaults).

Observed: 10 of 18 requests produced no downstream headers before the client's 125 s
deadline (all client-side aborts, empty status and empty cause — the client could not
observe which layer was responsible); successes at 2.3 s, 64.1 s, 70.0 s, 79.3 s.

Why this is **not** decisive on its own:

- **A 125 s wait with no response headers is legally reachable on current code.**
  In non-streaming mode the handler writes nothing until aggregation completes
  (`chat.go:308` `AggregateRaw`, single `writeJSON` at `chat.go:347`), the startup timer is
  stopped once preflight returns (`lifecycle.go:29-51` `started()`), and the upstream
  idle guard resets on every read (`stream_body.go:109` `timeoutReadCloser.Read`). An
  upstream that keeps sending content, reasoning or heartbeat lines for minutes while
  never sending a terminal marker therefore produces exactly this client-side
  observation. The lifecycle contract deliberately permits active generations beyond
  startup, and harness S6 (§3.2) exercises it: with a 150 ms startup budget a request
  whose upstream streams for 720 ms still returns 200 at 728 ms.
- **64/70/79 s is suggestive, not proof, of the old 10 s retry cadence.** There are no
  server logs, no attempt counts and no per-request records from that window, so the
  cadence cannot be attributed. A 2 s cadence with upstream queueing, provider
  backoff, or slow generation could also land there.

So the timing profile is *compatible* with the pre-repair path (which the §2.1
fingerprint independently establishes) but it must not be used to quantify behaviour.

## 3. What the current build actually does (measured on localhost)

Harness: `research/harness/main.go`, run with the temporary Go toolchain. It drives the
real handler (`server.NewHandler`) over real HTTP; the upstream is a loopback
`httptest` server with fake credentials. Every scenario below states its exact shape
and its limit; budgets are scaled down so the whole run takes ~2 s, so no number here
is a production latency claim.

### 3.1 Confirmed defect: `MaxRotate` can hide an available model

`chat.go:142` bounds rotation by `h.cfg.MaxRotate` (default 3, set in
`cmd/server/main.go`), while `lifecycle.go:153 chatCandidates` may legitimately return
more eligible candidates. Scenario: 5 accounts whose catalogs are unknown (learned at
runtime) and a model served only by the 5th; each trial uses a **fresh model id**
because availability negatives are cached process-wide per `accountID|model`
(`markUnusable`, 30 min TTL) — reusing one id would let the first trial's negatives
suppress accounts in the control run.

```
model=only-on-acct-4-rot3 MaxRotate=3 -> 404 model_not_available, 3ms
   "model only-on-acct-4-rot3 is not available on eligible configured accounts"
model=only-on-acct-4-rot5 MaxRotate=5 -> 200 OK, 3ms
```

The control genuinely walks all five accounts (fresh id, no cached negatives), so the
404 is a real false negative: a model that one eligible account serves is reported
unavailable, and the wording at `chat.go:364` is **false** — two eligible accounts were
never tried. Unrelated to deployment; relevant to a 15-model advertised surface on 4
accounts.

**Proposed fix (narrow, no retry-policy change):** exempt *capability-only* failures
(catalog `not registered` / `benefit not found`, i.e. `upstreamUnavailableReason != ""`
together with `markUnusable`) from the generic `MaxRotate` budget, and iterate over the
remaining capability-eligible candidates while (a) the finite `tried` set grows
monotonically, (b) the shared startup deadline still bounds the whole loop, and
(c) generic failures (5xx, 429, timeouts, transport) keep today's bound. Emit
`model_not_available` only when the capability sweep has exhausted every eligible
candidate, so `modelKnownUnusable` becomes the sole justification for the
"eligible configured accounts" wording.

### 3.2 Measured scenarios (exact shapes)

| # | Scenario | Result |
|---|---|---|
| S2 | 18 concurrent mixed-model/alias requests, 3/4 accounts cooling, `max_concurrent=5`, healthy fake upstream | 18/18 HTTP 200, wall 8 ms, 0 slots held — control for wiring, alias handling, slot reuse |
| S3 | ticket cap with 3/4 cooling | 84 tickets admitted (64 `MaxPending` + 4×5 capacities incl. cooling) vs 5 schedulable slots |
| S4 | 18 requests, **only acct-0 schedulable** (`max_concurrent=5`, acct-1/2/3 cooled for 10 min), upstream accepts the connection but never sends headers; scaled budgets idle 800 ms / account-wait 500 ms / startup 3 s | `429 x13 [501-502ms] 504 x5 [801ms]`, wall 802 ms, 0 slots held, still 1 schedulable account |
| S5 | 18 requests on one 2-slot account, upstream 429 on every attempt, 3 attempts × 400 ms, `MaxRotate=1` | `429 x3 503 x15`, all ≤805 ms, 39 upstream attempts total, 0 slots held |
| S6 | non-streaming request, upstream sends 12 SSE chunks over 720 ms then `[DONE]`; `startup=150 ms`, idle 200 ms | **200 OK at 728 ms** — exceeds the startup budget by 4.8× and still succeeds |

What each proves, and only that:

- **S4** is the corrected cooling case: the mixture is exactly what the budgets predict
  — the 5 requests holding the only real slots end at the upstream idle bound
  (`canceled_or_timeout`, 504), the 13 queued ones end at the account-wait bound
  (`admission_rejected`, 429). Cooling accounts are never touched and no slot leaks.
  It does **not** show saturation rejection (18 « 84 tickets) and it does not measure
  provider latency because the upstream is deliberately silent.
- **S5** measures *finite retry behaviour below the admission cap*: 39 attempts for 18
  requests, bounded by 3 attempts × 1 rotation, and the 503s appear only after the
  single account has been cooled by the first 429 (immediate soft cooldown, 60 s).
  Saturation **rejection** is not exercised here; it is already covered by
  `TestChatConcurrentSaturationCancellationFairnessAndHealth`
  (`internal/server/concurrency_test.go:28`), `TestConcurrentAdmissionNeverExceedsCapacity`
  and `TestConcurrentSaturationBoundsWaitersAndCancellationDrains`
  (`internal/pool/admission_test.go:315,413`).
- **S6** is the key correction to §2.4: a non-streaming request may legitimately run far
  past the startup budget while the upstream is still producing data. The startup timer
  is stopped after preflight (`lifecycle.go:29-51`), the idle guard resets on every read
  (`stream_body.go:109`), and the response is written only after
  `AggregateRaw` (`chat.go:308` → `writeJSON` at `chat.go:347`). So "no downstream
  headers for 125 s" is **not** by itself evidence of a stale build or of a stall.
- S2/S3 are wiring and cap-shape controls, nothing more.

### 3.3 Documented behaviour: admission counts cooling capacity

`admission.go:65-72` sums `maxConcurrent` for every non-disabled account including
cooling ones (measured 84 tickets against 5 schedulable slots). This matches the current
`request-lifecycle` spec ("enabled account capacity plus `MaxPending`") and stays bounded
— every waiter is capped by the 10 s account-wait budget, as S4 shows. **Retain as
documented behaviour**, not a defect and not an open product choice; worth one sentence
in spec/README so operators are not surprised that 429s arrive later than schedulable
capacity suggests.

## 4. Configuration vs code

| Observation | Category | Basis |
|---|---|---|
| Old 404 templates served | deployed path predates `3d87a5c` | §2.1 (strong) |
| `max_concurrent: 5` on all accounts | **observed configuration**; provider session-limit impact unverified | `live-reproduction.json` `initial`/`account_samples` |
| 3/4 accounts cooling, `err_count` 4/4/7 | shared pool policy (`pool.go:454`), old and new alike | §2.2, P1 report §2 |
| `last_error: "context canceled"` | deployment or persisted state | §2.2 (weak) |
| 10× no headers at 125 s | undetermined: old acquisition **or** slow-but-active generation on either build | §2.4 |
| 64/70/79 s successes | undetermined (no logs/attempts) | §2.4 |
| model-not-available despite an eligible account | **code** | §3.1 (proven locally) |
| 429 storm → immediate 60 s soft cooldown → 503 `no_healthy_account` on a one-account pool | code; accurate but unhelpful, and the 503 only appears because the first 429 cools the only account | S5 |

`max_concurrent=5` is observed, not diagnosed: with one schedulable account it permits
five simultaneous upstream sessions, and the code comments describe the provider's real
session limit as ~3 with slow release (`availability.go` `probeIdleBefore`), but **no
evidence in this dataset establishes that 5 caused the failures**. If anything, the
first repair's own contract calls for reducing concurrency only for an initial
verification pass (P0-R4), not as a root-cause claim.

## 5. Proposed localhost tests (real handler, loopback, fake auth)

1. `TestChatCapabilityFailureExemptFromRotationCap` — 5 unknown-catalog accounts, model
   served only by the last, **fresh model id per subtest** (availability negatives are
   cached per `accountID|model`); assert 200 today-404, and assert the control case
   actually attempts all five accounts. Must fail before the fix.
2. `TestChatModelUnavailableOnlyWhenAllCandidatesExhausted` — every capable candidate
   fails capability; assert the 404 wording is used only then, and that the recorded
   `tried` set equals the eligible candidate set.
3. `TestChatCapabilitySweepRespectsStartupDeadline` — capability failures across many
   accounts with a tight startup budget; assert the request still ends within budget
   rather than looping over a large pool.
4. `TestChatGenericFailuresStillBoundedByMaxRotate` — 5xx/429/timeouts keep today's
   `MaxRotate` bound (guards against the exemption widening retries).
5. `TestChatMixedModelWaveUnderCoolingStaysBounded` — S2 as an assertion (18 mixed
   model/alias requests, 3/4 cooling, healthy fake upstream, 0 slots held).
6. `TestChatSilentUpstreamWithOneSchedulableAccount` — S4 as an assertion: exactly
   `schedulable_capacity` 504s at the idle bound, the rest 429s at the account-wait
   bound, cooling accounts untouched, 0 slots held afterwards.
7. `TestChatActiveGenerationBeyondStartupStillSucceeds` — S6 as an assertion: a
   non-stream request whose upstream keeps sending data past the startup budget still
   returns 200 with the full aggregated content (documents the §2.4 case).
8. Keep existing `lifecycle_test.go`/`concurrency_test.go` green; no broad suite rerun
   during planning.

## 6. Credential-preserving build/config verification runbook (proposal)

Goal: identify the running image/commit and effective limits without touching
`auths/`, `data/`, `config.json` or `.env`.

1. **Read-only identity capture** (host; no container mutation):
   `docker compose ps --format '{{.Name}} {{.Image}} {{.Status}}'`;
   `docker inspect -f '{{.Image}} {{index .Config.Labels "org.opencontainers.image.revision"}}' <container>`;
   `docker image inspect -f '{{.Id}} {{.Created}}' <image>`;
   `git rev-parse HEAD`, `git status --porcelain`, `git log -1 --format=%H 3d87a5c`.
   All three (image id, git HEAD, created time) must agree before any conclusion.
2. **Effective limits from the API** — requires the P1 `effective`/`build` blocks (P1 report
   3.3). Today `adminOverview` returns only `schedule.watch` (8 keys, no timeouts), so this
   step is impossible: that is the blocking gap for P0-R1. The blocks list effective
   values and an explicit `known`/`unknown` build; no per-value provenance subsystem is
   proposed.
3. **Config precedence sanity check** (operator step, not a product feature): confirm
   which of `config.json` / Compose env / defaults won by reading the deployment inputs.
   Verified by inspection: every `applyEnv` read in `cmd/server/config.go:184-262` is
   guarded by `v != ""`, so the new Compose file passing empty strings is safe and does
   not blank `config.json`. `CA2A_LISTEN: ":${CA2A_PORT}"` does override
   `config.json:listen` by design (documented in README).
4. **Rebuild without credential risk**: copy `data/state.json` aside (it carries
   errCount/cooldown), then `docker compose build --pull` and `docker compose up -d`
   (recreate only). Mounts are `${CA2A_AUTHS_PATH}:/app/auths`,
   `${CA2A_DATA_PATH}:/app/data`, `${CA2A_CONFIG_PATH}:/app/config.json:ro`, so
   recreating preserves credentials and history. Never `down -v`, never delete
   `auths/`/`data/`, never re-login.
5. **Post-upgrade verification, no production load**: `/healthz` 200; overview shows
   the `effective` block with expected numbers and a build id equal to step 1; one
   `max_tokens=1` chat to a known-good model logs one `chat ... outcome=ok` line with
   attempts/first-data; `/v1/models` unchanged.
6. **Rollback**: retain the previous image id from step 1; re-run the prior image; no
   data migration involved (state JSON is additive/compatible).
7. Only after 1–5 may a production wave be repeated; reuse the parent's bounded
   18-request shape, with `max_concurrent=1` for the first pass if that is acceptable
   to the user (explicit override, not silently replaced).

## 7. Blocking product choices

1. **Rotation fix shape** (§3.1): capability-exempt sweep (recommended) vs. keeping a
   flat cap and only correcting the message. Recommendation: exempt capability-only
   failures, keep the startup deadline and finite `tried` set, leave generic retry
   policy untouched.
2. **Build identity mechanism** (owned by the P1 child, consumed by this runbook):
   `-ldflags -X` with `BUILD_COMMIT`/`BUILD_TIME` build args in `Dockerfile` — no `.git`
   is copied into the image, so `debug.ReadBuildInfo` VCS data is unavailable — and the
   fallback must be an explicit `known:false`, never blank or fabricated.
3. **Operational default for verification**: whether the first post-upgrade pass may
   run with `max_concurrent=1` (recommendation only; the user's explicit `5` must not
   be overwritten).

## 8. Limits of this research

- The production origin, image, New API channel config and container logs remain
  unavailable; §2.1 dates the *code path*, not the exact build, and no claim is made
  about the current container.
- `max_concurrent=5` is an observation; its effect on provider session limits is a
  hypothesis without evidence here.
- The harness proves bounds, ownership and the §3.1 defect on a synthetic upstream; it
  cannot reproduce provider queueing, heartbeats, or New API behaviour, and S4 measures
  the scaled silent-I/O bound only.
- No second production wave was run; attribution of the 125 s aborts stays open and is
  the primary motivation for the P1 audit.
- The S6 result is the reason no claim is made that the live no-header aborts were
  impossible on current code.
