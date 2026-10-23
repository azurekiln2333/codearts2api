package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codearts2api/internal/pool"
	"codearts2api/internal/telemetry"
)

func TestChatStickyWaitReevaluatesNewUpstreamBusy(t *testing.T) {
	f := newConcurrencyFixture(t, 2, func(_ int, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	})
	f.h.cfg.AccountWaitTimeout = 150 * time.Millisecond
	a := f.p.Accounts()[0]
	holder, err := f.p.AcquireAccount(context.Background(), []pool.AccountCandidate{{Name: a.Name}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	chatID := strings.Repeat("c", 32)
	f.h.convMu.Lock()
	f.h.convAcct[chatID] = a.Name
	f.h.convMu.Unlock()
	req := lifecycleRequest(context.Background(), false)
	req.Header.Set("X-Codearts-Chat-Id", chatID)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, req)
		done <- rec
	}()
	waitLifecycle(t, func() bool {
		active := f.sink.Connections().Active
		return len(active) == 1 && active[0].Phase == telemetry.WaitAccount
	}, "sticky request did not enter account wait")
	// Candidate selection has already pinned A. A separate request observes the
	// provider's refusal while B is still idle and compatible.
	f.p.NoteUpstreamBusy(a, a.AuthSnapshot(), time.Second)
	holder.Release()
	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("healthy B was ignored after A became provider-busy: status=%d body=%s", rec.Code, rec.Body.String())
		}
		f.assertAttempts(t, rec.Header().Get("X-Request-Id"), []int{1})
		f.assertNeutral(t)
	case <-time.After(time.Second):
		t.Fatal("sticky request retained past bounded wait")
	}
}
