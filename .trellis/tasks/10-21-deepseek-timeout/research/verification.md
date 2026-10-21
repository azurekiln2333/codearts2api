# Verification record

Date: 2026-10-21. Branch: `fix/deepseek-concurrency-20261021`.

## Before product changes

- Original full Go test suite passed.
- Local loopback/fail-on-extra-read reproductions failed for completed SSE reads,
  stream preflight timeout, cancellation/health isolation, and busy-first routing.
- Implementation workers confirmed initial targeted regressions failed against
  the original code before fixing them.

## Implementation validation

Temporary Go executable:
`D:\AppData\Local\Temp\codearts2api-toolchain-go1.27.3\go\bin\go.exe`.

The server implement worker ran and passed:

```text
go test -count=1 -timeout 120s ./...
go vet ./...
go build ./...
```

Parent independently ran the full race suite and passed:

```powershell
$taskCompilerBin = 'D:\AppData\Local\Temp\codearts2api-race-compiler\mingw64\bin'
$env:PATH = "$taskCompilerBin;$env:PATH"
$env:CC = Join-Path $taskCompilerBin 'gcc.exe'
$env:CGO_ENABLED = '1'
& 'D:\AppData\Local\Temp\codearts2api-toolchain-go1.27.3\go\bin\go.exe' test -race -count=1 -timeout 120s ./...
```

All test packages passed, including cmd/login, cmd/server, internal/pool,
internal/scheduler, internal/server, and internal/upstream. Other commands and
internal/auth have no tests. `git diff --check` also passed with the repository's
normal Git line-ending settings. Forcing `core.autocrlf=false` on this Windows
checkout produces CRLF-only noise and is not an appropriate content check.

Parent also built `./cmd/server` for Linux amd64 with `CGO_ENABLED=0`, writing the
artifact outside the repository at `%TEMP%\codearts2api-verified-linux-amd64`.
This validates compilation only; it is not a deployment or production test.

The parent reran the original diagnostic harness after implementation. All six
aggregate/stream terminal variants returned successfully without an extra read;
the 25ms preflight now returned `IdleTimeoutError`; an already-canceled request
returned promptly with healthy=1/cooling=0; the busy-first/free-second scenario
completed in approximately 1ms using the free account. The same harness produced
the blocked/health-penalty output recorded in `findings.md` before the changes.

## Meaningful concurrent boundary assertions

- Pool: three accounts with six slots; eight waiting callers; twelve excess
  callers rejected immediately. Cancellation drains waiters and capacity is reused.
- Pool lifecycle admission: with one active slot plus two pending tickets, exactly
  three of 32 simultaneous callers can enter, including during retry sleeps.
- Actual HTTP: two handlers share one pool, two accounts with one slot each, and
  two pending tickets. Twenty-four excess requests each receive 429 plus
  Retry-After within the test's 500ms bound. `/healthz` stays below 300ms. After
  cancellation, twelve further successful requests divide evenly between accounts.
- Rapid429 waves: three admitted retry sleepers hold all one-plus-two tickets;
  twenty later requests are rejected without contacting the mock upstream.
- Silent preflight and later idle reads terminate, both response modes finish on
  protocol markers without EOF, and a continuing 165ms stream survives startup
  50ms / upstream idle 75ms settings.
- Cancellation, failed reacquisition, slow downstream writes, a partial HTTP body,
  credential-refresh waiting, and runtime account reload have focused regressions.

These are deterministic local invariants using fake credentials and loopback HTTP,
not an estimate of production throughput or provider quota.

## Independent Pi review

The user explicitly requested using Herdr to direct an existing Pi in this
project's workspace. HERDR_ENV was verified as 1. Agent inventory identified Pi
`w1C:p4` at the project directory; it was idle and then observed working after
submission. The exact handoff is in `herdr-check-brief.md`.

The initial prompt's 45s lifecycle wait expired while Pi continued reviewing;
the prompt was not resubmitted. An incremental coordination message reported
completed validation and the parent's new executable lifecycle spec.

Pi completed full-scope AC1-AC9 review and fixed one additional P1 defect:
`probeModel` still released account capacity by name, so deleting and re-adding an
account while a probe was active could decrement a real request's replacement
account slot. It now holds an owned lease and verifies account identity. The new
`TestProbeLeaseSurvivesAccountRemoveAndReAdd` failed with the old code restored and
passed with the fix. Scratch probes were removed; permanent tests are in
`internal/server/probe_ownership_test.go` and `lifecycle_refresh_wait_test.go`.

The latter test adds actual handler evidence for startup timeout during shared
credential refresh: 70ms startup budget produces 504 promptly, no upstream chat,
released account lease/lifecycle ticket, and unchanged account health.

Pi reported passing full tests, vet, build, Linux compilation, race tests for all
four directly changed test packages, and three repeated runs of the new targeted
tests. Parent then reran and passed full `go test -race -count=1 -timeout 120s
./...`, `go vet ./...`, `go build ./...`, `git diff --check`, and task context
validation with the review changes included. No remaining acceptance blocker was
found. Detailed boundaries and deferred production verification remain in the
PRD/design and executable lifecycle spec.

## Deployment credential compatibility check

The user asked whether upgrading affects already-added credentials. Source
inspection confirms `internal/auth/auth.go` and its JSON schema, filename rules,
load/save behavior, default auth directory, and deployment volume mappings are
unchanged. Existing credentials load without migration. The old refresh path
already required the saved DPoP private key, so this is not a newly imposed
re-login requirement. The new shared refresh retains the original OAuth ClientID
and safely persists rotated credentials through the existing atomic save path.

Stock `docker-compose.yml` binds host `./auths` to `/app/auths`, `./data` to
`/app/data`, and the existing `./config.json` read-only to `/app/config.json`.
Upgrading with the same directories/mounts preserves accounts. Native/systemd
deployments must likewise keep their existing auth_dir and data/config paths.
No real credential files were opened or modified during this check. The user's
production deployment/mounts were not inspected, so preservation is conditional
on keeping those existing persistent paths. A service restart interrupts active
requests, and existing explicit config values remain in effect after upgrading.
