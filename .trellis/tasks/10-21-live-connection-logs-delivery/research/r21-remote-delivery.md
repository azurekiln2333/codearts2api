# R21 remote delivery - 2026-10-27

Product commit `6ee36e748cd751bce516f395b7f59307fc1cf021` is the annotated
`v1.0.1` tag on GitHub, CNB, GitCode, Gitee and GitLab. GitHub main validation
run `40478422030` and CNB main `cnb-tuv-1k5u9qsq3` passed before the tag.

GitHub tag run `40479083457` completed its test, release and image jobs. CNB
tag run `cnb-nnh-1k5ua4hlj` completed source release, attachments and image
publication. Anonymous registry inspection found `v1.0.1` and `latest` equal
within each registry; both `linux/amd64` and `linux/arm64` configs identify the
product commit:

- GHCR: `sha256:60933b609d3525d9f3833d4bdba39a42004be3a62768472cbc7c152611ea7e81`
- CNB: `sha256:5595e6efd35fe56bf22be6fba77e94fd442603b15ef50abc207bae1e3a0bed94`

The GitHub release archive `codearts2api-source-6ee36e748cd7.zip` has SHA256
`3ac5c8172134613c3e65938ce75a7017f5172e69f0fad48dbfbf70dc765d999e`.
An independently exported 154-entry archive matched all committed member hashes,
excluded private deployment inputs, and passed extracted build, simulated quota
and HTTP route checks with an empty account directory.

Actual remote publishing and synthetic local acceptance do not establish that a
production server has deployed the new image, that the provider returned real
credit data, or that a deployed New API channel works end to end. Existing R13
historical incident attribution remains open.
