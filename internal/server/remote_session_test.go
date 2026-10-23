package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

// The provider retains its session after HTTP completion until an explicit idle.
// An HTTP-only client can therefore finish three responses and still be full.
type remoteSessionFixture struct {
	mu                   sync.Mutex
	busy                 map[string]bool
	seen                 map[string]bool
	busyCalls, idleCalls int
	chatCalls, maximum   int
	stall                bool
	holdTransport        bool
	started              chan struct{}
	idleGate             <-chan struct{}
	idleStarted          chan struct{}
}

func newRemoteSessionBackend(t *testing.T) (*remoteSessionFixture, *httptest.Server) {
	t.Helper()
	f := &remoteSessionFixture{busy: map[string]bool{}, seen: map[string]bool{}, started: make(chan struct{}, 1)}
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("user-session-id")
		if r.Method == http.MethodPut && r.URL.Path == "/snap-manager/v1/chat-session/heartbeat" {
			body, err := io.ReadAll(io.LimitReader(r.Body, 100))
			if err != nil || string(body) != "{}" || id == "" || !strings.Contains(r.Header.Get("Authorization"), "user-session-id") || r.Header.Get(upstream.HeaderMaasType) != "" {
				t.Error("heartbeat does not have signed session identity, body, or route")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			switch r.URL.Query().Get("status") {
			case "busy":
				f.busy[id] = true
				f.busyCalls++
			case "idle":
				delete(f.busy, id)
				f.idleCalls++
			default:
				t.Error("invalid heartbeat status")
			}
			gate, started := f.idleGate, f.idleStarted
			f.mu.Unlock()
			if r.URL.Query().Get("status") == "idle" && gate != nil {
				if started != nil {
					select {
					case started <- struct{}{}:
					default:
					}
				}
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
			}
			_, _ = io.WriteString(w, `{}`)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.mu.Lock()
		f.chatCalls++
		if id == "" {
			id = fmt.Sprintf("untracked-%d", f.chatCalls)
			f.busy[id] = true
		} else if !f.busy[id] || f.seen[id] || !strings.Contains(r.Header.Get("Authorization"), "user-session-id") {
			t.Error("inference session was not opened, uniquely owned, and signed")
		}
		f.seen[id] = true
		count, stall := len(f.busy), f.stall
		if count > f.maximum {
			f.maximum = count
		}
		f.mu.Unlock()
		if count > 3 {
			writeConcurrencyRefusal(w, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if stall {
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
			w.(http.Flusher).Flush()
			select {
			case f.started <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		time.Sleep(5 * time.Millisecond)
		_, _ = io.WriteString(w, lifecycleSuccess)
		if f.holdTransport {
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	}))
	t.Cleanup(b.Close)
	return f, b
}

func TestRemoteSessionLifecycleRepeatedAndEighteenWave(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(fmt.Sprintf("managed=%t", managed), func(t *testing.T) {
			f, backend := newRemoteSessionBackend(t)
			sink := newAuditSink(t)
			h, p := auditHandler(t, backend.URL, 1, sink, nil)
			h.cfg.QueueMaxAttempts = 1
			h.cfg.AccountWaitTimeout = 3 * time.Second
			for _, account := range p.Accounts() {
				if managed {
					account.Client = upstream.NewWithSessionEndpoint(time.Second, backend.URL)
				}
			}
			front := httptest.NewServer(h)
			defer front.Close()
			client := &http.Client{Timeout: 5 * time.Second}
			type result struct {
				status int
				raw    []byte
				id     string
				err    error
			}
			post := func(ctx context.Context, stream bool) result {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, front.URL+"/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":"glm-5.2","messages":[{"role":"user","content":"same conversation"}],"stream":%t}`, stream)))
				if err != nil {
					return result{err: err}
				}
				req.Header.Set("Content-Type", "application/json")
				resp, err := client.Do(req)
				if err != nil {
					return result{err: err}
				}
				defer resp.Body.Close()
				raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
				return result{resp.StatusCode, raw, resp.Header.Get("X-Request-Id"), err}
			}
			for i := 0; i < 6; i++ {
				got := post(context.Background(), i%2 == 0)
				want := http.StatusOK
				if !managed && i >= 3 {
					want = http.StatusTooManyRequests
				}
				if got.err != nil || got.status != want {
					t.Fatalf("request %d: status=%d want=%d err=%v body=%s", i, got.status, want, got.err, got.raw)
				}
				if want == http.StatusOK {
					assertLiveIdentityContent(t, got.raw, i%2 == 0)
				}
				if !managed && i == 3 {
					return
				}
			}
			results := make(chan result, 18)
			for i := 0; i < 18; i++ {
				go func() { results <- post(context.Background(), true) }()
			}
			health, err := client.Get(front.URL + "/healthz")
			if err != nil {
				t.Fatal(err)
			}
			_ = health.Body.Close()
			if health.StatusCode != 200 {
				t.Fatalf("health during wave=%d", health.StatusCode)
			}
			for i := 0; i < 18; i++ {
				got := <-results
				if got.err != nil || got.status != 200 {
					t.Fatalf("wave status=%d error=%v body=%s", got.status, got.err, got.raw)
				}
				assertLiveIdentityContent(t, got.raw, true)
				ev := waitForRecord(t, sink, got.id)
				if ev.Outcome != "ok" || ev.Attempts != 1 {
					t.Fatalf("incorrect lifecycle audit: %+v", ev)
				}
			}
			if len(sink.Connections().Active) != 0 {
				t.Fatal("completed wave retained activity")
			}
			assertLifecycleHealthy(t, p)
			assertRemoteSessionAdmissionReusable(t, p)
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.busy) != 0 || f.busyCalls != 24 || f.idleCalls != 24 || f.chatCalls != 24 || len(f.seen) != 24 || f.maximum != 1 {
				t.Fatalf("remote lifecycle leaked or crossed sessions: busy=%d open=%d close=%d chat=%d unique=%d max=%d", len(f.busy), f.busyCalls, f.idleCalls, f.chatCalls, len(f.seen), f.maximum)
			}
		})
	}
}

func TestRemoteSessionLifecycleCallerCancellationReleasesUpstream(t *testing.T) {
	f, backend := newRemoteSessionBackend(t)
	f.stall = true
	sink := newAuditSink(t)
	h, p := auditHandler(t, backend.URL, 1, sink, nil)
	p.Accounts()[0].Client = upstream.NewWithSessionEndpoint(time.Second, backend.URL)
	front := httptest.NewServer(h)
	defer front.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, front.URL+"/v1/chat/completions", strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"cancel me"}],"stream":true}`))
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-f.started:
	case <-time.After(2 * time.Second):
		t.Fatal("inference did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("caller cancellation did not return")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		released := len(f.busy) == 0 && f.idleCalls == 1
		f.mu.Unlock()
		if released && len(sink.Connections().Active) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled caller left remote session or local activity")
		}
		time.Sleep(time.Millisecond)
	}
	assertLifecycleHealthy(t, p)
	assertRemoteSessionAdmissionReusable(t, p)
	f.mu.Lock()
	f.stall = false
	f.mu.Unlock()
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Post(front.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"after cancellation"}],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("subsequent request failed: status=%d err=%v body=%s", resp.StatusCode, err, raw)
	}
	assertLiveIdentityContent(t, raw, true)
	if ev := waitForRecord(t, sink, resp.Header.Get("X-Request-Id")); ev.Outcome != "ok" || ev.Attempts != 1 {
		t.Fatalf("subsequent request audit=%+v", ev)
	}
	assertLifecycleHealthy(t, p)
	assertRemoteSessionAdmissionReusable(t, p)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.busy) != 0 || f.busyCalls != 2 || f.idleCalls != 2 || f.chatCalls != 2 {
		t.Fatalf("cancel/reuse leaked sessions: active=%d busy=%d idle=%d chat=%d", len(f.busy), f.busyCalls, f.idleCalls, f.chatCalls)
	}
}

