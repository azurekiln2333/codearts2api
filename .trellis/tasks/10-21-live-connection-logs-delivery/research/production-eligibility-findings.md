# Production eligibility report, 2026-10-23

## Scope and evidence

The user reported HTTP 503 `no_healthy_account` and authorized investigation of
the previously supplied production gateway. Two bounded, authenticated GET-only
observations were taken at 11:34:36 UTC and 11:36:28 UTC on 2026-10-23
(19:34:36 and 19:36:28 Asia/Shanghai). The observations performed no inference,
refresh, claim, account mutation or deployment. API keys, account identities,
credentials and raw provider bodies are excluded from this report.

Sanitized local snapshots are retained outside Git:

- `.local-test/results/production-eligibility-get-1792755279507037900.json`
- `.local-test/results/production-eligibility-get-1792755392617900800.json`

Both observations received HTTP 200 from health, overview and request-history
GETs. The second additionally received HTTP 200 from the model snapshot.

## Observed request and account state

The first history snapshot contains a recent failed request with:

| Field | Observed value |
|---|---|
| Requested model | `glm-5.3-flash` |
| HTTP status / outcome | 503 / `no_healthy_account` |
| Attempts / tried account count | 0 / 0 |
| Total / queue / wait milliseconds | 0 / 0 / 0 |
| Recorded cause class | `none` |

Three configured accounts were enabled, outside cooldown and at error count 0;
one account was disabled with historical authentication failure metadata. The
effective per-account maximum was 5. During the second snapshot, one account
had one active request from other traffic; this investigation sent none.
Historical `last_failure_class=auth` on an enabled account does not establish a
current authentication failure. The second snapshot explicitly marked the three
enabled accounts eligible.

Their agent and builtin source snapshots were fresh successful pulls; each
account's benefit source was explicitly disabled. Model summaries reported:

| Model | Origin | Catalog accounts | Benefit accounts | Blocked accounts |
|---|---|---:|---:|---:|
| `glm-5.3` | discovered | 3 | 0 | 0 |
| `deepseek-v4.1-flash` | discovered | 3 | 0 | 0 |
| `glm-5.3-flash` | static | 0 | 0 | 0 |

No explicit target-model negative exclusions were present in that snapshot.
The second recent-history window no longer contained the first snapshot's 503;
the first retained observation is the evidence for that request. These are
sequential snapshots, not an atomic capture of the request's execution.

## Source defect and supported conclusion

`chatCandidates` already excludes an otherwise healthy account when its
captured-login model catalog is complete/current and lacks the requested model.
The terminal `modelKnownUnusable` check originally examined only explicit
negative capability records. Complete-current catalog exclusions therefore
left zero candidates without establishing model-unavailable, causing the generic
503 fallback. This is a concrete source defect independent of production.

The zero-attempt `glm-5.3-flash` failure and adjacent healthy/catalog observations
strongly match that defect. They do not establish a provider outage, a request
stalled for 120 seconds, all credentials being invalid, or exhausted concurrency.
Catalog presence itself is not proof of a successful model completion.

`glm-5.3-flash` and `glm-5.3` are distinct requested models. The repair must not
silently substitute one for the other or manufacture entitlement. A static
fallback name does not prove any account supports it.

## Deployment boundary

The running overview identified the full production source revision as
`709f63ede82f8f790a853a4550f9430a7e1b2cfc`, with `known=true`. That revision predates
the verified local identity/session repair `72e83fbed57d862d85e4d20b8b96f7a44e41019d`
and this classification follow-up. No production rebuild, container replacement,
push or deployment was performed during this investigation.

The prior bounded local successful model/concurrency measurements belong to the
tested local source, not this older running production revision. The previously
unassigned historical R13 stalls remain open.

## Required regression boundary

Actual chat HTTP regressions must return 404 `model_not_available` with capability
audit metadata and zero upstream sends when all otherwise healthy accounts are
excluded by current authoritative model knowledge, including a mix of explicit
denials and catalog absence. The cached public model list must use the same
knowledge and omit unsupported static fallback entries.

Empty, disabled, cooling and unusable-auth pools retain 503. Unknown, incomplete,
retained-stale, expired and previous-login catalog absence must not manufacture a
model denial. Preserve positive capability override, exact-wire/auth ownership,
429 busy/wait outcomes and 504 deadline outcomes. Verification and source release
results are recorded separately after the repaired source is reviewed.

## Local correction and verification

The repair extracts `cachedAccountModelEligibility` from the existing candidate
truth table and reuses it in `modelKnownUnusable`, which also filters the cached
public list. No retry, entitlement, provider protocol or credential policy is
changed. The pre-fix actual-HTTP regression failed both complete-catalog and
mixed-catalog/negative cases with 503, zero attempts and continued public
advertisement. Repaired source passes all twelve actual-HTTP subcases in
`internal/server/eligibility_error_test.go` and correlated audit assertions.

Parent integration commands all passed on 2026-10-23:

- `go test ./...`
- `go test -race ./...` with the local GCC toolchain
- `go vet ./...`
- `go build ./...`
- `git diff --check`

The toolchain was Go 1.27.3 on Windows; source continues to declare Go 1.22.
The independent reviewer additionally checked existing exact-wire/auth,
capability rotation, concurrency and upstream TTL knowledge regressions and
found no blocking issue. Retained-source stale evidence is tested through real
HTTP; TTL-expiry completeness is covered by the existing upstream test, not an
invented server HTTP clock fixture. The new cached-list assertion calls the
side-effect-free list projection; chat itself traverses the registered actual
HTTP route. Source packaging verification and production deployment remain
separate from these local checks.
