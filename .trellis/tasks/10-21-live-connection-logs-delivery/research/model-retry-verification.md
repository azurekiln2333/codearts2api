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
also passed for the artifact recorded below.

Independent Trellis review covered all product diffs and all three new test
files. No blocking or nonblocking defect remained, and no product edits followed
the integrated checks. Final source release verification passed as recorded below.
The parent reviewed all explicit owned text for service/private-key
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

## Verified source release

- Product commit: `b9b318ccc026426f593af09cc66eaea62cd19479`.
- Artifact: `codearts2api-source-b9b318ccc026.zip`.
- SHA256: `9a9c7b0430b7ce976ef495cefb1718fdd52dfacf06518de6d848c073ff5227b7`.
- 110 ZIP members, including 109 hashed product/generated identity members.

The exporter read the committed product manifest. The parent checked actual ZIP
membership: Compose/Dockerfile/examples and all new regression files are present;
real env/config/auth/data, platform/task files and previous ZIPs are excluded.
`verify-source-release.py` checked member hashes, extracted the final ZIP into a
temporary directory, compiled its server with the actual source commit, started
with empty isolated auth/data and ran the verifier from inside that extraction.
Health, cleaned console, build identity, requests, audit, models and connections
all passed. No production chat, refresh, claim, deployment or push occurred.

All earlier archives were verified byte-for-byte unchanged:

- `codearts2api.zip`: `7934b2e7827f5f4a685b9f2e367385752b4eb9aec063eea75b563f87c601a84c`.
- `codearts2api-source-365f4828d019.zip`: `eeeeeed3037704008fda0ff015f8667ecca1275a72d68bc35b9994feb040ff7e`.
- `codearts2api-source-709f63ede82f.zip`: `fd0be2f5910418c7144936d5f196d2fa98ba8743ee7ffe3b5bc0f54bcef9cbb6`.

Local code/release acceptance is complete. Retain the task in progress for
operational deployment verification and real account/model evidence. Use the
packaged `release.env` and existing mounts when rebuilding; this change requires
no credential/configuration/data migration.
