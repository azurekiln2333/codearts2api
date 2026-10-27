package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"codearts2api/internal/upstream"
)

func TestSnapshotRewriteAcrossProtocolRoutes(t *testing.T) {
	for _, protocol := range []struct {
		name, path, body, errorEvent, successEvent string
	}{
		{"chat", "/v1/chat/completions", `{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}],"stream":%t,"stream_options":{"include_usage":true}}`, "event: error", `"finish_reason"`},
		{"responses", "/v1/responses", `{"model":"glm-5.2","input":"hi","store":false,"stream":%t}`, "event: response.failed", "event: response.completed"},
		{"messages", "/v1/messages", `{"model":"glm-5.2","max_tokens":50,"messages":[{"role":"user","content":"hi"}],"stream":%t}`, "event: error", "event: message_stop"},
	} {
		for _, tc := range []struct {
			name, frame, replacement string
		}{
			{"equal length", `data: {"text":"XYZ_NEW"}` + "\n", "XYZ_NEW"},
			{"shorter", "event: onAnswer\n" + `data: {"text":"XY"}` + "\n", "XY"},
			{"longer output", `data: {"output":[{"type":"output_text","text":"XYZ_NEW_LONG"}]}` + "\n", "XYZ_NEW_LONG"},
		} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", protocol.name, tc.name, stream), func(t *testing.T) {
					var mu sync.Mutex
					sessions := make(map[string]bool)
					var busyCalls, idleCalls, chatCalls int
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						id := r.Header.Get("user-session-id")
						mu.Lock()
						defer mu.Unlock()
						if r.Method == http.MethodPut && r.URL.Path == "/snap-manager/v1/chat-session/heartbeat" {
							switch r.URL.Query().Get("status") {
							case "busy":
								if id == "" || sessions[id] {
									t.Error("invalid remote session admission")
								}
								sessions[id] = true
								busyCalls++
							case "idle":
								if !sessions[id] {
									t.Error("remote session released without admission")
								}
								delete(sessions, id)
								idleCalls++
							default:
								t.Error("invalid remote session status")
							}
							_, _ = io.WriteString(w, `{}`)
							return
						}
						if r.Method != http.MethodPost || !sessions[id] {
							t.Error("inference did not own an admitted remote session")
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						chatCalls++
						w.Header().Set("Content-Type", "text/event-stream")
						if chatCalls > 1 {
							_, _ = io.WriteString(w, lifecycleSuccess)
							return
						}
						_, _ = io.WriteString(w, `data: {"text":"ABC_OLD"}`+"\n"+tc.frame+`data: {"usage":{"prompt_tokens":2,"completion_tokens":7}}`+"\n"+"data: [DONE]\n")
					}))
					defer backend.Close()
					sink := newAuditSink(t)
					h, p := auditHandler(t, backend.URL, 1, sink, func(cfg *Config) { cfg.APIKey = "fixture-key" })
					p.Accounts()[0].Client = upstream.NewWithSessionEndpoint(3*time.Second, backend.URL)
					front := httptest.NewServer(h)
					defer front.Close()
					req, err := http.NewRequest(http.MethodPost, front.URL+protocol.path, strings.NewReader(fmt.Sprintf(protocol.body, stream)))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Authorization", "Bearer fixture-key")
					req.Header.Set("Content-Type", "application/json")
					client := &http.Client{Timeout: 5 * time.Second}
					resp, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					data, readErr := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if readErr != nil {
						t.Fatal(readErr)
					}
					body := string(data)
					event := waitForRecord(t, sink, resp.Header.Get("X-Request-Id"))
					if resp.StatusCode != http.StatusOK || !event.Committed || event.Status != http.StatusOK || event.Canceled || event.Attempts != 1 {
						t.Fatalf("status=%d body=%s audit=%+v", resp.StatusCode, body, event)
					}
					if stream {
						if !strings.Contains(body, "ABC_OLD") || strings.Contains(body, tc.replacement) || !strings.Contains(body, protocol.errorEvent) || strings.Contains(body, protocol.successEvent) || event.Outcome != "upstream_stream_error" || event.Usage != nil {
							t.Fatalf("rewrite reported as successful stream: body=%s audit=%+v", body, event)
						}
						metadata, err := json.Marshal(event)
						if err != nil || strings.Contains(string(metadata), "ABC_OLD") || strings.Contains(string(metadata), tc.replacement) {
							t.Fatalf("audit exposed stream content: %s err=%v", metadata, err)
						}
					} else if !strings.Contains(body, tc.replacement) || strings.Contains(body, "ABC_OLD") || event.Outcome != "ok" || event.Usage == nil {
						t.Fatalf("aggregate did not return final snapshot: body=%s audit=%+v", body, event)
					}
					assertLifecycleHealthy(t, p)
					assertRemoteSessionAdmissionReusable(t, p)
					wantCalls := 1
					if stream {
						reuse, err := http.NewRequest(http.MethodPost, front.URL+"/v1/chat/completions", strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"reuse"}]}`))
						if err != nil {
							t.Fatal(err)
						}
						reuse.Header.Set("Authorization", "Bearer fixture-key")
						reuseResp, err := client.Do(reuse)
						if err != nil {
							t.Fatal(err)
						}
						reuseBody, err := io.ReadAll(reuseResp.Body)
						_ = reuseResp.Body.Close()
						reuseEvent := waitForRecord(t, sink, reuseResp.Header.Get("X-Request-Id"))
						if err != nil || reuseResp.StatusCode != http.StatusOK || !strings.Contains(string(reuseBody), `"content":"ok"`) || reuseEvent.Outcome != "ok" {
							t.Fatalf("account could not be reused: status=%d body=%s audit=%+v err=%v", reuseResp.StatusCode, reuseBody, reuseEvent, err)
						}
						wantCalls++
						assertLifecycleHealthy(t, p)
					}
					mu.Lock()
					defer mu.Unlock()
					if len(sessions) != 0 || busyCalls != wantCalls || idleCalls != wantCalls || chatCalls != wantCalls {
						t.Fatalf("remote session leaked: active=%d busy=%d idle=%d chat=%d", len(sessions), busyCalls, idleCalls, chatCalls)
					}
				})
			}
		}
	}
}
