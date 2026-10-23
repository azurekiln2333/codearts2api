# OAuth / Compose source-release verification

Date: 2026-10-24. Product commit: `7a9bc5b8755ab30d203311995a17f14907f487c2`.

Artifact: `codearts2api-source-7a9bc5b8755a.zip`; SHA256:
`1a11e5b313dfc55540076ff6bc2744f3f2c06c8ea8b6530e974761449cd441ae`.

## Integrated evidence

Parent full Go 1.27.3 normal/race suites, vet and build passed after final backend
integration: server normal7.476s/race8.908s, audit0.768s/1.843s. Subsequent changes
were HTML-only helper styling and input-warning refinement; the final embedded
build, JavaScript syntax and dedicated Chrome suites passed afterward.

The UI worker's broad console and token Chrome regression passed, followed by
the final OAuth suite. Independent review repeated focused normal/race Go,
cmd/server tests, vet/build/format, the synthetic official Compose-parser matrix,
15 release-tool tests and the final OAuth Chrome suite, with no remaining blocker.
See `oauth-compose-quality-review.md` and the three implementation reports.

## Actual committed artifact

Exported committed product blobs into a new archive without replacing existing
ZIPs. All131 members and manifest hashes passed, alongside private-path and actual
credential-value exclusions. The packaged Compose text matches the source tested
by the isolated synthetic Compose-parser matrix.

Extracted the actual ZIP, compiled its server with the exact revision injected,
and started it on an isolated loopback port with empty auth, temporary config/data
and a generated key. The packaged GET-only verifier passed health, console,
revision, requests, audit, models and connections. Three synthetic persisted
audit events also passed usage replay/filter/paging/API checks and actual
embedded desktop/mobile token rendering.

The final OAuth Chrome fixture read the panel asset from this extraction through
`UI_PANEL_SOURCE`. It passed single-primary labels and clipboard contents,
import-done with deleted session and one refresh, direct detection, continuation/
second callback, queued poll-submit and completion winner, duplicates,
validation/transport/expiry/retry, callback-feedback privacy, close/logout races,
explicit-only popups, proxy prefix, desktop/mobile/short scroll and keyboard focus.
No external requests or page errors occurred. Parent opened the actual extracted
desktop pasted and mobile continuation screenshots and confirmed their layout.

Scratch/screenshots: `.local-test/oauth-compose-release-v76qmyzr/`.
Result: `.local-test/results/oauth-compose-release-7a9bc5b8755a-final.json`.
Original credentials matched the recorded baseline; root env/config, user ZIPs
and all protected earlier source ZIPs remained unchanged. Owned servers were
stopped and temporary key-bearing configs removed.

## Delivery boundary

No real provider OAuth, model inference, Docker-image runtime, push or production
deployment was performed. Remote native-loopback callbacks still require manual
import unless the browser can reach the gateway callback; polling cannot fetch a
failed browser address. The source fixes the genuine completion and port bugs,
not that network boundary. Update production source before rebuilding/recreating
with existing .env/config/auth/data mounts. R16/R17 are locally delivered;
historical R13 causes and production rollout remain open, so the parent task is
not archived and its whole-task quality flag remains false.
