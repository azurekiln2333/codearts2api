# Source release and deployed-feature verification

## 1. Scope / Trigger

Read this before changing source packaging, build-identity plumbing, deployment
instructions or `scripts/{release-source,verify-deployment,test_release_tools}.py`.
Source export and local verification do not imply a production deployment.

## 2. Signatures

- `python scripts/release-source.py --commit <ref> [--output <new.zip>]`
- `python scripts/verify-deployment.py --url <base> --expected-commit <full-sha>
  [--key-env CA2A_API_KEY] [--timeout 5]`
- Compose forwards `CA2A_BUILD_COMMIT` / `CA2A_BUILD_TIME` to Docker build args;
  ldflags inject `server.BuildCommit` / `server.BuildTime`.

## 3. Contracts

Export committed Git blobs from an explicit product manifest, never an arbitrary
working-tree archive. Include Go source/module, embedded panel, Dockerfile,
`.dockerignore`, Compose, `.env.example`, example config, README/license and
required scripts. Refuse missing inputs, nonregular product files and existing
output paths. Exclude real `.env`/`config.json`, credentials, data, old ZIPs,
Git/Trellis/platform files, even if tracked. Preserve the user's existing ZIP.

ZIP member ordering/timestamps/modes are deterministic for the commit. Generated
`release.env` contains its actual full source commit; `release.json` records the
commit timestamp and SHA-256 file hashes. Actual image build time is supplied at
build, never guessed from the source commit date. Building a ZIP without `.git`
uses `release.env`; rebuilding/recreating preserves the original auth/data/config
mounts and environment. A container restart alone does not rebuild an image.

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
Docker image runtime validation remains distinct if no Docker engine is available.

## 7. Wrong vs Correct

Wrong: claim deployment success because GET `/healthz` returns ok, or call a
credential-mutating admin GET while describing the verifier as read-only.

Correct: verify the full deployed revision, actual routes and cleaned interface
through side-effect-free admin snapshots, preserving mounts and reporting any
unverified production behavior explicitly.
