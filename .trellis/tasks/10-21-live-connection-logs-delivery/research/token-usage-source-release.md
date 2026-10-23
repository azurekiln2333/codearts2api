# Token usage source-release verification

Date: 2026-10-24. Product commit: `4f0ceff298243db75b88d340a7efa1f86a281593`.

Artifact: `codearts2api-source-4f0ceff29824.zip`; SHA256:
`fdc45ef16c889cb0d8b9d973ca99c72fd9ea5ccd73c68c5efcf5bc2709394a43`.

Exported the reviewed committed product manifest into a new archive. All 131
member names and manifest SHA256 values passed verification. Private auth/data/
config/tooling paths and actual credential values were excluded. Existing user
and previous source ZIPs were not replaced.

The actual archive was extracted and compiled with Go 1.27.3 and its exact source
revision injected, then started on an isolated loopback port with empty auth,
temporary data/config and a generated local key. The packaged GET-only verifier
passed health, console, build revision, requests, audit, models and connections.
No model inference or credential refresh was sent.

Seeded three synthetic persisted audit records: upstream10/20/30,
estimated1/2/3 and a legacy unknown. Actual replay/API assertions passed:

- Summary input11/output22/total33, two known and one unknown request;
  one upstream and one estimated source.
- Configured page.window512 remains distinct from usage.window3;
  account-filtered requests2 still report usage.window3 and total33.
- Pagination leaves the same summary; audit endpoint matches requests and
  completed connections retain all three records.

Real Chrome against the extracted embedded panel verified those actual API
values, per-row source/unknown labels, account/model groups, desktop/mobile local
scrolling and no external requests or page errors. Desktop and mobile screenshots
were opened and visually inspected. The first mobile screenshot captured the
existing sidebar transition; the final check waits for the closed drawer before
capture and passes with an unobstructed panel. No product fix was needed.

Final screenshots: `.local-test/token-release-mq55rg06/`.
Raw result: `.local-test/results/token-release-4f0ceff29824-final.json`.
Both full archive runs passed compilation/start, route/API and browser checks.
Owned servers were stopped and their temporary key-bearing configs removed.
Original credentials matched the recorded baseline; root config/env and user/
prior source archives remained byte-for-byte unchanged.

No Docker runtime test, push or production deployment occurred. Update the
deployment source before rebuilding/recreating with existing env/config/auth/data
mounts. Token summaries cover the retained recent window; old events stay unknown
and estimates stay labeled. R15 is locally delivered; historical R13 causes and
production rollout remain open, so the parent task stays in progress.
