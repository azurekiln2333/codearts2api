# R18 committed source release verification

Date: 2026-10-25. Source commit: `6623d00818abe770d94344515e787475dfeee535`.
Artifact: `codearts2api-source-6623d00818ab.zip`. Members:131.
SHA256: `a83cf4da12ca0c4619b6e955b77b4b20e2a1ffbde4bf0d50f5cab6916ceebb86`.

## Actual archive verification

- Explicit committed manifest, all131 member identities/hashes, duplicate/path
  checks and private credential-value exclusions passed. No .env/config/auth/data
  or AI tooling included. Existing10 protected inputs/archives were unchanged.
- Extracted to `.local-test/cnb-console-release-gnqt0a5o/source`.
- Compiled actual extracted Go server with its full commit identity; started
  only127.0.0.1 with empty isolated auth, synthetic audit replay and disabled jobs.
- GET-only health/console/build/requests/audit/models/connections verifier passed.
  No chat, model refresh, account mutation or production request sent.
- Actual embedded token browser passed against the running extracted binary,
  including replay/API totals and unknown/source semantics.
- Focused reference/login/loading suite, full console suite, OAuth suite and
  token fixture suite all passed using extracted UI_PANEL_SOURCE. Evidence dirs
  are `D:\My2KProjects\codearts2api\.local-test\cnb-console-release-gnqt0a5o/cnb-browser-check`, `/browser-check`, `/oauth-ui` and
  `/token-browser-check`. Parent inspected actual packaged login desktop/mobile
  and populated overview images.
- Owned test server stopped and temporary config removed.

## Source identity

Committed/extracted panel SHA256:
`f427767533e2ce8684fa988afd7d6ee15dc6ed594481e0170c3ae6b7cf22716e`.
Windows working-panel SHA256:
`d3f9ac3a35460962ac92199a35c0f1cb4cea7c9f209e580f1b360440fa139f47`.
The only byte difference is1183CRLF versus LF line endings: normalized equality
and exact archive-versus-committed-blob equality were independently asserted.
All packaged browser checks used actual LF release bytes, not the worktree.

Machine result: `.local-test/results/cnb-console-release-6623d00818ab-final.json`.

## Deployment boundary

Source delivery is complete. No push or production deployment occurred; Docker
image runtime is unverified. Rebuild/recreate the service to replace its embedded
HTML, preserving existing .env and auth/data/config mounts. A restart alone does
not replace an old binary. Whole parent task remains in progress with its overall
quality gate false for historical R13; this visual release does not close it.
