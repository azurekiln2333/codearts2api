# Verify production build and bound multi-model requests

## Goal
Prevent 120-second opaque waits during simultaneous model tests and establish which build and limits are actually running.

## Evidence
Parent research records 10/18 diagnostic requests without headers at the 125-second client deadline. Four succeeded and four returned model errors; health stayed responsive. Initially one account was schedulable and max_concurrent=5. Old error templates suggest an earlier deployment, not a confirmed new scheduler bug.

## Requirements
- P0-R1: Verify build identity and effective limits before attributing failures to repaired code. Provide credential-preserving update, verification and rollback instructions.
- P0-R2: Preserve bounded lifecycle tickets, fair available-account selection, cumulative waits, owned leases, refresh, SSE completion and neutral cancellation.
- P0-R3: Mixed-model/capability/cooling waves finish or fail explicitly within configured budgets; retry waits do not silently multiply into minutes and canceled capacity is reusable.
- P0-R4: Coordinate safe build/effective-config diagnostics with the audit child. Honor explicit settings; recommend concurrency 1 for initial operational verification without silently replacing user overrides.
- P0-R5: Fix the independently reproduced capability-failover false negative: the default three generic rotations must not prevent trying a fourth/fifth eligible account after model-specific capability rejections. Use a finite tried set and the existing startup budget; preserve the generic provider-failure retry bound. Do not claim every account lacks a model when eligible accounts were never tried.

## Acceptance Criteria
- Meaningful real-handler localhost checks cover at least 18 mixed-model requests, partial cooling, aliases, capability errors, cancellation and retry sleepers.
- A verified repaired build has no legacy 180-second acquisition; deployment instructions identify actual running image/commit/config.
- Capability/benefit errors do not fail the whole account; documented auth/quota/5xx/timeout/cancellation classification stays correct.
- A fresh, cache-isolated five-account regression succeeds when only the final eligible account can serve the model, and all-unavailable wording is truthful.
- Existing auth format and persistent mounts remain compatible.

## Boundaries
Implementation authorized under the reviewed parent scope. No additional Pi production calls, administrator mutations, distributed coordination, implicit deployment or unsupported throughput claims. Ownership is in the parent's research/implementation-brief.md.
