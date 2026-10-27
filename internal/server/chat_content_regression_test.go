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

func TestChatDeveloperAndTextPartsReachUpstream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
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
			payload := fmt.Sprintf(`{"model":"glm-5.2","stream":%t,"messages":[
				{"role":"system","name":"policy","content":"original policy"},
				{"role":"developer","name":"developer","content":[{"type":"text","text":"developer "},{"type":"text","text":"policy"}]},
				{"role":"user","name":"person","content":[{"type":"text","text":"hello"},{"text":" world"}]},
				{"role":"assistant","name":"agent","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},
				{"role":"tool","tool_call_id":"call_1","content":[{"type":"text","text":"found"}]}
			],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`, stream)
			resp := postChatContentTest(t, server.URL, "/v1/chat/completions", payload)
			defer resp.Body.Close()
			responseBody, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("response status=%d body=%s err=%v", resp.StatusCode, responseBody, err)
			}
			var upstreamBody map[string]any
			select {
			case upstreamBody = <-seen:
			default:
				t.Fatal("no upstream inference request")
			}
			messages, ok := upstreamBody["messages"].([]any)
			if !ok || len(messages) != 5 {
				t.Fatalf("upstream messages=%#v", upstreamBody["messages"])
			}
			wantRoles := []string{"system", "system", "user", "assistant", "tool"}
			wantContent := []any{"original policy", "developer policy", "hello world", nil, "found"}
			wantNames := []any{"policy", "developer", "person", "agent", nil}
			for i := range wantRoles {
				message, ok := messages[i].(map[string]any)
				if !ok || message["role"] != wantRoles[i] || message["content"] != wantContent[i] || message["name"] != wantNames[i] {
					t.Fatalf("upstream message[%d]=%#v", i, messages[i])
				}
			}
			assistant := messages[3].(map[string]any)
			tool := messages[4].(map[string]any)
			if assistant["tool_calls"] == nil || tool["tool_call_id"] != "call_1" || upstreamBody["tools"] == nil {
				t.Fatalf("tool history or definitions lost: %#v", upstreamBody)
			}
		})
	}
}

func TestProtocolRejectsNonTextWithoutInference(t *testing.T) {
	var calls atomic.Int32
	_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	for _, tc := range []struct {
		name, path, payload, wantType, wantCode string
	}{
		{"chat-image", "/v1/chat/completions", `{"messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"https://example.test/a.png"}}]}]}`, "api_error", "invalid_request"},
		{"chat-image-with-text-type", "/v1/chat/completions", `{"messages":[{"role":"user","content":[{"type":"text","text":"describe","image_url":{"url":"https://example.test/a.png"}}]}]}`, "api_error", "invalid_request"},
		{"chat-stream-audio", "/v1/chat/completions", `{"stream":true,"messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"aGVsbG8=","format":"wav"}}]}]}`, "api_error", "invalid_request"},
		{"chat-file-with-text", "/v1/chat/completions", `{"messages":[{"role":"user","content":[{"type":"file","text":"ignored text","file":{"file_id":"file_1"}}]}]}`, "api_error", "invalid_request"},
		{"chat-developer-image", "/v1/chat/completions", `{"messages":[{"role":"developer","content":[{"type":"image_url","image_url":{"url":"https://example.test/a.png"}}]},{"role":"user","content":"hi"}]}`, "api_error", "invalid_request"},
		{"chat-invalid-content", "/v1/chat/completions", `{"messages":[{"role":"user","content":{"image_url":"https://example.test/a.png"}}]}`, "api_error", "invalid_request"},
		{"responses-image", "/v1/responses", `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.test/a.png"}]}]}`, "api_error", "invalid_request"},
		{"messages-image", "/v1/messages", `{"model":"glm-5.2","max_tokens":10,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]}]}`, "invalid_request_error", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls.Load()
			resp := postChatContentTest(t, server.URL, tc.path, tc.payload)
			defer resp.Body.Close()
			var body struct {
				Type  string `json:"type"`
				Error struct {
					Type    string `json:"type"`
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusBadRequest || body.Error.Type != tc.wantType || body.Error.Code != tc.wantCode || body.Error.Message == "" {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, body)
			}
			if tc.path == "/v1/messages" && body.Type != "error" {
				t.Fatalf("Messages error format=%+v", body)
			}
			if got := calls.Load(); got != before {
				t.Fatalf("unsupported content reached upstream: before=%d after=%d", before, got)
			}
		})
	}
}

