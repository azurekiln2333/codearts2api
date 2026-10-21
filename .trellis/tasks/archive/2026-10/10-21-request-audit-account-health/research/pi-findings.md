# P1 research: bounded request audit and truthful account health

Read-only planning research by the independent Pi reviewer, aligned to the parent's
`plan-review.md` and this child's `prd.md`/`design.md`. No product code changed, no task
activated. Anchors are HEAD `1b98ff0` (repair `3d87a5c`).

Scope alignment: this report implements AUD-R1…R7 and nothing else. It does **not**
change automatic cooldown expiry, `Enable` or `ClearCooldown` policy (§3.4), does not
add a per-value config provenance subsystem (§3.3), does not archive content, and does
not claim per-end-user identity.

## 1. What is missing today (confirmed by source)

| Need | Current state | Anchor |
|---|---|---|
| Per-request record | one `log.Printf` summary line: model, account, attempts, wait, first_data, total, outcome | `chat.go:24-27` |
| Request ID | none. `X-Codearts-Chat-Id` is a *conversation* id, set on the success path only | `chat.go:273` |
| HTTP status in a record | not captured; `chatResponseWriter` has no `WriteHeader` override | `lifecycle.go:82-132` |
| Classified provider cause | classifiers exist but only feed the log/HTTP body | `handler.go:818-854`, `isParamInvalidError:856`, `isConcurrentLimitError:876`, `isQueueLimitError:881`, `upstreamUnavailableReason` (`availability.go:136`) |
| Accurate outcome | `admission_rejected` covers both the ticket-cap rejection (`chat.go:33`) and an account-acquire/queue-wait timeout (`chat.go:151-156`). Measured on localhost: 13 requests that merely timed out waiting for a slot logged `admission_rejected` | `chat.go:33,154,243` |
| Queryable history | none; no `/admin/api/requests` route | `handler.go:166-186` |
| Effective limits | `schedule.watch` only (8 keys, no timeouts); no build identity anywhere | `cmd/server/main.go:74-83` |
| Health vs history | `statusBadge` derives 健康/冷却/禁用 purely from `disabled`/`cooling` | `panel.html:277-281` |

Incident consequence: the 10 requests aborted at 125 s left only
`outcome=canceled_or_timeout`/`downstream_canceled` summary lines — no request id, no
status, no cause, no correlation to the account state captured minutes later.

## 2. Stale-error behaviour as it stands (confirmed trace)

`pool.Account` keeps `errCount` (`pool.go:43`), `coolUntil`, `lastErr`,
`disabledReason`; `loadState`/`saveState` at `pool.go:823`/`857`.

1. `NoteError` (`pool.go:454-470`) increments `errCount` (`:462`); at `>= threshold` it
   sets `coolUntil` and `lastErr = "consecutive errors"` (`:465`), then persists.
2. Cooldown expiry clears nothing: after `coolUntil` passes the account is schedulable
   again while `errCount` is still `>= threshold` and `lastErr` still says "consecutive
   errors". Live sample `hid_7z2k00f8g96788r`: `err_count: 7`, `cooling: false`.
3. `NoteSuccess` (`pool.go:474-484`) zeroes `errCount` (`:480`) **only**: `lastErr` stays
   and it never calls `saveState`, so a restart resurrects the old counter. Live sample
   `azure_kiln`: `err_count: 4` with `cooling: false` and `healthy` in `stats`.
4. `ClearCooldown` (`pool.go:279-294`) resets `coolUntil`/`coolKind` but not `errCount`
   or `lastErr`; `Enable` (`pool.go:259-276`) clears `disabledReason`/`lastErr` but not
   `errCount`.
5. `List()` (`pool.go:196`, fields at `:223`) exposes `reason = disabledReason ||
   lastErr` with no timestamp, and `panel.html:313,321-322` render it next to a 健康
   badge — so a stale string reads as a current fault.
6. Persistence happens inside `p.mu` (`pool.go:158-192`, `259-276`, `279-294`,
   `435-470`, `487-502`). Bounded today, but it is exactly the pattern the audit sink
   must not repeat.

