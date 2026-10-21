# Scoped local commit plan

The user previously requested local commits and has explicitly authorized creating
this task and continuing implementation. No renewed permission is required for
the same session's local commit/verification/archival. Do not push or deploy.

1. `feat: redesign admin panel with New API inspired workspace`
   - `internal/server/panel.html`
   - `internal/server/panel.go`
   - `.trellis/spec/backend/index.md`
   - `.trellis/spec/backend/admin-panel.md`
2. Archive only this task with its PRD/design/context, reports, synthetic browser
   harness and reviewed screenshots, in a separate bookkeeping commit.
3. Record the developer journal with the work commit's actual hash.

Leave all unrelated pre-existing platform/Trellis/bootstrap files unstaged.
