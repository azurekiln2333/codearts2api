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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/audit"
	"codearts2api/internal/auth"
	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

func anthropicFixture(t *testing.T, backend http.HandlerFunc) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(backend)
	t.Cleanup(up.Close)
	a := auth.New("test-account", "tester", "domain", "token", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", "")
	p, err := pool.New([]*auth.Auth{a}, pool.Config{MaxConcurrent: 1}, "")
	if err != nil {
		t.Fatal(err)
	}
	p.Accounts()[0].Client = upstream.NewWithChatEndpoint(5*time.Second, up.URL)
	h := NewHandler(Config{Pool: p, APIKey: "fixture-key", DefaultModel: "glm-5.2", MaxRotate: 1})
	front := httptest.NewServer(http.HandlerFunc(h.anthropicMessages))
	t.Cleanup(front.Close)
	return front
}

func anthropicPOST(t *testing.T, url, payload, key string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", url+"/v1/messages", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAnthropicRequestTranslation(t *testing.T) {
	raw := `{"model":"glm-5.2","max_tokens":512,"system":[{"type":"text","text":"sys"}],"messages":[{"role":"user","content":"first"},{"role":"assistant","content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"toolu_1","name":"weather","input":{"city":"Paris"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"sunny"}]}]}],"tools":[{"name":"weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}],"tool_choice":{"type":"tool","name":"weather"}}`
	converted, err := anthropicToChat([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	req, err := parseChatRequest(converted)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 4 || req.Messages[0].Role != "system" || req.Messages[0].Text != "sys" || req.Messages[2].ToolCalls[0].Name != "weather" || req.Messages[3].Role != "tool" || req.Messages[3].ToolCallID != "toolu_1" || req.Messages[3].Text != "sunny" || req.ToolChoice.Function != "weather" {
		t.Fatalf("translated request mismatch: %+v", req)
	}
	for _, input := range []string{
		`{"model":"x","max_tokens":1,"messages":[{"role":"user","content":[{"type":"image","source":{}}]}]}`,
		`{"model":"x","max_tokens":1,"messages":[{"role":"user","content":"x"}],"stop_sequences":["x"]}`,
		`{"model":"x","max_tokens":1,"messages":[{"role":"user","content":"x"}],"tool_choice":{"type":"auto","disable_parallel_tool_use":true}}`,
	} {
		if _, err := anthropicToChat([]byte(input)); err == nil {
			t.Fatalf("unsupported request accepted: %s", input)
		}
	}
}

func TestAnthropicNonStreamingAndAuth(t *testing.T) {
	front := anthropicFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")
	})
	payload := `{"model":"glm-5.2","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`
	bad := anthropicPOST(t, front.URL, payload, "wrong")
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("authentication status=%d", bad.StatusCode)
	}
	resp := anthropicPOST(t, front.URL, payload, "fixture-key")
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || body["type"] != "message" || body["stop_reason"] != "end_turn" {
		t.Fatalf("status=%d body=%+v", resp.StatusCode, body)
	}
	content := body["content"].([]any)[0].(map[string]any)
	usage := body["usage"].(map[string]any)
	if content["text"] != "hello" || usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(3) {
		t.Fatalf("body=%+v", body)
	}
}

func TestAnthropicInvalidRequestReturnsAnthropicError(t *testing.T) {
	front := anthropicFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request reached upstream") })
	resp := anthropicPOST(t, front.URL, `{"model":"glm-5.2","max_tokens":1,"messages":[{"role":"user","content":[{"type":"image"}]}]}`, "fixture-key")
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 || body["type"] != "error" || body["error"].(map[string]any)["type"] != "invalid_request_error" {
		t.Fatalf("status=%d body=%+v", resp.StatusCode, body)
	}
}

