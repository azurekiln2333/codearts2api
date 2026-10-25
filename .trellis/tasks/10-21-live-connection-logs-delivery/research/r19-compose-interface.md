# R19 Compose / image / auto-update interface (p4 → pA docs, p6 tests)

Date: 2026-10-26. Owner of the files below: p4 (Compose/Dockerfile/.env.example/.dockerignore).
This note is the shared contract other workers and the parent read; it does not
replace the parent's task/spec ownership.

## 1. Files owned and delivered

| File | Role |
|---|---|
| `docker-compose.yml` | default deployment: published image only, no local build, auto-update ON by default |
| `docker-compose.build.yml` | local/self-built image overlay (buildx, updater off by default, target not updatable) |
| `Dockerfile` | multi-arch image (linux/amd64, linux/arm64), build-identity injection |
| `.dockerignore` | allow-list; excludes every private/irrelevant path from build context |
| `.env.example` | documented knobs, standard path and local-build path |

Not touched: `README*`, `docs/`, `scripts/*`, `.github/*`, `.cnb.yml`, `.trellis/*`,
actual `.env`, `config.json`, `auths/`, `data/`, old ZIPs.

## 2. Image interface

- Default: `CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api:latest`
  (matches the actual `origin` remote `github.com/azurekiln2333/codearts2api`).
- Any other registry is a pure env override, no YAML edit:
  `CA2A_IMAGE=docker.cnb.cool/<namespace>/<repo>:latest` (CNB image published by
  the tag pipeline), or a pinned version tag / digest for manual rollback.
- Local/self-built: overlay sets `CA2A_LOCAL_IMAGE` (default `codearts2api:local`)
  with `pull_policy: build`, so a published image is never pulled over it.
- Build identity: `BUILD_COMMIT` / `BUILD_TIME` build args → ldflags
  `codearts2api/internal/server.BuildCommit|BuildTime`; unset stays empty and the
  console reports an explicit unknown build instead of guessing.

## 3. Auto-update interface (final, matches the reference project)

**Standard deployment starts the updater by default. No profile flag is required.**

| Path | Command | Runs |
|---|---|---|
| Standard (recommended, auto-update ON) | `docker compose up -d` | `codearts2api` + `watchtower` |
| Manual only (no updater) | `docker compose up -d codearts2api` | `codearts2api` only |
| Local/self-built image | `docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build` | `codearts2api` only |

The base file deliberately has **no** `profiles:` on `watchtower`, exactly like
the reference `cnb2api-panel/docker-compose.yaml`. Only the local-build overlay
adds `profiles: [auto-update]`, so overlaying it stops the updater by default;
if a local build really wants the updater, add `--profile auto-update`.

Turning auto-update off after it has run:

```
docker compose stop watchtower
docker compose rm -f watchtower
```

Contract check (p6): `DefaultComposeTests.test_updater_enablement_matches_documentation`
takes the `else` branch here (no profiles) and only requires `watchtower` to appear
in the README/docs — which it does. No README change is required by p4.

## 4. Isolation contract (label AND scope)

| Item | Value |
|---|---|
| target label | `com.centurylinklabs.watchtower.enable: "true"` |
| target scope | `com.centurylinklabs.watchtower.scope: ${COMPOSE_PROJECT_NAME:-codearts2api}` |
| updater `WATCHTOWER_LABEL_ENABLE` | `"true"` |
| updater `WATCHTOWER_SCOPE` | same expression as target scope |
| updater own labels | `enable: "false"`, `scope: ${COMPOSE_PROJECT_NAME:-codearts2api}` |
| updater image | `nickfedor/watchtower:1.23.2` (pinned, never `latest`) |
| interval | `WATCHTOWER_POLL_INTERVAL=${WATCHTOWER_POLL_INTERVAL:-600}` |
| cleanup | `WATCHTOWER_CLEANUP: "true"` |

Both barriers must hold: label-enable restricts to explicitly enabled containers,
scope restricts to this Compose project. The updater never updates itself
(`enable: "false"`), and it is never given credentials or a rewritten config.

Pinned updater provenance, verified 2026-10-26 against upstream and registry:
- maintained fork repository: `github.com/nicholas-fedor/watchtower`
  (release `v1.23.2`, published 2026-10-22; `containrrr/watchtower` is archived)
- image: `nickfedor/watchtower:1.23.2` (Docker Hub push 2026-10-22, tag exists)

## 5. Preserved deployment contract (unchanged)

- `container_name: ${CA2A_CONTAINER_NAME:-codearts2api}`, `restart: ${CA2A_RESTART_POLICY:-unless-stopped}`.
- One port value: `CA2A_PORT` drives host port, container port and the derived
  `CA2A_LISTEN: ":${CA2A_PORT:-7866}"`. No second listener variable.
- `env_file: .env` (direct read) plus only three explicit entries: required
  `CA2A_API_KEY` guard, `TZ` fallback, derived `CA2A_LISTEN`; `CA2A_PORT` is also
  forwarded for the image healthcheck only (the app reads only `CA2A_LISTEN`).
