package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

func responsesFixture(t *testing.T, upstreamHandler http.HandlerFunc) (*Handler, *httptest.Server) {
	t.Helper()
	up := httptest.NewServer(upstreamHandler)
	t.Cleanup(up.Close)
	a := auth.New("responses-account", "tester", "domain", "token", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", "")
	p, err := pool.New([]*auth.Auth{a}, pool.Config{MaxConcurrent: 1}, "")
	if err != nil {
		t.Fatal(err)
	}
	p.Accounts()[0].Client = upstream.NewWithChatEndpoint(5*time.Second, up.URL)
	h := NewHandler(Config{Pool: p, DefaultModel: "glm-5.2", MaxRotate: 1, APIKey: "test-key"})
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	return h, server
}

func TestResponsesWriterReportsActualCommit(t *testing.T) {
	for _, tc := range []struct {
		name, body          string
		stream              bool
		status, afterStatus int
		before, after       bool
		wantError           bool
	}{
		{"bad-chat-response", "invalid json", false, http.StatusOK, http.StatusBadGateway, false, true, true},
		{"complete-chat-response", `{"model":"m","choices":[]}`, false, http.StatusOK, http.StatusOK, false, true, false},
		{"partial-sse-frame", "data: {", true, http.StatusOK, 0, false, false, false},
		{"error-response", `{"error":"bad"}`, false, http.StatusBadRequest, http.StatusBadRequest, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			w := &responsesWriter{ResponseWriter: rec, opts: &responsesOptions{stream: tc.stream}}
			w.WriteHeader(tc.status)
			status, committed := w.ResponseStatus()
			if committed != tc.before || (committed && status != tc.status) {
				t.Fatalf("before write status=%d committed=%v", status, committed)
			}
			_, err := w.Write([]byte(tc.body))
			if (err != nil) != tc.wantError {
				t.Fatalf("write error=%v", err)
			}
			status, committed = w.ResponseStatus()
			if committed != tc.after || (committed && status != tc.afterStatus) || (!committed && status != 0) {
				t.Fatalf("after write status=%d committed=%v", status, committed)
			}
		})
	}
}

func responsesPost(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/v1/responses", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestResponsesRequestMapsToolsAndHistory(t *testing.T) {
	body := `{"model":"glm-5.2","instructions":"top","input":[{"role":"developer","content":[{"type":"input_text","text":"policy"}]},{"role":"user","content":[{"type":"input_text","text":"hello"}]},{"type":"function_call","call_id":"call1","name":"lookup","arguments":"{\"id\":1}"},{"type":"function_call_output","call_id":"call1","output":"found"}],"tools":[{"type":"function","name":"lookup","description":"find","parameters":{"type":"object"}}],"tool_choice":{"type":"function","name":"lookup"},"max_output_tokens":30,"reasoning":{"effort":"high"},"store":false}`
	var opts responsesOptions
	translated, err := responsesToChat([]byte(body), &opts)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseChatRequest(translated)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Messages) != 5 || parsed.Messages[0].Role != "system" || parsed.Messages[1].Role != "system" || parsed.Messages[2].Text != "hello" || parsed.Messages[3].ToolCalls[0].ID != "call1" || parsed.Messages[4].ToolCallID != "call1" {
		t.Fatalf("history not preserved: %+v", parsed.Messages)
	}
	if parsed.MaxTokens == nil || *parsed.MaxTokens != 30 || parsed.ReasoningEffort != "high" || parsed.ToolChoice.Function != "lookup" || len(parsed.Tools) != 1 {
		t.Fatalf("parameters not mapped: %+v", parsed)
	}
}

