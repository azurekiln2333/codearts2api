# Proposed local commit batch

The user approved this batch on 2026-10-21 ("提交"). Implementation, full-scope Pi
review, regression verification, and spec sync are complete. Execute the explicit
work paths below followed by scoped workflow bookkeeping. No push or deployment
is authorized by this commit approval.

## 1. Work commit

`fix: bound chat lifecycle and concurrent account admission`

Product code, regression tests, and operator documentation:

- `.env.example`
- `README.md`
- `cmd/server/config.go`
- `cmd/server/config_test.go`
- `cmd/server/main.go`
- `config.example.json`
- `internal/pool/admission.go`
- `internal/pool/admission_test.go`
- `internal/pool/checkin.go`
- `internal/pool/pool.go`
- `internal/pool/refresh.go`
- `internal/pool/refresh_test.go`
- `internal/scheduler/scheduler.go`
- `internal/server/affinity_lifecycle_test.go`
- `internal/server/availability.go`
- `internal/server/availability_test.go`
- `internal/server/chat.go`
- `internal/server/concurrency_test.go`
- `internal/server/handler.go`
- `internal/server/lifecycle.go`
- `internal/server/lifecycle_refresh_wait_test.go`
- `internal/server/lifecycle_test.go`
- `internal/server/lifecycle_timeouts_test.go`
- `internal/server/pick_account_test.go`
- `internal/server/probe_ownership_test.go`
- `internal/upstream/client.go`
- `internal/upstream/lifecycle_test.go`
- `internal/upstream/sse.go`
- `internal/upstream/stream_body.go`

Executable spec and this task's own planning/review evidence:

- `.trellis/spec/backend/index.md` (the existing untracked index was extended only for this lifecycle contract)
- `.trellis/spec/backend/request-lifecycle.md`
- `.trellis/tasks/10-21-deepseek-timeout/task.json`
- `.trellis/tasks/10-21-deepseek-timeout/prd.md`
- `.trellis/tasks/10-21-deepseek-timeout/design.md`
- `.trellis/tasks/10-21-deepseek-timeout/implement.md`
- `.trellis/tasks/10-21-deepseek-timeout/implement.jsonl`
- `.trellis/tasks/10-21-deepseek-timeout/check.jsonl`
- `.trellis/tasks/10-21-deepseek-timeout/commit-plan.md`
- `.trellis/tasks/10-21-deepseek-timeout/research/contracts.md`
- `.trellis/tasks/10-21-deepseek-timeout/research/findings.md`
- `.trellis/tasks/10-21-deepseek-timeout/research/herdr-check-brief.md`
- `.trellis/tasks/10-21-deepseek-timeout/research/pi-review.md`
- `.trellis/tasks/10-21-deepseek-timeout/research/repro/main.go`
- `.trellis/tasks/10-21-deepseek-timeout/research/verification.md`

Stage these explicit paths only, never the entire `.trellis/` or working tree.

## 2. Workflow bookkeeping after the work commit

Use finish-work to archive only this completed task and record this session in
the developer journal. Its separate archive/journal commits follow the work
commit. Do not archive the pre-existing bootstrap-guidelines task.

## Excluded pre-existing untracked scaffolding

- `.agents/`
- `.claude/`
- `.codex/`
- `.cursor/`
- `.gitattributes`
- `.pi/`
- `AGENTS.md`
- All other `.trellis/` paths, except the exact spec/task paths above and the
  session/archive bookkeeping explicitly described in step 2.

No credentials, local toolchain/compiler, production configuration, or temporary
Linux artifact are included.
