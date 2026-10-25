# R19 packaging / source-export / deployment-contract delivery (p6)

Date: 2026-10-26. Owner: `scripts/release-source.py`,
`scripts/test_release_tools.py`, `scripts/test_deployment_contracts.py` (new).
Read-only references: `.local-test/r19-pi-briefs/packaging.md`,
`.trellis/spec/backend/source-release.md`, design/PRD/implement R19, the other
workers' delivered files. Nothing committed, pushed, tagged or deployed.

## 1. Files owned and delivered

| File | Change |
|---|---|
| `scripts/release-source.py` | Explicit manifest extended; workflow wildcard removed |
| `scripts/test_release_tools.py` | Fixture and assertions updated for the explicit manifest |
| `scripts/test_deployment_contracts.py` | New: 27 Compose / workflow / CNB / README contracts |

Not touched: `README*`, `docs/*`, `docker-compose*.yml`, `.env.example`,
`Dockerfile`, `.dockerignore`, `.github/workflows/*`, `.cnb.yml`,
`scripts/release-metadata.py`, `scripts/test_release_metadata.py`,
`scripts/verify-deployment.py`, real `.env` / `config.json` / `auths/` / `data/`,
old ZIPs, `.pi` settings.

## 2. Explicit source manifest

`REQUIRED_FILES` now enumerates the release inputs, including:

- `.github/workflows/build-and-release.yml` is the **only** workflow input. The
  retired `.github/workflows/go.yml` is **not** required and is **not archived**
  even while it still exists in the tree; **no `WORKFLOW_ROOT` wildcard exists**
  — an extra workflow such as `.github/workflows/extra-release.yml` or
  `anything.yaml` is never product input even when tracked. One workflow owns
  push / PR / manual dispatch / `v*` tags.
- `docker-compose.yml`, `docker-compose.build.yml`, `.cnb.yml`, `Dockerfile`,
  `.dockerignore`, `.env.example`, `config.example.json`.
- `README.md`, `README.zh-TW.md`, `README.en.md` (all three languages).
- `scripts/release-source.py`, `scripts/release-metadata.py`,
  `scripts/verify-deployment.py`, `scripts/test_release_tools.py`,
  `scripts/test_release_metadata.py`, `scripts/test_deployment_contracts.py`.
- Unchanged rules: `cmd/**/*.go`, `internal/**/*.go`, `docs/**/*.md`
  (so `docs/deployment.md` ships), `go.sum`/`.gitignore` optional.

Refuses to open up `scripts/` or `.github/` by prefix; real `.env`,
`config.json`, `auths/`, `data/`, `.git/`, `.trellis/`, platform dirs and ZIPs
stay excluded even if tracked. Export remains deterministic (stored members,
commit timestamp, fixed modes), refuses a non-`.zip` output and preserves an
existing target byte-for-byte.

## 3. Verification evidence (local)

| Command | Result |
|---|---|
| `python -B scripts/test_release_tools.py` | 17 tests OK |
| `python -B scripts/test_release_metadata.py` (p7 helper) | 40 tests OK (measured; earlier runs saw 32) |
| `python -B scripts/test_deployment_contracts.py` | 28 tests OK |
| `actionlint` v1.7.12 on `build-and-release.yml` | deferred until p7 finishes the workflow dedupe |
| real-content export harness (throwaway temp Git repo) | 140 members, deterministic, only `build-and-release.yml` under `.github/` |

Real-content export harness (copies the actual worktree product files into a
temporary Git repo, commits, runs `release-source.create_release` twice): 140
members, byte-identical outputs (deterministic), only
`.github/workflows/build-and-release.yml` under `.github/` (the retired `go.yml`
is excluded even though it is still present), `README.md`/`README.zh-TW.md`/
`README.en.md`, `docs/deployment.md` present, no
`.env`/`config.json`/`auths/`/`data/`/`.trellis/` leakage, `release.json`
SHA-256 hashes valid, existing output refused. No credentials were read or
printed; only synthetic/temporary repos were used.

Re-run after the final sources landed (base updater de-profiled, CNB
`build-base` + `CGO_ENABLED=1`): still 141 members and deterministic; the
archive's `.cnb.yml` contains `build-base` and `CGO_ENABLED=1`, both workflow
files are present, `docs/deployment.md` ships, and `release.json` keeps
`format: 1` with a full 40-char source commit.

Contract-test logic was additionally mutation-tested with a throwaway harness:
a correct synthetic fixture passes (21 cases) and 18 deliberate violations are
each rejected (single port broken; default build; updater self-enable; base
updater profile-gated; overlay target enabled; overlay profile removed; PR
publish; missing test dependency; inline `:latest`; helper swapped; missing
manual dispatch; CNB PR login/push; race without a C toolchain; race without
`CGO_ENABLED=1`; non-Go runtime; image publish without the docker service;
missing language; missing disable documentation).

### Deployment contracts encoded (`test_deployment_contracts.py`)

