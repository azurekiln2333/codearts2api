# R19 reference & official-source findings (role: docs/review, pane w1C:pA)

Date: 2026-10-26. Read-only reference: `D:/My2KProjects/cnb2api-panel`.
Scope: pin the actual release/deployment interface for the R19 docs (three READMEs +
`docs/deployment.md`). Do not claim any image/workflow exists remotely.

## 1. Actual repository facts

- `origin` = `https://github.com/azurekiln2333/codearts2api.git` → GHCR image
  `ghcr.io/azurekiln2333/codearts2api` (all lowercase already).
- CNB slug is **not declared** in this repo; docs must use the dynamic
  `CNB_REPO_SLUG_LOWERCASE` (resolved by the CNB pipeline) and must not hard-code
  or invent a slug.
- Pre-R19 state: `docker-compose.yml` was build-only, only `.github/workflows/go.yml`
  existed, no `.cnb.yml`, no registry publication, no update service.
- Other R19 workers (this pane is pA): p4 compose/Dockerfile/.env/.dockerignore,
  p7 GitHub+CNB CI + `scripts/release-metadata.py`, p6 exporter + deployment contract
  tests. Shared contract (from `.local-test/r19-pi-briefs/*.md`): default image
  `ghcr.io/azurekiln2333/codearts2api:latest`, `CA2A_IMAGE` override, keep `CA2A_PORT`
  + `.env` + mounts, `v*` tag release boundary, stable-only `latest`, explicit source
  manifest, local build overlay.

## 2. Reference project (cnb2api-panel) patterns confirmed read-only

- `docker-compose.yaml` (default): `image: ${PANEL_IMAGE:-docker.cnb.cool/azurekiln/cnb2api-panel:latest}`,
  `env_file: [.env]`, `init: true`, `restart: unless-stopped`, json-file log rotation
  `max-size: 10m` / `max-file: "3"`, single published port derived from a port env,
  data/log mounts, and a **second `watchtower` service**:
  - image `nickfedor/watchtower:1.23.2`
  - `WATCHTOWER_LABEL_ENABLE: "true"`, `WATCHTOWER_SCOPE: ${COMPOSE_PROJECT_NAME:-cnb2api-panel}`,
    `WATCHTOWER_POLL_INTERVAL: ${WATCHTOWER_POLL_INTERVAL:-600}` (seconds), `WATCHTOWER_CLEANUP: "true"`
  - mounts `/var/run/docker.sock:/var/run/docker.sock`
  - target label `com.centurylinklabs.watchtower.enable: "true"` + `.scope` on the app
  - Watchtower's **own** labels: `...enable: "false"` + `.scope` → never self-updates
- `docker-compose.build.yaml` (overlay): app `image: cnb2api-panel:local`,
  `pull_policy: build`, `build: {context: ., dockerfile: Dockerfile}`,
  `...watchtower.enable: "false"`; and `watchtower: {profiles: [auto-update]}` so the
  updater is not started unless the `auto-update` profile is selected.
- `.github/workflows/build-and-release.yml` (reference): `test` → `console` (artifact) →
  `release` (tags only, `contents: write`, `gh release create --prerelease` for `-`
  versions) → `image` (tags only, `packages: write`, `ghcr.io/${GITHUB_REPOSITORY,,}`,
  stable adds `latest`, prerelease does not). `pull_request` only validates.
- `.cnb.yml` (reference): default `main.push` + `api_trigger` + `pull_request` validate;
  `"v*"` → `tag_push` validates tag vs package version, builds/tests, `git:release`
  (`optionsFrom` with `overlying/title/descriptionFromFile/preRelease/latest`) then
  `cnbcool/attachments` upload of ZIP+sha256; separate `publish-image` job
  (`services: [docker]`) logs in and pushes `$TAG` then `latest` only for stable.

## 3. Official sources verified (fetched 2026-10-26)