func TestResponsesNonstreamViaChatLifecycle(t *testing.T) {
	var seen atomic.Int32
	h, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`+"\n\ndata: [DONE]\n\n")
	})
	sink := newAuditSink(t)
	h.cfg.Audit = sink
	resp := responsesPost(t, server.URL, `{"model":"glm-5.2","input":"hi","store":false}`)
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || result["object"] != "response" || result["status"] != "completed" || !strings.HasPrefix(result["id"].(string), "resp_") {
		t.Fatalf("invalid response status=%d body=%#v", resp.StatusCode, result)
	}
	if tools, ok := result["tools"].([]any); !ok || len(tools) != 0 || result["tool_choice"] != "auto" {
		t.Fatalf("invalid Responses tool defaults: %#v", result)
	}
	output := result["output"].([]any)
	msg := output[0].(map[string]any)
	content := msg["content"].([]any)[0].(map[string]any)
	if msg["role"] != "assistant" || content["text"] != "Hello" {
		t.Fatalf("bad output: %#v", output)
	}
	u := result["usage"].(map[string]any)
	if u["input_tokens"] != float64(4) || u["output_tokens"] != float64(2) || u["total_tokens"] != float64(6) {
		t.Fatalf("bad usage: %#v", u)
	}
	if seen.Load() != 1 {
		t.Fatalf("upstream requests=%d", seen.Load())
	}
	ev := waitForRecord(t, sink, resp.Header.Get("X-Request-Id"))
	if ev.Outcome != "ok" || ev.Usage == nil || ev.Usage.Source != "upstream" || ev.Attempts != 1 {
		t.Fatalf("response did not preserve chat audit/usage: %+v", ev)
	}
	assertLifecycleHealthy(t, h.cfg.Pool)
}

func TestResponsesAuthenticationAndNonstreamToolHistory(t *testing.T) {
	var seen atomic.Int32
	requests := make(chan map[string]any, 1)
	_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		requests <- request
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_2","function":{"name":"lookup","arguments":"{\"id\":2}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
	})
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/responses", strings.NewReader(`{"input":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || seen.Load() != 0 {
		t.Fatalf("unauthorized request status=%d upstream calls=%d", resp.StatusCode, seen.Load())
	}
	body := `{"model":"glm-5.2","input":[{"role":"user","content":"hi"},{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"id\":1}"},{"type":"function_call_output","call_id":"call_1","output":"found"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"tool_choice":{"type":"function","name":"lookup"}}`
	resp = responsesPost(t, server.URL, body)
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%v", resp.StatusCode, result)
	}
	upstreamBody := <-requests
	messages := upstreamBody["messages"].([]any)
	if len(messages) != 3 || messages[1].(map[string]any)["tool_calls"] == nil || messages[2].(map[string]any)["tool_call_id"] != "call_1" {
		t.Fatalf("upstream tool history lost: %#v", messages)
	}
	if upstreamBody["tool_choice"] != "required" {
		t.Fatalf("named tool was not enforced: %#v", upstreamBody)
	}
	output := result["output"].([]any)
	if len(output) != 1 || output[0].(map[string]any)["type"] != "function_call" || output[0].(map[string]any)["call_id"] != "call_2" || output[0].(map[string]any)["arguments"] != `{"id":2}` {
		t.Fatalf("tool output not mapped: %#v", output)
	}
}

func TestResponsesBodyLimitAndReadTimeout(t *testing.T) {
	var seen atomic.Int32
	h, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) { seen.Add(1) })
	resp := responsesPost(t, server.URL, `{"input":"`+strings.Repeat("x", maxBodyBytes)+`"}`)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request status=%d", resp.StatusCode)
	}
	h.cfg.StartupTimeout = 200 * time.Millisecond
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = fmt.Fprint(conn, "POST /v1/responses HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer test-key\r\nContent-Type: application/json\r\nContent-Length: 1024\r\n\r\n{")
	if err != nil {
		t.Fatal(err)
	}
	timeoutResponse, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	timeoutResponse.Body.Close()
	if timeoutResponse.StatusCode != http.StatusGatewayTimeout || seen.Load() != 0 {
		t.Fatalf("body timeout status=%d upstream calls=%d", timeoutResponse.StatusCode, seen.Load())
	}
	assertLifecycleHealthy(t, h.cfg.Pool)
}

