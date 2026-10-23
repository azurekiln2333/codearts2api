package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/audit"
	"codearts2api/internal/auth"
	"codearts2api/internal/upstream"
)

func TestChatRepeatedProviderBusyIsSharedAcrossHandlersAndRecovers(t *testing.T) {
	var calls atomic.Int32
	var refuse atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if refuse.Load() {
			writeConcurrencyRefusal(w, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	}))
	defer backend.Close()
	sink := newAuditSink(t)
	h, p := auditHandler(t, backend.URL, 1, sink, func(c *Config) {
		c.QueueMaxAttempts = 1
		c.QueueRetryDelay = 400 * time.Millisecond
		c.AccountWaitTimeout = 15 * time.Millisecond
		c.StartupTimeout = time.Second
	})
	front := httptest.NewServer(h)
	defer front.Close()
	other := httptest.NewServer(NewHandler(h.cfg))
	defer other.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	request := func(base string) (status int, id, text string, err error) {
		resp, err := client.Post(base+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}],"stream":true}`))
		if err != nil {
			return 0, "", "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("X-Request-Id"), string(body), err
	}
	status, _, text, err := request(front.URL)
	if err != nil || status != http.StatusOK || !strings.Contains(text, "data: [DONE]") {
		t.Fatalf("initial completion: status=%d error=%v text=%s", status, err, text)
	}
	refuse.Store(true)
	status, id, _, err := request(front.URL)
	if err != nil || status != http.StatusTooManyRequests {
		t.Fatalf("actual provider refusal: status=%d error=%v", status, err)
	}
	ev := waitForRecord(t, sink, id)
	if ev.Attempts != 1 || ev.Cause.Code != "TM.00001041" {
		t.Fatalf("actual refusal lost audit correlation: %+v", ev)
	}
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for index := 0; index < 12; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			base := front.URL
			if index%2 != 0 {
				base = other.URL
			}
			status, id, text, err := request(base)
			if err != nil || status != http.StatusTooManyRequests || !strings.Contains(text, `"code":"upstream_busy"`) {
				results <- fmt.Errorf("remembered busy status=%d error=%v body=%s", status, err, text)
				return
			}
			ev := waitForRecord(t, sink, id)
			if ev.Attempts != 0 || ev.Cause.Class != audit.CauseConcurrencyLimit || ev.Cause.Code != "" || ev.ErrorCode != "upstream_busy" || ev.RetryAfterS < 1 {
				results <- fmt.Errorf("remembered busy invented an attempt/provider code: %+v", ev)
			}
		}(index)
	}
	wg.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("independent requests hammered recently rejected account: calls=%d want=2", got)
	}
	resp, err := client.Get(front.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health unresponsive during busy wave: %d", resp.StatusCode)
	}
	assertLifecycleHealthy(t, p)
	refuse.Store(false)
	// A new admitted request must wake at expiry without any unrelated mutation.
	h.cfg.AccountWaitTimeout = time.Second
	status, id, text, err = request(front.URL)
	if err != nil || status != http.StatusOK || !strings.Contains(text, "data: [DONE]") || !strings.Contains(text, `"content":"ok"`) {
		t.Fatalf("recovery completion: status=%d error=%v text=%s", status, err, text)
	}
	if ev := waitForRecord(t, sink, id); ev.Attempts != 1 || ev.Outcome != "ok" {
		t.Fatalf("recovery audit: %+v", ev)
	}
	if calls.Load() != 3 || p.UpstreamBusy(p.Accounts()[0]) || len(sink.Connections().Active) != 0 {
		t.Fatal("recovery retained provider backoff, active tracking or extra calls")
	}
	assertLifecycleHealthy(t, p)
}

func TestChatBusyStickyAccountUsesHealthyBackup(t *testing.T) {
	f := newConcurrencyFixture(t, 2, func(_ int, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	})
	old := f.p.Accounts()[0]
	f.p.NoteUpstreamBusy(old, old.AuthSnapshot(), time.Second)
	chatID := strings.Repeat("a", 32)
	f.h.convMu.Lock()
	f.h.convAcct[chatID] = old.Name
	f.h.convMu.Unlock()
	req := lifecycleRequest(context.Background(), false)
	req.Header.Set("X-Codearts-Chat-Id", chatID)
	rec := httptest.NewRecorder()
	started := time.Now()
	f.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || time.Since(started) > 300*time.Millisecond {
		t.Fatalf("sticky busy blocked healthy backup: status=%d elapsed=%s", rec.Code, time.Since(started))
	}
	f.assertAttempts(t, rec.Header().Get("X-Request-Id"), []int{1})
	f.assertNeutral(t)
}

func TestChatRetryAbandonsRemovedAndReaddedAccount(t *testing.T) {
	var oldCalls, newCalls atomic.Int32
	oldBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oldCalls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"InferHub.ModelArts.81111.429 TPM limit reached"}`)
	}))
	defer oldBackend.Close()
	newBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	}))
	defer newBackend.Close()
	h, p := lifecycleHandler(t, 1, oldBackend.URL)
	h.cfg.QueueMaxAttempts = 2
	h.cfg.QueueRetryDelay = 150 * time.Millisecond
	old := p.Accounts()[0]
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec, _ := doChat(h, "glm-5.2")
		done <- rec
	}()
	waitLifecycle(t, func() bool { return oldCalls.Load() == 1 && !p.Busy(old.Name) }, "first retry did not release its old lease")
	p.SyncToDir(nil)
	replacement := p.AddAccount(auth.New(old.UID, "replacement", "domain", "new-token", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", ""))
	replacement.Client = upstream.NewWithChatEndpoint(time.Second, newBackend.URL)
	select {
	case rec := <-done:
		if rec.Code == http.StatusOK || oldCalls.Load() != 1 || newCalls.Load() != 0 {
			t.Fatalf("stale retry sent after replacement: status=%d old=%d new=%d", rec.Code, oldCalls.Load(), newCalls.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("replacement retry did not finish")
	}
	if p.UpstreamBusy(replacement) {
		t.Fatal("old retry affected new account")
	}
	rec, _ := doChat(h, "glm-5.2")
	if rec.Code != http.StatusOK || newCalls.Load() != 1 {
		t.Fatalf("replacement capacity was not reusable: status=%d calls=%d", rec.Code, newCalls.Load())
	}
	assertLifecycleHealthy(t, p)
}
