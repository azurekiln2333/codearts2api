# Proposed final scope for user review

## Goal
Make simultaneous New API model tests bounded and explainable on the verified intended build.

## P0: deployment and model routing
Verify image/build and effective request limits. Preserve the existing lifecycle repair; fix the independently reproduced false-negative when three capability-rejected accounts consume MaxRotate before another eligible account is tried. Keep general provider-error retry bounds, finite tried candidates, startup/idle budgets and owned resources. Recommend concurrency 1 for initial operational verification, preserving explicit config until the operator changes it.

## P1: audit and account diagnostics
Add authenticated recent-request audit with request ID, model, selected account, HTTP/outcome, classified provider cause, attempts and timing. Metadata only: no prompts, generated bodies, service keys or provider credentials. Defaults: 7 days, 64 MiB total across at most eight 8-MiB files including active, 256 queued events, 512 recent events restored after restart. The panel filters/pages that recent window; older retained files are the archive. Queue/disk failure cannot block chat, and loss is visible.

Separate current schedulability from failure history. Clear/persist obsolete current error state when a real success follows errors, preserving active cooldown/manual disable. Do not change automatic expiry or manual Enable/ClearCooldown policies. Display unknown historical timestamps honestly.

## Acceptance
Meaningful local real-handler checks cover 18 concurrent mixed-model requests, partial cooling, silent upstreams, retries, cancellation and later-account capability failover. Audit checks cover correlation/auth, secret absence, stable recent-window paging, restart restoration, rotation/limits, overflow/storage failure and state compatibility. Live success is claimed only after the intended running build and effective config are verified and a bounded re-test is authorized.

## Boundaries and risk
No credential migration/reset, distributed scheduler, full-content archive, per-end-user identity claim, implicit push/deployment or unbounded live testing. Active upstream generation may outlast startup; a 125-second non-streaming wait alone does not prove a timeout bug. Production logs, exact image and New API config remain unavailable. The old error templates strongly suggest an older deployed request path, without identifying an exact commit.

## Artifacts and phase
Parent and both children have PRD/design/implementation proposals plus validated implement/check manifests and sanitized live evidence. Pi independently reviews research through Herdr. All three tasks remain planning; this final scope requires explicit subsequent approval before task activation and product implementation.
