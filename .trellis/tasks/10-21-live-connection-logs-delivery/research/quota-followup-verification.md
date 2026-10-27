# Quota and protocol follow-up

## Quota evidence

The CodeArts Agent 26.10.0 plugin's `CreditPackageService.extractFromResponse`
reads `usageTotalPackageCredit` and maps `package_credit_amount` to
`totalCredits`. Its UI displays unlimited when `totalCredits === -1` and
excludes that value from the exhausted condition. The inspected official
`extension.js` has SHA-256
`8291c42601805d2820d1ff117c80beb0d7390a54b631b2ba24a86d71b1d9a920`.
This does not establish that `-1` is valid for `used` or `remaining`.

The plugin's statistics request and this gateway construct different headers
before signing: the inspected plugin configuration supplies an empty
`x-auth-token` and places its STS token in `X-Security-Token`, while the gateway
also supplies the token in `X-Auth-Token` and signs additional headers. This is
static source evidence only. No real provider response was captured and the
difference is not a demonstrated cause of an authentication failure.

The local New API `UpdateChannelBalance` handler was exercised against the real
gateway handler and synthetic provider data. Finite balance conversion, measured
zero, upstream failure, and wrong gateway key passed; failure preserved the prior
New API balance. The conversion rate of 100 in the test is an example, not an
official rate. Chrome tests exercise the embedded overview with synthetic
complete, partial, unlimited, missing and zero values at desktop and mobile
widths. Different account UIDs may share a package, and benefit-model quota is
not part of the credit sum.

These tests do not verify real upstream quota, the deployed New API channel or
production rollout. Historical R13 root-cause attribution remains open.

## Final local checks

Full Go tests, the race detector, vet and build passed after integration. The
release metadata, source-export and deployment contract tests passed (85 tests).
The actual New API handler integration and synthetic Chrome quota suite were
rerun successfully, including unlimited and partial quota states. Chat,
Responses and Messages regressions cover tool-history validation and streaming
terminal and snapshot errors. No real account credentials or provider traffic
were used for these checks.