**Per the parent's scope decision, items 2/4 are policy that stays unchanged.**
This proposal does not touch automatic expiry, `Enable` or `ClearCooldown` semantics.
It fixes (a) presentation of state that already exists, and (b) persistence of a real
error→success recovery. The live `last_error: "context canceled"` remains evidence of an
older deployed path (`3d87a5c^:internal/server/handler.go:895` used `aerr.Error()`), and
may also be a value persisted by an older binary — it cannot date the running process.

## 3. Audit contract

### 3.1 One metadata record per chat request

```json
{"v":1,
 "seq":10231,
 "id":"7f3c9a1e5b204d68",
 "ts":"2026-10-21T08:33:44.012Z",
 "model":"glm-5.2",
 "model_canonical":"GLM-5.2",
 "stream":false,
 "status":200,
 "outcome":"ok",
 "error_code":"",
 "account":"hid_7z2k00f8g96788r",
 "attempts":1,
 "tried":["hid_7z2k00f8g96788r"],
 "wait_ms":12,
 "queue_ms":12,
 "first_data_ms":40,
 "total_ms":1900,
 "retry_after_s":0,
 "canceled":false,
 "cause":{"class":"none","code":"","text":""}}
```

Field rules (binding):

- `id`: 16 hex chars at handler entry, echoed as `X-Request-Id` on every response
  including streaming (set before headers are committed) and added to the summary log so
  journald and the audit file correlate.
- `status`: the **real** status captured by a `WriteHeader` override on
  `chatResponseWriter` (`lifecycle.go:82`). **No committed response ⇒ `status` is
  absent/`0`; never fabricate 200.** A streamed request that fails after headers keeps
  its committed status. Both facts are expressed separately: `status` (wire) and
  `outcome`/`canceled` (semantics).
- `first_data_ms`: **nullable** (`*int` / omitted). "Never received data" and "received
  at 0 ms" must not collapse into the same value. Same for `account` (`""` = none
  selected) and `retry_after_s` (absent = header not sent).
- `outcome`: existing vocabulary (`chat.go`) with **one** split: keep
  `admission_rejected` for the ticket-cap rejection (`chat.go:33`) and add
  `queue_wait_timeout` for the account-acquire timeout (`chat.go:151-156`). Different
  operator actions (add capacity vs. free slots), currently indistinguishable.
- `cause`: **fixed safe text only.** `class` ∈ {`none`,`capability`,`auth`,
  `param_invalid`,`rate_limit`,`concurrency_limit`,`upstream_5xx`,`timeout`,`cancel`,
  `downstream`,`saturation`,`queue_wait`,`unknown`} derived from the existing
  classifiers; `code` keeps a provider code token (≤64 bytes, e.g.
  `InferHub.002002009.404`, sanitised to `[A-Za-z0-9._-]` only); `text` is the
  classifier's own short constant (`"rate limited"`, `"benefit not found"`,
  `"model not registered"`, …). **Raw provider error strings are never stored**, so no
  free-text scrubbing heuristic is needed and no credential can leak through this field.
- Explicit limits: `model`/`model_canonical` ≤96 bytes, `account` ≤128, `tried` ≤8
  entries, `cause.text` ≤64, `cause.code` ≤64. Truncation is silent but bounded.
- Never stored: API key, `Authorization`, `x-auth-token`, access/refresh/security
  tokens, DPoP keys, prompts, generated content, tool arguments, request/response
  bodies, checkin payloads. Events are materialised from local values only — no
  references to request/response objects after the handler returns.

### 3.2 Sink

New package `internal/audit` (server imports it; `internal/pool` must not):

