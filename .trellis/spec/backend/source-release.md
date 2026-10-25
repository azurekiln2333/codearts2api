# Source release and deployed-feature verification

## 1. Scope / Trigger

Read this before changing source packaging, build-identity plumbing, deployment
instructions, published-image Compose, GitHub/CNB pipelines or release scripts.
Source export and local verification do not imply a production deployment.

## 2. Signatures

- `python scripts/release-source.py --commit <ref> [--output <new.zip>]`
- `python scripts/verify-deployment.py --url <base> --expected-commit <full-sha>
  [--key-env CA2A_API_KEY] [--timeout 5]`
- `docker compose pull && docker compose up -d` uses a published image plus
  project-scoped updater by default.
- `docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`
  builds locally, hides the updater behind a profile and disables target updates.
- The local-build Compose overlay forwards `CA2A_BUILD_COMMIT` / `CA2A_BUILD_TIME` to Docker build args;
  ldflags inject `server.BuildCommit` / `server.BuildTime`.

## 3. Contracts

Export committed Git blobs from an explicit product manifest, never an arbitrary
working-tree archive. Include Go source/module, embedded panel, Dockerfile,
`.dockerignore`, Compose, `.env.example`, example config, README/license and
required scripts. R19 adds the explicit build overlay, `.cnb.yml`, the sole named
GitHub workflow `.github/workflows/build-and-release.yml`, shared release metadata/tests and all three full README
translations (`README.md`, `README.zh-TW.md`, `README.en.md`). Do not allowlist
arbitrary `.github/workflows/*` or scripts by prefix. Refuse missing inputs, nonregular product files and existing
output paths. Exclude real `.env`/`config.json`, credentials, data, old ZIPs,
Git/Trellis/platform files, even if tracked. Preserve the user's existing ZIP.

ZIP member ordering/timestamps/modes are deterministic for the commit. Generated
`release.env` contains its actual full source commit; `release.json` records the
commit timestamp and SHA-256 file hashes. Actual image build time is supplied at
build, never guessed from the source commit date. Building a ZIP without `.git`
uses `release.env`; rebuilding/recreating preserves the original auth/data/config
mounts and environment. A container restart alone does not rebuild an image.

Compose reads optional application settings with `env_file: .env`. Preserve the
explicit API-key guard, timezone fallback and listener derived from `CA2A_PORT`;
users do not set a second deployment LISTEN. Image/port/name/restart/mount values
remain Compose interpolations; build context/identity live in the explicit local
build overlay. Explicit environment entries override env_file.
Optional application variables only in the invoking shell are not automatically
forwarded; put them in service .env or an explicit runtime override. CLI
`--env-file` controls interpolation input, not the service env_file path. Native
`CA2A_LISTEN` stays supported. Verify synthetic config without printing secrets.

Default deployment uses `CA2A_IMAGE` (GHCR path from the actual origin), never
`build:`. Preserve the existing application/container/mount identity and service
`.env`; one `CA2A_PORT` controls published/container/listener/health-check ports.
The updater uses pinned `nickfedor/watchtower`, label-enable AND matching
`COMPOSE_PROJECT_NAME` scope, excludes itself, polls at configurable
`WATCHTOWER_POLL_INTERVAL` (600 seconds), and rotates logs. Standard `up -d`
includes it; `up -d codearts2api` is app-only on a new stack, and disabling an
already-running updater requires stop/remove. Local-build target enable=false
prevents published-image replacement even if the updater is explicitly started.
Host registry login alone does not provide credentials inside an updater:
document an optional read-only Docker config override for private registries.

GitHub/CNB releases publish only trusted validated `v*` tags, after the full Go
normal/race/vet/build and Python release/deployment gates. PR validation has no
publication step or write credentials. Shared `release-metadata.py` owns version,
stable-only `latest`, build identity, notes/checksum and CNB release options.
Source ZIP/hash release and linux/amd64+linux/arm64 image publication are distinct
jobs, both gated; CNB image job runs its own validation before any registry write.
CNB Alpine race checks require build-base and CGO_ENABLED=1. Go image compilation
uses CGO_ENABLED=0 and target OS/architecture; build identity uses the full actual
commit and UTC time. CNB registry slug is a platform variable, never an invented
namespace. New GHCR visibility/first trusted tag are deployment prerequisites;
local tests cannot establish that an image is published. Preserve all existing
private files/old archives.

