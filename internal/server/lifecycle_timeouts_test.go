package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/upstream"
)

func waitLifecycle(t *testing.T, predicate func() bool, message string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !predicate() {
		select {
		case <-deadline.C:
			t.Fatal(message)
		case <-ticker.C:
		}
	}
}

func TestChatStartupTimeoutBeforeHeadersAndCapacityReuse(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var silent atomic.Bool
			silent.Store(true)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if silent.Load() {
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				_, _ = io.WriteString(w, lifecycleSuccess)
			}))
			defer backend.Close()
			h, p := lifecycleHandler(t, 2, backend.URL)
			h.cfg.StartupTimeout = 50 * time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			rec := httptest.NewRecorder()
			started := time.Now()
			h.ServeHTTP(rec, lifecycleRequest(ctx, stream))
			if rec.Code != http.StatusGatewayTimeout || time.Since(started) > 300*time.Millisecond {
				t.Fatalf("silent preflight escaped startup budget: status=%d elapsed=%s body=%s", rec.Code, time.Since(started), rec.Body.String())
			}
			assertLifecycleHealthy(t, p)
			silent.Store(false)
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, lifecycleRequest(ctx, stream))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
				t.Fatalf("capacity was not reused after startup timeout: %d %s", rec.Code, rec.Body.String())
			}
			assertLifecycleHealthy(t, p)
		})
	}
}

func TestChatSlowRequestBodyUsesStartupBudget(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("partial request body reached upstream")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()
	h, p := lifecycleHandler(t, 1, backend.URL)
	h.cfg.StartupTimeout = 40 * time.Millisecond
	front := httptest.NewServer(h)
	defer front.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, err = io.WriteString(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: test\r\nContent-Length: 1000\r\nContent-Type: application/json\r\n\r\n{")
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("startup timeout did not respond to a slow upload: %v", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusGatewayTimeout || !response.Close {
		t.Fatalf("slow upload escaped startup/connection cleanup: status=%d close=%t", response.StatusCode, response.Close)
	}
	assertLifecycleHealthy(t, p)
	release, err := p.AdmitRequest(context.Background())
	if err != nil {
		t.Fatalf("slow request leaked admission: %v", err)
	}
	release()
}

func TestChatLaterIdleTimeoutTerminatesBothResponseModes(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer backend.Close()
			h, p := lifecycleHandler(t, 1, backend.URL)
			p.Accounts()[0].Client = upstream.NewWithChatEndpoint(40*time.Millisecond, backend.URL)
			h.cfg.StartupTimeout = 100 * time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, lifecycleRequest(ctx, stream))
			if stream {
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "event: error") || !strings.Contains(rec.Body.String(), "data: [DONE]") {
					t.Fatalf("late idle timeout did not terminate SSE: %d %s", rec.Code, rec.Body.String())
				}
			} else if rec.Code != http.StatusGatewayTimeout {
				t.Fatalf("late idle timeout must be 504: %d %s", rec.Code, rec.Body.String())
			}
			assertLifecycleHealthy(t, p)
		})
	}
}

func TestChatActiveGenerationOutlastsStartupAndIdleBudgets(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for i := 0; i < 8; i++ {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
					w.(http.Flusher).Flush()
					select {
					case <-time.After(20 * time.Millisecond):
					case <-r.Context().Done():
						return
					}
				}
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer backend.Close()
			h, p := lifecycleHandler(t, 1, backend.URL)
			h.cfg.StartupTimeout = 50 * time.Millisecond
			p.Accounts()[0].Client = upstream.NewWithChatEndpoint(75*time.Millisecond, backend.URL)
			rec := httptest.NewRecorder()
			started := time.Now()
			h.ServeHTTP(rec, lifecycleRequest(context.Background(), stream))
			if rec.Code != http.StatusOK || time.Since(started) < 120*time.Millisecond || strings.Contains(rec.Body.String(), "event: error") {
				t.Fatalf("healthy long generation was cut off: status=%d elapsed=%s body=%s", rec.Code, time.Since(started), rec.Body.String())
			}
			if !stream && !strings.Contains(rec.Body.String(), `"content":"xxxxxxxx"`) {
				t.Fatalf("long generation lost content: %s", rec.Body.String())
			}
			assertLifecycleHealthy(t, p)
		})
	}
}

func TestChatCancellationDuringQueueRetryDoesNotRotate(t *testing.T) {
	var attempts atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"TPM limit"}`)
	}))
	defer backend.Close()
	h, p := lifecycleHandler(t, 2, backend.URL)
	h.cfg.QueueMaxAttempts = 3
	h.cfg.QueueRetryDelay = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), lifecycleRequest(ctx, false))
		close(done)
	}()
	waitLifecycle(t, func() bool { return attempts.Load() == 1 && !p.Busy(p.Accounts()[0].Name) }, "request did not release its slot for retry")
	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("canceled retry sleep did not finish promptly")
	}
	if attempts.Load() != 1 {
		t.Fatalf("canceled retry started another attempt: %d", attempts.Load())
	}
	assertLifecycleHealthy(t, p)
}

func TestChatFailedReacquisitionCannotReleaseAnotherRequestSlot(t *testing.T) {
	var attempts atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"TPM limit"}`)
	}))
	defer backend.Close()
	h, p := lifecycleHandler(t, 1, backend.URL)
	h.cfg.QueueMaxAttempts = 3
	h.cfg.QueueRetryDelay = 30 * time.Millisecond
	h.cfg.AccountWaitTimeout = 40 * time.Millisecond
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, lifecycleRequest(context.Background(), false))
		done <- rec
	}()
	name := p.Accounts()[0].Name
	waitLifecycle(t, func() bool { return attempts.Load() == 1 && !p.Busy(name) }, "retry did not release its first lease")
	if !p.AcquireLock(name) {
		t.Fatal("could not reserve released slot for another request")
	}
	owned := true
	defer func() {
		if owned {
			p.ReleaseLock(name)
		}
	}()
	select {
	case rec := <-done:
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
			t.Fatalf("failed reacquisition did not return retryable saturation: %d %s", rec.Code, rec.Body.String())
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("reacquisition was not bounded")
	}
	if !p.Busy(name) || attempts.Load() != 1 || !p.Healthy(name) {
		t.Fatalf("failed reacquisition released or punished another owner: attempts=%d state=%#v", attempts.Load(), p.List())
	}
	p.ReleaseLock(name)
	owned = false
	assertLifecycleHealthy(t, p)
}

type deadlineLifecycleWriter struct {
	header   http.Header
	deadline time.Time
}

func (w *deadlineLifecycleWriter) Header() http.Header { return w.header }
func (w *deadlineLifecycleWriter) WriteHeader(int)     {}
func (w *deadlineLifecycleWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func (w *deadlineLifecycleWriter) Write([]byte) (int, error) {
	if w.deadline.IsZero() {
		return 0, fmt.Errorf("write deadline was not set")
	}
	<-time.After(time.Until(w.deadline))
	return 0, os.ErrDeadlineExceeded
}

func TestChatDownstreamWriteDeadlineReleasesWithoutHealthPenalty(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	}))
	defer backend.Close()
	h, p := lifecycleHandler(t, 1, backend.URL)
	h.cfg.StreamWriteTimeout = 30 * time.Millisecond
	started := time.Now()
	h.ServeHTTP(&deadlineLifecycleWriter{header: make(http.Header)}, lifecycleRequest(context.Background(), true))
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("blocked downstream retained upstream capacity: %s", elapsed)
	}
	assertLifecycleHealthy(t, p)
}
