# CodeArts2API

> An OpenAI-compatible proxy for Huawei Cloud CodeArts Agent (Pangu Assistant / MaDao).
> **No CodeArts Agent client required** — it talks to the Huawei Cloud API directly in
> pure Go, with multi-account rotation, automatic token renewal and a Web UI.

**Language / 語言 / 语言:** [简体中文](README.md) · [繁體中文](README.zh-TW.md) · [English](README.en.md)

## Related projects

This project extends the same family of open-source work by
[Sliverkiss](https://github.com/Sliverkiss); its architecture and operational shape
follow these repositories:

- [workbuddy2api](https://github.com/Sliverkiss/workbuddy2api) — WorkBuddy CN OpenAI-compatible reverse proxy (account pool / rotation / check-in architecture)
- [traework2api](https://github.com/Sliverkiss/traework2api) — TRAE Work OpenAI-compatible reverse proxy (zero-dependency Go skeleton)
- [qoderwork2api](https://github.com/Sliverkiss/qoderwork2api) — QoderWork CN OpenAI-compatible reverse proxy (OAuth authorization flow)

Thanks to the original authors for their open-source work and design.

## Quick start (Ubuntu / Linux)

```bash
make linux            # four static Linux binaries under bin/
make test
```

### Sign in (Huawei Cloud account)

```bash
# Local machine with a browser
./login.sh

# Server (no browser): prints a link; open it in any browser and the ticket is polled back
./login.sh -print-only

# Credentials are written to auths/codearts-{user_id}.json
```

### Start

```bash
cp config.example.json config.json
export CA2A_API_KEY=your-random-secret
./bin/codearts2api -config config.json
```

### Verify + Web UI

```bash
curl http://127.0.0.1:7866/healthz
curl http://127.0.0.1:7866/v1/models -H "Authorization: Bearer $CA2A_API_KEY"
curl -X POST http://127.0.0.1:7866/v1/chat/completions \
  -H "Authorization: Bearer $CA2A_API_KEY" -H "Content-Type: application/json" \
  -d '{"model":"snap-chat","messages":[{"role":"user","content":"你好"}]}'
```

Open **http://127.0.0.1:7866/panel/** for the admin console: account authorization and
status, the model catalog, request audit and connection logs. Multi-turn context continues
per account automatically (grouped by chat_id); you can also pin a conversation with the
`X-Codearts-Chat-Id: <chatId>` header or `conversation_id` in the body.

For streaming requests, setting `"stream_options":{"include_usage":true}` returns one
`choices: []` usage chunk before `[DONE]`; all other chunks have `usage: null`. Without it
no usage is returned. Both streaming and non-streaming prefer upstream usage; when upstream
reports none, the value is estimated from text length and is not an exact token count.

The console's "request audit" stores input, output and total tokens for successful requests
and labels each value as *upstream-reported* or *estimated*; streaming clients that did not
enable `include_usage` are still recorded server-side. The per-account and per-model
summaries cover the **recent audit window under the current filters**, are unaffected by
paging, and are neither the full history nor an upstream bill. Old records, failed requests
and upstream components that were not reported show as unknown; disk archives outside the
retained window are not scanned, and after a restart the window is restored from the retained
logs. Statistics may be incomplete when audit is disabled or records were lost. The account
page's "credential remaining time" is when the login credential expires and is unrelated to
model token usage.

Tool calling supports `tool_choice` values `auto` / `none` / `required` and a named function
object. With a named function the gateway sends only that function's definition upstream and
converts it to the string `required` the upstream understands; a missing or duplicated
function returns 400. Upstream `InferHub.001001005` parameter errors also return 400 without
rotating or cooling the account; if the stream already started, an SSE error event ends it
and the account is still not penalized.

The gateway caps upstream `max_tokens` / `max_completion_tokens` at 65536 and caps
larger known `/v1/models` output limits accordingly; smaller or absent values stay
unchanged. `InferHub.001001005.400` is a general parameter error and can also be
caused by other parameters or a large request body.

### Credits and New API balance

The overview shows the total, used and remaining credits in account credit packages.
The server reads them on startup and every five minutes; the overview also provides a
manual refresh. Independent daily benefit-model quotas are excluded. Identical UIDs are
deduplicated, but different accounts may share a package and the sum can overlap. Failed,
stale or incomplete snapshots are not reported as a zero total.

`GET /v1/quota` uses the service Bearer key and returns raw credits and completeness;
reading the cache does not contact the upstream:

```bash
curl http://127.0.0.1:7866/v1/quota -H "Authorization: Bearer $CA2A_API_KEY"
```

The local New API project supports a balance route for advanced custom channels. It
interprets the balance as USD, so set `credits_per_usd` only after determining a
conversion yourself. The `100` below is an example, not an official exchange rate.
Set the channel Base URL to the service root (for example, `http://127.0.0.1:7866`,
without a trailing `/v1`) and its key to the service key. For a new channel, use the
following example under Advanced Custom > Configure routes > Full JSON. This JSON
belongs to the route editor, not the entire channel `settings`:

```json
{"advanced_routes":[
  {"incoming_path":"/v1/chat/completions","upstream_path":"/v1/chat/completions","converter":"none"},
  {"incoming_path":"/v1/responses","upstream_path":"/v1/responses","converter":"none"},
  {"incoming_path":"/v1/messages","upstream_path":"/v1/messages","converter":"none"},
  {"incoming_path":"/v1/models","upstream_path":"/v1/models","converter":"none"},
  {"incoming_path":"/v1/dashboard/billing/credit_grants","upstream_path":"/v1/quota?credits_per_usd=100","converter":"none"}
]}
```

Responses and Messages are protocol adapters supporting a subset including text and
some tools. Responses does not support persistence (`store=true`) or continuation via
`previous_response_id`; neither adapter supports images. Such requests return an error.

The example configures chat, Responses, Messages, model-list and balance routes; add routes for any
other endpoints you need. For an existing channel, keep its routes and add or edit the
balance route under Balance Query in the route editor. If it is not configured yet,
select Add management route first. Set the upstream path to
`/v1/quota?credits_per_usd=100` and authentication to Default Bearer, then save the
routes and the channel. Replace `100` with your chosen conversion rate. Balance
queries are not supported for multi-key channels.

Refreshing the channel balance converts only a complete, fresh snapshot; an incomplete
or stale snapshot returns 503 instead of writing a false zero, but New API may continue
to display the previous balance. Regular OpenAI channel types do not use this balance
route; use a New API version that supports it.

Independent benefit-model quotas are excluded from the credit balance. During a bulk or
scheduled balance update, New API may automatically disable a channel with auto-disable
enabled when its credit balance is zero, even if benefit-model quota remains. If you
rely on benefit models, turn off Auto-disable channel or avoid bulk and scheduled
balance updates. Disabling auto-disable also affects other automatic channel disabling
for that channel. A manual balance query for a single channel does not trigger the
bulk or scheduled update's auto-disable behavior.

### Multi-user concurrency and timeouts (New API integration)

Requests prefer healthy accounts that have a free slot and match the model capability;
equally loaded accounts rotate, and existing conversations keep valid account affinity.
Account picking reads only the cached model catalog: unknown catalogs are allowed to try,
and model discovery never runs synchronously inside a chat request. The default
`max_concurrent` per account is `1`; adjust it to the account's real capacity.

Slot waiting uses a bounded queue, defaulting to a process-wide `max_pending: 64`. In-flight
requests across the whole lifecycle (reading the body, waiting, retry gaps, generation and
response writes) are also capped at **the sum of concurrency capacity of enabled accounts +
max_pending**, so a fast upstream rejection cannot accumulate retries. Handlers sharing one
account pool share that limit; multiple processes or replicas count separately, so split
capacity according to the shared accounts' real quota. A full queue, or more than the default
10 seconds of cumulative waiting, returns 429 with `Retry-After`; gateways should back off.
A cancelled request leaves the queue, closes upstream and releases its slot, and a client
timeout never cools a healthy account.

Default time budgets (all overridable by config or environment variable):

| Stage | Setting | Default |
|-------|---------|---------|
| Read body → first upstream data (queueing, refresh, retries, account switch included) | `startup_timeout_seconds` | 90 s |
| Cumulative slot waiting per request | `account_wait_seconds` | 10 s |
| Upstream headers, first data and subsequent read idle | `upstream.timeout_seconds` | 60 s |
| Single downstream write or flush | `stream_write_timeout_seconds` | 30 s |
| Upstream concurrency / TPM retry | `queue_max_attempts` / `queue_retry_seconds` | 3 total attempts per account (first included), 2 s apart |

Startup timing stops once the first upstream data arrives, so a long but continuously
streaming answer may exceed 90 seconds; it is terminated only when upstream stays silent for
the idle timeout. A timeout before response headers returns 504; an already-started stream
sends an error event and `[DONE]` when writable. The upstream connection closes on the
protocol end marker instead of waiting for TCP teardown. New API and reverse-proxy timeouts
must exceed the configured startup/idle budgets and allow the expected long answers.

Existing explicit values such as `max_concurrent: 5` or `upstream.timeout_seconds: 120` in
your config are preserved on upgrade. `upstream.timeout_seconds` now actually applies to the
chat client, for both initial accounts and accounts added or reloaded at runtime; it had no
effect on chat in older versions. To adopt new defaults, update your own `config.json`, not
just the example. Logs record queueing, first data, total duration, attempts and outcome so
they line up with New API request times, and never log prompts or credentials.

### Model list and limited-time benefits

`/v1/models` returns upstream **exact model IDs** (case-sensitive, e.g. `GLM-5.2`,
`Qwen3-VL-235B`) and also adds a lowercase alias (`glm-5.2`) for IDs containing uppercase;
both work for chat. Limited-time benefit (free-tier) models carry `benefit: true`, and chat
adds the upstream-required `maas_type: benefit` header based on the account that issues the
request (accounts with different plans never leak into each other — the list is the union of
each account's models, while routing prefers an account whose catalog truly contains that
model).

New API can use lowercase model names everywhere, e.g. `glm-5.3`. Each request maps the name
through the selected account's current credential catalog to the name that route actually
returns; other accounts' casing never overrides this request, and a benefit source with a
different name still uses that source's original ID. New names without catalog evidence are
never guessed into another model. Audit keeps the client's original name; the "canonical
name" records the last name actually sent upstream. A successful name mapping still does not
mean upstream granted access to the model.

Benefit catalogs rotate with the free plan; check what the current accounts really offer with:

```bash
go run ./cmd/models                 # models available to accounts (built-in + benefit)
go run ./cmd/models -json           # machine readable
go run ./cmd/models -claim          # claim limited-time benefits first, then list (idempotent, a write)
```

A benefit model **must be claimed before it can be called**: unclaimed models always return
`InferHub.4004.200 benefit not found` upstream (observed). Claiming is idempotent and the
official client also calls it when the model menu opens, so the service claims by default
(`benefit_auto_claim: true`). To stop the service writing to accounts, disable it explicitly:

```jsonc
"benefit_auto_claim": false   // or CA2A_BENEFIT_AUTO_CLAIM=0
```

When disabled nothing is claimed proactively; already-claimed entitlements still follow the
upstream account state, and unclaimed models may be rejected upstream.

"Model catalog → refetch" in the console really re-requests each account's Agent, built-in and
benefit sources, showing source status, attempt/success times, retained older catalogs and
per-account model exclusions. Repeated clicks share one bounded job that starts at most every
15 seconds, runs at most 60 seconds and handles two accounts in parallel; failed sources keep
their last successful data marked as stale. `GET /admin/api/models` is a read-only cache
view; `POST /admin/api/models/refresh` returns 202 with job status.

`model_not_available` means no account currently eligible for scheduling offers the model, or
a valid capability rejection is cached. First check the exact model ID, source failures and
exclusion reasons in the catalog. A successful fetch of the relevant healthy source resets
the related older exclusions from before that fetch so the next real call can decide again; it
never treats catalog membership as permission proof and never clears a rejection written by a
concurrent request. Seeing a model on the official site does not mean every configured account
has that entitlement. When a complete, fresh account catalog confirms no account has the
requested model, chat returns 404 `model_not_available`, and public `/v1/models` filters such
presets with no account support. `glm-5.3-flash` and `glm-5.3` are different models and never
substitute for each other. 503 `no_healthy_account` means no healthy schedulable account right
now (all disabled, cooling or with unusable credentials); a full queue or waiting timeout
returns 429, and startup or upstream idle timeouts return 504. `attempts: 0` in audit means no
inference request has been sent upstream yet.

`TM.00001041` / `concurrency_limit` means the upstream rejected a concurrent session. If an
eligible untried account exists and the rotation budget allows, the gateway switches account
first; otherwise it falls back to bounded retries. With the default three accounts all
returning that error, at most five attempts (A/B/CCC) result, still capped at three per
account. Explicit concurrency rejections carried over HTTP 400, HTTP 429 or SSE never cool a
whole account; ordinary rate limiting and TPM keep their existing policy. `attempts` counts
real outbound calls; combine it with `tried` and the connection trace to see whether the
account changed.

### Model availability probing

`/v1/models` returns the discovered catalog with known-unavailable entries filtered out; not
being rejected does not mean callable was verified. Upstream discovery (agent-center / builtin
/ benefit gateway) may list models an account has not registered (e.g. returning
`InferHub.002002009.404`). The service periodically probes them with one minimal request and
caches the conclusion, and learns from real failures too (`not registered` /
`benefit not found` count as account capability issues and never cool a healthy account).
Probe results are cached for 30 minutes.

### API key is required

Without an API key the service **refuses to start** (older versions fell back to the public
`dummy-key-for-codearts`, exposing the API to anyone). Pick one:

```bash
# 1) env (recommended; works with .env / systemd EnvironmentFile)
export CA2A_API_KEY=$(openssl rand -hex 24)
# 2) the "api_key" field in config.json
# 3) .env for docker compose
```

### Account renewal

`refresh_token` is bound to the `client_id` and DPoP private key used at login, so credential
files store `client_id` and `dpop_private_key`; renewal reuses them unchanged and writes back
the rotated refresh token. If you changed `login_client_id` manually, existing accounts still
renew with their own recorded client_id.

### Daily check-in and starter credits

By default the service checks campaigns at startup and on every watchdog poll, automatically
claiming daily check-in credits and any claimable new-user credits upstream reports. New
accounts are handled on the next poll; you can also use "check in" / "check in all" in the Web
UI, or call `POST /admin/api/checkin` with your key (optionally `{"uid":"account ID"}`).
A claim must be confirmed: an unconfirmed claim is only confirmed, and a confirmed one is not
claimed twice. Eligibility, amounts, daily refresh and campaign start/end all follow upstream;
failures retry on the next poll and never cool or disable inference accounts because the
campaign API failed.

`credits_auto_claim: false` or `CA2A_CREDITS_AUTO_CLAIM=0` disables automatic claiming, and
`watch.enabled: false` disables the whole polling loop. Manual check-in still works. This
campaign is independent of the `benefit_auto_claim` model-token benefit; the panel shows the
latest claim result and never treats claimed amounts or healthy account counts as a credit
balance. `credit.sh` shows login state and `apply.sh` only renews.

The current [official daily check-in campaign](https://support.huaweicloud.com/offers-codeartsagent/codeartsagent_offers_0004.html)
grants 1000 credits per day for 30 days to users already billed with credits, ending
2026-12-31 00:00 (UTC+8); the [new-user signup reward](https://support.huaweicloud.com/price-codeartsagent/codeartsagent_billing_0029.html)
is 4000 credits. Ineligible or already-granted starter rewards are not claimed again.

## Deployment and upgrades

There are two ways to deploy: **use the published image** (default, no local build), or
**build locally from source**. Both share the same `docker-compose.yml` variables and mounts;
the local build uses the `docker-compose.build.yml` overlay to change the image source.

Requirements: Docker Engine 24+ and Docker Compose v2 (`docker compose`).

### Option 1: published image (default, recommended)

```bash
# Copy the examples on first deployment only; never overwrite existing files on upgrade
cp -n .env.example .env
cp -n config.example.json config.json
mkdir -p auths data

# Edit .env: at least set CA2A_API_KEY; to change the port set CA2A_PORT (default 7866)
docker compose config --quiet   # local syntax/interpolation check only
docker compose pull
docker compose up -d            # standard deployment: application + automatic updates
```

The standard command starts both the application container and the Watchtower updater (enabled
by default). For a manual deployment without automatic updates, use
`docker compose up -d codearts2api`: it starts only the application service.

The default image is `ghcr.io/azurekiln2333/codearts2api:latest`, matching the repository's
actual origin (`github.com/azurekiln2333/codearts2api`). Set `CA2A_IMAGE` in `.env` to switch
to another image or pin a version (see "Switching images and pinning versions" below).

> **First-release boundary:** `ghcr.io/azurekiln2333/codearts2api:latest` only exists after the
> first `v*` tag is pushed and CI publishes successfully. Until then `docker compose pull`
> fails with a missing manifest — that is expected, not a local misconfiguration; use option 2
> (local build) in the meantime. A GHCR package is **private by default on first push**: either
> switch it to public in the GitHub package settings, or `docker login ghcr.io` with a token
> that has `read:packages` before pulling.

Upgrade while keeping the existing `.env`, `config.json`, `auths/` and `data/`:

```bash
docker compose pull
docker compose up -d
```

### Option 2: build locally from source

```bash
# Overlay the local build: local image, pull_policy: build, auto-update target disabled
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

The overlay pins the application image to the local `codearts2api:local` (override with
`CA2A_LOCAL_IMAGE`), and Watchtower never replaces a locally built container with a published
image. To stamp the build identity (the console's "runtime settings" then shows the actual
commit):

```bash
CA2A_BUILD_COMMIT="$(git rev-parse HEAD)" \
CA2A_BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

`CA2A_BUILD_CONTEXT` can override the build context. After source changes you must
`up -d --build` again; restarting the old container does not replace the image. The build
overlay leaves the updater off by default (it keeps the `auto-update` profile); even with an
explicit `--profile auto-update`, the application container's `...enable` label is `false`, so
it is never replaced automatically.

### Automatic updates (Watchtower, on in the standard deployment, explicit opt-out)

The standard deployment command `docker compose up -d` also starts a Watchtower instance
**scoped to this project** that checks and updates the `codearts2api` container automatically.
Compose starts the updater by default; to opt out, start only the application service:

```bash
# Standard deployment: application + automatic updates
docker compose up -d

# No automatic updates: start the application only (the updater does not run)
docker compose up -d codearts2api
```

Watchtower behaviour:

- Pinned to the maintained fork `nickfedor/watchtower:1.23.2` (the original
  `containrrr/watchtower` is archived; the Docker Hub namespace is `nickfedor` and the GitHub
  repository is `nicholas-fedor/watchtower`).
- **Scope isolation:** both `--label-enable` and a scope are set. Only containers carrying
  `com.centurylinklabs.watchtower.enable: "true"` (this project's `codearts2api` service) and a
  `com.centurylinklabs.watchtower.scope` equal to this project's name are updated. The scope
  defaults to `COMPOSE_PROJECT_NAME`, falling back to `codearts2api`, so other projects, other
  stacks and unrelated containers are never touched.
- Watchtower labels itself `...enable: "false"` and never self-updates.
- It mounts `/var/run/docker.sock` (the Docker control channel) — necessary to update container
  images, and it never rewrites `.env`, `config.json` or credentials.
- Configurable in `.env`:
  - `WATCHTOWER_POLL_INTERVAL`: check interval in seconds, default `600`.
  - `CA2A_WATCHTOWER_CONTAINER_NAME`: updater container name, default `codearts2api-watchtower`.
  - Removing old images after an update (`WATCHTOWER_CLEANUP: "true"`) is fixed in the YAML and is not exposed as a `.env` variable.
- The `.env`, `config.json`, `auths/` and `data/` mounts are unchanged when the container is
  recreated by an update.

**Disable an enabled updater:**

```bash
# Stop and remove only the updater; the application container is unaffected
docker compose stop watchtower
docker compose rm -f watchtower
```

Note: `docker compose up -d codearts2api` starts only the application and does **not** stop an
already-running updater (Compose leaves services it does not list alone); use the two commands
above. The updater can only pull a **published** image; before the first `v*` tag exists there is
nothing to pull, so use the local build (option 2). Local builds (option 2) do not start the
updater and never update locally built containers.

#### Updater authentication for private registries

A host `docker login` writes only the host's own `~/.docker/config.json` and is **not passed to
the Watchtower container**. The updater mounts only the Docker socket by default and therefore
has no registry credentials: public images (for example a GHCR package switched to public) need
none, while private GHCR/CNB images need a **read-only** Docker config for automatic updates to
work.

Create an optional `docker-compose.override.yml` (Compose merges it automatically for the
default file chain; when you list files explicitly with `-f`, append
`-f docker-compose.override.yml` as well):

```yaml
services:
  watchtower:
    environment:
      DOCKER_CONFIG: /config
    volumes:
      - ${HOME}/.docker/config.json:/config/config.json:ro
```

The repository **does not mount this by default**, so a public-image deployment never fails on a
missing local private file; it is an optional opt-in. Never put tokens in YAML or logs, and
always log in with `--password-stdin`. When the updater recreates the container it keeps the
existing mounts (`.env`, `config.json`, `auths/`, `data/`), so no credential re-import is needed.

If the host `~/.docker/config.json` uses `credsStore`/`credHelpers` (for example Docker Desktop),
the updater container has no matching helper and authentication fails; use a hand-written
`config.json` with an `auths` entry instead (the official docs show how to generate the base64
`auth` value), or mount the helper as well.

### Switching images and pinning versions (GHCR / CNB / mirrors)

`CA2A_IMAGE` selects the image to pull and defaults to GHCR. Set it in `.env` and run
`docker compose pull && up -d`:

```bash
# Pin a specific version (recommended for production rollback)
CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api:v1.2.3

# Switch to the CNB artifact registry; the slug is dynamic — use your real repo path (lowercase)
CA2A_IMAGE=docker.cnb.cool/<CNB_REPO_SLUG_LOWERCASE>:latest
```

**Per-architecture intermediate tags:** when assembling the multi-arch image, the CNB
pipeline first builds and pushes two intermediate per-architecture tags, `vX.Y.Z-amd64` and
`vX.Y.Z-arm64`, and then assembles them into the multi-arch `vX.Y.Z` (plus `latest` for a
stable tag). Those `-amd64` / `-arm64` tags are produced by the CNB pipeline and kept for
debugging and single-architecture triage; **do not use them for normal deployments** — keep
using the multi-arch `vX.Y.Z` or `latest`, and Compose and Watchtower pick the image that
matches the host architecture automatically.

Log in to the CNB registry (`CNB_TOKEN` is issued by the CNB pipeline; locally use an access
token instead; feed the password on stdin so the token never lands in shell history or logs):

```bash
printf '%s' "$CNB_TOKEN" | docker login docker.cnb.cool -u cnb --password-stdin
```

This repository declares no fixed CNB slug; `<CNB_REPO_SLUG_LOWERCASE>` is a placeholder and
must not be treated as an existing public repository.

### Version tags, automatic release and permissions (v*)

Pushing a `v*` version tag (e.g. `v1.2.3`) triggers the trusted release flow:

1. **Test gate:** Go `test` / `race` / `vet` / `build` plus the Python release-tool tests run
   first; a failure publishes nothing.
2. **Image:** publishes `ghcr.io/azurekiln2333/codearts2api:v1.2.3`; a stable version (no `-`
   prerelease suffix) **also** updates `latest`.
3. **Source release:** creates the matching GitHub Release with the explicit-manifest source
   ZIP and its sha256 file (`scripts/release-source.py`).
4. **Prerelease:** `v1.2.3-rc.1` and similar are marked prerelease only and **never overwrite
   `latest`**.
5. **Pull requests** validate only and never authenticate or publish; workflows use minimal
   permissions (`contents: read`, gaining `packages: write` / `contents: write` only to publish).

The CNB side follows the same rules: `main` / `pull_request` validate only, and only a `v*` tag
publishes the source Release and image. CI uses the platform-issued temporary token
(`CNB_TOKEN`); no real credentials are stored in the repository.

> The above describes the **behaviour of the repository's workflows**. Until you push a tag and
> let the remote pipeline run, neither the image nor the Release exists. This document does not
> claim any remote run has happened.

CNB uses the platform repository path and login by default, without registry overrides. Configure custom publishing targets in **CNB repository CI variables**: `CNB_IMAGE` (a bare image repository without a tag), `CNB_REGISTRY`, `CNB_REGISTRY_USER`, and `CNB_REGISTRY_PASSWORD`. External registries require a password. Prefer `CNB_IMAGE` to avoid confusing it with the tagged or digest-pinned deployment `CA2A_IMAGE` in the server `.env`. Release tags support only `vMAJOR.MINOR.PATCH[-prerelease]`, without `+build` metadata; the length must also accommodate architecture suffixes.

### Rollback

Image updates roll back independently; the data and credential formats are additive and need no
migration:

```bash
# Record the running image ID for an exact rollback
docker inspect --format '{{.Image}}' codearts2api

# Roll back to a pinned version or digest
CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api:v1.2.2 docker compose pull
CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api:v1.2.2 docker compose up -d
# Or by digest: CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api@sha256:<digest>
```

Keep the **same set of mounted directories** (`.env`, `config.json`, `auths/`, `data/`) when
rolling back; never switch to an empty directory. If you do not want an automatic update to
undo the rollback, disable Watchtower first (above) and then roll back.

### Preserved mounts and credentials (upgrade safety)

Compose reads application variables directly via `env_file: .env`; set only `CA2A_PORT`
(default `7866`) and it drives the host port, the container port and the listener, with
`CA2A_LISTEN` generated by Compose. Host mount paths use `CA2A_AUTHS_PATH`, `CA2A_DATA_PATH`
and `CA2A_CONFIG_PATH`; `CA2A_AUTH_DIR` and `CA2A_STATE_FILE` are container-internal
application paths. Keeping the original mount paths on upgrade keeps already-added credentials
usable.

The YAML keeps only three `environment` exceptions (the mandatory API key check, the timezone
default and the derived listener); everything else goes in `.env`. Non-empty values override
`config.json`, while unset or empty values fall back to the config file and built-in defaults.
Application variables merely exported in the shell are not forwarded automatically. `.env` must
exist; CLI `--env-file` changes only the Compose interpolation input, not the fixed
`env_file: .env` in the YAML.

Before upgrading, back up `.env`, `config.json`, `auths/` and `data/` and record the running
image ID; keep the original directories and mount paths. An upgrade only recreates the
container and never requires re-importing credentials. If `config.json` explicitly set
concurrency `5`, that still applies; to validate upstream quota for the first time, set
`CA2A_MAX_CONCURRENT=1` in `.env`.

### systemd deployment (without Docker)

```bash
sudo mkdir -p /opt/codearts2api && sudo cp -r bin config.example.json auths /opt/codearts2api/
sudo cp deploy/codearts2api.service /etc/systemd/system/
# Edit /opt/codearts2api/.env to set CA2A_API_KEY, and adjust config.json
sudo systemctl daemon-reload && sudo systemctl enable --now codearts2api
```

### Full source package and deployment verification

After committing, Python 3.9+ can export a complete source package: only the explicit product
manifest from a specific Git commit, including Compose, the build overlay, the Dockerfile,
example config, Go sources, CI/release inputs and the verification scripts, while excluding the
real `.env`, `config.json`, credentials, data and workspace tooling. The same commit produces
the same ZIP and existing files are never overwritten:

```bash
python3 scripts/release-source.py --commit HEAD
```

The artifact is `codearts2api-source-<first 12 chars of commit>.zip`, accompanied by
`release.json` file hashes and a `release.env` containing only the source commit. The package
has no `.git`; extract it in the existing deployment directory, keep the current mount paths,
and build locally using the packaged identity:

```bash
set -a
. ./release.env
set +a
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

Deployment verification takes the key from an environment variable and sends only GETs: it
checks health, the actual commit, the cleaned console and the audit/model/connection APIs; an
old page, 404, 401, unknown identity or mismatched commit exits non-zero. If a reverse proxy
adds a path prefix, include it in `--url`. Replace the domain below with your own service
address:

```bash
read -r -s -p "service API key: " CA2A_API_KEY
export CA2A_API_KEY
python3 scripts/verify-deployment.py --url https://gateway.example.com --expected-commit "$CA2A_BUILD_COMMIT"
unset CA2A_API_KEY
```

The verifier starts no chat, model probe or refetch; it explicitly reports when audit is
disabled. It verifies the delivered revision and interfaces — real model entitlements still
depend on source results and real calls.

See [docs/deployment.md](docs/deployment.md) (extended operations guide, Simplified Chinese) for
more on pulling, upgrading, automatic updates, private registry login and rollback.

## Request audit and account status

The console's "request audit" shows each recent request's model, selected account, HTTP status
and outcome, safe provider error code, attempt count, and queue/first-data/total durations. The
`X-Request-Id` response header ties service logs to audit records. `GET /admin/api/requests` and
`GET /admin/api/audit` use the existing Bearer key; the request list supports `limit` (max 200),
`before` (sequence cursor), `model`, `account`, `outcome`, `status` and `since` filters.

Audit is retained for 7 days by default, at most 8 files of 8 MiB (current file included, total
bounded at 64 MiB), with a queue of 256 and a recent window of 512 records. A restart restores
recent records and the sequence; new requests can push the oldest window entry out of paging.
Older retained files live in `<state_file directory>/audit/` for local archiving. A full queue
or failed disk write never blocks on I/O; the console shows dropped and write-failure counters.
Audit stores metadata only — never prompts, completions, tool arguments, headers, keys or login
credentials — and a shared service key cannot distinguish individual New API end users.

"Connection logs" show in-flight requests: current stage, stage/total duration, account, attempt
count and the actual last upstream read progress, distinguishing waiting for an account, waiting
for credential refresh, waiting for upstream headers, waiting for the first SSE data,
forwarding/aggregating and writing back. The first SSE data is not the first generated token, and
a long stream is not by itself evidence of a stall. The page polls `GET /admin/api/connections`
about every 2 seconds only while signed in and in the foreground; leaving the page, backgrounding
or signing out stops it.

The benefit catalog first queries that account's benefit-channel switch. When upstream explicitly
reports it closed, the console shows "benefit channel closed", skips the legacy benefit claim and
catalog request, and revokes that account's old benefit routes; model identity follows what the
Agent / built-in catalog actually returns. A failed or ambiguous switch query keeps the existing
evidence and shows a failure — it never guesses that all accounts are closed. An HTTP success with
a non-success benefit business code shows a distinct business error instead of a generic HTTP
error, and business code `4000` alone is not treated as a global closure.

Activity tracking covers at most 512 requests with at most 32 stages each; overflow and truncation
show cumulative counters. Finished records reuse the audit recent window, and older records
without stage history explicitly show as unavailable. Disabling request audit also disables
connection tracking.

Each account's schedulable / cooling / disabled state is shown separately from its failure
history. `err N` is the consecutive-failure count; by default three failures cool the account for
10 minutes, and a natural expiry does not clear the historical count. A real success clears the
already-stale current error and persists once, keeps the failure history, and never revokes an
active cooldown or a manual disable. Failure times from before an upgrade cannot be recovered and
show as "time unknown". Check-in credits are not the same as a specific model's benefit
entitlement; `benefit not found` still means you must check that model's entitlement.

## Environment variables

In addition to `config.json`, the following environment variables are supported:

| Variable | Description | Default |
|----------|-------------|---------|
| `CA2A_API_KEY` | API access key | - |
| `CA2A_LISTEN` | Listen address | `:7866` |
| `CA2A_AUTH_DIR` | Credential directory | `./auths` |
| `CA2A_STATE_FILE` | State file | `./data/state.json` |
| `CA2A_DEFAULT_MODEL` | Default model | `glm-5.2` |
| `CA2A_OAUTH_CALLBACK_HOST` | OAuth callback port override (legacy name; only an explicit port applies, the callback host stays the browser's own `127.0.0.1`) | Follows the service port |
| `CA2A_WATCH_ENABLED` | Scheduler switch | `true` |
| `CA2A_WATCH_POLL_MINUTES` | Poll interval (minutes) | `30` |
| `CA2A_WATCH_REFRESH_SKEW` | Early refresh window (minutes) | `30` |
| `CA2A_WATCH_KEEPALIVE_INTERVAL` | Keepalive interval (minutes) | `15` |
| `CA2A_MAX_CONCURRENT` | Max active requests per account | `1` |
| `CA2A_MAX_PENDING` | In-process waiting cap (also caps total in-flight at capacity + this value) | `64` |
| `CA2A_STARTUP_TIMEOUT_SECONDS` | Budget from reading the request to first upstream data (seconds) | `90` |
| `CA2A_ACCOUNT_WAIT_SECONDS` | Cumulative slot wait budget per request (seconds) | `10` |
| `CA2A_UPSTREAM_TIMEOUT_SECONDS` | Upstream headers/first data/read idle timeout (seconds) | `60` |
| `CA2A_STREAM_WRITE_TIMEOUT_SECONDS` | Single downstream write/flush timeout (seconds) | `30` |
| `CA2A_QUEUE_RETRY_SECONDS` | Upstream queue/TPM retry interval (seconds) | `2` |
| `CA2A_QUEUE_MAX_ATTEMPTS` | Total attempts per account (first included) | `3` |
| `CA2A_KEEPALIVE_WINDOW` | Keepalive window | `10m` |
| `CA2A_BENEFIT_AUTO_CLAIM` | Auto-claim when the benefit channel is explicitly enabled (idempotent; no proactive claim when off) | `true` |
| `CA2A_CREDITS_AUTO_CLAIM` | Watchdog auto-claims daily check-in and new-user credits | `true` |
| `CA2A_LOGIN_CLIENT_ID` | OAuth client_id used by Web UI login | Existing accounts' value, otherwise `codearts-agent` |
| `CA2A_AUDIT_ENABLED` | Enable request metadata audit | `true` |
| `CA2A_AUDIT_DIR` | Audit directory inside the container | `<state_file dir>/audit` |
| `CA2A_AUDIT_MAX_QUEUE` | Non-blocking audit queue capacity | `256` |
| `CA2A_AUDIT_RECENT` | Queryable recent records | `512` |
| `CA2A_AUDIT_MAX_FILE_BYTES` | Max size per audit file (bytes) | `8388608` |
| `CA2A_AUDIT_MAX_FILES` | Max file count (current file included) | `8` |
| `CA2A_AUDIT_MAX_TOTAL_BYTES` | Max total size of all audit files (bytes) | `67108864` |
| `CA2A_AUDIT_RETENTION_DAYS` | Audit file retention (days) | `7` |
| `CA2A_AUDIT_FLUSH_INTERVAL` | Audit file sync interval | `1s` |

Deployment variables (Compose interpolation) are listed in `.env.example`: `CA2A_PORT`, `TZ`,
`CA2A_CONTAINER_NAME`, `CA2A_RESTART_POLICY`, `CA2A_AUTHS_PATH`, `CA2A_DATA_PATH`,
`CA2A_CONFIG_PATH`, `CA2A_IMAGE`, `CA2A_WATCHTOWER_CONTAINER_NAME` and
`WATCHTOWER_POLL_INTERVAL`, plus the local-build-only `CA2A_BUILD_CONTEXT`,
`CA2A_BUILD_COMMIT`, `CA2A_BUILD_TIME` and `CA2A_LOCAL_IMAGE`.

## Directory layout

```
cmd/server/        HTTP service (config + main)
cmd/login/         Huawei Cloud OAuth2 PKCE login
cmd/credit/        Account login-state report (with concurrency info)
cmd/apply/         Batch token renewal (uses the pool package)
cmd/models/        Show models available to accounts (built-in + benefit, -claim to claim)
internal/auth/     auth file read/write
internal/upstream/ Cloud client (login/chat/SSE/model discovery) + reverse-engineered constants
internal/pool/     Account pool (token validation/auto refresh/cooldown/concurrency)
internal/scheduler/ Token renewal watchdog (keepalive included)
internal/server/   OpenAI-compatible routing
internal/audit/    Bounded request audit and live connection tracking
internal/telemetry/ Request stage and read-progress observer
scripts/           Source packaging, release metadata and read-only deployment verification
deploy/            systemd unit example
docs/              Reverse-engineering notes, API inventory and deployment guide
```

## Disclaimer

For learning and research only. Users must comply with the Huawei Cloud terms of service and
accept the risks themselves.

## License

MIT