The verifier reads the service key from the named environment variable, sends
only bounded GETs, refuses redirects and prints no bodies/headers/key. Its base
URL may have a reverse-proxy prefix but no credentials, query or fragment. Check
health, cleaned panel markers, authenticated build identity and requests/audit/
models/connections schemas. Unknown/mismatched full source revision is a failure.
Disabled audit/tracking is an explicit runtime warning, not missing-route success.
The checked admin GET routes must themselves stay cached/read-only; they cannot
launch discovery, automatic claims or real model probes.

## 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Worktree contains local secrets/edits | Export only reviewed committed product blobs |
| Missing Compose/source input | Refuse incomplete release |
| Existing output ZIP | Refuse replacement and preserve original bytes |
| Missing/invalid environment key | Fail without printing key material |
| Old console / missing route / 401 | Nonzero verifier result with specific safe error |
| Unknown/wrong running revision | Fail even if health is ok |
| Redirect or oversized response | Refuse with bounded safe error |
| No production access | Report local checks and pending remote verification honestly |
| PR or branch push | Validate only, no release/login/push |
| Prerelease tag | Version image/release only; never move latest |
| Local build overlay | Local image/build policy; target updater=false; updater inactive |
| Different project container | Scope/label filter excludes it |
| Private registry | Updater needs its own configured read-only credentials |
| Alpine CNB race gate | Install C compiler/musl tools and enable CGO |

## 5. Good / Base / Bad Cases

- Good: export the work commit, build using its release identity, recreate using
  unchanged mounts, and verify the actual running commit and new interfaces.
- Base: ordinary Git-based builds inject `git rev-parse HEAD`; no config or
  credential migration is needed for additive model/connection diagnostics.
- Bad: zip the checkout including auths, overwrite the existing archive, restart
  an old image and infer successful rollout from public health alone.

## 6. Tests Required

`python -B scripts/test_release_tools.py` covers deterministic export, dirty and
tracked-private exclusion, required inputs, symlinks and existing output; synthetic
HTTP verifies prefixes, all GET methods, auth/404/old UI, build identity, disabled
state, malformed/oversized/slow responses, redirects and output redaction.
Keep real route envelopes distinct: requests returns `events`/`disabled`, whereas
audit statistics returns `audit.enabled`. Test disabled and malformed statistics
against that contract; do not reuse an invented request-history fixture for it.
Build from an extracted final source release and run the verifier against an
isolated local server with empty temp auth/data before presenting that artifact.
R19 additionally runs release metadata and deployment contract suites, official
Compose config against synthetic env/project/port/image/build cases, actionlint
against the sole GitHub workflow and shell syntax checks for all workflow scripts. Compare
all three README operational code blocks/language navigation, and verify every
new deployment input in the actual extracted release. Cross-compile all four
shipped binaries for amd64/arm64 and inspect ELF machine headers. Docker image
runtime/registry/remote workflow validation remains distinct if no engine or
publication authorization is available.

## 7. Wrong vs Correct

Wrong: claim deployment success because GET `/healthz` returns ok, or call a
credential-mutating admin GET while describing the verifier as read-only.

Correct: verify the full deployed revision, actual routes and cleaned interface
through side-effect-free admin snapshots, preserving mounts and reporting any
unverified production behavior explicitly.

Release tags accept only `vMAJOR.MINOR.PATCH[-prerelease]`; reject `+build` metadata and invalid/overlength architecture-suffixed Docker tags before publishing. CNB external registry overrides must provide credentials and fail early otherwise. Prefer bare `CNB_IMAGE` repository paths in CI; deployment `CA2A_IMAGE` includes a tag or digest. Retired `.github/workflows/go.yml` is never exported.
