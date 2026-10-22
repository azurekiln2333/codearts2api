# Selected-account model identity findings, 2026-10-22

The reported `glm-5.3` / canonical `GLM-5.3` already establishes that some
conversion occurred. The provider rejection alone does not establish whether
casing, route choice or account entitlement caused that particular live failure.
The following defects were independently reproduced locally.

## Identity flow before the repair

`upstream.CanonicalModel` consults a known-model index rebuilt from every account.
`chatBodyV2` applies that global mapping when constructing actual outbound JSON.
Chat audit computes its canonical name before account selection, while probes
also use the global function. Account model membership and benefit routing are
case-insensitive and account-specific, so selection can correctly choose an
account while the request body receives a differently-cased registration from
another account.

Within one account, display merging prefers richer metadata and uppercase IDs,
while benefit flags are ORed. A richer normal-source display entry can therefore
carry the case of the wrong route when a same-fold benefit entry governs sending.
Static `snap-chat` resolves to GLM-5.2 on the wire, but old negative cache writes
used the request alias; checks using the target ID could miss that denial.

## Executed regression

Copied the new server model-resolution tests into a temporary extraction of the
previous verified release `codearts2api-source-709f63ede82f.zip`, without changing
the working tree. Running `TestChatResolvesSelectedAccountModelCaseOnWire` against
that old source failed with a real handler HTTP 404 after two upstream attempts.
The fixture requires the second account's lowercase ID, while the old union-wide
mapping rewrites it to uppercase. The same test on updated source succeeds and
asserts the exact two outbound IDs plus the final actual audit canonical name.
An initial runner output-decoding error was fixed by explicitly decoding the
Go subprocess as UTF-8; the repeated completed run confirmed the expected 404.

Additional localhost regressions pass for lowercase/mixed input, default/auto,
unknown IDs despite another account's dynamic listing, exact probe requests,
benefit-to-normal closure, replacement auth, alias-consistent negative caching
and concurrent account-catalog publication. These fixtures establish implementation
behavior, not current production entitlement or a real GLM/V4.1 completion.

## Repair boundaries

- `ResolveModelForAuth` reads exact ID and benefit route under one catalog lock,
  uses the captured auth owner, and never fetches metadata on the chat path.
- Account exact-ID indexing uses the effective benefit source when applicable;
  public display merging remains independent of the wire representation.
- `ChatStreamWithExactModel` preserves the resolved ID. Legacy client methods
  retain their existing normalization behavior for compatibility.
- Chat/probe selection, cache reads/writes and audit use the resolved identity;
  the caller's requested model and public response name remain intact.
- No guessed alias/model version, entitlement bypass or cross-route retry was
  introduced. Unknown IDs preserve input except established static fallbacks.

The separate concurrency findings are in `busy-retry-findings.md`; they must not
be presented as a casing symptom. Implementation/check agents encountered service
concurrency errors during dispatch; the parent took over incomplete product work
and model tests, while a separate worker owns only concurrency HTTP regressions.
