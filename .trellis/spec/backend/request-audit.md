# Request audit and account diagnostics

## 1. Scope / Trigger

Read this when changing `internal/audit`, chat completion diagnostics, authenticated
audit/admin APIs, the embedded panel, account recovery state, or audit/build
configuration. Preserve the request-lifecycle contract and Go 1.22 compatibility.

## 2. Signatures

```go
audit.Open(audit.Options) (*audit.Sink, error)
(*audit.Sink).Record(audit.Event)
(*audit.Sink).Snapshot(audit.Query) audit.Page
(*audit.Sink).Stats() audit.Stats
(*audit.Sink).Close(context.Context)
(*audit.Sink).Begin(id string, started time.Time) *audit.Tracker
(*audit.Sink).Connections() audit.Connections
(*audit.Tracker).Finish(audit.Event)
```

Authenticated APIs: `GET /admin/api/requests`, `GET /admin/api/audit` and
`GET /admin/api/overview` and `GET /admin/api/connections`. Requests use `limit` (default 50, maximum 200), `before`
(exclusive uint64 sequence cursor), `outcome`, `account`, `model` (requested or
canonical, case insensitive), `status` and RFC3339 `since`.

Requests and audit-statistics responses additionally contain `usage`, the filtered
bounded recent-window generation summary. Events may contain nullable typed
`usage` counters/source. Follow [Generation Token Usage](./token-usage.md) for
normalization, persistence ownership, success-only observation and UI contracts;
this is distinct from OAuth credential lifetime and from provider billing.

## 3. Contracts

### Metadata and transport

Each completed authenticated chat attempts one metadata record. `X-Request-Id`
correlates the response, log and record. Record the request start time, requested
and canonical model, stream flag, nullable selected account and first-data time,
attempts/tried accounts, queue/first-data/total durations, outcome and classified
cause. `committed=false` omits `status`; never fabricate HTTP 200 for a disconnect
before any response commitment. A stream failure after HTTP 200 keeps status 200
and records its failure outcome separately.

A successfully flushed protocol DONE completes a streamed response. The client
may close immediately while the handler is still doing bounded detached remote
idle cleanup. That later context cancellation must not turn delivered success
into `canceled`: record `outcome=ok`, `canceled=false`. A real read/write failure
or disconnect before delivery remains canceled/error. Keep nonstreamed cancellation
before the final JSON write distinct. Record only after cleanup, and let diagnostics
observers wait a bounded cleanup window after DONE for the correlated event.

Once a real upstream attempt occurs, `model_canonical` is the exact ID actually
sent on the last attempt, resolved from that selected account/auth and route.
Update active tracking at the same point; a union-wide display name computed
before selection cannot stand in for the wire ID. Keep `model` as the caller's
literal request, including `auto`. Explicit concurrent-session failures use the
same classifier for scheduling and audit across HTTP400/429 and early/late SSE.

Cause text comes from a fixed allowlist; provider codes are bounded tokens.
Never copy arbitrary provider errors, requests, responses, bodies, prompts,
completions, tool arguments, headers, keys, credentials or OAuth tokens. One shared
service key cannot attribute distinct New API end users. HTML escapes every
dynamic audit field, including provider code and model/account names.

### Ownership, limits and failure isolation

Record after attempt/body/lease and lifecycle-ticket cleanup, outside pool locks.
`Record` copies bounded metadata, serializes sequence/insertion/enqueue order and
performs a nonblocking channel send. It performs no filesystem operations.
Caller and page slices/pointers never alias stored event metadata. Only one writer
owns audit files. Queue overflow and persistence errors remain visible counters;
memory may retain a dropped persistence event until it leaves the recent window.

Defaults: enabled, 7 days, 256 queued events, 512 recent events, at most eight
8-MiB files including the active file, 64 MiB total, one-second sync interval.
Enforce byte/count limits **before** creating/writing the next file/event. If an
archive cannot be removed, skip persistence and expose failure rather than exceed
the cap. Periodic housekeeping expires files even during idle traffic. An unusable
directory leaves metadata and failure diagnostics available without blocking chat.

Every string/list is bounded before sanitization/allocation. An encoded event is
at most 16 KiB. Startup replay seeks bounded file tails, sanitizes them and restores
the recent window and maximum retained sequence; its I/O budget is 64 MiB. Paging
uses exclusive sequences; new traffic may evict the oldest records. Older retained
files are an on-disk archive, not an unbounded API query. Post-close events count
as dropped. Shutdown waits/draining are bounded independently of chat.

Environment keys (all forwarded by Compose): `CA2A_AUDIT_ENABLED`,
`CA2A_AUDIT_DIR`, `CA2A_AUDIT_MAX_QUEUE`, `CA2A_AUDIT_RECENT`,
`CA2A_AUDIT_MAX_FILE_BYTES`, `CA2A_AUDIT_MAX_FILES`,
`CA2A_AUDIT_MAX_TOTAL_BYTES`, `CA2A_AUDIT_RETENTION_DAYS`,
`CA2A_AUDIT_FLUSH_INTERVAL`. Empty overrides preserve JSON/default values. Default
directory is `audit` beside `state_file`, preserving the existing data mount.
The sink clamps all numeric settings; total bytes also cannot exceed the combined
file limits, and one file cannot exceed the total budget.

### Live connection tracking

`internal/telemetry` owns fixed phase/progress callbacks carried by the request
context; it never receives request/response content. The sink tracks at most 512
active requests with 32 phase points each. Overflow and trace truncation are
cumulative visible counters. Disabled/nil/closed sinks have no stranded tracker.
`Connections()` returns bounded in-memory `active` records and the latest 50
completed `recent` events, plus `disabled`, `capacity`, `overflow` and
`trace_truncated`. No endpoint read performs network or disk I/O.