func TestAnthropicStreamingTextAndToolCall(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	front := anthropicFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"weather\",\"arguments\":\"{\\\"city\\\":\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"Paris\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")
		w.(http.Flusher).Flush()
	})
	result := make(chan *http.Response, 1)
	errs := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest("POST", front.URL+"/v1/messages", strings.NewReader(`{"model":"glm-5.2","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("x-api-key", "fixture-key")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errs <- err
			return
		}
		result <- resp
	}()
	var resp *http.Response
	select {
	case resp = <-result:
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		unblock()
		t.Fatal("stream did not start before upstream finished")
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	first, err := reader.ReadString('\n')
	if err != nil || first != "event: message_start\n" {
		unblock()
		t.Fatalf("first event=%q err=%v", first, err)
	}
	unblock()
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	wire := first + string(rest)
	for _, want := range []string{"event: content_block_start", `"text":"hello"`, `"type":"tool_use"`, `"name":"weather"`, `"type":"input_json_delta"`, `"stop_reason":"tool_use"`, `"input_tokens":10`, `"output_tokens":3`, "event: message_stop"} {
		if !strings.Contains(wire, want) {
			t.Fatalf("missing %q in stream: %s", want, wire)
		}
	}
}

func TestAnthropicCompletionToolInput(t *testing.T) {
	body := map[string]any{"model": "glm-5.2", "usage": map[string]any{"prompt_tokens": float64(2), "completion_tokens": float64(3)}, "choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"id": "call_a", "function": map[string]any{"name": "read", "arguments": `{"x":1}`}}}}, "finish_reason": "tool_calls"}}}
	got, err := anthropicCompletion(body)
	if err != nil {
		t.Fatal(err)
	}
	block := got["content"].([]any)[0].(map[string]any)
	if got["stop_reason"] != "tool_use" || block["type"] != "tool_use" || string(block["input"].(json.RawMessage)) != `{"x":1}` {
		t.Fatalf("bad tool completion: %+v", got)
	}
}

func TestAnthropicInterleavedToolStreaming(t *testing.T) {
	front := anthropicFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, data := range []string{
			`{"choices":[{"delta":{"content":"before"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"get_","arguments":""}}]}}]}`,
			`{"choices":[{"delta":{"content":"between"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"weather","arguments":"{\"x\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"other","arguments":"{\"y\":"}}]}}]}`,
			`{"choices":[{"delta":{"content":"after"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"arguments":"1}"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"2}"}}]},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4}}`,
			`[DONE]`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			w.(http.Flusher).Flush()
		}
	})
	resp := anthropicPOST(t, front.URL, `{"model":"glm-5.2","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, "fixture-key")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	blocks := map[int]map[string]any{}
	fragments := map[int]string{}
	text := map[int]string{}
	stops := map[int]bool{}
	current := -1
	var completed bool
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		index, _ := event["index"].(float64)
		switch event["type"] {
		case "content_block_start":
			if current != -1 || int(index) != len(blocks) {
				t.Fatalf("new content block %v opened while %d is active", index, current)
			}
			current = int(index)
			blocks[int(index)] = event["content_block"].(map[string]any)
		case "content_block_delta":
			if current != int(index) || current != len(blocks)-1 {
				t.Fatalf("delta for block %v while SDK current block is %d", index, current)
			}
			delta := event["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				fragments[current] += delta["partial_json"].(string)
			} else if delta["type"] == "text_delta" {
				text[current] += delta["text"].(string)
			}
		case "content_block_stop":
			if current != int(index) {
				t.Fatalf("block %v stopped while block %d is active", index, current)
			}
			stops[int(index)] = true
			current = -1
		case "error":
			t.Fatalf("unexpected stream error: %+v", event)
		case "message_stop":
			completed = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !completed || current != -1 || len(blocks) != 5 || blocks[1]["id"] != "call_a" || blocks[1]["name"] != "get_weather" || blocks[3]["id"] != "call_b" || fragments[1] != `{"x":1}` || fragments[3] != `{"y":2}` || text[0] != "before" || text[2] != "between" || text[4] != "after" || !stops[0] || !stops[1] || !stops[2] || !stops[3] || !stops[4] {
		t.Fatalf("invalid interleaved stream: blocks=%+v fragments=%+v text=%+v stops=%+v completed=%v", blocks, fragments, text, stops, completed)
	}
}

func TestAnthropicPrimaryToolStreamsBeforeCompletion(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	front := anthropicFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, data := range []string{
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"first","arguments":"{\"x\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"second","arguments":"{\"y\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1"}}]}}]}`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			w.(http.Flusher).Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}},{\"index\":1,\"function\":{\"arguments\":\"2}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	})
	defer unblock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, front.URL+"/v1/messages", strings.NewReader(`{"model":"glm-5.2","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-api-key", "fixture-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	var before strings.Builder
	for !strings.Contains(before.String(), `"partial_json":"1"`) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("first tool did not stream before completion: %v", err)
		}
		before.WriteString(line)
	}
	if !strings.Contains(before.String(), `"name":"first"`) || strings.Contains(before.String(), `"name":"second"`) || strings.Contains(before.String(), "event: message_stop") {
		t.Fatalf("interleaved tool was emitted before the first tool ended: %s", before.String())
	}
	unblock()
	remainder, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(remainder), `"name":"second"`) || !strings.Contains(string(remainder), `"partial_json":"{\"y\":2}"`) || !strings.Contains(string(remainder), "event: message_stop") {
		t.Fatalf("buffered tool did not complete: %s", remainder)
	}
}