func TestChatRejectsUnsupportedGenerationConstraints(t *testing.T) {
	var calls atomic.Int32
	_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	for _, tc := range []struct{ name, field, parameter string }{
		{"multiple choices", "n", `"n":2`},
		{"zero choices", "n", `"n":0`},
		{"json object", "response_format", `"response_format":{"type":"json_object"}`},
		{"json schema", "response_format", `"response_format":{"type":"json_schema","json_schema":{"name":"x","schema":{"type":"object"}}}`},
		{"text format extra", "response_format", `"response_format":{"type":"text","unknown":true}`},
		{"stop string", "stop", `"stop":"END"`},
		{"stop empty array", "stop", `"stop":[]`},
		{"disable parallel tools", "parallel_tool_calls", `"tools":[{"type":"function","function":{"name":"lookup"}}],"parallel_tool_calls":false`},
		{"frequency penalty", "frequency_penalty", `"frequency_penalty":0.5`},
		{"presence penalty", "presence_penalty", `"presence_penalty":-0.5`},
		{"bias", "logit_bias", `"logit_bias":{"42":-5}`},
		{"seed", "seed", `"seed":42`},
		{"logprobs", "logprobs", `"logprobs":true`},
		{"top logprobs", "top_logprobs", `"top_logprobs":1`},
		{"negative top logprobs", "top_logprobs", `"top_logprobs":-1`},
		{"audio modality", "modalities", `"modalities":["text","audio"]`},
		{"audio output", "audio", `"audio":{"voice":"alloy","format":"wav"}`},
		{"prediction", "prediction", `"prediction":{"type":"content","content":"example"}`},
		{"legacy functions", "functions", `"functions":[{"name":"lookup"}]`},
		{"legacy function choice", "function_call", `"function_call":{"name":"lookup"}`},
		{"stream obfuscation", "include_obfuscation", `"stream_options":{"include_obfuscation":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls.Load()
			payload := `{"messages":[{"role":"user","content":"hello"}],` + tc.parameter + `}`
			resp := postChatContentTest(t, server.URL, "/v1/chat/completions", payload)
			defer resp.Body.Close()
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusBadRequest || body.Error.Code != "invalid_request" || !strings.Contains(body.Error.Message, tc.field) {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, body)
			}
			if got := calls.Load(); got != before {
				t.Fatalf("unsupported constraint reached upstream: before=%d after=%d", before, got)
			}
		})
	}
}

func TestChatAcceptsNeutralGenerationDefaults(t *testing.T) {
	var calls atomic.Int32
	_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	for _, tc := range []struct{ name, options string }{
		{"nulls", `"n":null,"response_format":null,"stop":null,"parallel_tool_calls":null,"frequency_penalty":null,"presence_penalty":null,"logit_bias":null,"seed":null,"logprobs":null,"top_logprobs":null,"modalities":null,"audio":null,"prediction":null,"functions":null,"function_call":null,"stream_options":null`},
		{"defaults", `"n":1,"response_format":{"type":"text"},"parallel_tool_calls":true,"frequency_penalty":0,"presence_penalty":0,"logit_bias":{},"logprobs":false,"top_logprobs":0,"modalities":["text"],"functions":[],"function_call":"auto","stream_options":{"include_obfuscation":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls.Load()
			payload := `{"messages":[{"role":"user","content":"hello"}],` + tc.options + `}`
			resp := postChatContentTest(t, server.URL, "/v1/chat/completions", payload)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != http.StatusOK || calls.Load() != before+1 {
				t.Fatalf("status=%d body=%s err=%v upstream calls=%d", resp.StatusCode, body, err, calls.Load()-before)
			}
		})
	}
}

