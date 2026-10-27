package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTerminalReasonOverridesEarlierFinishAcrossProtocols(t *testing.T) {
	protocols := []struct {
		name, path, payload string
	}{
		{"chat", "/v1/chat/completions", `{"model":"glm-5.2","stream":%t,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`},
		{"responses", "/v1/responses", `{"model":"glm-5.2","stream":%t,"input":"hi","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`},
		{"messages", "/v1/messages", `{"model":"glm-5.2","stream":%t,"max_tokens":100,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`},
	}
	for _, terminal := range []struct {
		name, wire string
	}{
		{"named", "event: done\ndata: {\"finish_reason\":%q}\n\n"},
		{"done_text", "data: {\"text\":\"[DONE]\",\"finish_reason\":%q}\n\n"},
		{"onanswer_done_text", "event: onAnswer\ndata: {\"text\":\"[DONE]\",\"finish_reason\":%q}\n\n"},
	} {
		for _, tc := range []struct {
			finish, chat, responses, messages string
		}{
			{"length", `"finish_reason":"length"`, `"reason":"max_output_tokens"`, `"stop_reason":"max_tokens"`},
			{"content_filter", `"finish_reason":"content_filter"`, `"reason":"content_filter"`, `"stop_reason":"refusal"`},
			{"function_call", `"finish_reason":"tool_calls"`, `"status":"completed"`, `"stop_reason":"tool_use"`},
		} {
			for _, protocol := range protocols {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/stream=%t", terminal.name, tc.finish, protocol.name, stream), func(t *testing.T) {
						_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"stop"}]}`+"\n\n"+terminal.wire, tc.finish)
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
						if protocol.name == "responses" && tc.finish != "function_call" && !strings.Contains(string(body), `"status":"incomplete"`) {
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
}