func TestAnthropicInterleavedBufferLimit(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := &anthropicResponseWriter{ResponseWriter: recorder}
	if err := writer.begin(); err != nil {
		t.Fatal(err)
	}
	if err := writer.toolDelta(0, map[string]any{"id": "call_a", "function": map[string]any{"name": "read", "arguments": "{"}}); err != nil {
		t.Fatal(err)
	}
	err := writer.textDelta(strings.Repeat("x", maxBodyBytes))
	if !errors.Is(err, errAnthropicStreamTranslation) || !strings.Contains(recorder.Body.String(), "event: error") || writer.deferredSize != 0 {
		t.Fatalf("interleaved buffer was not bounded: err=%v size=%d", err, writer.deferredSize)
	}
}

func anthropicAuditFront(t *testing.T, backend http.HandlerFunc) (*httptest.Server, *pool.Pool, *audit.Sink) {
	t.Helper()
	up := httptest.NewServer(backend)
	t.Cleanup(up.Close)
	sink := newAuditSink(t)
	h, p := auditHandler(t, up.URL, 1, sink, func(c *Config) { c.APIKey = "fixture-key" })
	front := httptest.NewServer(http.HandlerFunc(h.anthropicMessages))
	t.Cleanup(front.Close)
	return front, p, sink
}

func TestAnthropicStreamAuditAndUpstreamError(t *testing.T) {
	front, p, sink := anthropicAuditFront(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "event: error\ndata: {\"code\":\"InferHub.4004.200\",\"message\":\"benefit not found\"}\n\n")
	})
	resp := anthropicPOST(t, front.URL, `{"model":"glm-5.2","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, "fixture-key")
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	event := waitForRecord(t, sink, resp.Header.Get("X-Request-Id"))
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), "event: error") || strings.Contains(string(data), "event: message_stop") || event.Outcome != "upstream_stream_error" || event.Status != http.StatusOK || event.Usage != nil {
		t.Fatalf("stream error not preserved: status=%d body=%s audit=%+v", resp.StatusCode, data, event)
	}
	assertLifecycleHealthy(t, p)
}

func TestAnthropicStreamInvalidToolInputFailsAudit(t *testing.T) {
	front, p, sink := anthropicAuditFront(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"function\":{\"name\":\"read\",\"arguments\":\"{\"}}]}}]}\n\ndata: [DONE]\n\n")
	})
	resp := anthropicPOST(t, front.URL, `{"model":"glm-5.2","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, "fixture-key")
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	event := waitForRecord(t, sink, resp.Header.Get("X-Request-Id"))
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), "event: error") || strings.Contains(string(data), "event: message_stop") || event.Status != http.StatusOK || event.Outcome != "upstream_stream_error" || event.Canceled || event.Usage != nil {
		t.Fatalf("invalid tool input was reported as success: body=%s audit=%+v", data, event)
	}
	assertLifecycleHealthy(t, p)
}