```go
type Event struct { /* §3.1 fields; strings + ints + nullable ints only */ }
type Options struct {
    Enabled       bool          // default true
    Dir           string        // default <dir(state_file)>/audit
    MaxQueue      int           // default 256,  clamp ≤4096
    Recent        int           // default 512,  clamp ≤8192
    MaxFileBytes  int64         // default 8 MiB, clamp ≤64 MiB
    MaxFiles      int           // default 8,    clamp ≤64  (total = MaxFiles*MaxFileBytes, active included)
    MaxTotalBytes int64         // default 64 MiB, enforced independently of the two above
    RetentionDays int           // default 7,    clamp 1..90
    FlushInterval time.Duration // default 1 s,  clamp 100ms..10s
}
func Open(Options) (*Sink, error)   // creates dir, restores recent window, opens active file
func (s *Sink) Record(ev Event)     // NON-BLOCKING: channel send or drop+counter
func (s *Sink) Snapshot(q Query) Page
func (s *Sink) Stats() Stats
func (s *Sink) Close(ctx context.Context) // bounded drain (default 2 s) then drop
```

- `Record` materialises the event, then
  `select { case s.ch <- ev: default: s.dropped.Add(1) }`. No locks, no I/O, no growth
  beyond the queue. Called from the existing deferred summary block (`chat.go:24-27`),
  which is registered first and therefore runs **after** `current.close()`
  (`chat.go:128`) — the sink can never extend cleanup or hold a lease or `p.mu`.
- **Recent window restored after restart** (per AUD-R7): `Open` reads the tail of the
  newest retained files, oldest→newest, up to `Recent` events and at most
  `min(MaxTotalBytes, 2*Recent lines)` of I/O, rebuilding the ring and continuing `seq`
  from the highest value seen. A restart therefore does not reset what the panel shows.
- **One writer goroutine owns all audit I/O**: append JSONL to the active file, flush
  every `FlushInterval`, rotate to `audit-<8-digit-seq>.jsonl` at `MaxFileBytes`, and
  prune oldest-first by `MaxFiles`/`MaxTotalBytes`/`RetentionDays` so the **active file
  counts toward 64 MiB**. Nothing here runs under a pool or request lock.
- Failure handling: queue full ⇒ drop + counter. Open/write/rotate error ⇒
  `write_failures` counter, record dropped, at most one rate-limited log line per
  minute. No synchronous retry, no backpressure onto chat.
- Bounded memory: `MaxQueue + Recent` events (default 768 × ~350 B ≈ 270 KiB).

### 3.3 API, panel, runtime diagnostics

- `GET /admin/api/requests` (existing `withAuth`): `limit` (default 50, max 200),
  `before` (seq cursor), `outcome`, `account`, `model`, `status`, `since`. Returns
  `{"events":[…],"next_before":N,"dropped":N,"write_failures":N,"window":512,
  "disabled":false}`. Serves the restored recent window only; the panel labels older
  retained files as **archive** and says the window is bounded.
- `GET /admin/api/overview` gains two small additive blocks — no provenance subsystem:
  - `effective`: the actual runtime limits — `startup_timeout`, `account_wait_timeout`,
    `upstream_idle_timeout`, `upstream_header_timeout`, `stream_write_timeout`,
    `max_rotate`, `max_pending`, per-account `max_concurrent`, `queue_max_attempts`,
    `queue_retry_delay` — as effective values, nothing about where they came from.
  - `build`: `{"commit":"…","known":true|false,"built_at":null|"…"}`. `known:false` is
    rendered explicitly; never a blank or a fabricated commit.
- Panel: "最近请求" table (id, time, model, account, status, outcome, cause, attempts,
  wait/first-data/total) with filter chips and `before`-cursor paging over the restored
  window; account rows split into **current schedulability** (badge unchanged:
  健康/冷却/禁用) and **failure history** (`上次失败 <time>` · `失败原因(历史)`), rendering
  unknown legacy timestamps as `未知（旧记录）`. All provider text goes through the
  existing `escapeHtml` (`panel.html:349`).

### 3.4 Account health: presentation + one real recovery

Policy unchanged: automatic cooldown expiry, `Enable` and `ClearCooldown` keep their
current semantics (`pool.go:259-276`, `279-294`), including not resetting `errCount`.