func TestResponsesCancellationReleasesAccountAndAdmission(t *testing.T) {
	var attempts atomic.Int32
	upstreamCanceled := make(chan struct{})
	h, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if attempts.Add(1) == 1 {
			_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"first"}}]}`+"\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(upstreamCanceled)
			return
		}
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"next"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	})
	sink := newAuditSink(t)
	h.cfg.Audit = sink
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/responses", strings.NewReader(`{"model":"glm-5.2","input":"hi","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	requestID := resp.Header.Get("X-Request-Id")
	cancel()
	resp.Body.Close()
	select {
	case <-upstreamCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("client cancellation did not reach upstream")
	}
	ev := waitForRecord(t, sink, requestID)
	if ev.Outcome != "canceled" && ev.Outcome != "downstream_canceled" {
		t.Fatalf("unexpected canceled audit outcome: %+v", ev)
	}
	if ev.Usage != nil {
		t.Fatalf("canceled response fabricated usage: %+v", ev)
	}
	assertLifecycleHealthy(t, h.cfg.Pool)
	resp = responsesPost(t, server.URL, `{"model":"glm-5.2","input":"hi"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("account not reusable after cancellation: status=%d body=%s", resp.StatusCode, raw)
	}
}

func TestResponsesUnsupportedInputReturns400BeforeUpstream(t *testing.T) {
	var seen atomic.Int32
	_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) { seen.Add(1) })
	for _, tc := range []struct{ body, want string }{
		{`{"input":"hello","previous_response_id":"resp_old"}`, "previous_response_id is not supported"},
		{`{"input":"hello","store":true}`, "store is not supported"},
		{`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.test/a"}]}]}`, "input_image"},
		{`{"input":"hello","tools":[{"type":"web_search"}]}`, "tools[0] type is not supported"},
		{`{"input":"hello","tools":[{"type":"function","name":"f","defer_loading":true}]}`, "defer_loading"},
		{`{"input":"hello","include":["reasoning.encrypted_content"]}`, "include is not supported"},
		{`{"input":"hello","reasoning":{"summary":"auto"}}`, "reasoning.summary"},
		{`{"input":"hello","reasoning":{"generate_summary":"auto"}}`, "reasoning.generate_summary"},
		{`{"input":"hello","reasoning":"high"}`, "reasoning must be an object"},
		{`{"input":"hello","text":{"format":{"type":"json_schema"}}}`, "non-text output formats"},
		{`{"input":"hello","text":{"format":"bad"}}`, "non-text output formats"},
		{`{"input":"hello","text":{"verbosity":"high"}}`, "text.verbosity"},
		{`{"input":"hello","stream_options":{"include_obfuscation":true}}`, "include_obfuscation"},
		{`{"input":"hello","prompt_cache_key":"abc"}`, "prompt_cache_key"},
		{`{"model":3,"input":"hello"}`, "model must be a string"},
		{`{"input":[{"role":"user","content":"hi"},{"type":"function_call_output","call_id":"call1","output":"orphan"}]}`, "no matching pending call"},
		{`{"input":[{"role":"user","content":"hi"},{"type":"function_call","call_id":"call1","name":"f","arguments":"{}"}]}`, "matching function_call_output"},
		{`{"input":[{"role":"user","content":"hi"},{"type":"function_call","call_id":"call1","name":"f","arguments":"{}"},{"role":"user","content":"interrupt"},{"type":"function_call_output","call_id":"call1","output":"x"}]}`, "interrupts pending"},
	} {
		resp := responsesPost(t, server.URL, tc.body)
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 400 || !strings.Contains(string(data), tc.want) {
			t.Fatalf("request=%s status=%d body=%s, want %q", tc.body, resp.StatusCode, data, tc.want)
		}
	}
	if seen.Load() != 0 {
		t.Fatalf("unsupported request reached upstream: %d", seen.Load())
	}
}

