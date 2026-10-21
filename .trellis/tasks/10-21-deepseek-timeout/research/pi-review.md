# Independent review: existing Pi through Herdr

Reviewer: existing Pi `w1C:p4`, project cwd `D:\My2KProjects\codearts2api`.
The user explicitly requested this delegation. Pi was observed working after the
brief and reached Herdr `done` state at completion. No additional native check
agent was dispatched. Role/context instructions are in `herdr-check-brief.md`.

## Finding and fix

**P1: background probe slot ownership across account removal/re-add.** Chat used
owned leases, but `availability.go:probeModel` retained the legacy name-based
lock/release. An old probe could release the replacement account's slot and
allow concurrent allocation beyond its configured capacity. Pi migrated probes
to `AcquireAccount`/`Lease.Release` and rejects a mismatching acquired account
object. The permanent regression fails with the former implementation restored.

Pi also added `TestChatStartupTimeoutDuringRefreshWaitReleasesEverything` to
validate the full handler path, rather than only the refresh/pool helpers.

## Acceptance review

| Criteria | Result | Boundary evidence |
|---|---|---|
| AC1 | Pass | Canceled admission/preflight/retry/refresh and body consumption release promptly; no health penalty/failover |
| AC2 | Pass | Free account selected immediately, cached capabilities, stale/explicit/history affinity |
| AC3 | Pass | Silent preflight and later idle/body failures bounded, slow-body real HTTP 504, capacity reuse |
| AC4 | Pass | Terminal markers stop without EOF; error-over-DONE; trailing usage/tools preserved |
| AC5 | Pass | Active long streams exceed startup/idle durations while data progresses |
| AC6 | Pass after probe fix | Failed reacquisition, idempotent/reload leases, background probe replacement ownership, error classification |
| AC7 | Pass | Config/env/default consistency and initial/add/reload propagation |
| AC8 | Pass | Full tests/vet/build; affected-package race; parent additionally ran full race |
| AC9 | Pass | Shared-handler HTTP saturation, retry-sleeper ticket cap, fair capacity reuse, responsive health |

## Remaining boundaries

- An upstream inactivity timeout returns 504 immediately; it does not initiate
  another account attempt. Slot selection remains fair for later requests.
- `finish_reason` alone cannot terminate consumption because usage may follow;
  a provider omitting a real terminal marker and EOF is bounded by inactivity.
- Limits are per Pool/process. Multiple replicas sharing accounts must divide
  provider capacity between replicas.
- The existing first-user-message-derived conversation ID can collide for
  unrelated later histories sharing that anchor. Explicit per-session affinity
  provides distinct keys; independent first turns remain fair.
- Production gateway settings, initial saturation/network trigger, and exact
  model mapping remain unverified. These local corrections are not a deployment
  or proof of provider capacity.

Pi reported no remaining acceptance blocker. All changes remained unstaged and
uncommitted. Parent reran full race/vet/build/diff validation after the fix.
