# DeepSeek request timeouts and New API gateway stalls

## Goal

Prevent the CodeArts proxy from accumulating stalled requests or cooling healthy accounts when DeepSeek Flash requests time out through New API, while preserving normal streaming, long generations, tools, and account routing.

## Background

The user reports that `deepseekv4.1flash` initially works, then requests repeatedly take 120 seconds or longer and New API appears stuck. The user authorized task creation and diagnosis/fixing. Production configuration, gateway logs, exact model mapping, and upstream telemetry are unavailable locally; provider congestion and the gateway's own timeout settings remain unverified. The repository's seeded Flash ID is `deepseek-v4-flash-0731`, not evidence of the user's current deployed ID (`internal/upstream/models.go:54`).

A local diagnostic harness uses only fake credentials and loopback HTTP servers. Its findings and baseline test results are in `research/findings.md`.

## Requirements and Evidence

- **R1 — Bound and cancel admission (P1).** A disconnected caller must stop waiting immediately; free eligible accounts should be used without first waiting on a saturated account. `internal/server/handler.go:737` waits 180 seconds on the first selected account; `internal/pool/pool.go:519` has no context or health check during its polling wait. The live handler bypasses the model-aware helper at `internal/server/handler.go:580` and uses `PickExcluding` at `internal/server/handler.go:728`. Both canceled admission and blocking behind a busy first account were reproduced locally.
- **R2 — Bound startup and inactivity (P1).** Header, first-data, retry, and subsequent idle waits must have effective limits, and every terminal path must close upstream resources. `internal/upstream/client.go:140` hard-codes a 120-second header timeout; `internal/upstream/client.go:144` leaves the streaming client without a total timeout; `internal/upstream/client.go:596` can wait indefinitely in preflight after response headers arrive. The constructor timeout was set to 25 milliseconds in the reproduction, yet preflight was still blocked after 150 milliseconds. `cmd/server/config.go:59` exposes `upstream.timeout_seconds`, but `cmd/server/main.go:47` and `internal/pool/pool.go:106` use fixed values instead.
- **R3 — Honor terminal SSE events (P1).** Both response modes must stop at protocol completion without waiting for TCP EOF, while preserving usage and tools. `internal/upstream/sse.go:406` aggregates until EOF even after `[DONE]`, a CodeArts done event, or an upstream error. `internal/upstream/sse.go:560` stops streaming on literal `[DONE]` and named terminal events but misses the CodeArts `{"text":"[DONE]","error_code":"0"}` terminal frame documented in `docs/reverse-engineering.md:108`. These terminal reads were reproduced with a reader that errors on any read past completion.
- **R4 — Protect account health and slot ownership (P1).** Downstream cancellation or write failure must not count as an upstream account failure. `internal/server/handler.go:837`, `internal/server/handler.go:876`, and `internal/server/handler.go:895` can penalize accounts for cancellation. With the supported error threshold set to 1, one already-canceled request cooled a healthy account in the reproduction. Retrying must release only a slot this request actually owns; after a failed reacquisition at `internal/server/handler.go:819`, the unconditional release at `internal/server/handler.go:828` can decrement another request's slot count (confirmed by control-flow inspection; regression still needed).
- **R5 — Reduce retry amplification and expose effective settings (P2).** Queue retries and account rotations must share a startup budget. Existing defaults are 30 attempts with 10-second delays (`internal/server/handler.go:130`), approximately 5 minutes of delays per account before network or admission time. The example config sets per-account concurrency to 5 (`config.example.json:19`), while code defaults to 1 (`cmd/server/config.go:81`). Align documented defaults and add concise timing/outcome logs without credentials or message bodies.
- **R6 — Protect multi-user admission (P1).** The user's implementation approval explicitly emphasizes account scheduling and many simultaneous users through New API. Admission must enforce per-account capacity atomically, distribute unaffined traffic fairly across eligible accounts, and bound the entire number of admitted chat handlers including retry sleepers, as well as slot waiters. Full admission queues must fail promptly with a retryable response rather than retain unbounded waiters. Cancellation and account add/reload must not leak or corrupt capacity.

## Acceptance Criteria

- **AC1 (R1, R4):** Canceled requests return promptly from initial admission, retry sleep/reacquisition, validation/refresh waits, preflight, and response consumption; they neither cool/disable an account nor start another account attempt.
- **AC2 (R1):** With one busy account and a second free eligible account, an unaffined request uses the free account; cached model capability and valid sticky routing remain effective.
- **AC3 (R2, R5):** Queue saturation, silent HTTP-200 SSE, and a later stalled body terminate within configured limits with an explicit API/SSE error; health checks remain responsive and slots return to zero after cleanup.
- **AC4 (R3):** Both response modes terminate on literal `[DONE]`, named done/end/finish events, CodeArts JSON DONE, and terminal upstream errors even if the upstream connection stays open. Trailing usage before the actual terminal marker is preserved.
- **AC5 (R2, R3):** An actively producing stream can run longer than an idle timeout; generated content, reasoning, tool calls, and optional usage keep their existing contracts.
- **AC6 (R4):** A failed reacquisition cannot release another request's slot; successful acquisitions are released exactly once. Genuine upstream authentication, parameter, rate, and server errors keep appropriate classification.
- **AC7 (R5):** Effective startup, idle, queue, and concurrency settings are configurable and documented consistently; test startup and runtime account add/reload use the same upstream policy.
- **AC8 (all):** Targeted regressions, the existing Go suite, vet, and build pass. Race checks run where the toolchain supports them; any environment limitation is explicitly reported.
- **AC9 (R6):** Concurrent local load never exceeds per-account active capacity, the configured global slot-waiter bound, or the process-wide admitted-handler cap of enabled account capacity plus MaxPending; excess requests receive prompt retryable errors even while admitted handlers sleep between rapid 429 responses. Saturation leaves `/healthz` responsive, cancellation drains waiting requests, and released capacity is reused without starving other eligible accounts.

## Out of Scope

- Production deployment, editing New API or its data, changing account credentials, claiming credits, or sending real provider chat requests.
- Guaranteeing provider capacity or claiming a provider outage without production evidence.
- Broad redesigns of authentication, background probing, discovery, tool features, or billing. Discovery/refresh changes are limited to avoiding blocking the chat lifecycle.

## Decisions and Deferred Verification

- Keep the OpenAI API shape and Go 1.22 compatibility; use local deterministic boundary tests.
- The user's follow-up approves implementation of the presented fixes and expressly includes multi-user scheduling, concurrent requests, and overload protection. No further process approval is needed before activation.
- Bound waiting and inactivity rather than imposing a blanket duration cap on active generation. Proposed operational defaults are recorded in `design.md` for review.
- Preserve a configurable single-account concurrency of 1 as the consistent default; do not claim the provider's current actual account limit from old source comments.
- Defer production trigger confirmation, exact gateway timeout/model mapping, and rollout to production telemetry and subsequent deployment authorization. These do not block correcting locally reproduced lifecycle defects.
