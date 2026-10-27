# R21 local verification - 2026-10-27

## Implemented

The overview and authenticated cache-only `/v1/quota` expose account credit
package balances. A supplied `credits_per_usd` converts a complete fresh snapshot
to the `credit_summary` format used by the local New API advanced-custom balance
adapter. The conversion has no official default rate. Distinct accounts may share
a package, and benefit-model allowances are separate. Missing, failed, stale or
expired account data cannot become an invented zero balance.

Responses and Messages adapters reuse the chat admission and audit lifecycle and
reject unsupported operations. Premature upstream EOF is a failure; empty streams
return 502 before response commitment. The request log filters successful and
unsuccessful outcomes before pagination and token summaries. A conservative 65536
output-token ceiling handles one excessive-parameter case; it is not proof that
all `InferHub.001001005.400` errors share this cause or that every model accepts
that many tokens. Huawei's ModelArts API documentation describes `max_tokens`
but does not establish a universal CodeArts limit:
https://support.huaweicloud.com/model-call-modelarts/model-call-modelarts-0004.html
An independently maintained CodeArts integration reports that some catalog
limits above 65536 trigger this upstream error in its own tests; that report is
not independent verification of this project's live requests:
https://dsh-plugins-mp.com/plugins/elegant01--dsh-codearts

## Evidence

- `go test ./... -count=1`, `go test -race ./... -count=1`, `go vet ./...` and
  `go build ./...` pass with the local Go 1.27.3 toolchain.
- Release metadata, source export and deployment contract tests pass: 40, 17 and
  28 cases respectively. `git diff --check` passes.
- Synthetic-provider route tests cover quota collection, auth, conversion,
  failures, streaming completion, protocol adapters and audit filtering.
- Chrome checks with synthetic APIs cover desktop/mobile quota display, zero,
  partial, unknown, refresh throttling and failure states, plus request
  filter/pagination. Eighteen synthetic slow accounts finish across bounded
  collection batches; a canceled batch preserves earlier fresh values.
- The local New API advanced-custom adapter was checked for its balance path,
  query forwarding and Bearer authorization. A full New API-to-gateway deployment
  and an actual provider quota response were not tested.
- An isolated empty-account preview returned healthy and reported an incomplete,
  null balance as expected. No production credentials or configuration were used.

This evidence does not close the historical R13 stall investigation or establish
that a remote deployment has been upgraded.
