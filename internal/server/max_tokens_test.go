package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codearts2api/internal/upstream"
)

func TestChatOutputLimitReachesUpstreamWithinCodeArtsBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		param  string
		stream bool
		want   float64
	}{
		{name: "legacy above", param: `"max_tokens":131072`, want: upstream.MaxOutputTokens},
		{name: "completion above", param: `"max_completion_tokens":131072`, stream: true, want: upstream.MaxOutputTokens},
		{name: "completion takes precedence", param: `"max_tokens":131072,"max_completion_tokens":4096`, want: 4096},
		{name: "boundary", param: `"max_tokens":65536`, stream: true, want: upstream.MaxOutputTokens},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan map[string]any, 1)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode upstream request: %v", err)
					return
				}
				seen <- body
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer backend.Close()
			h, _ := toolChoiceTestHandler(t, backend.URL)
			payload := fmt.Sprintf(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}],"stream":%t,%s}`, tc.stream, tc.param)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload)))
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if got := (<-seen)["max_tokens"]; got != tc.want {
				t.Fatalf("upstream max_tokens=%v want=%v", got, tc.want)
			}
		})
	}
}