func TestResponsesStreamsBeforeUpstreamCompletes(t *testing.T) {
	release := make(chan struct{})
	_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"first"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"second"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`+"\n\ndata: [DONE]\n\n")
	})
	resp := responsesPost(t, server.URL, `{"model":"glm-5.2","input":"hi","stream":true,"store":false}`)
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	var prefix strings.Builder
	for !strings.Contains(prefix.String(), "response.output_text.delta") {
		line, err := reader.ReadString('\n')
		if err != nil {
			close(release)
			t.Fatal(err)
		}
		prefix.WriteString(line)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	prefix.WriteString(line)
	if !strings.Contains(prefix.String(), `"delta":"first"`) {
		close(release)
		t.Fatalf("first chunk missing: %s", prefix.String())
	}
	close(release)
	tail, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	all := prefix.String() + string(tail)
	for _, v := range []string{"response.created", "response.in_progress", "response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.output_text.done", "response.content_part.done", "response.output_item.done", "response.completed", `"input_tokens":2`, `"total_tokens":5`} {
		if !strings.Contains(all, v) {
			t.Errorf("missing %s: %s", v, all)
		}
	}
	if strings.Contains(all, "[DONE]") {
		t.Fatalf("chat terminal leaked into Responses protocol: %s", all)
	}
}

func TestResponsesStreamToolCallDeltasAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name, wire, want string
		fail             bool
	}{
		{"tool", `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{\"id\":"}}]}}]}` + "\n\n" + `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n", `"arguments":"{\"id\":1}"`, false},
		{"error", `data: {"choices":[{"delta":{"content":"partial"}}]}` + "\n\n" + `event: done` + "\n" + `data: {"error_code":"failed","error_msg":"failed"}` + "\n\n", "response.failed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, tc.wire)
			})
			sink := newAuditSink(t)
			h.cfg.Audit = sink
			resp := responsesPost(t, server.URL, `{"model":"glm-5.2","input":"hi","stream":true,"store":false}`)
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			data := string(raw)
			if !strings.Contains(data, tc.want) {
				t.Fatalf("missing %s: %s", tc.want, data)
			}
			if tc.fail && strings.Contains(data, "response.completed") {
				t.Fatalf("failed stream reported completed: %s", data)
			}
			if !tc.fail && (!strings.Contains(data, "response.function_call_arguments.delta") || !strings.Contains(data, "response.function_call_arguments.done") || !strings.Contains(data, "response.completed")) {
				t.Fatalf("tool lifecycle missing: %s", data)
			}
			var arguments strings.Builder
			var toolID string
			seq := 0
			for _, frame := range strings.Split(strings.TrimSpace(data), "\n\n") {
				parts := strings.SplitN(frame, "\ndata: ", 2)
				if len(parts) != 2 {
					t.Fatalf("invalid Responses SSE frame: %q", frame)
				}
				var event map[string]any
				if err := json.Unmarshal([]byte(parts[1]), &event); err != nil {
					t.Fatal(err)
				}
				if event["sequence_number"] != float64(seq) || parts[0] != "event: "+event["type"].(string) {
					t.Fatalf("bad stream event order/frame: %s", frame)
				}
				seq++
				switch event["type"] {
				case "response.output_item.added":
					item := event["item"].(map[string]any)
					if item["type"] == "function_call" {
						toolID = item["id"].(string)
						if item["arguments"] != "" || item["call_id"] != "call_1" || item["name"] != "lookup" {
							t.Fatalf("invalid added function call: %#v", item)
						}
					}
				case "response.function_call_arguments.delta":
					if event["item_id"] != toolID {
						t.Fatalf("delta belongs to wrong tool: %#v", event)
					}
					arguments.WriteString(event["delta"].(string))
				case "response.function_call_arguments.done":
					if event["arguments"] != arguments.String() || event["item_id"] != toolID {
						t.Fatalf("tool arguments duplicated or lost: %#v", event)
					}
				}
			}
			if !tc.fail && (toolID == "" || arguments.String() != `{"id":1}`) {
				t.Fatalf("bad streamed tool arguments id=%q args=%q", toolID, arguments.String())
			}
			ev := waitForRecord(t, sink, resp.Header.Get("X-Request-Id"))
			if tc.fail && (ev.Outcome != "upstream_stream_error" || ev.Usage != nil || !ev.Committed || ev.Status != http.StatusOK) {
				t.Fatalf("failed stream audit is inaccurate: %+v", ev)
			}
			if !tc.fail && (ev.Outcome != "ok" || ev.Usage == nil) {
				t.Fatalf("successful stream audit is inaccurate: %+v", ev)
			}
			if tc.fail {
				for _, account := range h.cfg.Pool.List() {
					if account["active_concurrent"] != 0 || account["err_count"] != 1 {
						t.Fatalf("failed stream changed chat cleanup/health semantics: %#v", account)
					}
				}
			} else {
				assertLifecycleHealthy(t, h.cfg.Pool)
			}
		})
	}
}

func TestResponsesIncompleteStreamingAndNonstreaming(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"truncated"},"finish_reason":"length"}]}`+"\n\ndata: [DONE]\n\n")
			})
			resp := responsesPost(t, server.URL, fmt.Sprintf(`{"model":"glm-5.2","input":"hi","stream":%v}`, stream))
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"status":"incomplete"`) || !strings.Contains(string(raw), `"reason":"max_output_tokens"`) {
				t.Fatalf("incorrect incomplete response status=%d body=%s", resp.StatusCode, raw)
			}
			if stream && !strings.Contains(string(raw), "event: response.incomplete") {
				t.Fatalf("missing incomplete stream event: %s", raw)
			}
		})
	}
}

func TestResponsesMissingToolNameFails(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			h, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"arguments":"{}"}}]}}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`+"\n\ndata: [DONE]\n\n")
			})
			sink := newAuditSink(t)
			h.cfg.Audit = sink
			resp := responsesPost(t, server.URL, fmt.Sprintf(`{"model":"glm-5.2","input":"hi","stream":%t}`, stream))
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			ev := waitForRecord(t, sink, resp.Header.Get("X-Request-Id"))
			if stream {
				if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "event: response.failed\n") || strings.Contains(string(body), "event: response.completed\n") || strings.Contains(string(body), `"type":"function_call"`) {
					t.Fatalf("invalid failure status=%d body=%s", resp.StatusCode, body)
				}
				if ev.Outcome != "upstream_stream_error" || ev.Status != http.StatusOK || !ev.Committed || ev.Canceled || ev.Usage != nil {
					t.Fatalf("invalid stream failure audit: %+v", ev)
				}
			} else {
				if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), `"code":"upstream_error"`) {
					t.Fatalf("invalid failure status=%d body=%s", resp.StatusCode, body)
				}
				if ev.Outcome != "upstream_body_error" || ev.Status != http.StatusBadGateway || !ev.Committed || ev.Canceled || ev.Usage != nil {
					t.Fatalf("invalid response failure audit: %+v", ev)
				}
			}
			assertLifecycleHealthy(t, h.cfg.Pool)
		})
	}
}

