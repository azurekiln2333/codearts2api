# R19 docs delivery (role: docs + independent review, pane w1C:pA)

Date: 2026-10-26. Owner: README.md / README.zh-TW.md / README.en.md /
docs/deployment.md / research/r19-reference-findings.md / this note /
research/r19-quality-review.md (final independent review runs after handoff).

No commit, push, tag, publish, deploy or registry operation was performed.
No `.env`, `config.json`, `auths/`, `data/` or existing ZIP was modified.
Workspace note: root env `HERDR_WORKSPACE_ID` is stale; the live workspace is
`w1C` and this pane is `w1C:pA` (the four-Pi split p4/p6/p7/pA).

## Files written

| File | Content |
|---|---|
| `README.md` | Simplified Chinese, full product docs + deployment/release docs |
| `README.zh-TW.md` | Traditional Chinese, same structure and completeness |
| `README.en.md` | English, same structure and completeness |
| `docs/deployment.md` | Extended operations guide (Simplified Chinese) |
| `research/r19-reference-findings.md` | Reference + official-source findings |

All three READMEs are **self-contained and mutually linked**; none merely redirects to
another. Verified structural parity: each has 8 H2, 18 H3 and 42 code fences, and the same
deployment tokens. The existing feature documentation (multi-turn context / `X-Codearts-Chat-Id`,
streaming `include_usage` and token usage, request audit and connection logs, model catalog /
benefit / `model_not_available`, tool calling, timeouts, systemd) is preserved in all three
languages — the deployment section was **added**, not substituted for the product docs.

## Deployment contract documented (reconciled with p4's actual files)

Source of truth: `docker-compose.yml`, `docker-compose.build.yml`, `.env.example`,
`Dockerfile`, and p4's `research/r19-compose-interface.md`.

- Default image `ghcr.io/azurekiln2333/codearts2api:latest` (actual origin), `CA2A_IMAGE`
  override; single `CA2A_PORT` drives host port, container port and derived `CA2A_LISTEN`;
  `env_file: .env` direct read; preserved mounts `./auths`, `./data`, `./config.json:ro`;
  `init: true`, `restart`/container-name interpolation, json-file rotation 10m x 3.
- **Standard deployment enables automatic updates** (final interface after p4 removed the
  profile): `docker compose up -d` starts the application **and** Watchtower.
- **Explicit opt-out**: `docker compose up -d codearts2api` starts only the application;
  disabling an already-running updater uses `docker compose stop watchtower` +
  `docker compose rm -f watchtower`. Documented caveat (from p4): starting only the
  application does not stop an already-running updater.
  (Earlier draft, now superseded: the standard command was
  `docker compose --profile auto-update up -d` while the updater was profile-gated.)
- Updater: pinned `nickfedor/watchtower:1.23.2` (maintained fork; `containrrr/watchtower`
  archived), label **and** scope isolation
  (`com.centurylinklabs.watchtower.enable="true"` + `.scope=${COMPOSE_PROJECT_NAME:-codearts2api}`),
  own label `enable="false"` (never self-updates), Docker socket mount,
  `WATCHTOWER_POLL_INTERVAL` (default 600 s), fixed `WATCHTOWER_CLEANUP="true"`,
  `CA2A_WATCHTOWER_CONTAINER_NAME`.
- Local build overlay: `docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`,
  `CA2A_LOCAL_IMAGE` (default `codearts2api:local`), `pull_policy: build`, updater not started
  and local images never replaced; build identity via `CA2A_BUILD_COMMIT`/`CA2A_BUILD_TIME`.
- Multi-arch: documented `docker buildx build --platform linux/amd64,linux/arm64` with
  `BUILD_COMMIT`/`BUILD_TIME`, CGO-disabled cross-compile and OCI labels.
- First-release boundary: the image does not exist before the first `v*` tag publishes;
  `pull` fails until then; GHCR packages are private on first push (`read:packages` login or
  make public); use the local build in the meantime. No claim that any image exists.
- `v*` tag semantics: test gate → GHCR image → source Release (ZIP + sha256); stable updates
  `latest`, prerelease never does; PR validates only with minimal permissions.
- GHCR/CNB switch: `CA2A_IMAGE`, and the dynamic `CNB_REPO_SLUG_LOWERCASE` placeholder (no
  invented slug). CNB login uses `printf '%s' "$CNB_TOKEN" | docker login ... -u cnb
  --password-stdin` so the token is never printed or stored in shell history.
- Rollback by pinned tag or digest while keeping the same mounts.

## Verification performed (local, no engine, no secrets)

- `python -m unittest scripts.test_deployment_contracts` → **27 tests, OK** after p6 expanded
  the suite; `scripts.test_release_metadata` + `scripts.test_release_tools` → **55 tests, OK**.
  The docs satisfy `DefaultComposeTests.test_updater_enablement_matches_documentation` in its
  current (profile-less) branch: every documented entrypoint names the updater and shows how to
  disable it. `ReadmeLanguageTests` (three READMEs cross-link and share key commands/default
  image) and the manifest tests (three READMEs + `.cnb.yml` + build overlay required) also pass.
- Independent Compose rendering with the pinned Compose v2.29.7 binary in
  `.local-test/tools/` and a **synthetic** `.env` in a temp directory (real `.env` never read):
  - default: `image=ghcr.io/azurekiln2333/codearts2api:latest`, no `build:`, `init: true`,
    `7866:7866`, `CA2A_LISTEN=:7866`, three mounts (`config.json` read-only), target labels
    `enable=true` + scope, log rotation; `config --services` = `codearts2api`, `watchtower`
    (plain `up -d` starts both).
  - overlay: `image=codearts2api:local`, `pull_policy=build`, Dockerfile, empty build args,
    target label `enable=false`, mounts/port preserved, `config --services` = `codearts2api`.
