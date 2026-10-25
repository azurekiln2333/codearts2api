# R19 integrated delivery — 2026-10-26

User-requested reference: `D:/My2KProjects/cnb2api-panel`. Four existing Pi panes (`w1C:p4`, `p7`, `p6`, `pA`) implemented disjoint Compose, CI, packaging and multilingual documentation/review responsibilities through Herdr. Their delivery reports preserve snapshots; this report records final integrated state.

## Result

Default deployment pulls the published GHCR image and starts project-scoped Watchtower (600-second default). The local-build overlay builds a separate local image and disables updater eligibility. Runtime optional settings use `.env`; one deployment port controls listener/publishing/healthcheck. Original credential/config/data mount identities remain intact.

GitHub and CNB validate main/PR without publishing; validated version tags gate source Release, checksum and linux/amd64+arm64 images. Stable tags additionally update latest; prereleases do not. One shared helper validates image/version metadata and external registry authentication. Retired duplicate GitHub CI is removed and excluded from explicit committed-tree export. No workflow/script wildcard inclusion.

Three full synchronized READMEs (Simplified, Traditional, English) and deployment guide cover migration, release visibility, pull/update, private updater authentication, opt-out, local builds, CNB CI overrides and rollback.

## Verification

- Full Go normal tests, race tests, vet and build: PASS (Go1.27.3; recovered local GCC compiler for race).
- CGO0 linux/amd64 and linux/arm64: all four shipped binaries compile, ELF headers checked.
- Python suites: metadata40 + release17 + deployment28 =85 PASS.
- Official Compose v2.29.7: synthetic default/custom port21508/default local overlay/custom local overlay all PASS. No real `.env` consumed.
- actionlint v1.7.12: sole final GitHub workflow PASS (shellcheck/pyflakes unavailable and disabled).
- Git Bash syntax: all52 final workflow script blocks PASS.
- git diff --check PASS. Eleven protected preexisting inputs match baseline; extracted-package checks follow in separate source-release report.

## Boundaries

No Docker daemon is available: images were not built/run locally, and runtime healthcheck/updater were not observed. No remote GitHub/CNB execution, push/tag/Release/image publication or server deployment was performed. First trusted version-tag publication and public GHCR visibility (or private credentials) are prerequisites for default pull. Existing R13 concurrency investigation remains open; this delivery does not assert production traffic recovery.

Final Trellis check reviewer independently reran all85 Python tests, actionlint and diff check; no blockers. Clarified that Go cross-compilation needs no simulation while final Alpine RUN needs native/emulated target execution.