func TestResponsesStreamSplitToolNameAndArguments(t *testing.T) {
	h, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"hello","tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_","arguments":"{\"city\":"}}]}}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"weather","arguments":"\"Paris\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`+"\n\ndata: [DONE]\n\n")
	})
	sink := newAuditSink(t)
	h.cfg.Audit = sink
	resp := responsesPost(t, server.URL, `{"model":"glm-5.2","input":"hi","stream":true}`)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var name, callID, arguments, doneArguments string
	for _, frame := range strings.Split(strings.TrimSpace(string(data)), "\n\n") {
		parts := strings.SplitN(frame, "\ndata: ", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid stream frame: %q", frame)
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(parts[1]), &event); err != nil {
			t.Fatal(err)
		}
		switch event["type"] {
		case "response.output_item.added":
			item, _ := event["item"].(map[string]any)
			if item["type"] == "function_call" {
				name, _ = item["name"].(string)
				callID, _ = item["call_id"].(string)
			}
		case "response.function_call_arguments.delta":
			arguments += event["delta"].(string)
		case "response.function_call_arguments.done":
			doneArguments, _ = event["arguments"].(string)
		}
	}
	if resp.StatusCode != http.StatusOK || name != "get_weather" || callID != "call_1" || arguments != `{"city":"Paris"}` || doneArguments != arguments || !strings.Contains(string(data), `"delta":"hello"`) || !strings.Contains(string(data), "event: response.completed\n") {
		t.Fatalf("split call corrupted: status=%d name=%q id=%q args=%q done=%q body=%s", resp.StatusCode, name, callID, arguments, doneArguments, data)
	}
	ev := waitForRecord(t, sink, resp.Header.Get("X-Request-Id"))
	if ev.Outcome != "ok" || ev.Usage == nil || ev.Usage.Source != "upstream" {
		t.Fatalf("successful tool response lost usage: %+v", ev)
	}
	assertLifecycleHealthy(t, h.cfg.Pool)
}

