package upstream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

var lifecycleTestCredential = SignCredential{AccessKeyID: "session-test-ak", SecretAccessKey: "session-test-sk", SecurityToken: "session-test-token"}

type sessionFixture struct {
	t          *testing.T
	mu         sync.Mutex
	active     map[string]bool
	chatIDs    []string
	busy       map[string]int
	idle       map[string]int
	traceIDs   map[string]bool
	chatClosed map[string]chan struct{}
	chat       func(http.ResponseWriter, *http.Request)
	heartbeat  func(http.ResponseWriter, *http.Request) bool
}

func newSessionFixture(t *testing.T) (*sessionFixture, *httptest.Server) {
	f := &sessionFixture{t: t, active: map[string]bool{}, busy: map[string]int{}, idle: map[string]int{}, traceIDs: map[string]bool{}, chatClosed: map[string]chan struct{}{}}
	return f, httptest.NewServer(http.HandlerFunc(f.serveHTTP))
}

func (f *sessionFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Error(err)
	}
	id := r.Header.Get("user-session-id")
	if len(id) != 32 {
		f.t.Errorf("missing independent root session identity: %q", id)
	}
	verifySessionSignature(f.t, r, raw)
	if r.URL.Path == EpSessionHeartbeat {
		if r.Method != http.MethodPut || string(raw) != "{}" || r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("Accept") != "application/json" || r.Header.Get("X-Client-Type") != "kernel" || r.Header.Get("X-Language") != "zh-cn" {
			f.t.Errorf("incorrect official heartbeat envelope: method=%s body=%q", r.Method, raw)
		}
		if r.Header.Get(HeaderMaasType) != "" {
			f.t.Error("inference benefit header leaked onto heartbeat")
		}
		trace := r.Header.Get("X-Snap-Traceid")
		f.mu.Lock()
		if trace == "" || f.traceIDs[trace] {
			f.t.Error("heartbeat trace identity was missing or reused")
		}
		f.traceIDs[trace] = true
		switch r.URL.Query().Get("status") {
		case "busy":
			f.active[id] = true
			f.busy[id]++
		case "idle":
			delete(f.active, id)
			f.idle[id]++
		default:
			f.t.Errorf("invalid heartbeat status: %q", r.URL.RawQuery)
		}
		f.mu.Unlock()
		if r.URL.Query().Get("status") == "idle" {
			f.mu.Lock()
			closed := f.chatClosed[id]
			f.mu.Unlock()
			if closed != nil {
				select {
				case <-closed:
				case <-time.After(300 * time.Millisecond):
					f.t.Error("idle cleanup arrived while inference transport remained open")
				}
			}
		}
		if f.heartbeat != nil && f.heartbeat(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
		return
	}
	if r.URL.Path != EpChatV2 || r.Method != http.MethodPost {
		f.t.Errorf("unexpected inference route: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	f.mu.Lock()
	active := f.active[id]
	f.chatIDs = append(f.chatIDs, id)
	closed := make(chan struct{})
	f.chatClosed[id] = closed
	f.mu.Unlock()
	defer close(closed)
	if !active {
		f.t.Error("inference started before its matching busy notification")
	}
	if f.chat != nil {
		f.chat(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, "data: {\"delta\":{\"content\":\"ok\"}}\n\ndata: [DONE]\n\n")
	w.(http.Flusher).Flush()
	// Protocol completion is deliberately not HTTP EOF.
	<-r.Context().Done()
}

func verifySessionSignature(t *testing.T, r *http.Request, raw []byte) {
	t.Helper()
	authorization := r.Header.Get("Authorization")
	prefix := signingAlgorithm + " Access=" + lifecycleTestCredential.AccessKeyID + ", SignedHeaders="
	if !strings.HasPrefix(authorization, prefix) {
		t.Errorf("inference/heartbeat did not use this attempt's selected credential")
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(authorization, prefix), ", Signature=", 2)
	if len(parts) != 2 || !strings.Contains(";"+parts[0]+";", ";user-session-id;") || r.Header.Get("X-Security-Token") != lifecycleTestCredential.SecurityToken {
		t.Error("root session header and STS token are not signed")
		return
	}
	keys := strings.Split(parts[0], ";")
	if !sort.StringsAreSorted(keys) {
		t.Error("signed headers not canonical")
	}
	var headers strings.Builder
	for _, key := range keys {
		value := r.Header.Get(key)
		if key == "host" {
			value = r.Host
		}
		headers.WriteString(key + ":" + strings.TrimSpace(value) + "\n")
	}
	payloadHash := sha256Hex(raw)
	canonical := strings.Join([]string{r.Method, canonicalURI(r.URL.EscapedPath()), canonicalQuery(r.URL.RawQuery), headers.String(), parts[0], payloadHash}, "\n")
	toSign := strings.Join([]string{signingAlgorithm, r.Header.Get("X-Sdk-Date"), sha256Hex([]byte(canonical))}, "\n")
	if r.Header.Get("X-Sdk-Content-Sha256") != payloadHash || parts[1] != hmacHex(lifecycleTestCredential.SecretAccessKey, toSign) {
		t.Error("method, query, root session or body changed after signing")
	}
}

func (f *sessionFixture) assertReleased() {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.active) != 0 || len(f.chatIDs) == 0 {
		f.t.Errorf("remote occupancy retained after cleanup: active=%d chats=%d", len(f.active), len(f.chatIDs))
	}
	seen := map[string]bool{}
	for _, id := range f.chatIDs {
		if seen[id] || f.busy[id] < 1 || f.idle[id] != 1 {
			f.t.Errorf("unbalanced or reused attempt: busy=%d idle=%d reused=%v", f.busy[id], f.idle[id], seen[id])
		}
		seen[id] = true
	}
}

func managedTestChat(ctx context.Context, c *Client, conversation string, benefit bool) (io.ReadCloser, error) {
	return c.ChatStreamWithExactModel(ctx, conversation, []ChatMessage{{Role: "user", Content: "fixture"}}, "fixture-inference", lifecycleTestCredential, "", "deepseek-v4.1-flash", ChatOptions{}, benefit)
}

func assertSessionWorkerStopped(t *testing.T, body io.ReadCloser) {
	t.Helper()
	replay, ok := body.(*replayReadCloser)
	if !ok {
		t.Fatalf("unexpected preflight wrapper: %T", body)
	}
	managed, ok := replay.Closer.(*sessionReadCloser)
	if !ok {
		t.Fatalf("missing managed response body: %T", replay.Closer)
	}
	select {
	case <-managed.session.done:
	default:
		t.Fatal("remote release returned with an unjoined heartbeat worker")
	}
}

func TestManagedChatSessionRepeatedSignedCallsReleaseWithoutEOF(t *testing.T) {
	f, backend := newSessionFixture(t)
	defer backend.Close()
	c := NewWithSessionEndpoint(time.Second, backend.URL+EpChatV2)
	for i := 0; i < 7; i++ {
		body, err := managedTestChat(context.Background(), c, "same-conversation", i%2 == 0)
		if err != nil {
			t.Fatal(err)
		}
		result, err := AggregateRaw(body)
		if err != nil || result.Content != "ok" {
			t.Fatalf("repeat %d: result=%+v err=%v", i, result, err)
		}
		if err := body.Close(); err != nil {
			t.Fatal(err)
		}
		if err := body.Close(); err != nil {
			t.Fatalf("repeated close: %v", err)
		}
		assertSessionWorkerStopped(t, body)
	}
	f.assertReleased()
}

func TestManagedParallelSameConversationOwnsDistinctRemoteSessions(t *testing.T) {
	f, backend := newSessionFixture(t)
	defer backend.Close()
	c := NewWithSessionEndpoint(time.Second, backend.URL+EpChatV2)
	f.chat = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Chat-Id") != "shared-chat" || r.Header.Get("Session-Id") != sessionIDFor("shared-chat") {
			t.Error("attempt ownership changed prompt-cache/conversation affinity")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"delta\":{\"content\":\"ok\"}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
	first, err := managedTestChat(context.Background(), c, "shared-chat", false)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := managedTestChat(context.Background(), c, "shared-chat", false)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = first.Close()
	f.mu.Lock()
	remaining := len(f.active)
	f.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("closing one attempt freed another: active=%d", remaining)
	}
	_ = second.Close()
	assertSessionWorkerStopped(t, first)
	assertSessionWorkerStopped(t, second)
	f.assertReleased()
}

func TestManagedSessionRenewsLongStreamAndStopsWorker(t *testing.T) {
	f, backend := newSessionFixture(t)
	defer backend.Close()
	f.chat = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 10; i++ {
			_, _ = io.WriteString(w, "data: {\"delta\":{\"content\":\"x\"}}\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-time.After(15 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
	c := NewWithSessionEndpoint(100*time.Millisecond, backend.URL+EpChatV2)
	c.sessionInterval = 20 * time.Millisecond
	body, err := managedTestChat(context.Background(), c, "long", false)
	if err != nil {
		t.Fatal(err)
	}
	result, err := AggregateRaw(body)
	_ = body.Close()
	if err != nil || result.Content != strings.Repeat("x", 10) {
		t.Fatalf("heartbeat truncated active stream: result=%+v err=%v", result, err)
	}
	assertSessionWorkerStopped(t, body)
	f.assertReleased()
	f.mu.Lock()
	count := f.busy[f.chatIDs[0]]
	f.mu.Unlock()
	if count < 3 {
		t.Fatalf("no periodic busy renewal during long generation: %d", count)
	}
	time.Sleep(60 * time.Millisecond)
	f.mu.Lock()
	after := f.busy[f.chatIDs[0]]
	f.mu.Unlock()
	if after != count {
		t.Error("heartbeat continued after terminal cleanup")
	}
}

func TestManagedSessionReleasesEveryFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"http_error", "early_sse_error", "late_sse_error", "transport_error", "headers_timeout", "first_data_timeout", "later_idle_timeout", "headers_cancel", "first_data_cancel", "later_cancel", "explicit_close"} {
		t.Run(mode, func(t *testing.T) {
			f, backend := newSessionFixture(t)
			defer backend.Close()
			ready := make(chan struct{})
			f.chat = func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "http_error":
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, "model not registered")
					return
				case "early_sse_error":
					_, _ = io.WriteString(w, "data: {\"error_code\":\"TM.00001041\",\"error_msg\":\"upstream concurrent session limit\"}\n\n")
					return
				case "transport_error":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = conn.Close()
					}
					return
				}
				if !strings.HasPrefix(mode, "headers_") {
					w.Header().Set("Content-Type", "text/event-stream")
					if !strings.HasPrefix(mode, "first_data_") {
						_, _ = io.WriteString(w, "data: {\"delta\":{\"content\":\"partial\"}}\n\n")
					}
					if mode == "late_sse_error" {
						_, _ = io.WriteString(w, "data: {\"error_code\":\"TM.00001041\",\"error_msg\":\"upstream concurrent session limit\"}\n\n")
					}
					w.(http.Flusher).Flush()
				}
				close(ready)
				<-r.Context().Done()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if strings.HasSuffix(mode, "_cancel") {
				go func() { <-ready; cancel() }()
			}
			c := NewWithSessionEndpoint(70*time.Millisecond, backend.URL+EpChatV2)
			start := time.Now()
			body, err := managedTestChat(ctx, c, "failure", false)
			if body != nil {
				if mode != "explicit_close" {
					_, err = AggregateRaw(body)
				}
				_ = body.Close()
				assertSessionWorkerStopped(t, body)
			}
			if mode != "explicit_close" && err == nil {
				t.Fatal("failure fixture unexpectedly succeeded")
			}
			if time.Since(start) > time.Second {
				t.Fatalf("cleanup exceeded bounded request duration: %s", time.Since(start))
			}
			f.assertReleased()
		})
	}
}