func assertRemoteSessionAdmissionReusable(t *testing.T, p *pool.Pool) {
	t.Helper()
	capacity := p.Config().MaxPending + len(p.Accounts())
	for i := 0; i < capacity; i++ {
		release, err := p.AdmitRequest(context.Background())
		if err != nil {
			t.Fatalf("session cleanup retained admission: acquired %d/%d: %v", i, capacity, err)
		}
		defer release()
	}
}

func TestRemoteSessionLifecycleDisconnectAfterDoneRemainsSuccessful(t *testing.T) {
	f, backend := newRemoteSessionBackend(t)
	idleGate := make(chan struct{})
	f.idleGate, f.idleStarted = idleGate, make(chan struct{}, 1)
	f.holdTransport = true
	var releaseIdle sync.Once
	defer releaseIdle.Do(func() { close(idleGate) })
	sink := newAuditSink(t)
	h, p := auditHandler(t, backend.URL, 1, sink, nil)
	p.Accounts()[0].Client = upstream.NewWithSessionEndpoint(time.Second, backend.URL)
	callerClosed := make(chan struct{})
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stop := context.AfterFunc(r.Context(), func() { close(callerClosed) })
		defer stop()
		h.ServeHTTP(w, r)
	}))
	defer front.Close()
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	resp, err := client.Post(front.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"finish then close"}],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	gotDone := false
	for scanner.Scan() {
		if scanner.Text() == "data: [DONE]" {
			gotDone = true
			break
		}
	}
	if !gotDone || scanner.Err() != nil {
		t.Fatalf("client did not receive DONE: %v", scanner.Err())
	}
	select {
	case <-f.idleStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("remote cleanup did not start")
	}
	_ = resp.Body.Close()
	select {
	case <-callerClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("completed client's disconnect did not cancel server context")
	}
	releaseIdle.Do(func() { close(idleGate) })
	ev := waitForRecord(t, sink, resp.Header.Get("X-Request-Id"))
	if ev.Outcome != "ok" || ev.Canceled || ev.Attempts != 1 || ev.Status != http.StatusOK {
		t.Fatalf("delivered response was misclassified during cleanup: %+v", ev)
	}
	assertLifecycleHealthy(t, p)
	assertRemoteSessionAdmissionReusable(t, p)
	if len(sink.Connections().Active) != 0 {
		t.Fatal("completed disconnect retained tracking")
	}
}
