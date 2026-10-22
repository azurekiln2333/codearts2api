# Bug analysis: model visibility and operational diagnostics

## 1. Root cause category

- Cross-layer contract (B): source failure was effectively treated as absence,
  losing account catalog/benefit evidence and disagreeing with cached selection.
- Implicit assumption (E): a discovered model was confused with verified
  callability, and an empty filtered list fell back to the original entries.
- Test coverage gap (D): mixed-source failure/removal, concurrent negative writes,
  same-UID login replacement and active-request phases lacked boundary coverage.

## 2. Why earlier fixes did not settle the report

The earlier concurrency and panel tasks did not make discovery provenance or
in-progress requests visible. Refreshing overview was distinct from pulling model
sources, and completed request audit could not identify a currently blocked phase.
The user's rebuild resolved an earlier deployment/UI mismatch, but it did not
prove the new discovery behavior or the exact current provider model response.

Independent review also reproduced a casing mismatch in fresh-route reset and a
claim-failure display mismatch. Admin overview inherited model-list side effects,
so a GET-based diagnostic could trigger discovery, claims or real model probes.
Cold partial discovery also treated an unlisted model as authoritative absence:
the real route returned 503 without a single upstream chat attempt. The fixed
selection reads membership and completeness atomically, leaves partial/expired
absence unknown and retains actual provider denials. Mobile browser checks found
document-level vertical overflow; the shell now stays within the dynamic viewport
and the workspace/table own their necessary scrolling.

## 3. Prevention mechanisms

| Priority | Mechanism | Action | Status |
|---|---|---|---|
| P0 | Source ownership | Retain independently on failure; authoritative empty removes | Implemented |
| P0 | Bounded work | One 60s job, two account workers, source budgets and start interval | Implemented |
| P0 | Identity and revision | Exact login publication/read guard; monotonic negative guard | Implemented |
| P0 | Integration tests | Real source/admin/chat routes, stalled requests and replacement login | Implemented |
| P1 | Observability | Source/exclusion diagnostics plus bounded live phase history | Implemented |
| P1 | Deployment evidence | Commit-based release and actual revision/feature verifier | Implemented |

## 4. Systematic expansion

Use the same case-insensitive key for merging and fresh-route proof while retaining
exact inference IDs. Treat cached GETs and operations as different contracts,
including indirect call paths. Preserve the real request classifier and cleanup
ownership when adding observability. No provider alias or account entitlement can
be inferred solely from static seeds or synthetic fixture success.
Build and run the final extracted source, then call its actual routes: synthetic
release tests initially mirrored an incorrect audit-statistics envelope and all
passed. Actual release smoke caught the mismatch; separate validators, real-shape
fixtures and a malformed/disabled regression now guard that boundary.

## 5. Knowledge capture

Updated backend model-discovery, request-audit, admin-panel and source-release
specs/index. This repository has no generated product template tree to sync.
Production redeployment and the exact live V4.1 Flash source response remain
operational checks; local fixtures do not establish those results.
