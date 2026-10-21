# Request lifecycle correction

## Boundary and Flow

The change stays in the existing server, account pool, upstream client/SSE, configuration, and documentation boundaries. No new service or external dependency is required.

Request flow becomes: create request cancellation/startup budget -> obtain a shared request-lifetime ticket -> read/parse/validate the body within the startup budget -> select an eligible account with available capacity -> acquire an owned slot -> validate credentials -> send/preflight upstream -> consume with an idle limit -> stop on terminal SSE -> close body and release owned slot and ticket. Before streaming starts, retries and account rotation share the same startup budget. Cancellation terminates any phase without penalizing accounts or beginning a new attempt.

## Timeout Contract

- Introduce an explicit startup limit covering admission, refresh waiting, retries, rotation, response headers, and first SSE data. Proposed default: 90 seconds, configurable with a clearly documented setting/environment override.
- Replace 180-second fixed account admission with a short shared wait budget. Proposed default: 10 seconds; free eligible accounts are considered before waiting.
- Make the existing `upstream.timeout_seconds` effective for upstream header/first-data/subsequent idle waits and short operations. Proposed default for new configurations: 60 seconds. Explicit existing values remain honored. Do not set a total HTTP timeout on active SSE generation.
- Reduce default transient queue retries from 30 attempts with 10-second delays to 3 total attempts with 2-second delays. Keep explicit overrides, but enforce the startup budget even with large configured values. Do not sleep after the final exhausted attempt.
- Add a finite downstream stream write deadline (proposed default: 30 seconds) using facilities compatible with Go 1.22, so a peer that stops reading cannot retain upstream resources indefinitely.
- Before headers are committed, return an OpenAI-shaped 429/503 for saturation and 504 for startup/upstream timeout, as appropriate. After streaming begins, emit an error and terminal `[DONE]` where the downstream remains writable; return/close directly if it has disconnected.

A request context carrying a fixed startup deadline must not accidentally terminate a healthy long generation after startup. Use a cancellation lifetime whose startup timer can stop when preflight succeeds, while caller cancellation and the independently reset idle guard remain active. Bound the actual HTTP request lifetime so stalled preflight/error-body reads are covered, and ensure timers, readers, and bodies are cleaned up without background goroutine leaks. Reuse standard-library timers/context primitives, with race-safe cancellation and idempotent close behavior.

## Account Selection and Ownership

- Bound global waiting chat requests with `pool.Config.MaxPending` (default 64, configurable). In addition, acquire one process-wide request-lifetime admission ticket at chat entry, capped at the sum of enabled account capacities plus MaxPending; retain it through retry sleeps, rotation, I/O, and response cleanup. Without this outer ticket, rapid upstream 429 responses could release slots and accumulate unbounded sleeping retry handlers. Admission with no free eligible slot may wait at most the shared account-wait/startup budget; full admission fails immediately with a retryable status and `Retry-After`. Both bounds belong to the Pool and are shared across handlers.
- Use atomic free-slot reservation and notification on release, not unbounded per-account polling. Choose available candidates by model-capability priority, then least utilization, and rotate equal-load choices for fair unaffined traffic. Sticky conversations may wait for their one eligible account within the same bounds.
- Prefer cached model-capable healthy accounts with available slots; allow unknown catalogs optimistically, as existing contracts require. Preserve valid sticky affinity, but reject stale/ineligible sticky choices and avoid holding up unaffined requests behind a busy account.
- Enable derived sticky affinity only when a request has explicit body/header affinity or actual assistant/tool conversation history. Identical first-turn prompts from unrelated users use fair allocation. Keep conversation ID generation unchanged for the upstream protocol and tool chains.
- Positive availability of an unknown-catalog account must not promote it above other unknown accounts; successful probing/chat is evidence of eligibility, while catalog capability supplies priority. Otherwise the first successful account can monopolize equal-load first-turn traffic.
- Do not blindly wire the existing lazy-discovery helper into chat: provider discovery must respect the remaining startup/caller budget or stay outside the chat hot path.
- Add a context-aware acquisition API or equivalent acquisition loop. Check account health/capacity while actually acquiring, rather than only when initially selecting.
- Track whether this request currently holds an account slot. Encapsulate an attempt's cleanup so bodies close and slots release exactly once on normal completion, error, cancellation, retry exhaustion, and failed reacquisition. A request that no longer owns a slot must never decrement active count.
- Preserve serialized credential refresh, but make a waiting chat caller cancelable. Shared/administrative refresh may continue independently; canceled waiters must not hold a chat slot after returning.
- Keep configuration effective for initial accounts and runtime add/reload paths.

The native implement agents share the concrete pool/upstream/server interface contract in `research/contracts.md`. Each lease refers to the actual account object and releases idempotently, so concurrent reload cannot decrement a different account's slot.

## SSE Contract

Share a small terminal-event classifier between streaming and aggregation. Recognize literal `[DONE]`, named done/end/finish events, and CodeArts JSON `text: [DONE]` frames with existing event variants. Apply payload/error/usage/tool updates first, then terminate. Upstream errors win over a DONE-shaped text field. Do not use a non-empty `finish_reason` alone as a terminal marker because usage can follow it.

Both response modes must stop reading after a terminal signal even if the socket stays open. Keep existing finite EOF compatibility unless an explicit malformed/error condition requires failure. Stream errors must not be reported as a successful completion or cause onDone/success health updates.

## Health Classification and Diagnostics

Classify request cancellation, startup admission expiry, and downstream write failure separately from upstream health. No cooldown, disable, availability mark, or failover should occur because a caller disconnected. Genuine provider 401, parameter, queue/429, unavailable-model, and 5xx errors retain appropriate existing handling.

Record concise model/account, time spent waiting, first-data latency, total duration, attempt count, and terminal outcome where useful. Never log credentials, authorization headers, or message content. Correct README and example concurrency to 1 and document the exact timeout phases and migration of previously inert configuration.

## Compatibility, Rollout, and Rollback

- Preserve OpenAI routes, body schemas, tool semantics, usage behavior, case canonicalization, and explicit model IDs.
- Long responses with continuing data remain valid; a provider that remains silent beyond the new inactivity/startup defaults receives an explicit timeout. Operators can raise limits, preferably keeping the gateway timeout above proxy startup/idle limits.
- Explicit concurrency settings continue to work. Align only the default and example; do not impose an assumed provider-specific maximum.
- No production deployment or real inference is part of this change. Rollback consists of reverting the coherent code/config/docs change; no persistent data migration is required.

## Deferred Verification

Production logs are needed to identify the initial trigger, validate New API cancellation/timeout behavior, and confirm the exact model mapping. The local correction targets independently reproducible proxy defects and cannot guarantee upstream model availability.
