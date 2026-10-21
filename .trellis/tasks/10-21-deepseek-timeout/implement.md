# Implementation and validation plan

## Planning Gate

- [x] User authorized task creation and diagnosis/fixing.
- [x] Inspect the actual HTTP chat path, account pool, transport/preflight/SSE, config, and existing tests.
- [x] Reproduce terminal reads, ineffective stream timeout, canceled admission/health penalty, and busy-first-account blocking locally.
- [x] Run the unchanged Go test suite successfully.
- [x] Write converged PRD, design, and curated implement/check context manifests.
- [x] Present final planning summary and receive a subsequent explicit implementation approval. User explicitly requested concurrent multi-user scheduling/overload protection in that approval; R6/AC9 capture it.
- [x] Run `task.py start` only after that approval. Active status is `in_progress` on `fix/deepseek-concurrency-20261021`.

## Ordered Work

1. Add focused regressions for the reproduced cases using live mock HTTP streams or fail-on-extra-read readers; verify they fail against the original implementation. Include error-before-JSON-DONE and trailing usage coverage.
2. Correct shared terminal classification in SSE streaming and aggregation without stopping prematurely on finish_reason.
3. Add effective, configurable startup/inactivity/write limits and context propagation through client construction, initial/runtime pool accounts, preflight, retries, and refresh waiting.
4. Replace long contextless admission with bounded cancellable acquisition, try free eligible accounts first, and preserve model capability and affinity semantics without synchronous unbounded discovery.
5. Make body/slot/timer cleanup explicit and owned. Add regressions for failed reacquisition, cancellation in each wait phase, no post-cancel rotation, and subsequent successful requests.
6. Exclude caller cancellation/downstream failure from account punishment; preserve genuine provider error policies and existing named-tool/usage behavior.
7. Align example/README concurrency and timeout defaults, provide configuration migration notes, and add concise useful timings.
8. Have the Trellis check agent review the whole diff against acceptance criteria and fix findings. Capture the lifecycle contracts in the project spec after verification.

The user also explicitly requires multi-user overload protection: implement the bounded global admission queue, fair atomic slot allocation, idempotent leases surviving reload, and a meaningful concurrent saturation/cancellation test as part of steps 4-5.

## Validation

Temporary native Go executable:

```powershell
$taskGo = 'D:\AppData\Local\Temp\codearts2api-toolchain-go1.27.3\go\bin\go.exe'
& $taskGo test -timeout 90s ./internal/upstream ./internal/pool ./internal/server ./cmd/server
& $taskGo test -timeout 120s ./...
& $taskGo vet ./...
& $taskGo build ./...
& $taskGo test -race -timeout 120s ./...
```

Use targeted failing regressions before fixes and run a single full verification after the final code changes. The native system initially had no C compiler on PATH and WSL Ubuntu cannot mount its disk. A temporary compiler is now available at `D:\AppData\Local\Temp\codearts2api-race-compiler\mingw64\bin\gcc.exe`; prepend that directory to the checking process PATH and set `CC` to its executable and `CGO_ENABLED=1` for the race suite. The native race smoke test passed. Keep language/library usage compatible with the module's Go 1.22 declaration.

Observe that a completed protocol stream returns even when upstream remains open, a silent stream times out, an actively producing stream survives multiple idle intervals, cancellation releases slots without changing health, another eligible account proceeds while the first is busy, and `/healthz` remains responsive under saturation. All boundary servers use fake credentials and local loopback addresses.

## Ownership and Dispatch

After approval/start, dispatch `trellis-implement` for the focused production code, regression tests, and user documentation above. Its prompt must begin with the active task path, declare it is already the implement agent, and prohibit recursive implement/check delegation. A separate `trellis-check` performs full-scope review after implementation. Parent coordinates approval, review context, spec updates, and completion. Do not change unrelated user files or the pre-existing untracked Trellis/platform scaffolding.

Implementation is split among three native workers with shared contracts: upstream lifecycle, pool admission/refresh, and server/config/documentation. Pool ownership includes the minimal `checkin.go` and `scheduler.go` changes required for context-aware refresh and safe auth snapshots. Server ownership includes minimal snapshot reads in availability/discovery/login paths. These are necessary integrations of the same account reload/refresh contract; no background feature redesign is included.

Integration tests also exposed two fairness traps. Independent first-turn requests with the same message must not acquire derived sticky affinity; only explicit affinity or actual assistant/tool history enables reuse. A successful unknown-catalog account must not gain permanent priority from positive availability alone, or it can monopolize subsequent first-turn requests. Preserve cached catalog priority and negative model availability, with equal-priority allocation remaining fair.

## Execution status

- [x] Implement upstream terminal/inactivity lifecycle and regression coverage.
- [x] Implement bounded fair admission, owned leases, refresh cancellation, and runtime timeout propagation.
- [x] Integrate server startup/write budgets, retry cleanup, health classification, model routing, and configuration/docs.
- [x] User-requested Herdr delegation: existing project Pi `w1C:p4` performed the complete trellis-check review. It corrected the remaining background-probe name-based release and added handler-level refresh-wait coverage; no remaining AC1-AC9 blocker.
- [x] Parent reran the original diagnostic harness successfully and passed full race/tests, vet, build, Linux server compilation, diff check, and task context validation. See `research/verification.md`.
- [x] Capture executable contracts in `.trellis/spec/backend/request-lifecycle.md` and connect the backend index.
- [x] Present the concrete one-shot commit plan and receive Phase 3.4 approval. The user explicitly replied "提交" on 2026-10-21; commit the approved paths without another approval request.
- [ ] Commit only the approved paths, then archive/record the task through finish-work.

## Rollback Points

No schema, credential, or production migration is planned. Keep the change reviewable as one lifecycle fix. Revert only task-owned edits if a regression requires rollback; preserve unrelated files and existing account state. Before any commit, inspect the diff and obtain the repository-required concrete commit-plan approval.
