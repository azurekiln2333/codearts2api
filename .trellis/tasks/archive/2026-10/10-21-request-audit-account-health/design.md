# Technical proposal

Introduce a dedicated bounded audit sink with one process-local writer and a bounded recent index. Completion enqueues immutable size-limited metadata outside pool critical sections after releasing request/account resources. Overflow increments a visible drop counter; I/O failure increments a visible persistence counter. Neither can hang requests.

Share one schema across server, authenticated API and embedded panel. Assign/validate a request ID at entry and echo it in a response header. Capture classified provider errors before generic cooldown labeling. Do not copy request/response objects or raw errors into records; bound and scrub text.

Persist under the existing data mount. Defaults: 7 days, eight files including the active file, 8 MiB per file, 256 queued events, 512 recent events; reload the recent window on startup. The authenticated panel exposes this recent window, with limits and older on-disk archive behavior stated explicitly. A single API key cannot attribute separate end users. Current schedulability and last-failure history must be separate; new state fields are additive and old auth JSON stays unchanged.

Keep automatic cooldown expiry and manual Enable/ClearCooldown policy intact. Clear obsolete current failure state and persist once on an actual error-to-success transition, without overriding active cooldown/manual disable or adding a disk write for every normal successful request. Historical failure timestamps remain separate; legacy timestamps are unknown, never invented.

Only the audit writer performs audit I/O, outside pool locks and resource cleanup. Status must distinguish no committed response from an actual HTTP 200; first-data timing is nullable. Store provider code and fixed safe classification text rather than unbounded raw provider errors. All model/account/cause/tried fields have explicit length/count limits. Runtime diagnostics show actual limits and an explicit known/unknown build identity; a full per-value provenance subsystem is outside this scope.

Likely boundaries: new internal/audit package; server chat/handler/admin wiring; pool diagnostics; embedded panel; cmd/server config and examples. Pi proposes exact ownership, defaults and tests before implementation.
