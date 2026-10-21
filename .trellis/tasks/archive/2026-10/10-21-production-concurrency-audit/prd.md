# Production concurrency and request audit

## Goal
Bound simultaneous New API model tests and let an operator explain individual failures from the existing panel.

## Authorization and evidence
The user explicitly authorized live endpoint testing, Pi optimization and repairs, then reported stale account error badges and missing audit. Following independent Pi review, the latest request asks why implementation has not started. Implementation under research/plan-review.md is authorized and active. The production key is absent from task artifacts and was cleared from diagnostic memory.

On 2026-10-21 an 18-request wave across 15 advertised model IDs plus three repeats yielded four HTTP 200 completions, four model-specific HTTP 404 failures and ten client aborts at 125 seconds without response headers. Health remained HTTP 200 in 233–1864 ms. Initially 3/4 accounts were cooling and every account had explicit max_concurrent=5. A stale/mixed deployment is strongly suggested by old response templates, but build identity is unverified. See research/findings.md and research/live-reproduction.json.

## Task map and requirements
- P0 child: ../10-21-production-multimodel-timeouts verifies deployed build/effective limits and request recovery before changing the repaired scheduler.
- P1 child: ../10-21-request-audit-account-health adds bounded metadata audit, authenticated panel access and truthful account error presentation.
- Preserve the existing lifecycle/lease/cancellation contracts, auth format and persistent mounts.
- Separate proven failures, deployment/configuration issues and hypotheses. Passing localhost tests do not prove production throughput.

## Acceptance Criteria
- Verified intended build and explicit effective config precede any production-success claim.
- Mixed-model overload fails explicitly within configured budgets; cancellation frees capacity without account penalty.
- Audit explains selected account, model, status, upstream cause, attempts and timing without recording keys, credentials, prompts or response bodies.
- Audit storage/queue failures cannot block chat or hold pool locks/leases.
- Both children have reviewable plans, scoped ownership, meaningful checks and independent review.

## Boundaries
No credential reset, blanket re-login, administrator enable/disable/clear-cooldown action, distributed scheduler, unbounded live load or implicit push/deployment. Production logs, exact image and New API config remain unavailable through the supplied HTTP API. Preserve the existing uncommitted Compose/.env.example/README changes.
