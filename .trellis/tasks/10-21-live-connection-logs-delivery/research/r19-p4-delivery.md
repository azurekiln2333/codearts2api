# R19 p4 delivery — Compose / Dockerfile / .env.example / .dockerignore

Date: 2026-10-26. Role: p4 (deployment build/deploy files). No commit, push,
release or deploy performed. No application code changed.

## Files delivered (all inside p4 ownership)

| File | Status |
|---|---|
| `docker-compose.yml` | rewritten: image-only default, **auto-update ON by default**, log rotation, labels/scope |
| `docker-compose.build.yml` | new: local build overlay (`pull_policy: build`, updater gated by profile, target `enable=false`) |
| `Dockerfile` | rewritten: buildx multi-arch, maintained base images, CGO off, identity ldflags, OCI labels, healthcheck |
| `.dockerignore` | rewritten: allow-list, no private path can enter the context |
| `.env.example` | additive: `CA2A_IMAGE`, updater knobs, standard + local-build paths |

Interface note for pA/p6: `research/r19-compose-interface.md`.

## Decisions

1. **Standard deployment enables auto-update by default**, matching the reference
   project: `watchtower` has **no** `profiles:` in `docker-compose.yml`, so
   `docker compose up -d` starts `codearts2api` + `watchtower`. The documented
   default path therefore does not omit auto-update. Turning it off is explicit:
   `docker compose up -d codearts2api`, or `docker compose stop watchtower &&
   docker compose rm -f watchtower`.
2. **Only the local-build overlay gates the updater** (`profiles: [auto-update]`
   in `docker-compose.build.yml`), so a self-built image is not auto-replaced;
   the overlay also forces the target label `enable=false`. Reference parity:
   `cnb2api-panel/docker-compose.yaml` has no profile, `docker-compose.build.yaml` adds one.
3. **Updater pinned to `nickfedor/watchtower:1.23.2`** after checking upstream:
   maintained fork repo `github.com/nicholas-fedor/watchtower`, release `v1.23.2`
   published 2026-10-22; Docker Hub `nickfedor/watchtower:1.23.2` exists (pushed
   2026-10-22) and `containrrr/watchtower` is archived.
4. **Default image `ghcr.io/azurekiln2333/codearts2api:latest`** matches the real
   `origin` remote; `CA2A_IMAGE` is the only switch (CNB registry, pinned version,
   digest rollback).
5. **Base images moved to maintained official tags** (checked on Docker Hub
   2026-10-26): `GO_VERSION=1.27` (`golang:1.27-alpine`, updated 2026-10-22) and
   `ALPINE_VERSION=3.24` (`alpine:3.24`, updated 2026-10-23); both publish
   amd64+arm64 manifests. The previous `1.24`/`3.20` pair was stale
   (2026-02-08 / 2026-04-17). Toolchain-only change: `go.mod` stays `go 1.22`,
   no application code upgrade.
6. **`CA2A_PORT` is also forwarded into the container** (inert for the app, which
   reads only `CA2A_LISTEN`) so the image healthcheck derives the port correctly;
   it also falls back to the port part of `CA2A_LISTEN`, then `7866`.
7. **Image runs as root on purpose** — switching UID would break writes into the
   existing `auths/`/`data/` host mounts.
8. Preserved unchanged: container name, restart policy, single `CA2A_PORT`
   derivation, `env_file: .env` direct read, the required API-key guard, `TZ`
   fallback, and all three mounts (`config.json` read-only).

## Verification performed

- Official Compose CLI (pinned standalone `v2.29.7` in Git-excluded
  `.local-test/tools/`, no engine needed) with **synthetic** `.env` only:
  `.local-test/r19_compose_verify.ps1` → `RESULT: ALL COMPOSE CHECKS PASSED`.
  Covers default image/no build/ports/listener/labels/scope/logs/mounts, that the
  standard command selects `codearts2api,watchtower` with no profile, the
  `CA2A_IMAGE` override, `CA2A_PORT=21508` derivation,
  `COMPOSE_PROJECT_NAME` propagation, the overlay (`pull_policy: build`, build
  args, target `enable=false`, updater gated so only `codearts2api` starts),
  `CA2A_LOCAL_IMAGE`, and the required API-key rejection.
- Dockerfile build loop reproduced locally (Go 1.27.3, identical flags):
  linux/amd64 (e_machine 0x3E) and linux/arm64 (0xB7) ELF binaries for
  `server login credit apply` with `CGO_ENABLED=0`; a Windows build with the same
  ldflags contains the injected commit and UTC time.
- Healthcheck port derivation executed under POSIX sh, 5 cases → correct ports.
- `python -B scripts/test_deployment_contracts.py` (p6-owned): **22 tests OK**.
- `python -B scripts/test_release_tools.py` (p6-owned): 16 tests OK.
- All five p4 files are LF-only (no CRLF), `git diff --check` clean.
- No real `.env`, `config.json`, `auths/`, `data/` or ZIP was read, moved or
  modified; other workers' files were not touched or reverted.

## Boundaries / not verified

- No Docker engine on this machine: **no `docker build`, no image, no container
  run, no pull, no registry login**. `HEALTHCHECK`, `init: true`, socket mount and
  the actual overlay build are therefore unexecuted here.
- No workflow/tag pipeline ran; no image or source Release exists remotely.
- `FROM --platform=$BUILDPLATFORM` requires BuildKit/buildx (Compose v2 default);
  the classic builder is not supported for multi-arch.
- GHCR package visibility and the first `v*` tag publication remain prerequisites
  for the image-only default path.