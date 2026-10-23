# Eligibility source-release verification

Date: 2026-10-23. Product commit: `4622f3ef2f4b02ed311ca5cffb617e148c1bb4eb`.

Artifact: `codearts2api-source-4622f3ef2f4b.zip`; SHA256:
`7cc1050dc6313c9138efddd129b34a3c9c98affac0b58fde76a558dad3b21d17`.

Exported the reviewed committed manifest, including the R14 HTTP regressions,
into a new archive without replacing any existing ZIP. All 128
member names and manifest SHA256 values passed checks. Real credential values
and private config/auth/data/tooling paths were excluded.

The actual archive was extracted into an isolated private directory, compiled
with the Go 1.27.3 toolchain and its exact source revision injected, then started
with an empty auth directory, temporary data/config and a generated local key.
The packaged GET-only verifier passed health, console, actual build identity,
requests, audit, models and connections. No inference or refresh was sent by the
verifier. The owned server was stopped and its temporary key-bearing config
removed. Docker image runtime validation was not performed.

Root original credentials matched the recorded current-round baseline. Both user
ZIPs and the previous verified source ZIP remained byte-for-byte unchanged.
Raw sanitized result: `.local-test/results/eligibility-release-4622f3ef2f4b.json`.

No push or production deployment occurred. Production's GET-observed `709f63e`
remains distinct from this delivered source. Update deployment source before
rebuilding/recreating with existing env/config/auth/data mounts. This local R14
delivery does not close unresolved historical R13 causes or guarantee a missing
model entitlement.