func TestManagedSessionHeartbeatFailuresPreserveActualCompletion(t *testing.T) {
	for _, mode := range []string{"busy_http", "idle_http", "busy_timeout", "idle_timeout"} {
		t.Run(mode, func(t *testing.T) {
			f, backend := newSessionFixture(t)
			defer backend.Close()
			f.heartbeat = func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.HasPrefix(mode, r.URL.Query().Get("status")) {
					return false
				}
				if strings.HasSuffix(mode, "_http") {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, "private provider details must not become inference result")
				} else {
					<-r.Context().Done()
				}
				return true
			}
			c := NewWithSessionEndpoint(time.Second, backend.URL+EpChatV2)
			c.sessionTimeout = 40 * time.Millisecond
			start := time.Now()
			body, err := managedTestChat(context.Background(), c, "notification-failure", false)
			if err != nil {
				t.Fatal(err)
			}
			result, err := AggregateRaw(body)
			_ = body.Close()
			assertSessionWorkerStopped(t, body)
			if err != nil || result.Content != "ok" || time.Since(start) > 500*time.Millisecond {
				t.Fatalf("notification failure replaced actual completion or hung: result=%+v err=%v", result, err)
			}
			f.assertReleased()
		})
	}
}

func TestManagedSessionCloseCancelsAndJoinsInFlightRenewal(t *testing.T) {
	f, backend := newSessionFixture(t)
	defer backend.Close()
	renewalStarted := make(chan struct{})
	var renewOnce sync.Once
	f.heartbeat = func(w http.ResponseWriter, r *http.Request) bool {
		f.mu.Lock()
		count := f.busy[r.Header.Get("user-session-id")]
		f.mu.Unlock()
		if r.URL.Query().Get("status") == "busy" && count > 1 {
			renewOnce.Do(func() { close(renewalStarted) })
			<-r.Context().Done()
			return true
		}
		return false
	}
	c := NewWithSessionEndpoint(time.Second, backend.URL+EpChatV2)
	c.sessionInterval = 15 * time.Millisecond
	body, err := managedTestChat(context.Background(), c, "join-worker", false)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-renewalStarted:
	case <-time.After(time.Second):
		t.Fatal("renewal never started")
	}
	start := time.Now()
	_ = body.Close()
	if time.Since(start) > 300*time.Millisecond {
		t.Fatal("close waited for full heartbeat timeout instead of canceling its worker")
	}
	assertSessionWorkerStopped(t, body)
	f.assertReleased()
}

func TestManagedSessionCallerCancellationClosesUnreadResponse(t *testing.T) {
	f, backend := newSessionFixture(t)
	defer backend.Close()
	c := NewWithSessionEndpoint(time.Second, backend.URL+EpChatV2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body, err := managedTestChat(ctx, c, "abandoned-body", false)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	// The caller neither reads nor closes the handed-off response. Its canceled
	// context must still stop the worker, close HTTP and send detached idle.
	cancel()
	managed := body.(*replayReadCloser).Closer.(*sessionReadCloser)
	select {
	case <-managed.session.done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("caller cancellation left an unread response/session alive")
	}
	f.assertReleased()
}

func TestLegacyChatEndpointDoesNotSendSessionHeartbeat(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = fmt.Fprint(w, "data: {}\n\ndata: [DONE]\n\n")
	}))
	defer backend.Close()
	body, err := NewWithChatEndpoint(time.Second, backend.URL+EpChatV2).SendChatV2(context.Background(), map[string]any{}, "", SignCredential{}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != EpChatV2 {
		t.Fatalf("legacy chat-only fixture unexpectedly invoked another endpoint: %v", paths)
	}
}