Active records contain `id`, `ts`, `model`, `model_canonical`, `stream`, nullable
`account`, `attempt`, `phase`, `phase_age_ms`, `total_ms`, nullable
`last_upstream_progress_age_ms` / `first_sse_data_ms`, `timeline` and
`trace_truncated`. Points are `{phase,at_ms,attempt,account?}`. Completed events
optionally add `trace{timeline,first_sse_data_ms,last_upstream_progress_ms,truncated}`;
old JSON without `trace` remains valid and is not given fabricated history.

Callbacks happen outside pool/account/stream locks, have bounded memory cost and
perform no I/O. Only actual upstream reads returning bytes update progress;
only accepted SSE data sets first SSE timing. HTTP header wait includes client
request work, not an isolated TCP metric. Credential observers report each
caller's wait after unlock; shared background refresh inherits no request observer.
There is no upstream session-creation RPC in the v2 request flow.

Finish once after body/lease/admission cleanup using the existing final
status/outcome/cause classifier. Ignore late callbacks after finish; close detaches
trackers without inventing a completion. Trace sanitization/copying applies on
record, page and replay, and encoded events still fit the existing 16-KiB ceiling.
Streaming request age alone does not imply a hang or justify cancellation.

### Account state and build identity

Schedulable/cooling/disabled describe current availability; failure class/text/time
describe history. Every counted failure updates its failure timestamp, including
counts below threshold. Old state without timestamps shows unknown time. Additive
pool-state fields preserve old JSON and leave auth JSON untouched. On a real
error-to-success transition, clear stale current state and persist once while
holding the existing synchronized state transition. Routine success does not
write state. Success never overrides active cooldown or manual disable. Automatic
expiry, `Enable`, and `ClearCooldown` policies remain unchanged.

Overview exposes actual effective limits and `build{commit,known,built_at}`. Empty
build identity is explicitly unknown. Docker's `.git` exclusion means build args
`BUILD_COMMIT` / `BUILD_TIME` must inject ldflags for identity; Compose forwards
`CA2A_BUILD_COMMIT` / `CA2A_BUILD_TIME`. Do not guess production identity from a
local checkout or claim a successful deployment without checking the running build.

## 4. Validation & Error Matrix

| Condition | Required observation |
|---|---|
| Admission ticket cap reached | 429, `admission_rejected`, no invented account/first data |
| Account wait expires | 429, `queue_wait_timeout`, distinct from admission rejection |
| Caller disconnect before commitment | Cancellation outcome, absent HTTP status |
| Streaming upstream fails after 200 | Committed 200 plus failure outcome and safe cause |
| Provider capability error | Safe provider code/description; no account-wide penalty |
| Healthy pool excludes model from current authoritative catalogs before sending | Committed 404 `model_not_available`, audit `model_unavailable`, capability cause, attempts/tried 0; no invented provider code |
| Empty/unhealthy pool before sending | Committed 503, `no_healthy_account`, attempts/tried 0; no invented provider failure |
| Full audit queue / failed storage | Chat and health stay responsive; visible loss counters |
| Audit closed / disabled | No stranded enqueues; post-close drops counted; disabled API honest |
| Unauthenticated audit API | Existing 401 envelope; no metadata disclosure |
| Old state JSON | Schedulability preserved; unknown historic time; no auth migration |
| Header or first-SSE wait | Active phase/age visible; absent first SSE remains null |
| Long healthy stream | Recent actual-read age stays visible; no lifetime-based termination |
| Active/phase caps reached | Fixed bounds, explicit loss/truncation counters, eventual capacity reuse |
| Late callbacks / legacy replay | No resurrected active entry or invented legacy trace |

## 5. Good / Base / Bad Cases

- Good: trace a slow request by response request ID, model, chosen account and
  queue/first-data/total duration without saving any conversation content.
- Base: default data mount holds bounded archive files and restores recent 512 on
  restart; a successful recovery clears stale current errors but retains history.
- Bad: a full disk cannot retain a chat lease, undo a manual disable, invent a 200
  for a canceled caller, or silently grow beyond configured storage limits.

## 6. Tests Required

- `internal/audit/audit_test.go`: concurrent monotonic ordering, metadata ownership,
  restart window/sequence/cursor, pre-write file/count/total caps, periodic expiry,
  overflow, unusable storage, closed/nil sink, field bounds and fixed safe text.
- Real chat/admin route checks: request-ID correlation, honest absent fields,
  actual HTTP versus execution outcome, auth, filtering/pagination, no fixture
  keys/prompts/completions in files or API, safe provider code rendering.
- Pool-state compatibility/recovery checks: timestamps below threshold, legacy
  unknown time, persisted recovery, retained history, no routine-success writes,
  concurrent cooldown/manual disable preservation.
- Config checks: defaults, JSON/env precedence, false/zero/empty settings,
  derived audit directory and normalized effective limits. Verify actual Compose
  interpolation, synchronized ports and credential mount preservation.
- Run full tests, race detector, vet, build and diff checks after integration.
- Connection regressions exercise actual stalled HTTP headers, silent SSE,
  comment-only progress, long stream/aggregate, blocked downstream, slot and
  credential waits, admission rejection and cancellation. Assert resource cleanup,
  final classifier consistency, no sensitive strings, active/trace bounds,
  worst-case escaped event size, replay and late-callback suppression.

## 7. Wrong vs Correct

Wrong: write a raw provider error or response body to disk inside a pool lock,
then rotate only after the file has exceeded its limit.

Correct: classify into bounded fixed metadata, release account/request ownership,
enqueue without waiting for disk, and let the writer enforce limits before I/O.
