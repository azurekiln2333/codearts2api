package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codearts2api/internal/audit"
)

func TestAuditStartupDeadlineWhileWaitingIsTimeout(t *testing.T) {
	sink := newAuditSink(t)
	backend := okBackend(t)
	h, p := auditHandler(t, backend.URL, 1, sink, func(c *Config) {
		c.StartupTimeout = 50 * time.Millisecond
		c.AccountWaitTimeout = time.Second
	})
	name := p.Accounts()[0].Name
	if !p.AcquireLock(name) {
		t.Fatal("failed to occupy account")
	}
	defer p.ReleaseLock(name)
	rec, id := doChat(h, "glm-5.2")
	ev := waitForRecord(t, sink, id)
	if rec.Code != http.StatusGatewayTimeout || ev.Status != rec.Code || ev.Outcome != "timeout" || ev.Cause.Class != audit.CauseTimeout || ev.Account != nil || ev.FirstDataMS != nil {
		t.Fatalf("startup expiry while waiting was misclassified: status=%d event=%+v", rec.Code, ev)
	}
}

func TestAuditPreCanceledAdmissionHasNoResponseStatus(t *testing.T) {
	sink := newAuditSink(t)
	backend := okBackend(t)
	h, _ := auditHandler(t, backend.URL, 1, sink, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, lifecycleRequest(ctx, false))
	ev := waitForRecord(t, sink, rec.Header().Get("X-Request-Id"))
	if ev.Outcome != "canceled" || ev.Cause.Class != audit.CauseCancel || !ev.Canceled || ev.Committed || ev.Status != 0 || ev.Account != nil {
		t.Fatalf("pre-canceled caller invented an admission failure/status: %+v", ev)
	}
}

func TestAuditStreamFailurePreservesHTTP200AndOmitsContentOnDisk(t *testing.T) {
	const providerSecret = "fixture-provider-secret-must-not-be-recorded"
	const prompt = "fixture-prompt-must-not-be-recorded"
	const completion = "fixture-completion-must-not-be-recorded"
	const key = "fixture-service-key-must-not-be-recorded"
	sink := newAuditSink(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\""+completion+"\"}}]}\n\n")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "event: error\ndata: {\"code\":\"InferHub.4004.200\",\"message\":\"benefit not found "+providerSecret+"\"}\n\n")
	}))
	defer backend.Close()
	h, p := auditHandler(t, backend.URL, 1, sink, func(c *Config) { c.APIKey = key })
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"glm-5.2","stream":true,"messages":[{"role":"user","content":"`+prompt+`"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	ev := waitForRecord(t, sink, rec.Header().Get("X-Request-Id"))
	if rec.Code != http.StatusOK || !ev.Committed || ev.Status != http.StatusOK || ev.Outcome != "upstream_stream_error" || ev.Cause.Class != audit.CauseCapability || ev.Cause.Code != "InferHub.4004.200" || ev.FirstDataMS == nil {
		t.Fatalf("stream transport and execution failure were conflated: status=%d event=%+v", rec.Code, ev)
	}
	assertLifecycleHealthy(t, p)
	sink.Close(context.Background())
	raw, _ := json.Marshal(ev)
	files, err := filepath.Glob(filepath.Join(sink.Options().Dir, "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("audit file was not persisted: %v", err)
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, data...)
	}
	for _, forbidden := range []string{providerSecret, prompt, completion, key, "Bearer "} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("forbidden fixture content reached audit metadata: %q", forbidden)
		}
	}
}

func TestAuditDownstreamWriteTimeoutIsNotUpstreamTimeout(t *testing.T) {
	sink := newAuditSink(t)
	backend := okBackend(t)
	h, _ := auditHandler(t, backend.URL, 1, sink, func(c *Config) { c.StreamWriteTimeout = 30 * time.Millisecond })
	w := &deadlineLifecycleWriter{header: make(http.Header)}
	h.ServeHTTP(w, lifecycleRequest(context.Background(), false))
	ev := waitForRecord(t, sink, w.Header().Get("X-Request-Id"))
	if ev.Cause.Class != audit.CauseDownstream || ev.Outcome != "downstream_canceled" {
		t.Fatalf("downstream write deadline was classified as upstream/caller failure: %+v", ev)
	}
}

func TestProviderCodeRejectsMessageAndCredentialLikeSuffixes(t *testing.T) {
	for _, check := range []struct{ message, want string }{
		{"InferHub.002002009.404 request failed", "InferHub.002002009.404"},
		{"InferHub.ModelArts.81111.429 retry", "InferHub.ModelArts.81111.429"},
		{"TM.00001041 concurrent session limit", "TM.00001041"},
		{"InferHub.fixture-secret-123", ""},
		{"InferHub.4004.200-fixture-secret-123", "InferHub.4004.200"},
	} {
		if got := providerCode(check.message); got != check.want {
			t.Errorf("provider code for %q = %q want %q", check.message, got, check.want)
		}
	}
}
