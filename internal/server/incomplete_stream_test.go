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

func TestIncompleteUpstreamEOFDoesNotReportSuccess(t *testing.T) {
	for _, protocol := range []struct {
		name, path, body, errorEvent, successEvent string
	}{
		{"chat", "/v1/chat/completions", `{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}],"stream":%t}`, "event: error", ""},
		{"responses", "/v1/responses", `{"model":"glm-5.2","input":"hi","store":false,"stream":%t}`, "event: response.failed", "event: response.completed"},
		{"messages", "/v1/messages", `{"model":"glm-5.2","max_tokens":50,"messages":[{"role":"user","content":"hi"}],"stream":%t}`, "event: error", "event: message_stop"},
	} {
		for _, tc := range []struct {
			name, wire string
			emitted    bool
		}{
			{name: "empty"},
			{name: "heartbeat", wire: ": keepalive\n\n"},
			{name: "usage-only", wire: "data: {\"usage\":{\"prompt_tokens\":1}}\n\n"},
			{name: "partial", wire: "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", emitted: true},
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
						_, _ = io.WriteString(w, tc.wire)
					}))
					defer backend.Close()
					sink := newAuditSink(t)
					h, p := auditHandler(t, backend.URL, 1, sink, nil)
					p.Accounts()[0].Client = upstream.NewWithSessionEndpoint(time.Second, backend.URL)
					body := fmt.Sprintf(protocol.body, stream)
					req := httptest.NewRequest(http.MethodPost, protocol.path, strings.NewReader(body))
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)
					event := waitForRecord(t, sink, rec.Header().Get("X-Request-Id"))
					wantStatus, wantOutcome := http.StatusBadGateway, "upstream_body_error"
					if stream {
						wantOutcome = "upstream_stream_error"
						if tc.emitted {
							wantStatus = http.StatusOK
						}
					}
					if wantStatus == http.StatusOK {
						if !strings.Contains(rec.Body.String(), "partial") || !strings.Contains(rec.Body.String(), protocol.errorEvent) || protocol.successEvent != "" && strings.Contains(rec.Body.String(), protocol.successEvent) {
							t.Fatalf("missing stream error: %s", rec.Body.String())
						}
					} else if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || !json.Valid(rec.Body.Bytes()) || !strings.Contains(rec.Body.String(), `"error"`) {
						t.Fatalf("expected JSON error: content-type=%s body=%s", rec.Header().Get("Content-Type"), rec.Body.String())
					}
					if rec.Code != wantStatus || !event.Committed || event.Status != wantStatus || event.Outcome != wantOutcome || event.Usage != nil || event.Canceled || event.Attempts != 1 {
						t.Fatalf("status=%d body=%s audit=%+v", rec.Code, rec.Body.String(), event)
					}
					assertLifecycleHealthy(t, p)
					assertRemoteSessionAdmissionReusable(t, p)
					mu.Lock()
					defer mu.Unlock()
					if len(sessions) != 0 || busyCalls != 1 || idleCalls != 1 || chatCalls != 1 {
						t.Fatalf("remote session leaked: active=%d busy=%d idle=%d chat=%d", len(sessions), busyCalls, idleCalls, chatCalls)
					}
				})
			}
		}
	}
}
