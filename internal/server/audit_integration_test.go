package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codearts2api/internal/audit"
	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

func newAuditSink(t *testing.T) *audit.Sink {
	t.Helper()
	sink, err := audit.Open(audit.Options{
		Enabled:       true,
		Dir:           t.TempDir(),
		MaxQueue:      64,
		Recent:        128,
		MaxFileBytes:  1 << 16,
		MaxFiles:      2,
		RetentionDays: 1,
		FlushInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sink.Close(context.Background())
	})
	return sink
}

// waitForRecord polls the recent window until a record with the given id shows
// up, then returns it.
func waitForRecord(t *testing.T, sink *audit.Sink, id string) audit.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		page := sink.Snapshot(audit.Query{Limit: 200})
		for _, ev := range page.Events {
			if ev.ID == id {
				return ev
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no audit record for request id %q", id)
	return audit.Event{}
}

func auditHandler(t *testing.T, endpoint string, accounts int, sink *audit.Sink, mutate func(*Config)) (*Handler, *pool.Pool) {
	t.Helper()
	h, p := lifecycleHandlerConfig(t, accounts, endpoint, pool.Config{MaxConcurrent: 1, ErrThreshold: 3})
	h.cfg.Audit = sink
	h.cfg.MaxRotate = accounts
	if mutate != nil {
		mutate(&h.cfg)
	}
	return h, p
}

// doChat sends a non-streaming chat request and returns the recorder.
func doChat(h *Handler, model string) (*httptest.ResponseRecorder, string) {
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, rec.Header().Get("X-Request-Id")
}

func okBackend(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
		w.(http.Flusher).Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A successful request must produce exactly one correlated record whose request
// id matches the X-Request-Id response header.
func TestAuditRecordsCorrelatedSuccessWithCommittedStatus(t *testing.T) {
	sink := newAuditSink(t)
	backend := okBackend(t)
	h, _ := auditHandler(t, backend.URL, 1, sink, nil)

	rec, id := doChat(h, "glm-5.2")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if id == "" {
		t.Fatal("X-Request-Id header is missing")
	}
	ev := waitForRecord(t, sink, id)
	if ev.Outcome != "ok" {
		t.Fatalf("outcome = %q", ev.Outcome)
	}
	if !ev.Committed || ev.Status != http.StatusOK {
		t.Fatalf("committed=%v status=%d, want committed 200", ev.Committed, ev.Status)
	}
	if ev.Model == "" || ev.Account == nil || *ev.Account == "" {
		t.Fatalf("model/account not recorded: %+v", ev)
	}
	if ev.Attempts != 1 || ev.TotalMS < 0 {
		t.Fatalf("attempts/total not recorded: %+v", ev)
	}
	if ev.FirstDataMS == nil {
		t.Fatal("first_data_ms must be set for a served request")
	}
	if ev.Cause.Class != audit.CauseNone {
		t.Fatalf("cause = %+v", ev.Cause)
	}
}

// A startup timeout is recorded truthfully: the 504 was committed, no first data
// ever arrived, and the cause is a timeout rather than a cancel.
func TestAuditRecordsStartupTimeoutWithCommittedStatus(t *testing.T) {
	sink := newAuditSink(t)
	block := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept, then never send headers.
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	defer close(block)

	h, _ := auditHandler(t, backend.URL, 1, sink, func(c *Config) {
		c.StartupTimeout = 300 * time.Millisecond
		c.AccountWaitTimeout = 200 * time.Millisecond
	})
	rec, id := doChat(h, "glm-5.2")
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", rec.Code)
	}
	ev := waitForRecord(t, sink, id)
	if !ev.Committed || ev.Status != http.StatusGatewayTimeout {
		t.Fatalf("committed=%v status=%d, want committed 504", ev.Committed, ev.Status)
	}
	if ev.FirstDataMS != nil {
		t.Fatalf("first_data_ms must be null when no data arrived, got %d", *ev.FirstDataMS)
	}
	if ev.Cause.Class != audit.CauseTimeout {
		t.Fatalf("cause = %+v, want timeout", ev.Cause)
	}
	if ev.Outcome != "timeout" {
		t.Fatalf("outcome = %q, want timeout", ev.Outcome)
	}
}

// A model that no account can serve records the capability class with a fixed
// description and a provider code, never the raw provider message.
func TestAuditCapabilityCauseUsesFixedTextNotRawError(t *testing.T) {
	sink := newAuditSink(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"InferHub.002002009.404 The model is not registered, please request other model"}`)
	}))
	defer backend.Close()
	h, _ := auditHandler(t, backend.URL, 1, sink, nil)

	rec, id := doChat(h, "never-served-model")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	ev := waitForRecord(t, sink, id)
	if ev.Cause.Class != audit.CauseCapability {
		t.Fatalf("cause class = %q", ev.Cause.Class)
	}
	if ev.Cause.Text != "model not registered on account" {
		t.Fatalf("cause text = %q, want the fixed description", ev.Cause.Text)
	}
	if !strings.HasPrefix(ev.Cause.Code, "InferHub.") {
		t.Fatalf("provider code = %q", ev.Cause.Code)
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "please request other model") {
		t.Fatalf("raw provider message leaked into the record: %s", raw)
	}
}

// The audit record for a chat request must never contain credentials, prompts or
// completion content.
func TestAuditNeverRecordsSecretsOrContent(t *testing.T) {
	sink := newAuditSink(t)
	backend := okBackend(t)
	h, _ := auditHandler(t, backend.URL, 1, sink, nil)

	const secret = "sk-super-secret-value"
	const prompt = "PROMPT-MUST-NOT-BE-STORED"
	body := `{"model":"glm-5.2","messages":[{"role":"user","content":"` + prompt + `"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	id := rec.Header().Get("X-Request-Id")
	ev := waitForRecord(t, sink, id)

	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secret, prompt, "authorization", "Bearer"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("record leaked %q: %s", forbidden, raw)
		}
	}
}

// The audit API is protected by the existing bearer key.
func TestAuditAPIRegressRequiresAuth(t *testing.T) {
	sink := newAuditSink(t)
	backend := okBackend(t)
	h, _ := auditHandler(t, backend.URL, 1, sink, func(c *Config) { c.APIKey = "secret-key" })

	// No key.
	req := httptest.NewRequest(http.MethodGet, "/admin/api/requests", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated audit API = %d, want 401", rec.Code)
	}
	// Wrong key.
	req = httptest.NewRequest(http.MethodGet, "/admin/api/requests", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key = %d, want 401", rec.Code)
	}
	// Correct key sees the envelope.
	req = httptest.NewRequest(http.MethodGet, "/admin/api/requests?limit=5", nil)
	req.Header.Set("Authorization", "Bearer secret-key")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated audit API = %d body=%s", rec.Code, rec.Body.String())
	}
	var page map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"events", "next_before", "window", "dropped", "write_failures", "archive"} {
		if _, ok := page[key]; !ok {
			t.Fatalf("audit response missing %q: %s", key, rec.Body.String())
		}
	}
}

