# Journal - lichengyu (Part 1)

> AI development session journal
> Started: 2026-10-17

---



## Session 1: Fix DeepSeek timeouts and account concurrency
<!-- trellis-session: v=2 fp=e722f7872c5481ca -->

**Date**: 2026-10-21
**Task**: Fix DeepSeek timeouts and account concurrency
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

Fixed cancellable startup and upstream idle budgets, SSE completion, fair bounded account admission, retry lifecycle permits, lease cleanup, and refresh/reload safety. Existing Pi reviewed through Herdr and fixed background probe ownership. Full tests and race, vet, build, Linux compilation, and before/after reproductions passed. Credential schema and persistent deployment paths remain compatible. User approved the local commit; no production deployment or push.

### Git Commits

| Hash | Message |
|------|---------|
| `3d87a5c` | fix: bound chat lifecycle and concurrent account admission |

### Status

[OK] **Completed**


## Session 2: Production concurrency repair and request audit
<!-- trellis-session: v=2 fp=961068a52a252ddf -->

**Date**: 2026-10-21
**Task**: Production concurrency repair and request audit
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

Implemented capability-aware bounded failover, metadata audit and truthful account diagnostics with Pi; completed independent review and local verification.

### Main Changes

- Added authenticated request audit APIs/panel, bounded nonblocking storage, safe failure causes and request-ID correlation.
- Preserved lifecycle leases, credential mounts and state compatibility; forwarded Compose env/port and build identity.

### Git Commits

| Hash | Message |
|------|---------|
| `3fefda8` | fix: improve capability failover and add request audit |

### Testing

- [OK] Full Go test/race, vet, build and diff/format checks passed; independent 18-request localhost wave restored all capacity.
- [OK] Actual Compose config, embedded JavaScript fixture, isolated executable/auth/API verification and Linux amd64 build passed.

### Status

[OK] **Completed**

### Next Steps

- Deploy intended build with existing auth/data mounts, verify reported build/effective limits, then evaluate production traffic; no push or deployment performed.


## Session 3: New API inspired admin panel redesign
<!-- trellis-session: v=2 fp=3f0b6997ddd546d6 -->

**Date**: 2026-10-21
**Task**: New API inspired admin panel redesign
**Branch**: `fix/deepseek-concurrency-20261021`

### Summary

Rebuilt the embedded admin panel as five workspace views with mobile navigation and light/dark themes. Preserved account, OAuth and audit contracts; independent review fixed failed-filter cursors, logout refresh races, exact model copying and schedule unknown states. Final synthetic localhost Chrome suite, targeted Go tests, vet/build, script checks and diff checks passed. UI task archived; no push or deployment.

### Git Commits

| Hash | Message |
|------|---------|
| `42fc19f` | feat: redesign admin panel with New API inspired workspace |

### Status

[OK] **Completed**
