# Generation token usage and bounded console summaries

## 1. Scope / Trigger

Read this before changing chat usage capture, audit token metadata or summary
rendering. Implementation spans `internal/server/{chat,admin}.go`,
`internal/audit` and the embedded Requests view. Preserve the request lifecycle,
audit privacy/retention bounds and existing OpenAI response behavior.

## 2. Signatures

```go
type TokenUsage struct {
    PromptTokens *int64 `json:"prompt_tokens"`
    CompletionTokens *int64 `json:"completion_tokens"`
    TotalTokens *int64 `json:"total_tokens"`
    Source string `json:"source"`
}
func UsageFromMap(map[string]any, string) *TokenUsage
// audit.Event adds Usage *TokenUsage; audit.Page adds Usage UsageSummary.
```

Authenticated `GET /admin/api/requests` adds a top-level `usage` summary;
`GET /admin/api/audit` returns `audit` storage counters plus top-level `usage`.
There is no new endpoint, database, environment variable or independent poller.

## 3. Contracts

Successful nonstream and stream requests record nullable input/output/total
counts independently of `stream_options.include_usage`. Use upstream usage when
provided; otherwise use the existing text-length estimate. Only `upstream` and
`estimated` are valid source labels. Preserve the public usage map and its
streaming opt-in behavior; this metadata normalization is separate from response
serialization. Never persist arbitrary source maps or conversation content.

Accept integral, finite, nonnegative values no greater than 9007199254740991.
Known zero stays zero. Missing, malformed, fractional, negative or out-of-range
components remain null. An absent total may be derived when both components and
their sum are safe. An explicit invalid total cannot be silently labeled exact.
Native partial evidence is not replaced by a complete estimate. Invalid usage or
legacy missing fields must not discard an otherwise valid replay event.

Only successful full-generation observations receive usage. Failed/canceled
requests, rejections and legacy records remain unknown, without a fabricated
zero or billable estimate. Retain existing delivered-stream success semantics
when cancellation happens after DONE during remote cleanup. Preserve bounded
recording outside request/lease cleanup and existing retention/file-size limits.

Record, persistence/replay, pages and completed-connection snapshots own their
copies of nullable counters. Invalid/missing source metadata cannot retain raw
provider-controlled strings.

Summary `scope` is `recent_window`. `usage.window` is the actual count of
timestamp-valid, nonexpired retained events before query filters; preserve
the existing top-level `page.window` as configured capacity. For example, with
capacity 512, three retained events and one account-filter match, return
`page.window=512`, `usage.window=3`, `usage.requests=1`. `disabled`, `from`, `to`
describe availability and the matched time range. Totals are `requests`, `with_usage`,
`upstream_requests`, `estimated_requests`, `unknown_requests`, `prompt_tokens`,
`completion_tokens`, `total_tokens`, `prompt_known`, `completion_known`,
`total_known`, `overflowed`. Known component counts explain coverage: sums over
known components are not full-window exact costs. `by_account` and `by_model`
contain `{key,...totals}`. Group by selected account and final exact canonical
model, falling back to requested model; an absent identity has an empty key.
Return at most 100 groups of each kind with `accounts_truncated` and
`models_truncated`; overall totals still include all matches. Saturate numeric
sums at the JSON-safe boundary and surface `overflowed`.

Use one copied retained ring and identical expiry/outcome/account/model/status/
since matching for paging and summary. Apply `before` and `limit` only to page
rows, so pagination does not alter or multiply totals. Existing loss counters
remain visible. Bound computation by the configured recent-ring cap and perform
no disk scan, network work or growing lifetime accumulation. Nil/disabled sinks
return disabled summaries and nonnull empty group arrays.

Requests UI reads server totals, never sums loaded pages. Show per-event
input/output/total, upstream/estimate/unknown labels, filtered retained-window
scope, coverage and safe account/model groups. Empty, legacy, old-server,
disabled, overflow and truncated states are explicit. Query/auth changes clear
old summaries; late responses cannot repopulate another login or filter. Escape
all keys and keep table overflow local. Credential expiry is labeled as
credential time rather than generation token quota.

## 4. Validation & Error Matrix

| Input or state | Required result |
|---|---|
| Native valid counters, including 0 | Nullable known counts, source upstream |
| No native usage on a successful generation | Existing estimate, source estimated |
| Native partial or invalid component | Preserve valid components; invalid/missing component unknown |
| Native prompt and completion, absent safe total | Derive their sum; no unsafe arithmetic |
| Failure, cancellation, admission/model rejection | No fabricated successful-generation usage |
| Legacy event or invalid usage in replay | Keep event; unknown usage/components |
| Next page with same filters | Same full filtered-window totals; no double counting |
| Ring eviction or retention expiry | Both page and summary exclude expired/evicted event |
| Capacity 512, retained 3, account match 1 | page.window 512, usage.window 3, requests 1; paging does not change summary |
| Disabled sink | Explicit disabled state, no stale totals |
| Group limit or safe-sum limit reached | Explicit truncation/overflow; fixed bounds |

## 5. Good / Base / Bad Cases

- Good: a successful stream without include_usage remains wire-compatible and
  still contributes persisted audit counts. The UI labels an estimated count.
- Base: a restart replays retained token metadata into the existing recent
  window; older archives outside that window do not become all-time totals.
- Bad: render every missing old record as 0, sum only loaded rows, call estimates
  exact, retain raw usage objects, or claim failed generations were free.

## 6. Tests Required

Use actual registered chat/admin HTTP routes with correlated audit IDs. Cover
stream/nonstream native/estimate, opt-in/omitted/disabled include_usage, known
zero, partial/invalid native maps, failure/cancellation and complete cleanup.
Verify public usage compatibility alongside audit independence. Exercise typed
normalization, pointer ownership, persistence/replay including legacy/malformed
usage, completed connection copies, query/pagination/expiry/eviction consistency,
concurrency, bounded groups and overflow.

Real Chrome fixtures must verify row counts/source, component coverage,
account/model groups, filter changes, pagination, empty/legacy/disabled/failure,
query/auth races, escaping and desktop/mobile scroll. Run full Go normal/race,
vet/build, JS syntax and whitespace checks after integration. No real credential
or production load is required for this metadata change.

## 7. Wrong vs Correct

Wrong: count usage only inside the callback enabled by include_usage, and sum
the frontend's loaded rows as historical total usage.

Correct: observe successful generation completion independently for bounded
metadata, persist safe typed counts/source, and render the server's explicitly
filtered retained-window summary with coverage and unknown states.
