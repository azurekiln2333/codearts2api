# Eligibility classification independent quality review

Date: 2026-10-23, Asia/Shanghai.

Scope: R14 in `10-21-live-connection-logs-delivery`. Reviewed the stable integrated
diff from `2600f75`, including the new untracked
`internal/server/eligibility_error_test.go`, task requirements/design/check
context, lifecycle/model-discovery/audit/release specifications, README changes
and the sanitized production report. Existing integrated session/discovery
verification remains prior evidence rather than a new live test.

## Findings (fixed)

No issue required a reviewer source or test change. The implementer corrected the
demonstrated classification defect in `handler.go` and factored the existing
candidate predicate in `lifecycle.go` before review. This reviewer wrote only
this report.

## Reviewed behavior

- `cachedAccountModelEligibility` preserves the prior candidate truth table:
  captured-auth applicable negative capability excludes; a valid usable result
  overrides catalog absence; present catalog entries retain selection priority;
  absent entries exclude only when the catalog is complete/current. The helper
  adds no network, persistence, new cache or provider permission assumption.
- `modelKnownUnusable` now shares this predicate and requires at least one
  otherwise healthy account. Consequently the existing terminal chat branch
  returns 404 `model_not_available` for authoritative catalog exclusions instead
  of reaching generic 503. Empty/disabled/cooling pools retain 503. Validation
  failure without a usable refresh credential still reaches the existing 503
  path without fabricating an inference attempt.
- The same final predicate filters cached public model entries, including static
  fallback names. The distinct `glm-5.3-flash` absence test retains discovered
  `glm-5.3` and `deepseek-v4.1-flash`; there is no alias or model substitution.
- Account/auth and exact-wire availability guards remain at their existing
  boundaries. The helper uses the existing atomic membership/completeness read,
  does not retain pool/catalog locks across I/O, and keeps health separate from
  model capability. Request budgets, lease ownership, retries, busy backoff,
  session cleanup and public/admin route side effects are unchanged.
- Twelve new actual localhost HTTP subcases assert correlated response/audit
  metadata: two complete/mixed exclusion cases, four unavailable-pool cases,
  five nonauthoritative/usable-override successes, and one partial-catalog case
  preserving a real cached provider denial. Zero-attempt exclusions require no
  selected account, first-data time or fabricated provider code. Real accepted
  calls preserve the requested exact ID and return content.

The cached-list assertion calls `modelList()` directly to prove cache-only
filtering, alongside existing public-route coverage. New HTTP tests cover unknown,
partial, retained-after-failure and replacement-login absence; TTL expiration is
covered by existing upstream `TestModelKnowledgeRequiresCompleteCurrentDiscovery`,
not by a newly clock-advanced HTTP fixture. This distinction is retained in specs.

## Findings (not fixed)

No remaining concrete source-level blocker was found in this follow-up.

Production evidence consists of sequential GET snapshots and an adjacent recent
request record, not an atomic capture of that request's account state. The
reported zero-attempt `glm-5.3-flash` 503 strongly matches the repaired branch; it
does not prove that every 503 or historical stall has this cause. The running
revision reported by production was `709f63e`, predating both the earlier local
`72e83fb` repair and this change. No production deployment or model request was
performed by this reviewer.

Historical R13 stall attribution and the earlier reasoning-only answer remain
open. Neither these tests nor the classification correction close whole-incident
AC13, prove provider capacity or establish production rollout. No panel asset or
release-tool implementation changed in this follow-up, so previous browser/tool
acceptance was not repeated by this reviewer. New archive verification belongs
to the parent after committing the reviewed product source.

## Verification

Independent reviewer checks with the provided Go 1.27.3 toolchain:

| Check | Result |
|---|---|
| Four new R14 test functions, `-count=1 -v` | PASS, all 12 actual-HTTP subcases; server 0.135 s |
| Existing capability rotation/generic bound, cold identity, wire/login ownership, concurrency policy and upstream completeness/TTL selection | PASS; server 0.196 s, upstream 0.045 s |
| `go vet ./internal/server ./internal/upstream` | PASS |
| `gofmt -l` on both changed Go files and new test | PASS; empty output |
| `git diff --check` | PASS; no whitespace finding |
| Explicit untracked test `git diff --no-index --check` | No whitespace finding; ordinary new-file difference exit 1 |

The parent separately reported the final integrated checks against the same
frozen product source. These were not rerun independently by this reviewer:

| Parent integration check | Reported result |
|---|---|
| Full `go test ./...` | PASS; server 15.726 s, upstream 9.328 s; unchanged audit package reused its passing cache |
| Full `go test -race ./...` | PASS; server 11.948 s, upstream 21.734 s, pool 20.351 s |
| Full `go vet ./...` | PASS |
| Full `go build ./...` | PASS |

Type checking is provided by compiling the focused tests and the parent's full
Go checks. There is no separate configured TypeScript/linter stage for this Go
backend diff. No credentials, live inference, account mutations, commits,
archives or deployment were accessed or changed by this reviewer.