func TestResponsesStreamBufferLimits(t *testing.T) {
	t.Run("frame", func(t *testing.T) {
		rec := httptest.NewRecorder()
		w := &responsesWriter{ResponseWriter: rec, opts: &responsesOptions{stream: true}}
		if _, err := w.Write([]byte(strings.Repeat("x", maxBodyBytes))); err != nil {
			t.Fatal(err)
		}
		_, err := w.Write([]byte("x"))
		if !errors.Is(err, errResponsesStreamTranslation) || len(w.buffer) != maxBodyBytes || !rec.Flushed || !strings.Contains(rec.Body.String(), "event: response.failed\n") {
			t.Fatalf("frame limit: err=%v buffered=%d flushed=%v", err, len(w.buffer), rec.Flushed)
		}
	})
	t.Run("output", func(t *testing.T) {
		rec := httptest.NewRecorder()
		w := &responsesWriter{ResponseWriter: rec, opts: &responsesOptions{stream: true}}
		part := strings.Repeat("x", maxBodyBytes/2)
		for i := 0; i < 2; i++ {
			frame, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"name": "read", "arguments": part}}}}}}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = w.Write(append(append([]byte("data: "), frame...), '\n', '\n'))
			if i == 0 && err != nil {
				t.Fatal(err)
			}
			if i == 1 && (!errors.Is(err, errResponsesStreamTranslation) || w.toolCalls[0].text.Len() != len(part) || !strings.Contains(rec.Body.String(), "event: response.failed\n")) {
				t.Fatalf("output limit: err=%v buffered=%d", err, w.toolCalls[0].text.Len())
			}
		}
	})
	t.Run("tool-count", func(t *testing.T) {
		rec := httptest.NewRecorder()
		w := &responsesWriter{ResponseWriter: rec, opts: &responsesOptions{stream: true}}
		for i := 0; i <= maxResponsesToolCalls; i++ {
			_, err := w.Write([]byte(fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":%d,"function":{"name":"read"}}]}}]}`+"\n\n", i)))
			if i < maxResponsesToolCalls && err != nil {
				t.Fatalf("call %d failed: %v", i, err)
			}
			if i == maxResponsesToolCalls && (!errors.Is(err, errResponsesStreamTranslation) || len(w.toolCalls) != maxResponsesToolCalls || !strings.Contains(rec.Body.String(), "event: response.failed\n")) {
				t.Fatalf("tool limit: err=%v calls=%d", err, len(w.toolCalls))
			}
		}
	})
}

type responsesErrorWriter struct {
	header   http.Header
	writeErr error
	flushErr error
	failedAt int
	writes   int
}

func (w *responsesErrorWriter) Header() http.Header { return w.header }
func (w *responsesErrorWriter) WriteHeader(int)     {}
func (w *responsesErrorWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes >= w.failedAt && w.writeErr != nil {
		return 0, w.writeErr
	}
	return len(p), nil
}
func (w *responsesErrorWriter) FlushError() error { return w.flushErr }

func TestResponsesFailurePreservesDownstreamErrors(t *testing.T) {
	sinkErr := errors.New("downstream unavailable")
	for _, tc := range []struct {
		name     string
		stream   bool
		writeErr error
		flushErr error
		failedAt int
	}{
		{"nonstream-write", false, sinkErr, nil, 1},
		{"stream-write", true, sinkErr, nil, 3},
		{"stream-flush", true, nil, sinkErr, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := &responsesErrorWriter{header: make(http.Header), writeErr: tc.writeErr, flushErr: tc.flushErr, failedAt: tc.failedAt}
			w := &responsesWriter{ResponseWriter: dst, opts: &responsesOptions{stream: tc.stream}}
			body := `{"choices":[{"message":{"tool_calls":[{"function":{"arguments":"{}"}}]}}]}`
			if tc.stream {
				body = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}` + "\n\ndata: [DONE]\n\n"
			}
			_, err := w.Write([]byte(body))
			if !errors.Is(err, sinkErr) || errors.Is(err, errResponsesResponseTranslation) || errors.Is(err, errResponsesStreamTranslation) {
				t.Fatalf("downstream failure hidden: %v", err)
			}
		})
	}
}

func TestResponsesSplitFrameSeparator(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &responsesWriter{ResponseWriter: rec, opts: &responsesOptions{stream: true}}
	for _, part := range []string{`data: {"choices":[{"delta":{"content":"first"}}]}` + "\n", "\ndata: [DONE]\n", "\n"} {
		if _, err := w.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(rec.Body.String(), `"delta":"first"`) || !strings.Contains(rec.Body.String(), "event: response.completed\n") {
		t.Fatalf("split SSE separator lost frame: %s", rec.Body.String())
	}
}

func TestResponsesToolNameWaitsForCompletion(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &responsesWriter{ResponseWriter: rec, opts: &responsesOptions{stream: true}}
	frames := []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_","arguments":"{"}}]}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"weather","arguments":"}"}}]}}]}` + "\n\n",
	}
	for _, frame := range frames {
		if _, err := w.Write([]byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(rec.Body.String(), "response.output_item.added") || strings.Contains(rec.Body.String(), "response.function_call_arguments.delta") {
		t.Fatalf("tool emitted before its name was complete: %s", rec.Body.String())
	}
	if _, err := w.Write([]byte("data: [DONE]\n\n")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), `"name":"get_weather"`) || !strings.Contains(rec.Body.String(), `"arguments":"{}"`) {
		t.Fatalf("completed tool missing full name or arguments: %s", rec.Body.String())
	}
}

func TestResponsesCancellationWhileToolNameBuffered(t *testing.T) {
	var attempts atomic.Int32
	canceled := make(chan struct{})
	h, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if attempts.Add(1) == 1 {
			_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_"}}]}}]}`+"\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(canceled)
			return
		}
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"next"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	})
	sink := newAuditSink(t)
	h.cfg.Audit = sink
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/responses", strings.NewReader(`{"model":"glm-5.2","input":"hi","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	id := resp.Header.Get("X-Request-Id")
	cancel()
	resp.Body.Close()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("buffered tool request did not cancel upstream")
	}
	ev := waitForRecord(t, sink, id)
	if (ev.Outcome != "canceled" && ev.Outcome != "downstream_canceled") || ev.Usage != nil {
		t.Fatalf("canceled tool request audit: %+v", ev)
	}
	assertLifecycleHealthy(t, h.cfg.Pool)
	resp = responsesPost(t, server.URL, `{"model":"glm-5.2","input":"hi"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("account not reusable after cancellation: status=%d body=%s", resp.StatusCode, body)
	}
}