// The overview exposes actual limits and an explicit build identity.
func TestOverviewExposesEffectiveLimitsAndBuild(t *testing.T) {
	sink := newAuditSink(t)
	backend := okBackend(t)
	h, _ := auditHandler(t, backend.URL, 1, sink, func(c *Config) { c.APIKey = "k" })
	h.cfg.StartupTimeout = 42 * time.Second
	h.cfg.QueueMaxAttempts = 7

	req := httptest.NewRequest(http.MethodGet, "/admin/api/overview", nil)
	req.Header.Set("Authorization", "Bearer k")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("overview = %d", rec.Code)
	}
	var data struct {
		Effective map[string]any `json:"effective"`
		Build     map[string]any `json:"build"`
		Accounts  []map[string]any
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.Effective["startup_timeout"] != "42s" {
		t.Fatalf("effective startup_timeout = %v", data.Effective["startup_timeout"])
	}
	if data.Effective["max_rotate"] == nil || data.Effective["max_pending"] == nil {
		t.Fatalf("effective limits incomplete: %#v", data.Effective)
	}
	if _, ok := data.Build["known"]; !ok {
		t.Fatalf("build identity must state known explicitly: %#v", data.Build)
	}
	if len(data.Accounts) == 0 {
		t.Fatal("no accounts in overview")
	}
	for _, key := range []string{"schedulable", "last_error_at", "last_success_at", "last_failure_class"} {
		if _, ok := data.Accounts[0][key]; !ok {
			t.Fatalf("account snapshot missing %q: %#v", key, data.Accounts[0])
		}
	}
}