func TestChatRejectsUnsupportedHistoryAndToolChoice(t *testing.T) {
	var calls atomic.Int32
	_, server := responsesFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	for _, tc := range []struct{ name, payload, field string }{
		{"unknown role", `{"messages":[{"role":"critic","content":"policy"},{"role":"user","content":"hi"}]}`, "role"},
		{"legacy role", `{"messages":[{"role":"function","name":"lookup","content":"data"},{"role":"user","content":"hi"}]}`, "role"},
		{"missing role", `{"messages":[{"content":"policy"},{"role":"user","content":"hi"}]}`, "role"},
		{"legacy assistant call", `{"messages":[{"role":"assistant","function_call":{"name":"lookup","arguments":"{}"}},{"role":"user","content":"hi"}]}`, "function_call"},
		{"assistant audio", `{"messages":[{"role":"assistant","audio":{"id":"audio_1"}},{"role":"user","content":"hi"}]}`, "audio"},
		{"assistant refusal", `{"messages":[{"role":"assistant","refusal":"cannot answer"},{"role":"user","content":"hi"}]}`, "refusal"},
		{"malformed tool calls", `{"messages":[{"role":"assistant","tool_calls":{"id":"call_1"}},{"role":"user","content":"hi"}]}`, "tool_calls"},
		{"unsupported tool call type", `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_1","type":"custom","function":{"name":"lookup","arguments":"{}"}}]},{"role":"user","content":"hi"}]}`, "tool_calls"},
		{"missing tool call id", `{"messages":[{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"user","content":"hi"}]}`, "tool_calls"},
		{"missing tool call name", `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"arguments":"{}"}}]},{"role":"user","content":"hi"}]}`, "tool_calls"},
		{"tool calls on user", `{"messages":[{"role":"user","content":"hi","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]}`, "tool_calls"},
		{"unfinished tool call", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]}`, "tool_calls"},
		{"orphan tool result", `{"messages":[{"role":"user","content":"hi"},{"role":"tool","tool_call_id":"call_1","content":"result"}]}`, "tool_call_id"},
		{"tool result missing id", `{"messages":[{"role":"user","content":"hi"},{"role":"tool","content":"result"}]}`, "tool_call_id"},
		{"tool id on user", `{"messages":[{"role":"user","content":"hi","tool_call_id":"call_1"}]}`, "tool_call_id"},
		{"unknown choice", `{"messages":[{"role":"user","content":"hi"}],"tool_choice":"sometimes"}`, "tool_choice"},
		{"choice wrong type", `{"messages":[{"role":"user","content":"hi"}],"tool_choice":["auto"]}`, "tool_choice"},
		{"choice unsupported object", `{"messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"custom","function":{"name":"lookup"}}}`, "tool_choice"},
		{"choice missing type", `{"messages":[{"role":"user","content":"hi"}],"tool_choice":{"function":{"name":"lookup"}}}`, "tool_choice"},
		{"choice missing name", `{"messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"function","function":{}}}`, "tool_choice"},
		{"choice extra constraint", `{"messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"function","function":{"name":"lookup","constraint":true}}}`, "tool_choice"},
		{"required without tools", `{"messages":[{"role":"user","content":"hi"}],"tool_choice":"required"}`, "tool_choice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls.Load()
			resp := postChatContentTest(t, server.URL, "/v1/chat/completions", tc.payload)
			defer resp.Body.Close()
			var body struct {
				Error struct{ Code, Message string } `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusBadRequest || body.Error.Code != "invalid_request" || !strings.Contains(body.Error.Message, tc.field) || calls.Load() != before {
				t.Fatalf("status=%d error=%+v upstream calls=%d", resp.StatusCode, body.Error, calls.Load()-before)
			}
		})
	}
}

func TestChatAcceptsNeutralParallelToolCalls(t *testing.T) {
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
	for _, tc := range []struct{ name, options, wantChoice string }{
		{"no tools", `"parallel_tool_calls":false`, ""},
		{"empty tools", `"tools":[],"tool_choice":"auto","parallel_tool_calls":false`, ""},
		{"tools disabled", `"tools":[{"type":"function","function":{"name":"lookup"}}],"tool_choice":"none","parallel_tool_calls":false`, "none"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				payload := fmt.Sprintf(`{"stream":%t,"messages":[{"role":"assistant","content":"ok","audio":null,"function_call":null,"refusal":null},{"role":"user","content":"hi"}],%s}`, stream, tc.options)
				resp := postChatContentTest(t, server.URL, "/v1/chat/completions", payload)
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil || resp.StatusCode != http.StatusOK {
					t.Fatalf("status=%d body=%s err=%v", resp.StatusCode, body, err)
				}
				upstreamBody := <-seen
				if tc.wantChoice == "" && upstreamBody["tool_choice"] != nil || tc.wantChoice != "" && upstreamBody["tool_choice"] != tc.wantChoice {
					t.Fatalf("upstream tool_choice=%#v, want %q", upstreamBody["tool_choice"], tc.wantChoice)
				}
			})
		}
	}
}

func postChatContentTest(t *testing.T, base, path, payload string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(payload))
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