func TestAnthropicOversizedBodyAndBearer(t *testing.T) {
	var calls atomic.Int32
	front, p, sink := anthropicAuditFront(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	})
	resp := anthropicPOST(t, front.URL, strings.Repeat("x", maxBodyBytes+1), "fixture-key")
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	event := waitForRecord(t, sink, resp.Header.Get("X-Request-Id"))
	if resp.StatusCode != http.StatusRequestEntityTooLarge || event.Status != http.StatusRequestEntityTooLarge || event.Outcome != "invalid_request" || calls.Load() != 0 {
		t.Fatalf("oversized body: status=%d calls=%d audit=%+v", resp.StatusCode, calls.Load(), event)
	}
	assertLifecycleHealthy(t, p)
	req, err := http.NewRequest(http.MethodPost, front.URL+"/v1/messages", strings.NewReader(`{"model":"glm-5.2","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer fixture-key")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("bearer authentication status=%d calls=%d", resp.StatusCode, calls.Load())
	}
}

func TestAnthropicStreamDisconnectReleasesAccount(t *testing.T) {
	ended := make(chan struct{})
	front, p, sink := anthropicAuditFront(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(ended)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, front.URL+"/v1/messages", strings.NewReader(`{"model":"glm-5.2","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-api-key", "fixture-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	id := resp.Header.Get("X-Request-Id")
	cancel()
	resp.Body.Close()
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not stop after disconnect")
	}
	event := waitForRecord(t, sink, id)
	if event.Outcome == "ok" || !event.Canceled || event.Usage != nil {
		t.Fatalf("disconnect was not recorded: %+v", event)
	}
	assertLifecycleHealthy(t, p)
}

func TestAnthropicToolUsageMatchesAudit(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			front, p, sink := anthropicAuditFront(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"x\\\":1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3,\"total_tokens\":13}}\n\ndata: [DONE]\n\n")
			})
			payload := fmt.Sprintf(`{"model":"glm-5.2","max_tokens":100,"stream":%t,"messages":[{"role":"user","content":"hi"}]}`, stream)
			resp := anthropicPOST(t, front.URL, payload, "fixture-key")
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			event := waitForRecord(t, sink, resp.Header.Get("X-Request-Id"))
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"type":"tool_use"`) || !strings.Contains(string(body), `"input_tokens":10`) || !strings.Contains(string(body), `"output_tokens":3`) {
				t.Fatalf("wrong tool output or usage: status=%d body=%s", resp.StatusCode, body)
			}
			if event.Outcome != "ok" || event.Stream != stream || event.Usage == nil || event.Usage.Source != audit.UsageSourceUpstream || event.Usage.PromptTokens == nil || *event.Usage.PromptTokens != 10 || event.Usage.CompletionTokens == nil || *event.Usage.CompletionTokens != 3 {
				t.Fatalf("wrong tool usage audit: %+v", event)
			}
			assertLifecycleHealthy(t, p)
		})
	}
}

func TestAnthropicSlowRequestBodyUsesStartupBudget(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("partial Messages body reached upstream")
	}))
	defer backend.Close()
	sink := newAuditSink(t)
	h, p := auditHandler(t, backend.URL, 1, sink, func(c *Config) {
		c.StartupTimeout = 60 * time.Millisecond
		c.APIKey = "fixture-key"
	})
	front := httptest.NewServer(http.HandlerFunc(h.anthropicMessages))
	defer front.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, err = io.WriteString(conn, "POST /v1/messages HTTP/1.1\r\nHost: test\r\nContent-Length: 1000\r\nx-api-key: fixture-key\r\nContent-Type: application/json\r\n\r\n{")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("slow upload did not time out: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	event := waitForRecord(t, sink, resp.Header.Get("X-Request-Id"))
	if resp.StatusCode != http.StatusGatewayTimeout || !resp.Close || !strings.Contains(string(body), `"type":"error"`) || event.Outcome != "request_body_timeout" {
		t.Fatalf("slow upload: status=%d close=%t body=%s audit=%+v", resp.StatusCode, resp.Close, body, event)
	}
	assertLifecycleHealthy(t, p)
	release, err := p.AdmitRequest(context.Background())
	if err != nil {
		t.Fatalf("slow request leaked admission: %v", err)
	}
	release()
}

func TestAnthropicPreservesToolInputNumbers(t *testing.T) {
	const number = "9007199254740993"
	observed := make(chan string, 1)
	front := anthropicFixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		observed <- string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_result\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"n\\\":%s}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", number)
	})
	payload := fmt.Sprintf(`{"model":"glm-5.2","max_tokens":10,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use","id":"call_input","name":"read","input":{"n":%s}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_input","content":"ok"}]}]}`, number)
	resp := anthropicPOST(t, front.URL, payload, "fixture-key")
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), number) || !strings.Contains(<-observed, number) {
		t.Fatalf("tool integer changed: status=%d body=%s", resp.StatusCode, body)
	}
}

func TestAnthropicPreservesToolSchemaNumbers(t *testing.T) {
	const number = "9007199254740993"
	observed := make(chan string, 1)
	front := anthropicFixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		observed <- string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	})
	payload := fmt.Sprintf(`{"model":"glm-5.2","max_tokens":10,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"read","input_schema":{"type":"object","properties":{"n":{"type":"integer","minimum":%s}}}}]}`, number)
	resp := anthropicPOST(t, front.URL, payload, "fixture-key")
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(<-observed, number) {
		t.Fatalf("tool schema integer changed: status=%d", resp.StatusCode)
	}
}

func TestAnthropicNonstreamInvalidToolInputFailsAudit(t *testing.T) {
	front, p, sink := anthropicAuditFront(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"function\":{\"name\":\"read\",\"arguments\":\"{\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	})
	resp := anthropicPOST(t, front.URL, `{"model":"glm-5.2","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, "fixture-key")
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	event := waitForRecord(t, sink, resp.Header.Get("X-Request-Id"))
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), `"type":"error"`) || event.Status != http.StatusBadGateway || event.Outcome != "upstream_body_error" || event.Canceled || event.Usage != nil {
		t.Fatalf("invalid tool input was recorded as success: status=%d body=%s audit=%+v", resp.StatusCode, body, event)
	}
	assertLifecycleHealthy(t, p)
}