// With audit disabled nothing is recorded and the API says so.
func TestAuditDisabledHasNoSideEffects(t *testing.T) {
	backend := okBackend(t)
	h, _ := auditHandler(t, backend.URL, 1, nil, func(c *Config) { c.APIKey = "k" })

	body := `{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer k")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if id := rec.Header().Get("X-Request-Id"); id == "" {
		t.Fatal("X-Request-Id must still be set when audit is disabled")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/admin/api/requests", nil)
	req2.Header.Set("Authorization", "Bearer k")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	var page map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if disabled, _ := page["disabled"].(bool); !disabled {
		t.Fatalf("expected disabled=true: %s", rec2.Body.String())
	}
}

// A sealed upstream must still be bounded and leave no slot held.
func TestAuditBoundedOnSilentUpstream(t *testing.T) {
	sink := newAuditSink(t)
	block := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	defer close(block)
	h, p := auditHandler(t, backend.URL, 1, sink, func(c *Config) {
		c.StartupTimeout = 300 * time.Millisecond
		c.AccountWaitTimeout = 100 * time.Millisecond
	})
	started := time.Now()
	rec, id := doChat(h, "glm-5.2")
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("silent upstream not bounded: %s", elapsed)
	}
	if rec.Code == http.StatusOK {
		t.Fatal("sealed upstream must not succeed")
	}
	ev := waitForRecord(t, sink, id)
	if ev.Outcome == "ok" {
		t.Fatalf("outcome = %q", ev.Outcome)
	}
	for _, st := range p.List() {
		if active, _ := st["active_concurrent"].(int); active != 0 {
			t.Fatalf("leaked a slot: %#v", st)
		}
	}
}

var _ = upstream.New

// A client disconnect is recorded as canceled, never conflated with a timeout.
func TestAuditDistinguishesCancelFromTimeout(t *testing.T) {
	sink := newAuditSink(t)
	release := make(chan struct{})
	started := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	h, _ := auditHandler(t, backend.URL, 1, sink, func(c *Config) {
		c.StartupTimeout = 5 * time.Second
		c.AccountWaitTimeout = time.Second
	})

	ctx, cancel := context.WithCancel(context.Background())
	body := `{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(rec, req) }()
	<-started
	id := rec.Header().Get("X-Request-Id")
	cancel()
	<-done
	close(release)

	ev := waitForRecord(t, sink, id)
	if ev.Outcome != "canceled" {
		t.Fatalf("outcome = %q, want canceled", ev.Outcome)
	}
	if ev.Cause.Class != audit.CauseCancel {
		t.Fatalf("cause = %+v, want cancel", ev.Cause)
	}
	if !ev.Canceled {
		t.Fatal("canceled flag not set")
	}
	if ev.Committed || ev.Status != 0 {
		t.Fatalf("disconnect before commitment must not record a status: %+v", ev)
	}
}

// A queue-wait timeout is recorded as queue_wait_timeout, not admission_rejected.
func TestAuditQueueWaitTimeoutIsNotAdmissionRejected(t *testing.T) {
	sink := newAuditSink(t)
	hold := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-hold:
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	defer close(hold)
	h, _ := auditHandler(t, backend.URL, 1, sink, func(c *Config) {
		c.StartupTimeout = 10 * time.Second
		c.AccountWaitTimeout = 150 * time.Millisecond
	})

	// Occupy the only slot, then ask again: the second request can only wait.
	first := make(chan struct{})
	go func() {
		defer close(first)
		body := `{"model":"glm-5.2","messages":[{"role":"user","content":"hold"}],"stream":false}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}()
	time.Sleep(100 * time.Millisecond)

	rec, id := doChat(h, "glm-5.2")
	if rec.Code == http.StatusOK {
		t.Fatalf("expected queue failure, got %d", rec.Code)
	}
	ev := waitForRecord(t, sink, id)
	if ev.Outcome == "admission_rejected" {
		t.Fatalf("queue wait mislabeled as admission rejection: %+v", ev)
	}
	if ev.Outcome != "queue_wait_timeout" {
		t.Fatalf("outcome = %q, want queue_wait_timeout", ev.Outcome)
	}
}

// The recorded error_code and status must be exactly what the client received.
func TestAuditErrorCodeMatchesEmittedResponse(t *testing.T) {
	sink := newAuditSink(t)
	backends := map[string]struct {
		status int
		body   string
	}{
		"model_not_available": {http.StatusBadRequest, `{"error":"InferHub.002002009.404 The model is not registered"}`},
	}
	for wantCode, spec := range backends {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(spec.status)
			_, _ = io.WriteString(w, spec.body)
		}))
		h, _ := auditHandler(t, backend.URL, 1, sink, nil)
		rec, id := doChat(h, "never-served-filter-model")
		backend.Close()
		ev := waitForRecord(t, sink, id)
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body.String())
		}
		if ev.ErrorCode != envelope.Error.Code {
			t.Fatalf("recorded error_code %q != emitted %q", ev.ErrorCode, envelope.Error.Code)
		}
		if ev.ErrorCode != wantCode {
			t.Fatalf("error_code = %q, want %q", ev.ErrorCode, wantCode)
		}
		if ev.Status != rec.Code {
			t.Fatalf("recorded status %d != emitted %d", ev.Status, rec.Code)
		}
	}
}

// Retry-After offered to the client is captured from the response header, not
// guessed from the outcome. Upstream 429 on the only attempt produces the
// upstream_busy branch, which sets Retry-After: 2.
func TestAuditCapturesRetryAfterHeader(t *testing.T) {
	sink := newAuditSink(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"TPM limit reached"}`)
	}))
	defer backend.Close()
	h, _ := auditHandler(t, backend.URL, 1, sink, func(c *Config) {
		c.MaxRotate = 1
		c.QueueMaxAttempts = 1
		c.StartupTimeout = 5 * time.Second
		c.AccountWaitTimeout = time.Second
	})
	rec, id := doChat(h, "glm-5.2")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("client Retry-After = %q, want 2", got)
	}
	ev := waitForRecord(t, sink, id)
	if ev.RetryAfterS != 2 {
		t.Fatalf("recorded retry_after_s = %d, want 2", ev.RetryAfterS)
	}
	if ev.ErrorCode != "upstream_busy" {
		t.Fatalf("recorded error_code = %q, want upstream_busy", ev.ErrorCode)
	}
	if ev.Outcome != "upstream_saturated" {
		t.Fatalf("outcome = %q, want upstream_saturated", ev.Outcome)
	}
	if ev.Committed != true || ev.Status != http.StatusTooManyRequests {
		t.Fatalf("committed=%v status=%d", ev.Committed, ev.Status)
	}
}

