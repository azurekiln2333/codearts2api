package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/pool"
	"codearts2api/internal/telemetry"
	"codearts2api/internal/upstream"
)

// A startup timeout must interrupt a hanging credential-refresh wait: the caller
// returns 504 promptly, the account slot and the process-wide lifecycle ticket are
// released, the upstream chat is never reached, and the account keeps its health.
func TestChatStartupTimeoutDuringRefreshWaitReleasesEverything(t *testing.T) {
	stop := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(stop) }) }
	var refreshHits atomic.Int32
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshHits.Add(1)
		// Drain the form body first: a handler that never reads the request body
		// delays the server-side context cancellation.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	t.Cleanup(func() {
		release()
		sts.Close()
	})

	var chatCalls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chatCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(lifecycleSuccess))
	}))
	defer backend.Close()

	jwk, err := upstream.NewDPoPPrivateJWK()
	if err != nil {
		t.Fatal(err)
	}
	login := upstream.DefaultLoginConfig()
	login.STSHost = sts.URL // never a production host
	if login.ClientID == "" {
		t.Fatal("test setup: empty client id would fall back to production login config")
	}
	a := auth.New("refresh-wait", "user", "domain", "old-token", "ak", "sk",
		time.Now().Add(10*time.Minute).Format(time.RFC3339), "old-refresh", "verifier", map[string]string(jwk))
	a.SetClientID("refresh-wait-client")
	p, err := pool.New([]*auth.Auth{a}, pool.Config{
		MaxConcurrent: 1, MaxPending: 1, RefreshSkew: time.Hour,
		UpstreamTimeout: 300 * time.Millisecond, LoginConfig: login,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, acct := range p.Accounts() {
		acct.Client = upstream.NewWithChatEndpoint(300*time.Millisecond, backend.URL)
	}
	h := NewHandler(Config{Pool: p, MaxRotate: 1, QueueMaxAttempts: 1, ErrThreshold: 1})
	h.cfg.StartupTimeout = 120 * time.Millisecond
	h.cfg.Audit = newAuditSink(t)

	started := time.Now()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, lifecycleRequest(context.Background(), false))
	}()
	active := waitConnectionPhase(t, h, telemetry.WaitRefresh)
	if active.Account == nil || active.FirstSSEDataMS != nil || active.LastUpstreamProgressAgeMS != nil {
		t.Fatalf("refresh wait invented upstream chat activity: %+v", active)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh wait retained its request after startup timeout")
	}
	elapsed := time.Since(started)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("refresh wait escaped the startup budget: %s", elapsed)
	}
	if chatCalls.Load() != 0 {
		t.Fatalf("timed-out request still reached upstream chat: %d", chatCalls.Load())
	}
	if refreshHits.Load() != 1 {
		t.Fatalf("refresh hits=%d, want one shared refresh", refreshHits.Load())
	}
	assertLifecycleHealthy(t, p)
	ticket, err := p.AdmitRequest(context.Background())
	if err != nil {
		t.Fatalf("lifecycle ticket leaked: %v", err)
	}
	ticket()
	if p.Busy(p.Accounts()[0].Name) {
		t.Fatal("timed-out refresh wait retained the account slot")
	}
	// The pool's shared refresh continues in the background by design; it must not
	// retain the chat slot the canceled caller already released.
	waitLifecycle(t, func() bool { return !p.Busy(p.Accounts()[0].Name) }, "background refresh retained the account slot")
	if !p.Healthy(p.Accounts()[0].Name) {
		t.Fatal("canceled refresh wait changed account health")
	}
	event := assertFinishedConnection(t, h, p, active.ID)
	if event.Outcome != "timeout" || !traceHasPhase(event, telemetry.WaitRefresh) || event.Trace.FirstSSEDataMS != nil {
		t.Fatalf("refresh timeout lost the actual waiting phase: %+v", event)
	}
}