func TestAnthropicPreCanceledDoesNotWrite(t *testing.T) {
	sink := newAuditSink(t)
	backend := okBackend(t)
	h, _ := auditHandler(t, backend.URL, 1, sink, func(c *Config) { c.APIKey = "fixture-key" })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"glm-5.2","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`)).WithContext(ctx)
	req.Header.Set("x-api-key", "fixture-key")
	rec := httptest.NewRecorder()
	h.anthropicMessages(rec, req)
	event := waitForRecord(t, sink, rec.Header().Get("X-Request-Id"))
	if rec.Body.Len() != 0 || event.Committed || event.Outcome != "canceled" {
		t.Fatalf("canceled request wrote a response: body=%s audit=%+v", rec.Body.String(), event)
	}
}

func TestAnthropicRegisteredRoute(t *testing.T) {
	backend := okBackend(t)
	sink := newAuditSink(t)
	h, p := auditHandler(t, backend.URL, 1, sink, func(c *Config) { c.APIKey = "fixture-key" })
	front := httptest.NewServer(h)
	defer front.Close()
	resp := anthropicPOST(t, front.URL, `{"model":"glm-5.2","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, "fixture-key")
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"type":"message"`) {
		t.Fatalf("registered Messages route: status=%d body=%s", resp.StatusCode, body)
	}
	event := waitForRecord(t, sink, resp.Header.Get("X-Request-Id"))
	if event.Outcome != "ok" || event.Status != http.StatusOK {
		t.Fatalf("registered Messages route: status=%d body=%s audit=%+v", resp.StatusCode, body, event)
	}
	assertLifecycleHealthy(t, p)
}