- **Default Compose** (`docker-compose.yml`): image-only (no `build:`),
  `ghcr.io/azurekiln2333/codearts2api:latest` overridable via `CA2A_IMAGE`,
  one `CA2A_PORT` driving host port, container port and `CA2A_LISTEN`, required
  `CA2A_API_KEY` guard, `env_file: .env`, `auths`/`data`/`config.json:ro`
  mounts, `json-file` rotation; updater runs by default (no `profiles`) with
  `enable=true` + project scope, self `enable=false`, pinned
  `nickfedor/watchtower:1.23.2`, configurable interval, socket mount; no
  credential literals.
- **Local-build overlay** (`docker-compose.build.yml`): local image +
  `pull_policy: build` + `BUILD_COMMIT`/`BUILD_TIME` args, app label
  `enable=false`, updater gated by `profiles: [auto-update]`.
- **GitHub workflow** (`build-and-release.yml`, the only one): triggers on
  branch pushes, `v*` tags, `pull_request` and `workflow_dispatch`;
  `pull_request` never gets write;
  version-tag publishing (`if: refs/tags/v`), publish jobs `needs` the
  validation job, `contents: write` / `packages: write` scoped to publish jobs,
  shared `scripts/release-metadata.py` + `scripts/release-source.py`,
  `.sha256` verified with `sha256sum --check`, `github.sha` +
  `CA2A_BUILD_COMMIT` + `CA2A_BUILD_TIME` (UTC), no inline `:latest` tag push
  (latest is helper-owned), helper installs `pyyaml` for these tests.
- **CNB** (`.cnb.yml`): `main`/`pull_request` validate only (no login/push/
  release/`CNB_TOKEN` in the PR subtree); `v*` publishes checksummed source +
  image; **runtime kinds** — Go jobs use a Go image, Python release jobs install
  `python3`/`py3-yaml`, the image job has the `docker` service; **race
  toolchain** — every `-race` pipeline installs `build-base`/gcc/musl-dev and
  sets `CGO_ENABLED=1`; the image pipeline runs `go test` and the shared helper
  so it cannot bypass the gate; image path from `CNB_REPO_SLUG_LOWERCASE` /
  `CNB_IMAGE` / `CA2A_IMAGE`; no credential literals.
- **README languages + docs**: the updater enablement description is checked
  across `README.md`, `README.zh-TW.md`, `README.en.md`, `.env.example` and
  `docs/deployment.md`; each language README links the other two, uses
  `docker compose pull` + `docker compose up -d`, names `CA2A_PORT`/`CA2A_IMAGE`
  and the default GHCR image, with no credential literals.

## 4. Findings for the coordinator

1. **README standard-command still stale (pA).** `README.md`
   (`README.zh-TW.md`, `README.en.md` identical wording) still presents
   `docker compose --profile auto-update up -d` as the standard deployment and
   says plain `docker compose up -d` will not start the updater, because the
   updater is "profile-gated". The final `docker-compose.yml` has **no**
   profile, so plain `docker compose up -d` does start Watchtower. Only the
   local-build overlay keeps `profiles: [auto-update]`. `.env.example` already
   documents the final default-on model. The README commands need the same
   correction: standard = `docker compose up -d` (app + updater); opt-out =
   `docker compose up -d codearts2api` / `docker compose stop watchtower &&
   docker compose rm -f watchtower`; profile only for the local-build overlay.
   `docs/deployment.md` should be checked for the same wording.
2. **`test_deployment_contracts.py` requires PyYAML as a test-only dependency.**
   The GitHub `test` job already installs it (`python -m pip install pyyaml`)
   and the CNB pipeline installs `py3-yaml`; the shipped product never imports
   it. Keep that in mind if the CI steps are reshaped.
3. **CNB race toolchain is now satisfied**: `prepare-tools` installs
   `git build-base python3 py3-yaml` and `go-race` runs
   `CGO_ENABLED=1 go test -race ./...`; the new assertions pass.
   `test_release_metadata.py` now reports **40** tests locally (earlier runs saw
   32) — reporting the measured number.
4. **Retired `go.yml`**: p7 still has to delete `.github/workflows/go.yml`; the
   manifest and tests already treat it as non-product, so it is excluded from
   exports today. `actionlint` is re-run after p7's final workflow dedupe.
5. **No Docker engine/CLI and no registry access here.** Compose rendering,
   image build, container run, push and the remote tag pipelines are
   **unverified locally** and must be reported as pending by whoever runs them.
   No claim is made that any image, release or workflow exists remotely.
   (The parent reported the local Go normal/race/vet/build and 8 ELF
   cross-compiles as passing.)

## 5. Honest boundary

Local only: exporter unit tests, contract tests, mutation harness, real-content
export harness. `actionlint` is deferred to after p7's workflow dedupe. No
commit, no push, no tag, no image, no release, no production traffic. The parent
owns the final commit, spec updates and integration; pA owns the remaining
README command correction.