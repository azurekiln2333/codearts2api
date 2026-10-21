# Independent Pi planning review

Reviewer: existing project Pi at Herdr w1C:p4, cwd D:/My2KProjects/codearts2api. The user explicitly requested this delegation. Herdr reported working after dispatch and idle with completion_seq 201 after the final research turn. Pi changed only its assigned child research files; product implementation was not activated.

## Accepted findings
- Live model-error HTTP text matches pre-3d87a5c paths; exact production image/commit remains unknown.
- The current three-rotation bound can falsely report model unavailability before a later eligible account is tried. Pi reproduced this with isolated model IDs: three attempted accounts produce 404, while five attempted accounts reach the only capable account and produce 200.
- With 18 loopback requests and three of four accounts cooling, a silent upstream plus scaled budgets yields 13 queued HTTP 429 at about 501–502 ms and five HTTP 504 at about 801 ms; no slots remain held.
- Retry-sleeper requests finish within scaled budgets. Existing ticket-saturation tests, rather than this below-capacity wave, establish the ticket ceiling.
- Current error presentation/persistence lacks useful request causes and success recovery can leave stale lastErr / persisted errCount. A bounded metadata audit and explicit current-vs-history presentation address this without changing existing cooldown policy.

## Review corrections applied
The parent required and Pi incorporated two evidence corrections: non-streaming aggregation of an actively progressing generation may legitimately exceed startup without downstream headers, so the 125-second abort alone proves neither old deployment nor silent-I/O failure; 64/70/79-second completions do not establish retry cadence. Pi added an active-generation control. The parent also corrected the silent-upstream harness to actually cool three accounts and isolated model IDs to avoid cross-run availability-cache contamination.

## Final technical decisions
The parent final scope in plan-review.md takes precedence over earlier exploratory alternatives. Include a distinct queue_wait_timeout outcome for waiting-slot expiry; keep actual transport status separate. Show the safe provider code alongside its fixed human-readable description in the authenticated audit view, and show an explicit unknown-build label when identity is unavailable. Preserve the enabled-account admission-cap contract and existing automatic/manual cooldown policy. The parent final plan still needs the user's explicit implementation approval.

## Verification boundaries
Pi research uses real localhost HTTP handlers and fake credentials, not production traffic. It proves bounded resource behavior in those exact scenarios, not provider/New API throughput. Product code and production state remain unchanged by this new planning task. The separate directly authorized Compose change passed actual Docker Compose config validation for defaults, port 21508/listener synchronization, unset/false/0 forwarding, existing credential path, example config, shell precedence and missing-key rejection.

Reports: ../10-21-production-multimodel-timeouts/research/pi-findings.md and ../10-21-request-audit-account-health/research/pi-findings.md. All three tasks remain planning.
