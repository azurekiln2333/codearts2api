package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"codearts2api/internal/upstream"
)

func TestChatIdenticalFirstMessagesStayFairWhileExplicitAndHistoryStaySticky(t *testing.T) {
	seen := make(chan int, 32)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		seen <- index
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	}))
	defer backend.Close()
	h, p := lifecycleHandler(t, 2, backend.URL)
	for index, a := range p.Accounts() {
		a.Client = upstream.NewWithChatEndpoint(time.Second, fmt.Sprintf("%s/%d", backend.URL, index))
	}
	first, err := parseChatRequest([]byte(`{"messages":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	derived := conversationIDFor(first, "")
	h.convAcct[derived] = p.Accounts()[0].Name
	var counts [2]int
	for i := 0; i < 6; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, lifecycleRequest(context.Background(), false))
		if rec.Code != http.StatusOK {
			t.Fatalf("same-message new session failed: %d %s", rec.Code, rec.Body.String())
		}
		counts[<-seen]++
	}
	if counts[0] != 3 || counts[1] != 3 {
		t.Fatalf("identical first prompts accidentally pinned different users: %v", counts)
	}
	for _, kind := range []string{"explicit", "header", "history"} {
		t.Run(kind, func(t *testing.T) {
			payload := `{"messages":[{"role":"user","content":"hello"}]}`
			header := ""
			switch kind {
			case "explicit":
				payload = `{"conversation_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","messages":[{"role":"user","content":"hello"}]}`
			case "header":
				header = "caller-provided-affinity"
			case "history":
				payload = `{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"prior answer"},{"role":"user","content":"continue"}]}`
			}
			parsed, err := parseChatRequest([]byte(payload))
			if err != nil {
				t.Fatal(err)
			}
			h.convAcct[conversationIDFor(parsed, header)] = p.Accounts()[1].Name
			for i := 0; i < 2; i++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
				if header != "" {
					req.Header.Set("X-Session-Id", header)
				}
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK || <-seen != 1 {
					t.Fatalf("valid %s affinity was lost: %d %s", kind, rec.Code, rec.Body.String())
				}
			}
		})
	}
	assertLifecycleHealthy(t, p)
}

func TestChatCachedCapabilityAvoidsWastingRotationAndRejectsStaleSticky(t *testing.T) {
	seen := make(chan int, 4)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		seen <- index
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	}))
	defer backend.Close()
	h, p := lifecycleHandler(t, 4, backend.URL)
	h.cfg.MaxRotate = 1
	for index, a := range p.Accounts() {
		a.Client = upstream.NewWithChatEndpoint(time.Second, fmt.Sprintf("%s/%d", backend.URL, index))
		models := []upstream.ModelInfo{{ID: "different-model"}}
		if index == 3 {
			models = []upstream.ModelInfo{{ID: "GLM-5.2"}}
		}
		upstream.SetAccountModels(a.UID, models)
	}
	const conversation = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	h.convAcct[conversation] = p.Accounts()[0].Name
	rec := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"glm-5.2","conversation_id":"`+conversation+`","messages":[{"role":"user","content":"hello"}]}`))
	h.ServeHTTP(rec, request)
	if rec.Code != http.StatusOK {
		t.Fatalf("cached capable account beyond rotation limit was missed: %d %s", rec.Code, rec.Body.String())
	}
	if index := <-seen; index != 3 || len(seen) != 0 {
		t.Fatalf("ineligible sticky/catalog accounts consumed attempts: first=%d pending=%d", index, len(seen))
	}
	assertLifecycleHealthy(t, p)
}
