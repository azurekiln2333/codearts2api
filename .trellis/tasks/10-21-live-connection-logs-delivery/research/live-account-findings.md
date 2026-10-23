# Authorized local real-account validation, 2026-10-22

The user supplied four credential JSON files in root auths/ and explicitly
requested actual calls and progressive concurrency verification. Parent copied
them to Git-excluded .local-test/auths; tests operate on those copies. Only parent
operates credentials and real network requests. No credential contents or account
identifiers belong in source, reports or commits. Diagnostic helpers and raw
runtime files stay under .local-test/. Public result reports use account-1 etc.

This is the initial investigation, not the final acceptance report. See
`live-gateway-session-verification.md` for the rebuilt gateway results and exact
preservation scope. The fourth refresh-expired test copy must not be used to
infer that a subsequently updated original login is expired.

## Initial actual observations (product b9b318c)

An isolated compiled server bound to 127.0.0.1 with empty test state/audit,
per-account concurrency 1, pending 24, startup 60s, upstream idle 30s, account
wait 10s and three queue attempts/two-second delay. Watchdog and both auto-claim
policies were disabled. The existing automatic availability prober remains
separate from watchdog and may run after its two-minute startup delay; its
pre-chat negatives were visible and are not attributed to the explicit request.

All four catalogs completed: agent six entries, builtin eight, benefit disabled.
The actual merged display catalog includes GLM-5.3 and DeepSeek-V4.1-Flash. This
is the first real account evidence for the formerly unobserved directory/gate.
It proves discovery and account-specific channel state, not inference rights.

The first explicit lowercase glm-5.3 request returned HTTP404 model_not_available
in 2.901 seconds. Audit reports wire/canonical GLM-5.3, three actual sends,
InferHub.002002009.404, and all four accounts considered. Account-2 was disabled
by the actual refresh-token-expired path before sending; three other accounts
were capability-denied. No account errors/cooling were added for that denial,
and active chat tracking returned to zero. One considered account need not equal
one actual outbound attempt.

The parent stopped the isolated server before identity-level controlled probes,
preventing background availability work from contaminating the comparison.
Current hypothesis: display/inference ID or route protocol mismatch remains to
be checked; this result alone does not establish absent entitlement.

## Pending

- Read safe model identity fields from actual normal-source responses.
- Compare evidenced exact IDs through bounded one-account calls, without guessed
  model versions, alternate billing routes or automatic claims.
- If a product defect is confirmed: repair at its owner, add actual HTTP regression,
  run independent check plus full required checks and repeat real call.
- Progress to stream/repeated/multi-account concurrency only after a viable call.
- Keep expired refresh auth separate from model and scheduler defects.

## Confirmed real source identity and controlled calls

At 2026-10-22 15:32:05 UTC, account-1 returned normal builtin HTTP200 with explicit
IDs glm-5.3, deepseek-v4.1-flash, openpangu-2.0-pro/flash and
 glm-5.2-sft-harmony; display names have different casing/naming. Agent parameter
model_id and model_alias agree with those builtin IDs. GLM-5.2 and Qwen3-VL-235B
are uppercase explicit IDs. Current metadata had one extra Kimi-K3 entry compared
to the initial catalog; source counts are time-specific observations.

At 15:32:35 UTC, one controlled exact-model normal-route account-1 call to
glm-5.3 returned content (2 bytes) in 1.354 seconds. It used the existing exported
ChatStreamWithExactModel and AggregateRaw, 16 output tokens, no refresh/claim.
This bypassed catalog normalization for diagnosis only; no product route fallback
was introduced. Earlier real handler GLM-5.3 failed on that same account.

At 15:33:25 UTC, deepseek-v4.1-flash was accepted and aggregated without error in
1.127 seconds but returned zero content bytes at the 16-token limit. This alone
is not a useful-answer success; a complete-content test with adequate bounded
output remains required. Neither case used the closed benefit route.

Superseding result: at 15:44:41 UTC, the same exact DeepSeek ID returned actual
content (2 bytes) in 1.332 seconds at a 256-token bound, normal route, no claim.

## Repaired gateway and repeated-busy evidence, October 23 local time

At 17:12 UTC the rebuilt isolated gateway completed actual lowercase GLM and
DeepSeek calls with content, exact lowercase audit IDs and no retained active
requests. At 17:17 UTC a real forced metadata refresh restored agent/builtin
catalogs for the three valid test logins; each benefit switch explicitly disabled
the legacy benefit source. One expired refresh login remains separate/disabled.

Twelve sequential streamed calls alternating lower/display case completed eight
with actual content and DONE; four received TM.00001041 after four/five sends.
All 12 audits correlated and local active tracking returned to zero. Therefore
identity repair alone does not solve the user's repeated concurrency refusal.

Controlled direct account-3 calls at 17:37:33/34/35 UTC fully drained the actual
HTTP response to EOF, returned content, then calls four to six received TM.
Minimal request bodies without optional chat/session/cache fields reproduced
three completed calls followed by TM at 17:46 UTC. Independent versus fixed IDs
did not eliminate saturation. These are actual provider observations, not proof
of a specific universal quota, expiry or undocumented cleanup endpoint.
