package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/telemetry"
)

func TestChatLateBusyRecordedBeforeLeaseRelease(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			testChatLateBusyRecordedBeforeLeaseRelease(t, stream)
		})
	}
}

func testChatLateBusyRecordedBeforeLeaseRelease(t *testing.T, stream bool) {
	var calls atomic.Int32
	finishFirst := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) != 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, lifecycleSuccess)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-finishFirst:
			_, _ = io.WriteString(w, concurrencyErrorSSE)
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	sink := newAuditSink(t)
	h, p := auditHandler(t, backend.URL, 1, sink, func(c *Config) {
		c.QueueMaxAttempts = 1
		c.QueueRetryDelay = time.Second
		c.AccountWaitTimeout = 150 * time.Millisecond
		c.StartupTimeout = time.Second
	})
	start := func() <-chan *httptest.ResponseRecorder {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, lifecycleRequest(context.Background(), stream))
			done <- rec
		}()
		return done
	}
	first := start()
	waitLifecycle(t, func() bool { return calls.Load() == 1 && p.Busy(p.Accounts()[0].Name) }, "first stream did not own slot")
	second := start()
	waitLifecycle(t, func() bool {
		for _, active := range sink.Connections().Active {
			if active.Phase == telemetry.WaitAccount {
				return true
			}
		}
		return false
	}, "second request did not enter account wait")
	close(finishFirst)
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("first late failure did not release")
	}
	select {
	case rec := <-second:
		if rec.Code != http.StatusTooManyRequests || calls.Load() != 1 {
			t.Fatalf("waiter sent before late provider busy was recorded: status=%d calls=%d body=%s", rec.Code, calls.Load(), rec.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("second request retained past bounded wait")
	}
	assertLifecycleHealthy(t, p)
	if active := sink.Connections().Active; len(active) != 0 {
		t.Fatalf("late refusal retained active requests: %+v", active)
	}
}