- **New additive state**: `lastErrAt`, `lastSuccessAt` (`time.Time`, zero = unknown);
  persisted as `last_error_at` / `last_success_at` alongside the existing keys so old
  binaries still read `state.json` and `loadState` tolerates their absence.
- **Presentation fix**: `List()` (`pool.go:196`) additionally exposes `schedulable`
  (= `!disabled && !cooling`), `last_error_at`, `last_success_at`. The panel stops
  printing `last_error` as a current reason: when the account is schedulable, the same
  string is shown as `失败原因(历史)`. A cooldown-expired account with `err_count: 7`
  therefore reads "当前可调度 · 历史连续失败 7 次 · 上次失败 <time>" instead of a 健康 badge
  with a contradictory error string.
- **Persist a real error→success recovery** (`NoteSuccess`, `pool.go:474`): if
  `errCount > 0 || lastErr != ""`, then clear `errCount`/`lastErr`, set `lastSuccessAt`
  and call `saveState()` **once** for that transition; otherwise update only in memory
  and write nothing. Guarded by `!disabled` and no *active* cooldown, so a concurrent
  manual disable or cooling is never overridden. This fixes the restart resurrection in
  §2.3 without adding a disk write to every successful request.
- Historical timestamps absent from a legacy `state.json` stay unknown and are labelled
  as such — never inferred from `coolUntil` or from the journal.

## 4. Defaults (per parent decision)

| Setting | Default | Clamp | Note |
|---|---|---|---|
| `audit.enabled` | `true` | — | |
| `audit.retention_days` | 7 | 1..90 | |
| `audit.max_files` | 8 | ≤64 | **active file included** |
| `audit.max_file_bytes` | 8 MiB | ≤64 MiB | 8 × 8 MiB = 64 MiB total |
| `audit.max_total_bytes` | 64 MiB | ≤1 GiB | independent hard cap incl. active |
| `audit.max_queue` | 256 | ≤4096 | absorbs 429 storms without blocking |
| `audit.recent` | 512 | ≤8192 | ~270 KiB RAM, restored on restart |
| `audit.flush_interval` | 1 s | 100 ms..10 s | bounded loss window |
| `audit.dir` | `<dir(state_file)>/audit` | — | inside the existing `/app/data` mount |

Env: `CA2A_AUDIT_ENABLED`, `CA2A_AUDIT_DIR`, `CA2A_AUDIT_MAX_QUEUE`,
`CA2A_AUDIT_RECENT`, `CA2A_AUDIT_MAX_FILE_BYTES`, `CA2A_AUDIT_MAX_FILES`,
`CA2A_AUDIT_MAX_TOTAL_BYTES`, `CA2A_AUDIT_RETENTION_DAYS`, each guarded by `v != ""`
like the existing `applyEnv` (`cmd/server/config.go:184-262`). Nonpositive ⇒ default,
then clamp.

## 5. Ownership map

- **new `internal/audit/**`** — event, sink, ring, restore-on-open, rotation, query,
  fixed classifier text mapping. No dependency on server/pool.
- `internal/server/chat.go` — request id, one `Record` inside the existing deferred
  block, outcome split, cause classification.
- `internal/server/lifecycle.go` — `chatResponseWriter.WriteHeader` status capture.
- `internal/server/handler.go` — `Config.Audit *audit.Sink`, `X-Request-Id`, route.
- `internal/server/admin.go` — `/admin/api/requests`, `effective` + `build` + `audit`.
- `internal/server/panel.html` — request table + paging, current-vs-history split.
- `internal/pool/pool.go` — additive `List()` fields, `lastErrAt`/`lastSuccessAt`,
  transition-only `NoteSuccess` persistence. No audit import, no new I/O under `p.mu`.
- `cmd/server/{config,main}.go`, `config.example.json`, `.env.example`, `README.md` —
  options, wiring, shutdown drain, docs.

## 6. Tests

`internal/audit`:
1. `TestRecordNeverBlocksAndCountsDrops` — stalled writer, `MaxQueue=2`, 100 Records →
   all return immediately, `dropped == 98`.