### Watchtower (maintained fork)
- GitHub repo `nicholas-fedor/watchtower` (active; `containrrr/watchtower` is **archived**).
  Docker Hub image namespace is `nickfedor/watchtower`.
- Tag `1.23.2` exists on Docker Hub (`hub.docker.com/v2/repositories/nickfedor/watchtower/tags/1.23.2`,
  last_updated 2026-10-22; platforms amd64, arm64, 386, arm, riscv64). Pinning
  `nickfedor/watchtower:1.23.2` is valid.
- `--label-enable` / `WATCHTOWER_LABEL_ENABLE` (default false): with it set, **only**
  containers with `com.centurylinklabs.watchtower.enable=true` are monitored.
- `--scope` / `WATCHTOWER_SCOPE`: only containers carrying a matching
  `com.centurylinklabs.watchtower.scope` label are monitored; enables multiple
  Watchtower instances without cross-updating. Docs warn all self-updating Watchtower
  containers should share the same scope label so renamed containers/old images are
  cleaned up.
- `WATCHTOWER_POLL_INTERVAL` (interval, seconds; default 86400) cannot be combined with
  `--schedule`; `WATCHTOWER_CLEANUP` (default false) removes old images after update and
  is deferred during self-update.
- `WATCHTOWER_RUN_ONCE` exists (one pass then exit).
- Wrong split for docs: label-enable alone does not scope a project; **both** the
  opt-in label **and** the scope value are required to bound updates to this Compose
  project.

### Compose spec / Docker docs
- `pull_policy` values: `always`, `never`, `missing`/`if_not_present` (default when no
  build section; the `latest` tag is always pulled even with `missing`), `build`
  (Compose builds; rebuilds if present), `daily`, `weekly`, `every_<duration>`.
- `image` may be omitted only if a `build` section exists; otherwise Compose fails.
  → default (published image) service must declare `image` and no `build`; the local
  overlay must add `image` + `build` + `pull_policy: build`.
- `env_file: .env` gives the container the file's variables directly; CLI `--env-file`
  only changes interpolation input, not the service `env_file` path (already in the
  source-release spec).
- `profiles`: services with a profile are not started by a plain `docker compose up`
  unless the profile is activated (`--profile <name>`) or the service is named
  explicitly. Selecting `--scale <svc>=0` scales a service to zero instances.

### GHCR
- Publish with `GITHUB_TOKEN` (Actions) or a PAT; workflow needs `packages: write`
  (and `contents: read` minimum). Reference uses `docker/login-action` with
  `username: ${{ github.actor }}`, `password: ${{ github.token }}`.
- Image path form `ghcr.io/<namespace>/<image>:<tag>`; `GITHUB_REPOSITORY` lowercased.
- **First publish default visibility is private.** Public anonymous pull requires
  changing the package visibility; otherwise pulls need `docker login ghcr.io` with a
  token that has `read:packages`.
- If a package was already pushed to the namespace without the repo connected,
  `GITHUB_TOKEN` may lack push permission; linking via the
  `org.opencontainers.image.source` label (or publishing from a workflow) is recommended.

### CNB (docs.cnb.cool, `/zh/llms.txt` index)
- Built-in env vars include `CNB_REPO_SLUG`, `CNB_REPO_SLUG_LOWERCASE` ("仓库路径小写格式"),
  `CNB_BRANCH` (branch or **tag name**), `CNB_TOKEN` (pipeline-scoped temporary token,
  auto-destroyed; used for code + artifact pull/push and API), `CNB_DOCKER_REGISTRY`
  ("制品库 Docker 源地址").
- Docker artifact registry: login `docker login docker.cnb.cool -u cnb -p ${CNB_TOKEN}`;
  same-name artifact path `docker.cnb.cool/${CNB_REPO_SLUG_LOWERCASE}`.
- `git:release` supports `overlying`, `tag` (not needed on `tag_push`), `title`,
  `description`/`descriptionFromFile`, `preRelease`, `latest`; **it cannot upload
  attachments** — use `cnbcool/attachments` (reference does this).