- Mounts unchanged: `./auths → /app/auths`, `./data → /app/data`,
  `./config.json → /app/config.json:ro`.
- Log rotation: `json-file`, `max-size: 10m`, `max-file: "3"` on both services.

## 6. Dockerfile contract

- Base images pinned to maintained official tags, checked 2026-10-26 on Docker Hub:
  `GO_VERSION=1.27` (`golang:1.27-alpine`, updated 2026-10-22) and
  `ALPINE_VERSION=3.24` (`alpine:3.24`, updated 2026-10-23); both publish
  amd64+arm64 manifests. The previous `1.24`/`3.20` pair was stale
  (2026-02-08 / 2026-04-17). This is a toolchain-only change — `go.mod` stays
  `go 1.22`, no application code upgrade.
- `FROM --platform=$BUILDPLATFORM golang:1.27-alpine` build stage,
  `GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -trimpath` for
  `server login credit apply` (binaries `codearts2api`, `codearts2api-<name>`).
- Requires BuildKit/buildx (Compose v2 default); classic builder is unsupported
  for multi-arch. Example:
  `docker buildx build --platform linux/amd64,linux/arm64 --build-arg BUILD_COMMIT=$(git rev-parse HEAD) --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) -t <image> .`
- Runtime: `alpine:3.24` + `ca-certificates tzdata`, non-source image, no secrets.
- `HEALTHCHECK` probes `/healthz` using `CA2A_PORT`, else the port part of
  `CA2A_LISTEN`, else `7866`.
- Runs as root on purpose: changing the UID would break writes into the existing
  `auths/`/`data/` host mounts.
- OCI labels: `title`, `source`, `revision=${BUILD_COMMIT}`, `created=${BUILD_TIME}`.

## 7. `.dockerignore` contract

Allow-list only (`**` then negations): `Dockerfile`, `go.mod`, `go.sum`, `cmd/`,
`internal/`, `config.example.json`. Nothing else enters the context — `.env`,
`config.json`, `auths/`, `data/`, `.local-test/`, `*.zip`, `.git/`, platform
directories are excluded even if tracked. The allow-list exactly matches the
Dockerfile `COPY` set.

## 8. Verification evidence (local, no engine)

`docker` is not installed. Used the pinned official Compose standalone `v2.29.7`
already present in the Git-excluded `.local-test/tools/` (config parsing needs no
engine) with **synthetic** `.env` only; no real `.env`, config or credential read.

`.local-test/r19_compose_verify.ps1` → `RESULT: ALL COMPOSE CHECKS PASSED`:

- standard command `docker compose up -d` selects `codearts2api,watchtower`;
  base `watchtower` has no `profiles`.
- default image/no `build:`, `7866:7866`, `CA2A_LISTEN=:7866`, `CA2A_PORT=7866`,
  three mounts with `config.json` read-only, log rotation, target label
  `enable=true` + `scope=codearts2api`.
- `CA2A_IMAGE` override → CNB image rendered verbatim.
- `CA2A_PORT=21508` → published/container/`CA2A_LISTEN`/`CA2A_PORT` all 21508.
- `COMPOSE_PROJECT_NAME=mystack` → target label scope, `WATCHTOWER_SCOPE` and
  updater label scope all `mystack`.
- overlay: `image=codearts2api:local`, `pull_policy=build`, dockerfile,
  `BUILD_COMMIT`/`BUILD_TIME` verbatim, target `enable=false`, mounts/port
  preserved, `services=codearts2api` by default (updater gated).
- `CA2A_LOCAL_IMAGE` honoured; missing `CA2A_API_KEY` still rejected.

Cross-compile proof of the Dockerfile loop (local Go 1.27.3, identical flags):
linux/amd64 and linux/arm64 ELF binaries for all four commands, `CGO_ENABLED=0`,
with correct `e_machine` (0x3E = x86-64, 0xB7 = AArch64) and the identity
ldflags. A Windows build with the same ldflags contains the injected commit and
UTC time, proving the ldflag variable paths.

Healthcheck port derivation executed under POSIX sh for 5 cases
(`21508/""`, `""/":9000"`, `""/"0.0.0.0:9123"`, `""/""`, `"7866"/":9999"`) →
`21508, 9000, 9123, 7866, 7866`.

`python -B scripts/test_deployment_contracts.py` (p6-owned) → 22 tests OK.

## 9. Notes for other workers

1. **pA** — READMEs should describe the standard path `docker compose up -d`
   (auto-update included), the manual-only path `docker compose up -d codearts2api`,
   the overlay path, and the stop/rm off procedure. No `--profile auto-update`
   requirement exists for the base file; only the overlay gates the updater.
2. **p6** — no change required; the suite is green with the default-on model.
3. Nobody should claim the image, registry or workflows ran remotely: only local
   Compose parsing, cross-compilation and contract tests were executed here.

## 10. Honest boundary

No Docker engine/registry, no image build, no container run, no push, no deploy,
no production traffic. `docker build`, `docker compose up` and the tag pipelines
remain unverified locally.