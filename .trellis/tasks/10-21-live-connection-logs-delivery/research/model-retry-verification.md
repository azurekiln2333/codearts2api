# Model identity and concurrency follow-up verification, 2026-10-22

## Confirmed defects and implemented boundaries

Account selection was account-specific, but outbound chat and probes could use a
union-wide casing index. Within an account, merged display metadata could also
replace the exact ID of the benefit source governing the route. Static alias
denials were cached under a different identity from subsequent target lookups.

The resolver now captures the selected account/auth and reads its exact ID and
benefit route together. Actual outbound chat and probes preserve that ID without
a second global rewrite. Candidate checks, negative-cache keys and final audit
canonical identity agree with actual sending. Public request/response names and
existing static aliases remain compatible; unknown names and replacement auth
cannot borrow another account's dynamic registration or model entitlement.

Explicit concurrency refusals previously retried the current account before
considering a backup. HTTP429/first-SSE and late-SSE versions could also cool or
increment errors for an otherwise healthy account. The fix prefers an untried
eligible account within the existing generic rotation budget and makes explicit
concurrency health-neutral across those wrappers. Ordinary429/TPM and actual
authentication failures retain their policies. Late SSE never regenerates.

The default three-account always-busy sequence is A/B/CCC, five actual calls,
instead of AAA/BBB/CCC, nine calls. A single account or exhausted rotation budget
keeps bounded same-account retries. This is not a universal total-attempt limit
or a promise of additional upstream capacity.

## Executed regression evidence

The two-account wire test was copied into a temporary extraction of the prior
verified 709f63e release. Old source fails with actual HTTP404 after two attempts;
current source succeeds and asserts the exact account-specific IDs plus actual
last-sent audit name. The detailed reproduction is in `model-case-findings.md`.

Real localhost chat/probe tests cover lowercase/mixed names, GLM-5.3 fixtures,
empty/default/auto, static aliases, unknown foreign-listed names, benefit/normal
source casing conflicts, confirmed closure and replaced auth. Concurrent catalog
publication is race-tested. Twelve concurrency scenarios assert actual call
order, rotation/queue limits, HTTP400/429/first and late SSE, ordinary429/TPM,
audit attempts/tried/account/cause, Retry-After, lease cleanup and complete
lifecycle-admission capacity reuse.

Final integrated checks passed on the complete current product implementation:

- `go test ./... -count=1`
- `go test -race ./... -count=1`
- `go vet ./...`
- `go build ./...`
- `gofmt -l` for every changed/new Go file (no output)
- `git diff --check`
- Both task context manifests parse and reference existing files.

Tools: temporary Go 1.27.3 and MinGW GCC. Source remains Go 1.22 compatible;
no separate Go 1.22 or Docker runtime check is claimed. The panel and release
scripts are unchanged this round; their previously passed browser/export unit
suites remain historical evidence. Actual final source extraction/build/routes
will be checked again for this artifact.

Independent Trellis review covered all product diffs and all three new test
files. No blocking or nonblocking defect remained, and no product edits followed
the integrated checks. Final source release verification is pending at this
checkpoint. The parent reviewed all explicit owned text for service/private-key
patterns before staging; unrelated bootstrap/platform files and archives are
excluded from the commit.

## Prevention and operational limits

The root defect was conflating a union display index with a per-account wire
identity. Earlier case normalization and source-aware routing did not test their
composition on actual outbound requests. New HTTP regressions and the discovery/
audit specs require a single account/auth/source identity through payload,
capability cache and diagnostics. Concurrency health now shares one explicit
classifier with audit; lifecycle specs preserve bounded rotation and late-stream
termination without treating all HTTP429 responses alike.

No production deployment, live account gate/entitlement, real GLM/V4.1 completion
or production load-test success is asserted. Prior bounded remote GETs received
HTTP403; this round uses localhost fixtures only. Task remains operationally open.
Rebuild/recreate the deployment from the verified source using the existing
`.env`, `config.json`, `auths/`, `data/` and mounted paths, then pull the catalog
and inspect actual per-account source results. A real provider `not registered`
can still mean the account has no authorization after casing is corrected.
