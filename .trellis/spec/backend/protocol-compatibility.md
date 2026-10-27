# Responses and Messages protocol adapters

## 1. Scope / Trigger

Read this before changing `POST /v1/responses`, `POST /v1/messages`, their
request/output translators, shared chat admission, streaming, usage or audit.
Implementation lives in `internal/server/{responses,responses_output,anthropic,
anthropic_response,chat,lifecycle}.go` and `internal/upstream/sse.go`. Keep
Go 1.22 compatibility and the contracts in [Chat Request Lifecycle](./request-lifecycle.md),
[Request Audit](./request-audit.md) and [Generation Token Usage](./token-usage.md).

These are adapters for a documented subset of the Responses and Messages wire
formats, not full implementations of either provider's API. They translate to
the existing CodeArts chat pipeline and do not implement provider-side response
storage, retrieval, background tasks or other unlisted capabilities. Accepting
an optional field is not evidence that CodeArts supports all its semantics.

## 2. Signatures

```http
POST /v1/responses
Authorization: Bearer <gateway-api-key>
Content-Type: application/json

POST /v1/messages
x-api-key: <gateway-api-key>
Content-Type: application/json
```

```go
func (*Handler) responses(http.ResponseWriter, *http.Request)
func responsesToChat([]byte, *responsesOptions) ([]byte, error)
func (*Handler) anthropicMessages(http.ResponseWriter, *http.Request)
func anthropicToChat([]byte) ([]byte, error)
func (*Handler) chatCompletions(http.ResponseWriter, *http.Request)
```

Both routes are registered on the real `Handler` mux. The key is the gateway's
configured `CA2A_API_KEY`, not an upstream credential. Responses uses the same
Bearer authentication as chat. Messages accepts `x-api-key` or, when it is
absent, `Authorization: Bearer`; a present `x-api-key` takes precedence. As
with the existing service, an empty configured key disables this authentication
check. Messages does not require or validate `anthropic-version`.

## 3. Contracts

### Shared lifecycle and generation parameters

Input conversion happens lazily when chat reads the body, after pool admission
and under the same startup/read deadline. The original and translated bodies
are subject to the existing 8 MiB request limit. Preserve account-specific model
resolution, bounded admission/queueing, remote session cleanup, lease ownership,
downstream backpressure, cancellation and request IDs. Conversion is not a
separate inference path and must not do synchronous model discovery or bypass
account health and retry rules.

`max_output_tokens` (Responses) and `max_tokens` (Messages) map to chat's
generation limit. The shared parser rejects nonpositive values. The actual
upstream request caps an explicitly requested maximum at 65,536 tokens; a model
may have a smaller limit. `temperature` must be in `[0,2]` and `top_p` in
`[0,1]`. These are forwarded through the chat pipeline; acceptance does not
guarantee a particular model will accept every combination. For named function
choice, the shared pipeline sends only the named tool and string `required`,
because InferHub does not accept the named choice object. Missing or duplicate
definitions for that named tool are rejected.

Successful adapters use the same usage calculation as chat: upstream usage when
available, otherwise the existing estimate. The audit record stores normalized,
nullable counters and source independently of wire formatting; failed and
canceled requests do not acquire successful-generation usage. A delivered
Responses `incomplete` result can still be audit `ok`: that outcome reports
delivery, while the client must inspect the response status and reason. Messages'
wire counters default missing values to zero; do not interpret those wire zeros
as authoritative billing data or replace nullable audit values with them.

Authenticated requests reaching chat retain `X-Request-Id` correlation and
the actual HTTP commitment/status. Invalid adapter output uses
`errResponsesResponseTranslation` / `errAnthropicResponseTranslation` for
`upstream_body_error`, and the stream equivalents for
`upstream_stream_error`. A write/flush error or client cancellation retains
its downstream/cancellation classification. An upstream EOF with neither a
recognized terminal event nor finish evidence is an incomplete stream, not a
successful generation; a finish reason by itself does not stop reading later
usage frames. Never copy prompt, completion, tool input, credentials or raw
provider error text into audit metadata.

### Responses request and result

- `input` is a string or an array of text messages and function history.
  Message roles are `system`, `developer`, `user`, `assistant`; `developer`
  maps to `system`. Text parts accept `input_text`, `output_text` or `text`.
- `function_call` requires `call_id`, `name` and string `arguments`;
  `function_call_output` requires a matching `call_id` and text output. Reject
  duplicate IDs, orphan results, unfinished calls and messages interleaving
  pending results. Supply the history explicitly on subsequent requests.
- Optional `instructions` is a string prepended as a system message;
  `metadata` is a string-valued object echoed in the response, not persisted.
  `model` may be omitted and then uses the configured chat default.
- Supported tools have `type=function`, a name and optional description,
  parameters and strict flag. `tool_choice` accepts `auto`, `none`, `required`
  or a named function object. `reasoning.effort` is forwarded as a string;
  this adapter does not produce reasoning-summary items.
- `stream` is a boolean. The accepted text format is `{ "type": "text" }`;
  stream options only allow disabled `include_obfuscation`. `store` must be
  false or absent, `truncation` must be `disabled` or absent,
  `parallel_tool_calls` must be true or absent, and `include` must be empty.
- Nonempty `previous_response_id`, `conversation`, `prompt` and `background`,
  images, non-function tools, non-text formats and unsupported non-null
  top-level fields are rejected. Some unknown nested fields are not validated;
  do not claim blanket rejection or support for their semantics.

