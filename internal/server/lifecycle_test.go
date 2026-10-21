package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

const lifecycleSuccess = "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

var lifecycleSequence atomic.Uint64

func lifecycleHandler(t *testing.T, count int, endpoint string) (*Handler, *pool.Pool) {
	return lifecycleHandlerConfig(t, count, endpoint, pool.Config{MaxConcurrent: 1, ErrThreshold: 1})
}

func lifecycleHandlerConfig(t *testing.T, count int, endpoint string, cfg pool.Config) (*Handler, *pool.Pool) {
	t.Helper()
	var auths []*auth.Auth
	sequence := lifecycleSequence.Add(1)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("%s-%d-%d", t.Name(), sequence, i)
		auths = append(auths, auth.New(id, "tester", "domain", "token", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", ""))
	}
	p, err := pool.New(auths, cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range p.Accounts() {
		a.Client = upstream.NewWithChatEndpoint(time.Second, endpoint)
	}
	return NewHandler(Config{Pool: p, MaxRotate: count, QueueMaxAttempts: 1, ErrThreshold: 1}), p
}

func lifecycleRequest(ctx context.Context, stream bool) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":"glm-5.2","messages":[{"role":"user","content":"hello"}],"stream":%t}`, stream))).WithContext(ctx)
}

func assertLifecycleHealthy(t *testing.T, p *pool.Pool) {
	t.Helper()
	for _, s := range p.List() {
		if s["err_count"] != 0 || s["active_concurrent"] != 0 || s["disabled"] != false {
			t.Fatalf("request leaked capacity or punished an account: %#v", s)
		}
		if !p.Healthy(s["name"].(string)) {
			t.Fatalf("request cooled a healthy account: %#v", s)
		}
	}
}

func TestChatCancellationDuringPreflightDoesNotPenalizeOrRotate(t *testing.T) {
	started := make(chan struct{}, 2)
	var attempts atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer backend.Close()
	h, p := lifecycleHandler(t, 2, backend.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), lifecycleRequest(ctx, false))
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream request did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled request retained preflight")
	}
	if attempts.Load() != 1 {
		t.Fatalf("canceled request rotated accounts: attempts=%d", attempts.Load())
	}
	assertLifecycleHealthy(t, p)
}

func TestChatUsesFreeAccountWithoutWaitingOnBusyFirst(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	}))
	defer backend.Close()
	h, p := lifecycleHandler(t, 2, backend.URL)
	first := p.Accounts()[0].Name
	if !p.AcquireLock(first) {
		t.Fatal("could not occupy first account")
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, lifecycleRequest(context.Background(), false))
		done <- rec
	}()
	select {
	case rec := <-done:
		p.ReleaseLock(first)
		if rec.Code != http.StatusOK {
			t.Fatalf("free account failed: %d %s", rec.Code, rec.Body.String())
		}
	case <-time.After(250 * time.Millisecond):
		p.ReleaseLock(first)
		<-done // Let the old implementation clean up before failing the regression.
		t.Fatal("free second account was blocked behind a busy first account")
	}
	assertLifecycleHealthy(t, p)
}

func TestChatModelFailureTriesAnotherEligibleAccount(t *testing.T) {
	var attempts atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if r.URL.Path == "/first" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"InferHub.002002009.404 model not registered"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	}))
	defer backend.Close()
	h, p := lifecycleHandler(t, 2, backend.URL)
	p.Accounts()[0].Client = upstream.NewWithChatEndpoint(time.Second, backend.URL+"/first")
	p.Accounts()[1].Client = upstream.NewWithChatEndpoint(time.Second, backend.URL+"/second")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, lifecycleRequest(context.Background(), false))
	if rec.Code != http.StatusOK || attempts.Load() != 2 {
		t.Fatalf("one account globally poisoned the model: status=%d attempts=%d body=%s", rec.Code, attempts.Load(), rec.Body.String())
	}
	assertLifecycleHealthy(t, p)
}

type failedLifecycleWriter struct{ header http.Header }

func (w *failedLifecycleWriter) Header() http.Header { return w.header }
func (w *failedLifecycleWriter) WriteHeader(int)     {}
func (w *failedLifecycleWriter) Write([]byte) (int, error) {
	return 0, errors.New("downstream disconnected")
}
func (w *failedLifecycleWriter) Flush() {}

func TestChatDownstreamWriteFailureDoesNotPenalize(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	}))
	defer backend.Close()
	h, p := lifecycleHandler(t, 2, backend.URL)
	h.ServeHTTP(&failedLifecycleWriter{header: make(http.Header)}, lifecycleRequest(context.Background(), true))
	assertLifecycleHealthy(t, p)
}

func TestChatQueueExhaustionDoesNotSleepAfterFinalAttempt(t *testing.T) {
	var attempts atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"TPM limit"}`)
	}))
	defer backend.Close()
	h, _ := lifecycleHandler(t, 1, backend.URL)
	h.cfg.QueueRetryDelay = 400 * time.Millisecond
	started := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, lifecycleRequest(context.Background(), false))
	if elapsed := time.Since(started); elapsed >= 200*time.Millisecond {
		t.Fatalf("last exhausted attempt slept needlessly: %s", elapsed)
	}
	if attempts.Load() != 1 || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("queue exhaustion must be bounded and retryable: attempts=%d status=%d headers=%v", attempts.Load(), rec.Code, rec.Header())
	}
}
