# R19 source release verification — 2026-10-26

Source: `32f33db3460b239ee1f4a581c2c4cd1fee762287`

Artifact: `codearts2api-source-32f33db3460b.zip`

SHA256: `2e47db401bfcd67684c3fa206fa3625539de5b9a25d4bb3da8135b24b1616848`

Actual140-member archive: member hashes and private path/value exclusions passed. All three complete READMEs, sole GitHub workflow, CNB pipeline, build overlay, helper/tests and deployment guide included; old go.yml absent.

Extracted server compiled/started with isolated empty auth and synthetic audit. All seven GET routes verified expected revision; token replay/API passed. Extracted Python suites40+17+28=85, actionlint and official Compose four-scenario matrix passed. Server stopped. Original credential baseline, config/env and prior archives preserved. Private evidence: `.local-test/results/r19-release-32f33db3460b-final.json`.

No product changes after source commit. No Docker engine build/run, remote workflow execution/publication or server deployment. First trusted tag and registry access required before pull. Parent R13 remains open; R19 local source delivery complete.