The nonstream result is a `response` with assistant message and/or
`function_call` output items, translated token counts when available, and
`status=completed` or `incomplete`. `length` maps to
`incomplete_details.reason=max_output_tokens`; `content_filter` maps to
`content_filter`. `store` is always false. In stream mode, text deltas are sent
as they arrive with Responses event names and sequence numbers; completion is
`response.completed`, `response.incomplete` or `response.failed`, never the
chat `[DONE]` marker. An upstream function call missing its name is an output
translation error, not a successful empty-name function call.

Responses buffers each tool's name, call ID and arguments until chat's terminal
frame, so a name split as `get_` + `weather` is emitted as `get_weather` in
`response.output_item.added`. Tool arguments therefore arrive at completion,
while text still streams immediately. Each translated SSE frame and total
retained output text/tool data are bounded to 8 MiB, with at most 256 tool calls.
Those adapter bounds do not imply that the shared upstream aggregator itself
has an equivalent total-memory bound.

### Messages request and result

- `model`, a positive integer `max_tokens` and nonempty `messages` are
  required. `system` accepts text or text blocks; messages accept user and
  assistant text, assistant `tool_use` and user `tool_result` blocks.
  A tool use requires ID, name and an object-valued input. An error result is
  translated with a `[tool error] ` prefix. Preserve numeric values in tool
  inputs and schemas through translation.
- Custom tools require a name and object `input_schema`; `tool_choice` supports
  `auto`, `any`, `none` and a named `tool`. Disabling parallel tool use is
  rejected. `stream`, `temperature` and `top_p` use the shared rules.
- Non-null unsupported top-level fields such as `stop_sequences` or `thinking`,
  and unsupported block/tool types such as image or provider-specific tools,
  are rejected. `metadata` is accepted but is not forwarded by this adapter.
  Unknown nested fields can be ignored by JSON decoding; their presence does
  not establish support and must not be described as universally rejected.

Nonstream output is a Messages-shaped `message` with text/tool-use blocks and
input/output usage. Tool arguments must decode to a JSON object; empty
arguments become `{}`. Chat finish reasons map to `end_turn`, `tool_use`,
`max_tokens`, or `refusal`. Streaming emits `message_start`, ordered content
block start/delta/stop events, `message_delta`, and `message_stop`; failures
emit a Messages `error` event when the downstream is writable.

Messages streams initial text and the primary tool incrementally once its ID,
name and argument data are present. Subsequent tools and text are deferred to
keep content blocks ordered until completion. It permits at most 256 tool calls
and bounds retained tool data plus deferred text to 8 MiB. A name or ID changed
after a tool block starts is a protocol error; a later fragmented name is not
silently substituted. Invalid final tool JSON fails the stream rather than
emitting `message_stop` as a successful result.

## 4. Validation & Error Matrix

| Condition | Required observable behavior |
| --- | --- |
| Invalid gateway key | HTTP 401; Responses OpenAI error or Messages authentication error; no upstream call |
| Invalid/unsupported request or mismatched Responses tool history | HTTP 400 in that route's error format; no inference attempt |
| Request body above 8 MiB | HTTP 413; no inference attempt |
| Valid named tool choice absent/duplicated in tools | HTTP 400 before inference |
| Selected account rejects model or parameters | Preserve shared chat status and audit classification; do not pretend an alias or parameter was accepted |
| Invalid output tool name (Responses) or non-object tool input (Messages), nonstream | HTTP 502; `upstream_body_error`, no success usage |
| Equivalent error after streaming begins | Responses `response.failed` or Messages `error` if writable; committed status remains 200 and audit outcome is `upstream_stream_error` |
| Output exceeds a stated adapter bound | Fail translation rather than grow its retained state without bound; preserve actual downstream error if writing fails |
| Upstream EOF without terminal or finish evidence | Fail aggregation/stream and release resources; do not emit a successful completion |
| Client cancels before completion | Propagate cancellation, release body/admission/lease and do not mark account failure or successful usage |
| `length` completion delivered | Responses `incomplete`, Messages `max_tokens`; a successfully delivered response may be audit `ok` |

## 5. Good / Base / Bad Cases

- Good: a Responses client provides explicit function call and matching result
  history, then receives a full split function name and correlated usage.
- Base: a text-only Messages request uses `x-api-key`, returns one text block,
  and shares the same account scheduling and cleanup as chat.
- Bad: accept `previous_response_id` as though response state were persisted,
  report an empty tool name as success, or treat a disconnected consumer as an
  upstream account fault.

## 6. Tests Required

Exercise the actual registered mux with synthetic localhost upstreams, not only
translator helpers. `internal/server/responses_test.go` and
`internal/server/anthropic_test.go` cover the individual adapters; shared SSE,
lifecycle, audit and usage tests cover the common pipeline. Run at least:

```sh
go test ./internal/server -run 'Test(Responses|Anthropic)'
go test -race ./internal/server -run 'Test(Responses|Anthropic)'
go test ./internal/upstream
```

Maintain assertions for auth precedence, no upstream traffic on rejection, body
limit and slow-body deadlines, exact tool history and numeric fidelity, selected
tool constraints, text streaming before upstream completion, split names and
interleaved tool ordering, 8 MiB/256-call boundaries, malformed tool output,
downstream write/flush failures, cancellation with account reuse, status/usage
and correlated audit records. Cover premature EOF, terminal error precedence and
usage arriving after finish evidence in shared SSE tests. Include normal and race
full-suite checks when integrating shared lifecycle changes.

## 7. Wrong vs Correct

Wrong: start a Responses tool item on its first name fragment and assume the
first `get_` fragment is the complete function name.

Correct: retain bounded name/argument fragments until terminal evidence, emit
`get_weather` once, and fail visibly if the name never arrives. For Messages,
preserve its separate ordered-block rules and treat a name changed after start
as an explicit stream error.