- Structural parity + feature-token parity check across the three READMEs (8/18/1/44 and all
  feature anchors present in each).
- Traditional Chinese reviewed for Simplified leakage: only the intentional `简体中文`
  switcher label remains.
- Official sources re-verified by fetch: Watchtower container-selection / scheduling /
  update-behavior docs; Compose spec `pull_policy`; GHCR container-registry docs; CNB
  `llms.txt` → `build/build-in-env.md`, `artifact/docker.md`,
  `internal-steps/git/release.md`; Docker Hub confirms `nickfedor/watchtower:1.23.2`.

Not verified (and not claimed): no image pull, registry login, `compose up`, container run,
GitHub/CNB workflow run, or production traffic.

## Deviation to flag to the parent

> **Superseded.** p4 has since removed the profile from the default Compose, so the standard
deployment is a plain `docker compose up -d` (application + updater) and the opt-out is
`docker compose up -d codearts2api`. The docs were re-synced to that interface; only the
local-build overlay keeps `profiles: [auto-update]`. The paragraph below records the earlier
state for history.

`design.md` R19 says automatic updating is "enabled in standard deployment ... with explicit
documented disabling", while p4 implemented opt-in `profiles: [auto-update]`. To satisfy both
the user's latest instruction and p4's actual interface, the docs now make the **standard
deployment command** `docker compose --profile auto-update up -d` (updates enabled) and
document `docker compose up -d` as the explicit opt-out. The Compose default remains
profile-gated; docs and Compose therefore agree, but the wording differs from design.md.
If the parent wants Compose itself to enable the updater by default, p4 must drop the profile
and the docs' opt-out command becomes `--scale watchtower=0`/`stop`+`rm` instead.

## Late revision: updater authentication for private registries

Found during the integrated review as a cross-stream gap, not by a failing test: the updater
mounts only `/var/run/docker.sock`, so a host `docker login` never reaches it (credentials live
in the host's `~/.docker/config.json`, not in the Docker daemon). Verified against the
maintained fork's official docs (`nicholas-fedor/watchtower` →
`docs/advanced-features/private-registries`): Watchtower reads `/config.json` inside the
container, or a directory named by `DOCKER_CONFIG`; the documented Compose recipe is a bind
mount of the host config file.

Changes made in all four documents (`README.md`, `README.zh-TW.md`, `README.en.md`,
`docs/deployment.md`):

- New subsection "updater authentication for private registries": states plainly that a host
  `docker login` does **not** guarantee automatic updates for a private image, and offers an
  **optional** `docker-compose.override.yml` with `DOCKER_CONFIG: /config` plus
  `${HOME}/.docker/config.json:/config/config.json:ro`.
- The repository deliberately does **not** mount that file by default, so a public-image
  deployment never fails on a missing local private file (explicit user requirement).
- Noted that Compose only auto-merges `docker-compose.override.yml` for the default file
  chain; with explicit `-f` lists it must be appended.
- All CNB login examples switched from `docker login ... -p "$CNB_TOKEN"` to
  `printf '%s' "$CNB_TOKEN" | docker login ... -u cnb --password-stdin`, so the token never
  lands in shell history or logs; username `cnb` confirmed by CNB's `artifact/docker.md`.
- Documented that the local-build overlay keeps the `auto-update` profile but the application
  container's `enable` label is `false`, so it is never replaced automatically.

Re-verified after the edit: `scripts.test_deployment_contracts` 27 OK,
`scripts.test_release_metadata` + `scripts.test_release_tools` 55 OK, README structural parity
`8/18/1/44` in all three languages, zero `-p "$CNB_TOKEN"` occurrences, and an isolated Compose
render (synthetic `.env`, real `.env` never read) showing plain `up -d` starts
`codearts2api` + `watchtower` while the overlay starts only `codearts2api`.

## Final revision: architecture intermediate tags documented

The coordinator adopted the review and decided to **keep** the per-architecture intermediate tags
for debugging rather than delete them, so they are now documented instead of treated as a defect.

Added to all four documents (`README.md`, `README.zh-TW.md`, `README.en.md`,
`docs/deployment.md`) in the image-switching section:

- The CNB pipeline builds and pushes `vX.Y.Z-amd64` and `vX.Y.Z-arm64` from
  `scripts/release-metadata.py arch-tags` (fixed order: amd64 then arm64, verified by running
  the helper), then assembles the multi-arch `vX.Y.Z` (plus `latest` for a stable tag) with
  `docker buildx imagetools create`.
- Those tags exist for debugging and single-architecture triage; **normal deployments keep using
  the multi-arch `vX.Y.Z` or `latest`**, and Compose/Watchtower select the host-matching image.

Also corrected a misleading claim in the review: the earlier "CNB Alpine missing gcc" issue was
a **real early defect** (since fixed by p7 and locked by a contract test), not something that
never happened.

Re-verified after the edit: `scripts.test_deployment_contracts` 28 tests OK,
`scripts.test_release_metadata` + `scripts.test_release_tools` 57 tests OK (85 total), and
README structural parity still `8/18/1/44` across all three languages.

## Boundary

Local documentation plus read-only review only. Historical R13 stays open. Docker/registry
runtime and remote workflows remain unverified until their owners run them; the integrated
review and its residual-risk list live in `research/r19-quality-review.md`. The parent reports
full Go normal/race/vet/build plus real amd64/arm64 cross-compilation with ELF verification;
that is build-artifact evidence, not Docker runtime evidence.