package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestToolFinishReasonAcrossProtocols(t *testing.T) {
	protocols := []struct {
		name, path, payload string
	}{
		{"chat", "/v1/chat/completions", `{"model":"glm-5.2","stream":%t,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`},
		{"responses", "/v1/responses", `{"model":"glm-5.2","stream":%t,"input":"hi","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`},
		{"messages", "/v1/messages", `{"model":"glm-5.2","stream":%t,"max_tokens":100,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`},
	}
	for _, tc := range []struct {
		name, finish, terminal, chat, responses, messages string
	}{
		{"length", `,"finish_reason":"length"`, "", `"finish_reason":"length"`, `"reason":"max_output_tokens"`, `"stop_reason":"max_tokens"`},
		{"content_filter", `,"finish_reason":"content_filter"`, "", `"finish_reason":"content_filter"`, `"reason":"content_filter"`, `"stop_reason":"refusal"`},
		{"tool_calls", `,"finish_reason":"tool_calls"`, "", `"finish_reason":"tool_calls"`, `"status":"completed"`, `"stop_reason":"tool_use"`},
		{"stop", `,"finish_reason":"stop"`, "", `"finish_reason":"tool_calls"`, `"status":"completed"`, `"stop_reason":"tool_use"`},
		{"legacy_function_call", `,"finish_reason":"function_call"`, "", `"finish_reason":"tool_calls"`, `"status":"completed"`, `"stop_reason":"tool_use"`},
		{"done_only", "", "", `"finish_reason":"tool_calls"`, `"status":"completed"`, `"stop_reason":"tool_use"`},
		{"length_done_event", `,"finish_reason":"length"`, "event: done\ndata: {\"error_code\":\"0\"}\n\n", `"finish_reason":"length"`, `"reason":"max_output_tokens"`, `"stop_reason":"max_tokens"`},
		{"length_end_event", `,"finish_reason":"length"`, "event: end\ndata: {\"error_code\":\"0\"}\n\n", `"finish_reason":"length"`, `"reason":"max_output_tokens"`, `"stop_reason":"max_tokens"`},
		{"filter_finish_event", `,"finish_reason":"content_filter"`, "event: finish\ndata: {\"error_code\":\"0\"}\n\n", `"finish_reason":"content_filter"`, `"reason":"content_filter"`, `"stop_reason":"refusal"`},
		{"length_done_text", `,"finish_reason":"length"`, "data: {\"text\":\"[DONE]\",\"error_code\":\"0\"}\n\n", `"finish_reason":"length"`, `"reason":"max_output_tokens"`, `"stop_reason":"max_tokens"`},
		{"filter_onanswer_done_text", `,"finish_reason":"content_filter"`, "event: onAnswer\ndata: {\"text\":\"[DONE]\",\"error_code\":\"0\"}\n\n", `"finish_reason":"content_filter"`, `"reason":"content_filter"`, `"stop_reason":"refusal"`},
	} {
		for _, protocol := range protocols {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", tc.name, protocol.name, stream), func(t *testing.T) {
					_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "text/event-stream")
						terminal := tc.terminal
						if terminal == "" {
							terminal = "data: [DONE]\n\n"
						}
						_, _ = fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}%s}]}`+"\n\n%s", tc.finish, terminal)
					})
					req, err := http.NewRequest(http.MethodPost, server.URL+protocol.path, strings.NewReader(fmt.Sprintf(protocol.payload, stream)))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Authorization", "Bearer test-key")
					req.Header.Set("Content-Type", "application/json")
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					body, err := io.ReadAll(resp.Body)
					if err != nil {
						t.Fatal(err)
					}
					want := map[string]string{"chat": tc.chat, "responses": tc.responses, "messages": tc.messages}[protocol.name]
					if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), want) {
						t.Fatalf("status=%d, want %s, body=%s", resp.StatusCode, want, body)
					}
					if protocol.name == "responses" && (strings.Contains(tc.finish, "length") || strings.Contains(tc.finish, "content_filter")) && !strings.Contains(string(body), `"status":"incomplete"`) {
						t.Fatalf("truncated or filtered response reported success: %s", body)
					}
					if stream && protocol.name == "chat" && strings.Count(string(body), `"finish_reason"`) != 1 {
						t.Fatalf("expected exactly one finish reason: %s", body)
					}
				})
			}
		}
	}
}

func TestTextToolFallbackPreservesIncompleteFinish(t *testing.T) {
	for _, finish := range []string{"length", "content_filter", "stop"} {
		t.Run(finish, func(t *testing.T) {
			_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, `data: {"choices":[{"delta":{"content":"{\"name\":\"lookup\",\"arguments\":{}}"},"finish_reason":%q}]}`+"\n\ndata: [DONE]\n\n", finish)
			})
			req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer test-key")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := finish
			if want == "stop" {
				want = "tool_calls"
			}
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"finish_reason":"`+want+`"`) || !strings.Contains(string(body), `"name":"lookup"`) {
				t.Fatalf("status=%d, want finish %s and tool call, body=%s", resp.StatusCode, want, body)
			}
		})
	}
}
