# Native implementation ownership and integration contract

The user approved the planning summary and expressly requested robust scheduling/concurrency for many New API users. The default-mode work may now proceed after activation. Agents share this checkout; do not revert or overwrite other agents' changes. No production inference, deployment, credentials changes, git commit, or push.

## File Ownership

- Upstream implement agent: `internal/upstream/client.go`, `internal/upstream/sse.go`, optional new `internal/upstream/stream_body.go`, and upstream regression test files. Own SSE terminal handling and bounded/cancelable streaming inactivity. Do not edit pool/server/config/docs files.
- Pool implement agent: `internal/pool/pool.go`, optional new `internal/pool/admission.go`, and pool regression test files. Own fair bounded admission/leases, context-aware validation/refresh, and initial/runtime upstream timeout propagation. Do not edit upstream/server/config/docs files.
- Server implement agent: `internal/server/handler.go`, new server lifecycle helpers/tests, `cmd/server/config.go`, `cmd/server/main.go`, config tests, README, `config.example.json`, and `.env.example` if needed. Own startup/write budgets, queue retry/health/error cleanup, cached model routing, wiring, docs, and local concurrent integration tests. Do not edit upstream/pool files.
- Parent coordinates integration and updates Trellis planning/spec artifacts; a separate check agent reviews the full result after implementation.

Approved minimal ownership extensions: pool worker also updates `internal/pool/checkin.go` and `internal/scheduler/scheduler.go` for context-aware refresh/validation and AuthSnapshot; server worker updates corresponding availability/discovery/login auth reads. All other ownership boundaries remain in effect.

## Shared Pool API

Keep existing public APIs as compatibility wrappers for current commands/tests. Add the following Go-compatible concepts (notify the parent before changing signatures needed by another agent):

```go
// pool.Config additions, effective for initial accounts and add/reload.
MaxPending      int           // default 64 waiting chat requests process-wide
UpstreamTimeout time.Duration // default 60s; use upstream.New for account clients

type AccountCandidate struct {
    Name     string
    Priority int // lower wins among currently available candidates
}
type Lease struct {
    Account *Account
    // private bookkeeping
}
func (l *Lease) Release() // idempotent, release actual acquired account pointer
func (p *Pool) AdmitRequest(ctx context.Context) (release func(), err error)
func (p *Pool) AcquireAccount(ctx context.Context, candidates []AccountCandidate, wait time.Duration) (*Lease, error)
func (p *Pool) ValidateContext(ctx context.Context, a *Account) (bool, error)
func (p *Pool) RefreshTokenContext(ctx context.Context, name string) error
func (a *Account) AuthSnapshot() *auth.Auth

// Package-level sentinel errors suitable for errors.Is.
ErrQueueFull
ErrNoHealthyAccount
ErrAcquireTimeout
```

AcquireAccount atomically checks health/capacity and reserves one slot; it first considers all free candidates rather than blocking on the first account. Prefer lower priority, then lower active/max utilization, then round-robin ties. Check context before reservation and on notification/wait. The bounded waiter count belongs to the Pool, not each request or handler. Queue limits, wait expiry, all-ineligible accounts, canceled waiters, lease double-release, account disable/remove/reload, and concurrent wakeups must be covered. Keep pooled account pointers stable across normal reload; removed old leases must never release a newly added replacement.

AdmitRequest is an outer lifecycle permit acquired at chat entry and retained until the full handler returns. The shared Pool caps admitted handlers at enabled account capacity plus MaxPending, so fast provider429/retry-sleep cycles cannot bypass the waiter cap. Reject a full cap immediately with ErrQueueFull and Retry-After. The returned release is idempotent and the entry context is checked. Test ticket capacity, cancellation/release, shared handlers, and rapid429 retry sleeping under concurrent load.

Do not call server callbacks while holding pool locks. The server creates cached candidate lists without network discovery. Existing single-account acquisition helpers remain compatible; health checks and short admin reads must not be blocked by network I/O under pool mutexes. Credential-refresh serialization must allow canceled chat callers to stop waiting without unbounded goroutines or counting cancellation as an auth failure.

## Shared Upstream Contract

Keep `upstream.New(timeout)` and `NewWithChatEndpoint(timeout, endpoint)` signatures. The provided timeout must protect response headers, first SSE body data/preflight, error body reads, and idle body reads. Active SSE streams can last longer than that timeout while bytes continue arriving. Cancellation/Close must unblock I/O, stop timers, and clean up without leaked per-read goroutines. Use Go 1.22-compatible standard library features. Provide a recognizable timeout error implementing `net.Error` or wrapping `context.DeadlineExceeded` so the server can return 504 rather than retry/cool an account as a generic failure.

SSE aggregation/streaming must stop at literal DONE, named terminal events, and CodeArts JSON DONE without another read. Apply usage/tools/content/error before terminating; never treat finish_reason alone as a terminal marker. On a still-writable stream, a body read error should emit a terminal SSE error/DONE and return the actual error. Do not mask downstream write errors as successful upstream completion.

## Server Defaults and Contract

- Default startup budget 90s shared across admission/validation/retries/rotation/preflight, stopped after first data/preflight success; caller cancellation remains wired through active streams.
- Default account wait 10s, queue capacity 64 (pool-wide), queue retry delay 2s, total attempts 3, stream write deadline 30s, upstream inactivity 60s, per-account active capacity 1.
- Config/environment overrides use existing project style. Existing explicit upstream/concurrency values remain honored. Do not cut active generation off after the startup timeout once it is producing data.
- Saturation is explicit/retryable with `Retry-After`; full waiter queue returns promptly. Startup/upstream timeout before response headers is 504. Caller cancellation/downstream failure does not cool accounts or trigger rotation. Genuine upstream API failures keep appropriate existing treatment.
- Acquire a lease per real attempt; release before retry delay, set local ownership to nil, reacquire through the bounded scheduler, and release only the currently held lease. Close upstream body on every exit.
- Prefer cached eligible model accounts; unknown catalogs remain optimistic. A model-level failure is scoped to that account and must allow another eligible account to succeed before globally returning model unavailable.
- Derived affinity applies only with explicit body/header affinity or actual assistant/tool conversation history. Independent identical first-turn prompts remain fairly allocated. A positive model-availability cache entry does not boost an unknown-catalog account's priority above other unknown accounts.
- Add deterministic concurrent local tests that hold upstream streams open, fill all active slots and a small configured waiting queue, then verify prompt overload rejection, cancellation drain, fair distribution, limit invariants, healthy accounts, responsive health endpoints, and subsequent success. Use only fake auth and httptest servers.

## Toolchain

Native temporary Go executable: `D:\AppData\Local\Temp\codearts2api-toolchain-go1.27.3\go\bin\go.exe`. Baseline suite already passed. Add its `bin` directory to task-specific process PATH if helpful. Keep module compatibility with Go 1.22. Race compiler is now available at `D:\AppData\Local\Temp\codearts2api-race-compiler\mingw64\bin\gcc.exe`; native race smoke has passed. Parent coordinates the final full race suite.
