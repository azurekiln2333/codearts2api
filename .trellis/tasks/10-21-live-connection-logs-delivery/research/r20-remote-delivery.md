# R20 remote CI and image delivery — 2026-10-26

## Repair

Source commit `ea9642229ed6dd27c87e65fa32e3b470f5e9875d` is synchronized to main on GitHub, GitCode, Gitee, GitLab and CNB. Test-only correction normalizes audit retention fixture and preserves all queue/nonblocking/storage/close assertions. Independent review and full Go normal/race/vet/build pass. The Ubuntu26 migration annotation was only a notice; it did not cause failure.

## Actual remote evidence

- GitHub main run40304104020: success, all Go/Python validation steps pass.
- CNB main cnb-lfc-1k5rqgbgs: success, verification and source export pass.
- Trusted prerelease `v0.1.0-rc.1` created at the tested source commit (not moved/replaced).
- GitHub tag run40304404431: test/release/image all success. Source ZIP and checksum attached to prerelease.
- CNB tag cnb-u3q-1k5rqkmo6: publish-image success; source Release created and attachment upload monitored separately below.
- Anonymous Registry requests retrieve both image indexes and both Linux platform configs. Each image has amd64 and arm64, and each platform config reports exactly the tested source commit. Attestation descriptors with unknown platform are not executable target images.

- `docker.cnb.cool/azurekiln/codearts2api:v0.1.0-rc.1` — `sha256:5d10c56e872cf412e8e06e8f3d18daeaa34ea1fb148cd9f79dbe6fc9dcd3c722`
- `ghcr.io/azurekiln2333/codearts2api:v0.1.0-rc.1` — `sha256:ea8a520c40b6abb1834168f5dc7c1494841ca9f06c86c29fb593a4eff59f6b66`

## Source artifact

Actual140-member `codearts2api-source-ea9642229ed6.zip` SHA256 `abadf04373861fa1ab5ed41ad7d61e09384fee26062e86785a336e1c16e4fc41` matches the GitHub Release attachment digest. Local extracted compile/start, seven GET routes, synthetic token replay,85 Python tests, actionlint and official Compose four scenarios pass. Original credential/config/env/archive hashes remain unchanged.

## Usage and boundary

CNB deployment can select `CA2A_IMAGE=docker.cnb.cool/azurekiln/codearts2api:v0.1.0-rc.1` in the existing server .env, then pull/up in the existing deployment directory. This prerelease deliberately does not publish latest. No server deployment, remote credential import or production chat was performed. Actual multiarch remote image build/push is now verified; container runtime application smoke was not exercised in production. Historical R13 stays open.

CNB final stage verification: publish-source-release prepare/validate/package/read-manifest/create-release/upload-release-assets all success, and publish-image all stages success.
