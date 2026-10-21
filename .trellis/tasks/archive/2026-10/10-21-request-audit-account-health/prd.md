# Request audit and truthful account health

## Goal
Let an authenticated operator explain slow/failed requests and distinguish schedulability from historical failures.

## Evidence
Parent research records opaque request timeouts and generic/stale account reasons. NoteError (pool.go:454) stores consecutive errors; NoteSuccess (line 474) does not clear lastErr. panel.html:273 equates not-disabled/not-cooling with healthy. No request audit API/view exists.

## Requirements
- AUD-R1: One bounded metadata summary per chat request: request ID/time, requested/canonical model, selected account, HTTP status/outcome, attempts, queue/first-data/total timing, and classified sanitized provider cause. Preserve honest absent-account/timing fields.
- AUD-R2: Existing authenticated API/panel gains stable pagination/filtering and response request-ID correlation.
- AUD-R3: Queue, memory and persistent storage are bounded and rotate within documented configurable limits. Overflow/disk failures do not block chats or hold leases/pool locks; expose dropped/write-failed counts.
- AUD-R4: Never store API keys, Authorization headers, OAuth/access/refresh credentials, prompts or generated content. Shared service API keys do not identify individual New API users.
- AUD-R5: Separate schedulable/cooling/disabled state from historical failures. Success clears obsolete current errors without overriding concurrent manual disable or active cooldown. Keep old state JSON compatible.
- AUD-R6: Show safe effective build/limits with P0 and preserve auth files/mounts.
- AUD-R7: Default to 7-day retention, at most eight 8-MiB files including the active file (64 MiB total), a 256-event queue and a 512-event recent index. Reload the recent index from retained files after restart. The panel queries this bounded recent window; retained older files are the local archive. Expose limits and loss counters honestly.

## Acceptance Criteria
- Success, rejection, upstream failure, timeout and cancellation produce accurate correlated metadata when accepted by the sink.
- Auth, pagination/filtering, safe provider-text rendering and forbidden-data absence are verified.
- Rotation, full queues and unwritable storage remain bounded and leave health/chat/capacity responsive; loss is observable.
- Meaningful regressions cover cooldown expiry, success after errors, manual disable and reload.
- Historic errors absent from the old journal are labeled unavailable instead of fabricated.

## Boundaries and review
Implementation authorized with the reviewed metadata defaults above. Preserve current automatic cooldown expiry, Enable and ClearCooldown policy; this task fixes presentation and persists a real error-to-success recovery, without silently changing circuit-breaker behavior. No content archive, tenant identity system, external analytics, inferred balances, credential migration or production mutation.