// The requested model is recorded literally (including "auto") and canonical is
// records the effective upstream model, including the configured auto default.
func TestAuditRecordsRequestedModelLiterally(t *testing.T) {
	sink := newAuditSink(t)
	backend := okBackend(t)
	h, _ := auditHandler(t, backend.URL, 1, sink, func(c *Config) { c.DefaultModel = "glm-5.2" })

	rec, id := doChat(h, "auto")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	ev := waitForRecord(t, sink, id)
	if ev.Model != "auto" {
		t.Fatalf("requested model = %q, want the literal \"auto\"", ev.Model)
	}
	if ev.ModelCanonical != upstream.CanonicalModel(h.cfg.DefaultModel) {
		t.Fatalf("canonical must identify the effective auto default, got %q", ev.ModelCanonical)
	}

	rec2, id2 := doChat(h, "GLM-5.2")
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d", rec2.Code)
	}
	ev2 := waitForRecord(t, sink, id2)
	if ev2.Model != "GLM-5.2" || ev2.ModelCanonical == "" {
		t.Fatalf("model=%q canonical=%q", ev2.Model, ev2.ModelCanonical)
	}
}

// The record timestamp is the request start, not the completion time.
func TestAuditTimestampIsRequestStart(t *testing.T) {
	sink := newAuditSink(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
		w.(http.Flusher).Flush()
	}))
	defer backend.Close()
	h, _ := auditHandler(t, backend.URL, 1, sink, nil)

	before := time.Now().Add(-5 * time.Millisecond)
	rec, id := doChat(h, "glm-5.2")
	after := time.Now().Add(5 * time.Millisecond)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	ev := waitForRecord(t, sink, id)
	ts, err := time.Parse(time.RFC3339Nano, ev.TS)
	if err != nil {
		t.Fatalf("ts %q: %v", ev.TS, err)
	}
	if ts.Before(before) || ts.After(after) {
		t.Fatalf("ts %s outside request window [%s, %s] — it is not the start time", ts, before, after)
	}
}

// Model and account filters narrow the recent window (the API already supports
// them; this guards the contract the panel relies on).
func TestAuditQueryFiltersByModelAndAccount(t *testing.T) {
	sink := newAuditSink(t)
	backend := okBackend(t)
	h, p := auditHandler(t, backend.URL, 1, sink, nil)
	_, idOne := doChat(h, "glm-5.2")
	waitForRecord(t, sink, idOne)
	_, idTwo := doChat(h, "kimi-k2.6-vl")
	waitForRecord(t, sink, idTwo)

	acct := p.Accounts()[0].Name
	byModel := sink.Snapshot(audit.Query{Model: "kimi-k2.6-vl", Limit: 50})
	if len(byModel.Events) != 1 || byModel.Events[0].ID != idTwo {
		t.Fatalf("model filter returned %d events", len(byModel.Events))
	}
	byAccount := sink.Snapshot(audit.Query{Account: acct, Limit: 50})
	if len(byAccount.Events) != 2 {
		t.Fatalf("account filter returned %d events, want 2", len(byAccount.Events))
	}
	missing := sink.Snapshot(audit.Query{Account: "no-such-account", Limit: 50})
	if len(missing.Events) != 0 {
		t.Fatalf("unknown account filter returned %d events", len(missing.Events))
	}
}
