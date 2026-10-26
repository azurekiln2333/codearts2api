# Research: actual CNB remote Docker build

- Query: Inspect real CNB push/build failure and image trigger for azurekiln/codearts2api.
- Scope: mixed
- Date: 2026-10-26

## Findings

Public CNB build-list SSR __NEXT_DATA__ exposes actual build metadata without API authorization. Main commit75ed55c281ca6f7eef8c643d66a4001b78124fc3 ran cnb-7br-1k5rnfjli at2026-10-26T09:08:23Z. Result:error after65.6s. Pipeline verify, prepare-tools succeeded; go-test failed. Remaining race/vet/build/Python checks pending; source export skipped.

Exact failure: internal/audit/audit_test.go:197, TestOverflowStorageFailureAndClosedSinkRemainNonBlocking: full queue blocked or became unbounded: elapsed=6.556017ms stats={Enabled:true Dir: Queued:1 Written:0 Dropped:9999 WriteFailures:0 Rotations:0 Recent:2 MaxQueue:0 MaxFiles:0 MaxFileBytes:0 MaxTotalBytes:0 RetentionDays:0}. CNB has not reached Docker; main validation fails the audit test.

.cnb.yml:12 main push only validates/exports. .cnb.yml:56 version tag_push starts source Release and :112 publish-image with docker service. The existing main push cannot publish an image. There is no api_trigger event, unlike reference D:/My2KProjects/cnb2api-panel/.cnb.yml:42 (which aliases verification). A validated version tag is the current publication trigger; no tag was created in this research.

Official CNB grammar services/docker docs explicitly state multi-architecture builds work by default using docker buildx without extra configuration. This supports final Alpine target RUN execution on the platform Docker service. Actual tag run is still needed to verify build and registry behavior.

## Files and patterns

- .cnb.yml:16: golang1.27-alpine runtime and main verify gate.
- .cnb.yml:114: Docker service for image job.
- .cnb.yml:145: per-architecture buildx --load, push arch refs, imagetools combine.
- internal/audit/audit_test.go:197: real Linux runner failure.
- .trellis/spec/backend/source-release.md: distinguish source/local and registry proof.

## External references

- https://cnb.cool/azurekiln/codearts2api/-/build/logs
- https://cnb.cool/azurekiln/codearts2api/-/build/logs/cnb-7br-1k5rnfjli
- https://docs.cnb.cool/en/build/grammar.html (services/docker multi-architecture)
- https://api.cnb.cool/swagger.json (official API schema)

## Caveats / Not Found

Public API build/logs returns401. Stored Git credential can push but build-history API returns403; no credential printed or persisted. Public SSR supplies exact failure metadata without credentials. API start needs repo-cnb-trigger:rw and event api_trigger; existing config has no such event. No remote triggering/publication attempted. Parent owns repair and tag/build verification.

## Public monitoring helper

Role write boundary permits only this task research directory, so helper is `research/cnb-ci-status.py` instead of `.local-test`. Parent may copy it into excluded scratch if preferred. Tested against real failed run and correctly extracted pipeline/stage/job stderr.

Call: `python -B .trellis/tasks/10-21-live-connection-logs-delivery/research/cnb-ci-status.py` (latest), optionally `--sha COMMIT_PREFIX` or `--sn cnb-...`. Standard-library only, no Git credentials, cookies, API calls or private tokens. Fetches public SSR metadata only; outputs concise JSON with pipeline/stage statuses and failing job stderr capped12000 characters.