2. `TestRecentWindowRestoredAfterRestart` — write 600 events, reopen → `Recent=512`
   restored in order, `seq` continues (no reset), oldest 88 only on disk.
3. `TestRotationIncludesActiveFileInTotal` — 8 MiB cap, tiny `MaxFileBytes` → file count
   ≤ `MaxFiles` *including* active, total ≤ `MaxTotalBytes`, newest-first prunes.
4. `TestRetentionPrunesByAge`.
5. `TestStorageFailureIsObservableAndHarmless` — unwritable dir → `Record` still
   non-blocking, `write_failures > 0`, no panic, `Close` returns.
6. `TestQueryPagingStableUnderConcurrentWrites` — cursor stability + limit clamps.
7. `TestCauseTextIsFromFixedSet` — no raw provider string can reach `cause.text`; a
   token-shaped value is dropped by the sanitiser.

`internal/server` (real handler, loopback fake upstream, fake creds):
8. `TestRequestIDHeaderOnEveryOutcome` — 200/400/404/429/503/504 + streaming all carry
   `X-Request-Id`, exactly one event matches.
9. `TestAuditStatusAbsentWithoutCommittedResponse` — client cancel before headers ⇒
   `status` absent/`0`, `first_data_ms` null, `canceled:true`; a post-header stream
   failure keeps its committed status.
10. `TestAuditClassifiesCancelTimeoutAndCapability` — right `outcome`/`cause.class`.
11. `TestAuditDistinguishesQueueWaitFromCapRejection` — the §3.1 split.
12. `TestAuditRecordsLongActiveGeneration` — P0 harness S6 shape (upstream streams past
    the startup budget, request still 200) records a real status and a total above the
    startup budget, proving "slow but alive" is representable.
13. `TestAuditDisabledHasNoSideEffects` — no files, API reports disabled.
14. `TestAuditNeverRecordsSecrets` — `Authorization` + prompt in the request ⇒ the
    serialized event contains neither.

`internal/pool`:
15. `TestSuccessAfterErrorsClearsStateAndPersistsOnce` (transition writes once;
    steady-state success writes zero times — count `saveState` calls).
16. `TestSuccessDoesNotClearManualDisableOrActiveCooldown`.
17. `TestCooldownExpiryKeepsCounterAndHistoryIsPresentation` — policy unchanged
    (`errCount` retained), `List()` reports `schedulable:true` + `last_error_at`.
18. `TestLegacyStateJSONHasUnknownTimestamps` — old file loads, timestamps nil, panel
    label path renders `未知（旧记录）`.
19. `TestStateJSONRoundTripAdditive`.

Panel/API: authenticated fetch without key → 401; escaping of provider text; absent
fields render as `—`/`未知`.

## 7. Compatibility, risks, limits

- All API/payload changes additive; old `state.json` loads, new keys ignored by old
  binaries. Auth files and mounts untouched.
- A shared service key cannot attribute end users; the record is per chat request, not
  per tenant. Stated in panel/README so it is never read as a user audit trail.
- Audit is local, best-effort and bounded: under overload it drops by design and the
  counter shows it. A hard kill loses at most `flush_interval` of records, and the
  restored window may therefore be shorter than 512 after a crash.
- Recording happens after the response completes, so the record never delays the
  client and never holds resources.
- Audit is not a health source of truth; the pool stays authoritative.
- Audit does not fix the deployment question — but `X-Request-Id`, the request records
  and the `build`/`effective` blocks are themselves new fingerprints for it.

## 8. Residual choices for the final review

1. Whether the `queue_wait_timeout` outcome split is in scope now (proposed: yes — it is
   the one place where the current vocabulary actively misleads).
2. Whether `cause.code` should be exposed in the panel or only in the JSON API
   (proposed: JSON only, panel shows the fixed text).
3. `build.known:false` rendering when the deployed image has no ldflags yet (proposed:
   explicit `未知构建` chip, never blank).
