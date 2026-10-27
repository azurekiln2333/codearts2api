# Account credit quota and New API balance

## 1. Scope / Trigger

Read this when changing provider credit collection, the overview quota display,
or the New API balance endpoint. This is a cached view of account credit packages,
not token usage, model-specific benefit quota, or an official currency conversion.

## 2. Signatures

- `(*upstream.Client).FetchCreditQuota(context.Context, upstream.SignCredential)`
- `(*pool.Pool).StartQuotaCollection(context.Context)` and
  `RequestQuotaRefresh() pool.QuotaRefresh`
- `(*pool.Pool).QuotaSnapshot() pool.QuotaSnapshot`
- Authenticated `GET /v1/quota`, `POST /admin/api/quota/refresh`, and
  `GET /admin/api/overview`.

## 3. Contracts

The read-only collector calls the signed `GET /snap-manager/v1/statistics/plugin`
with `x-auth-token` included before signing, and reads `usageTotalPackageCredit`.
Other metadata requests keep their existing authentication headers. The collector
never refreshes or persists credentials,
claims benefits, changes account health, or runs on a quota GET. Start it with
the service context; limit collection to two concurrent requests, eight seconds
per call, thirty seconds per batch, and at least thirty seconds between batch
starts. Periodic refresh runs every five minutes. Only a successful measurement
at most fifteen minutes old, with a still-valid credential, can contribute to a
complete balance. Dispatch only when a worker is available; automatically
continue interrupted or unstarted targets in later batches and retain their
previous cache entries. A true call failure is recorded once per sweep rather
than retried indefinitely. A very large, slow account pool may take longer than
the freshness window to scan. New accounts and credential changes are captured
on manual refresh or the next periodic sweep. If a representative credential
is rejected during a sweep, a duplicate's usable credential is selected on a
subsequent sweep; it is not retried immediately in the current sweep.

Snapshot aggregation deduplicates identical nonempty account UIDs, preferring a
complete, unexpired credential not known to have an authentication error, and
inspects at most 512 account slots. Different UIDs may share one provider package, so
the sum can overlap. If any target is missing, expired, failed, stale or beyond
the limit, `complete=false` and total balance fields remain null; a separate
`known_remaining` may describe the successful subset. An empty account pool is
unknown, not a zero balance. Copy pointers before returning cached values.
Only publish a result while the account and credential generation still match.

The overview returns `quota` with state, coverage, nullable totals, freshness and
refresh status; the panel clearly labels partial results, refresh throttling and
excludes benefits.
`GET /v1/quota` returns the raw cache in credits without account identities.
An upstream total of `-1` denotes an unlimited package. A complete snapshot with
any unlimited package sets `unlimited=true`, leaves aggregate total/remaining
null, and displays unlimited in the overview. Other negative numbers are not
finite credit amounts. A partial snapshot cannot assert an unlimited aggregate.
Its optional `credits_per_usd` is an explicitly supplied positive finite number.
With a complete finite snapshot it returns `object=credit_summary`, numeric
`total_available=remaining/credits_per_usd`, `unit=USD`, the source credits and
coverage metadata. No default or official exchange rate is implied. New API's
advanced-custom balance route can forward to this URL; standard OpenAI channels
do not use that advanced-custom balance route.

## 4. Validation & Error Matrix

| Condition | Result |
|---|---|
| Known upstream zero | Zero is a valid measured value |
| Missing metric, expired credential, failed or stale collection | Incomplete snapshot; never fabricate a zero balance |
| Complete snapshot contains an unlimited package | Overview reports unlimited; converted New API balance returns 503 rather than a fabricated finite amount |
| Invalid, repeated, zero, negative, nonfinite or overflowing conversion rate | HTTP 400 |
| Requested converted balance with incomplete or stale snapshot | HTTP 503 `quota_unavailable` |
| Old account or replaced credentials finish a request | Discard the result |
| Batch deadline interrupts a target | Preserve its prior cache and resume it later |
| Duplicate UID with an unusable first credential | Prefer a usable account representative |
| Repeated refresh or concurrent clients | Join/throttle the bounded background job |
| Missing or invalid service key | HTTP 401 |

## 5. Good / Base / Bad Cases

- Good: a complete measured balance is displayed with its last update time.
- Base: a failed account leaves aggregate totals unknown and labels the known subset.
- Bad: count healthy accounts as credits, label missing upstream values as zero,
  invent a currency rate, or query the provider once per external balance request.

## 6. Tests Required

Use synthetic signed upstream responses and actual registered HTTP routes. Check
known zero, unlimited and partial/failure/stale/empty, UID deduplication, account/credential
replacement, multi-batch completion, bounded workers, shutdown, refresh throttling, authentication,
conversion, cache-only GET and no secret leakage. Real-browser checks cover
desktop/mobile overview, manual refresh, loading, unlimited, partial, zero and error states.
Document whether any real provider validation has actually been performed.

## 7. Wrong vs Correct

Wrong: return zero to New API when one account has no current measurement.

Correct: return HTTP 503 for the converted balance until all included accounts
have a current successful measurement, and expose partial coverage separately.