- `cnb:read-file` reads a local file and exports it to an env var (used for the
  manifest-derived archive/version without logging contents).

## 4. Interface documented (reconciled with the shipped p4 files)

- Default compose `docker-compose.yml`: `image: ${CA2A_IMAGE:-ghcr.io/azurekiln2333/codearts2api:latest}`,
  no `build:`; `CA2A_PORT` single source for host+container+`CA2A_LISTEN`; `env_file: .env`;
  mounts `${CA2A_AUTHS_PATH:-./auths}:/app/auths`,
  `${CA2A_DATA_PATH:-./data}:/app/data`, `${CA2A_CONFIG_PATH:-./config.json}:/app/config.json:ro`;
  `init: true`; json-file rotation 10m x 3; bundled Watchtower with `enable: "false"` on itself.
- Watchtower is gated by `profiles: [auto-update]`. Documented as: standard deployment
  `docker compose --profile auto-update up -d` (app + updater); opt-out `docker compose up -d`
  (app only); disable an enabled updater with `--profile auto-update stop watchtower` +
  `rm -f watchtower`.
- Local overlay `docker-compose.build.yml`: `image: ${CA2A_LOCAL_IMAGE:-codearts2api:local}`,
  `pull_policy: build`, `build.args BUILD_COMMIT/BUILD_TIME`, target label `enable: "false"`,
  updater profile-gated. Explicit command:
  `docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`.
- `CA2A_IMAGE` switches registry (GHCR ↔ CNB `docker.cnb.cool/<slug>` ↔ mirror).
- `v*` tag pipeline: tests gate → GHCR image + source Release (ZIP + sha256); stable
  `vX.Y.Z` updates `latest`, prerelease (`vX.Y.Z-rc...`) never does; PR/`pull_request`
  validates only, no publish, minimal permissions.
- Dockerfile: `BUILDPLATFORM` build stage, `TARGETOS/TARGETARCH` + `CGO_ENABLED=0`,
  `-trimpath`, identity ldflags, OCI labels (incl. `org.opencontainers.image.source`, which
  also helps GHCR package↔repo linking), `HEALTHCHECK` using `CA2A_PORT` then `CA2A_LISTEN`.

## 5. Resolved vs still open

Resolved after p4 delivered its files:

1. Updater mechanism = `profiles: [auto-update]` in the default Compose (opt-in at the Compose
   level); docs expose it as the standard deployment command plus an explicit opt-out.
2. Variable names: `CA2A_IMAGE`, `CA2A_LOCAL_IMAGE`, `CA2A_WATCHTOWER_CONTAINER_NAME`,
   `WATCHTOWER_POLL_INTERVAL` (default 600); `WATCHTOWER_CLEANUP` is fixed `"true"` in YAML.
3. Default file names stay `docker-compose.yml` / `docker-compose.build.yml`.
4. Project scope fallback is `COMPOSE_PROJECT_NAME` → `codearts2api`; the real deployment
   directory is `codearts2api`, so the scope resolves as documented (the temp-dir rendering
   checked in this session only proved the expression is consistent).
5. `docs/*.md` is already included by the exporter's `product_path`, so
   `docs/deployment.md` ships in the source release and README links stay valid.

Still open (owner: p7 / parent):

6. CNB override variable names (`CNB_IMAGE`, registry auth overrides) and whether they match
   the documented `CA2A_IMAGE` guidance.
7. CNB slug/namespace stays dynamic (`CNB_REPO_SLUG_LOWERCASE`); no real public CNB image can
   be asserted until a CNB repo exists and the pipeline runs.
8. No image is published yet → first-publish boundary remains real; docs state it explicitly.

## 6. Honest boundary

All findings above are from local read-only reference plus official documentation
fetches. Nothing was pushed, published, tagged, deployed or run against a registry.
Docker/registry runtime and remote CNB/GitHub workflows remain unverified until
executed by their owners.