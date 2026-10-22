# Repair model discovery, simplify the console and add live connection logs

## Goal

Make the deployed gateway's admin console useful for operating real models and diagnosing slow requests: discover models seen in the official CodeArts client, refresh the catalog on demand, explain account/model exclusions, provide live connection diagnostics, and remove redundant presentation.

## Background and confirmed evidence

- The user approved task creation and continued implementation, and identified the previously supplied production site as their console. On 2026-10-21 public read-only checks initially found the old panel and 404 audit routes. The user subsequently rebuilt the image and reports that the redesigned panel is now present; the earlier public check is historical, not evidence of its current state.
- `internal/server/chat.go:523` generates the reported `model_not_available` 404 after cached candidate filtering and model-capability rejection. The response does not identify the actual account/source or reason for exclusion.
- `internal/server/availability.go:30` caches negative account/model conclusions for 30 minutes. Discovery and negative availability are separate caches.
- `internal/upstream/models.go:292` merges three model sources sequentially; individual failures are logged but not surfaced to the admin. Research confirms source failures can erase known catalog/benefit routes, removed benefit entries can resurrect, and returned results can differ from retained account data (`models.go:126-153`, `303-328`). Exact production source responses for DeepSeek V4.1 Flash are not available yet.
- `internal/server/handler.go:96` uses a one-hour global catalog cache and five-minute failure backoff. The panel's refresh fetches overview, which is distinct from forcing upstream discovery.
- `internal/server/admin.go:27` exposes hard-coded service/region labels. The panel displays these in its header, repeats branding/build details in a sidebar footer, and adds a redundant overview shortcut card (`panel.html:226`, `245`, `266`).
- `internal/server/handler.go:436-446` retains the original model list when filtering excludes every item, so the list can advertise models that chat routing excludes.

## Requirements

- R1: Correct discovery, source parsing/partial-result handling and per-account benefit routing wherever repository tests or current upstream evidence demonstrates a gap. The user's reported target is `deepseek-v4.1-flash`; no guessed alias, entitlement or substituted model is acceptable.
- R2: Provide a real authenticated catalog refresh operation and a model-directory button. It must actually refetch upstream sources, expose per-account/source results and freshness, bound time/concurrency and prevent overlapping refresh jobs. A browser refresh or static fallback is not a successful pull.
- R3: Make discovery versus callability clear. Show sanitized reasons for model exclusion, failed sources and cached negative conclusions. Successful discovery must not falsely mark a model callable; refreshing valid metadata must offer a bounded way to recover from stale negative capability without a 30-minute wait or container restart.
- R4: Remove the entire lower-left branding/build block, header service-region caption and overview shortcut card. Remove associated unused CSS, DOM updates and duplicate handlers. Build metadata stays available through the existing authenticated diagnostic API and unobtrusively in runtime settings.
- R5: Reduce rounded corners using a restrained MD3-inspired scale: controls/navigation 8 px, cards 12 px and major shells/dialogs at most 16 px. Avoid oversized pills, including on mobile, while preserving functional focus, layout and theme behavior.
- R6: Add a dedicated connection-log view displaying active request phase/age, actual upstream progress and bounded completed phase histories. Distinguish account waiting, credential waiting, upstream response headers, first SSE data and streaming/aggregation; do not label local chat-ID generation as an upstream session request or infer that a healthy long stream is stuck.
- R7: Produce a complete source release and reproducible read-only verification instructions for the actual deployed routes/UI. Do not overwrite existing credentials, configuration, data or the user-owned ZIP. Do not claim remote deployment without access and verification.

## Acceptance criteria

- [x] AC1 / R1: Synthetic source fixtures include a dynamically returned V4.1 Flash model. Exact ID and per-account route metadata survive merging and refresh; partial source failure cannot silently erase previously confirmed usable metadata.
- [x] AC2 / R2: Clicking the catalog pull button performs upstream discovery once, reports loading and completion/partial failure, and updates models/freshness without server restart. Concurrent clicks/requests share bounded work; authentication, cancellation and deadlines are tested.
- [x] AC3 / R3: A cached rejection is inspectable by account, reason and expiry. A refresh/recheck can recover stale negative state without globally clearing unrelated capability history. Actual provider denial remains an honest failure.
- [x] AC4 / R4-R5: Deleted content is absent after login, reload, refresh and navigation. Desktop/mobile screenshots show the reduced radius scale, no leftover blank card or JavaScript errors, and working account/OAuth/audit flows.
- [x] AC5 / R6: Stalled upstream fixtures remain visible while running and leave accurate completed traces after resource cleanup. Live tracking, phase points and polling are bounded; logs contain no message bodies, headers, keys or credentials.
- [x] AC6 / R7: Relevant Go tests, race tests for backend changes, vet, build, browser checks and diff checks pass. Release includes Compose, Dockerfile and example configuration, excludes live auth/data/secrets, and identifies the actual source revision.
- [x] AC7 / deployed benefit follow-up: A strictly validated per-account channel switch gates legacy benefit calls. Explicit closure removes old contributions/seed routing, skips claims/config and appears as confirmed closed; unknown/failing state retains prior evidence. HTTP-success business failures remain distinct from real HTTP errors. Actual normal-route chat, channel transitions, source retention, account isolation and safe diagnostics are verified with localhost fixtures; production account results remain pending.

## Scope and compatibility

Retain the Go-embedded panel, API-key authentication, existing account/OAuth actions, audit API and credential/data mounts. Keep Go 1.22 source compatibility. Do not rewrite the frontend framework, add decorative dashboard content, fake model availability, bypass account authorization, run another production 18-request load test, or alter unrelated scheduling policy.

## Deferred operational evidence

Server/deployment access is not available. Remote deployment verification and the exact official-client response for V4.1 Flash require operational evidence; local implementation and deterministic checks do not depend on that access. The final scope was presented and the user's subsequent 2026-10-22 direction authorized execution. No further user product choices are required for the listed scope.
