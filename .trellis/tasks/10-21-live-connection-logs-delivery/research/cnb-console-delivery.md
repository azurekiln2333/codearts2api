# R18 reference console delivery (2026-10-25)

## Authorized scope and boundary

Reproduce the local cnb2api-panel login, actual connecting stage and full console
layout in the existing self-contained Go-embedded panel. Keep all six operational
pages, stored API key, proxy authentication, audit/token and OAuth contracts.
Reference project remains read-only. Synthetic local APIs only; no production
credentials or traffic. Original config, .env, credentials and archives protected
by a private hash baseline. Whole parent task remains in progress for R13.

## Research

- cnb-reference-findings.md: exact reference composition, tokens and local render.
- cnb-migration-findings.md: operational IDs, async ownership and verification.

## Verification status

Implemented the split login and gateway illustration, real connecting state,
52px header/180px sidebar/64px collapse and all six page styles. No additional
assets/dependencies/routes or persistence changes. Initial overview validation
is cancellable and bounded at15s; no-key startup still attempts proxy auth and
expected401 returns silently, while an entered invalid key produces an error.
Independent review removed login implementation details/decorative footer.

Reference13 screenshots: `.local-test/cnb-reference-20261025/`. Reference
synthetic services were confirmed stopped (ports19220/19221 refused). Parent
inspected actual reference and new login/boot/overview/accounts/mobile/dark
images; structure, color, whitespace and compact content match the reference
with CodeArts-specific operations and truthful metrics.

Independent review: `cnb-quality-review.md`; final panel SHA256
`d3f9ac3a35460962ac92199a35c0f1cb4cea7c9f209e580f1b360440fa139f47`.
Go server tests and build passed after final source edits. Go vet ./... and
15 release-tool tests passed. This presentation-only change did not rerun live
model/concurrency stress tests and does not close historical R13.
Final browser source identity and committed-source export checks follow below.


## Final working-source browser proof

Four suites passed against the final panel with SHA256 matching independent
review before/after execution:
- focused: `.local-test/cnb-ui-1792926649134/`
- full console: `.local-test/oauth-full-ui-1792926673062/`
- OAuth: `.local-test/oauth-ui-1792926696099/`
- token: `.local-test/token-ui-1792926718457/`

See `cnb-browser-verification.md` for assertions and commands. Existing behavior
assertions remain; only obsolete login-card selector and extracted-asset/output
overrides were adapted. Final15s timeout uses real browser cancellation; delayed
responses cannot authorize after failure. Original10 protected inputs/archives
match the preservation baseline. Commit and actual archive evidence are recorded
separately after immutable source export.


## Immutable source delivery

Source `6623d00818abe770d94344515e787475dfeee535`, archive
`codearts2api-source-6623d00818ab.zip`, verified with actual extracted asset and
running isolated binary. See `cnb-console-source-release.md` for complete hashes,
line-ending identity proof, browser evidence, preservation and deployment boundary.
