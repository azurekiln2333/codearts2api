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
