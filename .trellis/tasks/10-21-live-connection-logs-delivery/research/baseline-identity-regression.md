# Baseline inference identity regression

The historical b9b318c source ZIP was not present in the current workspace.
An isolated extraction of Git commit
`b9b318ccc026426f593af09cc66eaea62cd19479` supplied go.mod and internal/ under
Git-excluded `.local-test/baseline-identity`. Only the new server
`live_identity_test.go` was copied onto that baseline. Historical archives were
neither modified nor recreated.

`go test ./internal/server -run '^TestLiveIdentity' -count=1` failed on that
baseline on 2026-10-23. The real localhost gateway sent GLM-5.3 for requested
glm-5.3 and DeepSeek-V4.1-Flash for requested deepseek-v4.1-flash. The fixture
required the captured actual lowercase IDs and returned provider registration
rejection; gateway emitted HTTP404. Later mixed-case requests had zero actual
sends because the first rejection populated their shared negative cache.
Explicit ArkTS display alias and builtin-freshness assertions also failed.

The identical tests pass against the current repaired identity worktree.
Both runs use synthetic credentials and local HTTP fixtures, not user credentials
or a modified provider. This proves the tests distinguish the confirmed defect;
real upstream content evidence is recorded separately in live-account-findings.
