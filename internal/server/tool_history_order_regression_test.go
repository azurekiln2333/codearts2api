package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestChatRejectsToolHistoryOrder(t *testing.T) {
	var calls atomic.Int32
	_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	for _, tc := range []struct{ name, messages string }{
		{"result before call", `[{"role":"user","content":"hi"},{"role":"tool","tool_call_id":"call_1","content":"result"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]}]`},
		{"user interrupts pending", `[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"user","content":"interruption"},{"role":"tool","tool_call_id":"call_1","content":"result"}]`},
		{"duplicate calls in batch", `[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}},{"id":"call_1","type":"function","function":{"name":"g","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"result"}]`},
		{"duplicate calls across batches", `[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"first"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"g","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"second"}]`},
		{"duplicate tool result", `[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"first"},{"role":"tool","tool_call_id":"call_1","content":"second"}]`},
		{"assistant interrupts pending", `[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"assistant","content":"interruption"},{"role":"tool","tool_call_id":"call_1","content":"result"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls.Load()
			resp := postChatContentTest(t, server.URL, "/v1/chat/completions", `{"messages":`+tc.messages+`}`)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "invalid_request") || calls.Load() != before {
				t.Fatalf("status=%d body=%s err=%v upstream calls=%d", resp.StatusCode, body, err, calls.Load()-before)
			}
		})
	}
}

func TestProtocolMultiToolHistoryReachesUpstream(t *testing.T) {
	seen := make(chan map[string]any, 1)
	_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream body: %v", err)
			return
		}
		seen <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	for _, tc := range []struct{ name, path, payload, assistantText string }{
		{"chat", "/v1/chat/completions", `{"model":"glm-5.2","stream":%t,"messages":[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"call_a","type":"function","function":{"name":"alpha","arguments":"{}"}},{"id":"call_b","type":"function","function":{"name":"beta","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_b","content":"B"},{"role":"tool","tool_call_id":"call_a","content":"A"},{"role":"user","content":"next"}]}`, ""},
		{"anthropic", "/v1/messages", `{"model":"glm-5.2","max_tokens":20,"stream":%t,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"text","text":"before "},{"type":"tool_use","id":"call_a","name":"alpha","input":{}},{"type":"text","text":"between "},{"type":"tool_use","id":"call_b","name":"beta","input":{}},{"type":"text","text":"after"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_b","content":"B"},{"type":"tool_result","tool_use_id":"call_a","content":"A"},{"type":"text","text":"next"}]}]}`, "before between after"},
		{"responses", "/v1/responses", `{"model":"glm-5.2","stream":%t,"input":[{"role":"user","content":"hi"},{"type":"function_call","call_id":"call_a","name":"alpha","arguments":"{}"},{"type":"function_call","call_id":"call_b","name":"beta","arguments":"{}"},{"type":"function_call_output","call_id":"call_b","output":"B"},{"type":"function_call_output","call_id":"call_a","output":"A"},{"role":"user","content":"next"}]}`, ""},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				resp := postChatContentTest(t, server.URL, tc.path, fmt.Sprintf(tc.payload, stream))
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil || resp.StatusCode != http.StatusOK {
					t.Fatalf("status=%d body=%s err=%v", resp.StatusCode, body, err)
				}
				upstreamBody := <-seen
				messages, ok := upstreamBody["messages"].([]any)
				if !ok || len(messages) != 5 {
					t.Fatalf("upstream messages=%#v", upstreamBody["messages"])
				}
				assistant := messages[1].(map[string]any)
				calls, ok := assistant["tool_calls"].([]any)
				if assistant["role"] != "assistant" || (tc.assistantText != "" && assistant["content"] != tc.assistantText) || !ok || len(calls) != 2 || calls[0].(map[string]any)["id"] != "call_a" || calls[1].(map[string]any)["id"] != "call_b" {
					t.Fatalf("assistant history=%#v", assistant)
				}
				if messages[2].(map[string]any)["tool_call_id"] != "call_b" || messages[3].(map[string]any)["tool_call_id"] != "call_a" || messages[4].(map[string]any)["content"] != "next" {
					t.Fatalf("tool or user history=%#v", messages)
				}
			})
		}
	}
}

func TestConversationIDIncludesNameAndPreservesUnnamed(t *testing.T) {
	unnamed := openAIMessage{Role: "user", Text: "hello"}
	if got := canonical(unnamed); got != "user\x00hello" {
		t.Fatalf("unnamed canonical changed: %q", got)
	}
	if got := conversationIDFor(&chatRequest{Messages: []openAIMessage{unnamed}}, ""); got != "87ce4613405ac8c20165d125a5c2219e" {
		t.Fatalf("unnamed conversation ID changed: %s", got)
	}
	alice := openAIMessage{Role: "user", Text: "hello", Name: "alice"}
	bob := openAIMessage{Role: "user", Text: "hello", Name: "bob"}
	spoof := openAIMessage{Role: "user", Text: "hello\x00name:alice"}
	ids := make(map[string]bool)
	for _, msg := range []openAIMessage{unnamed, alice, bob, spoof} {
		id := conversationIDFor(&chatRequest{Messages: []openAIMessage{msg}}, "")
		if ids[id] {
			t.Fatalf("name or content collided: %#v", msg)
		}
		ids[id] = true
	}
}

func TestChatNamedUsersHaveDistinctUpstreamChatIDs(t *testing.T) {
	seen := make(chan map[string]any, 1)
	_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream body: %v", err)
			return
		}
		seen <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	ids := make(map[string]string)
	for _, name := range []string{"", "alice", "bob", "alice"} {
		payload, err := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "name": name, "content": "hello"}}})
		if err != nil {
			t.Fatal(err)
		}
		resp := postChatContentTest(t, server.URL, "/v1/chat/completions", string(payload))
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d body=%s err=%v", resp.StatusCode, body, readErr)
		}
		upstreamBody := <-seen
		id, ok := upstreamBody["chat_id"].(string)
		if !ok || id == "" || upstreamBody["prompt_cache_key"] != id {
			t.Fatalf("invalid upstream chat ID: %#v", upstreamBody)
		}
		if previous := ids[name]; previous != "" && previous != id {
			t.Fatalf("name %q changed chat ID: %q vs %q", name, previous, id)
		}
		ids[name] = id
	}
	if ids[""] != "87ce4613405ac8c20165d125a5c2219e" || ids[""] == ids["alice"] || ids["alice"] == ids["bob"] || ids["bob"] == ids[""] {
		t.Fatalf("upstream chat IDs=%#v", ids)
	}
}
