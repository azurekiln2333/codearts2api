package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

// Exercise the production-shaped partial-capacity wave through live HTTP, with
// scaled budgets. A successful health route alone cannot establish chat cleanup;
// verify every result, all leases and the entire admission capacity afterward.
func TestProductionWaveEighteenMixedRequestsWithPartialCooling(t *testing.T) {
	var active, maximum, calls atomic.Int32
	started := make(chan struct{}, 5)
	var retryCalls sync.Map
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls.Add(1)
		updateLifecycleMax(&maximum, active.Add(1))
		defer active.Add(-1)
		switch {
		case strings.Contains(body.Model, "-stall-"):
			started <- struct{}{}
			<-r.Context().Done()
			return
		case strings.Contains(body.Model, "-unavailable-"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"InferHub.4004.200 benefit not found"}`)
			return
		case strings.Contains(body.Model, "-retry-"):
			counter, _ := retryCalls.LoadOrStore(body.Model, &atomic.Int32{})
			if counter.(*atomic.Int32).Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"TM.00001041 concurrent session limit"}`)
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	}))
	defer backend.Close()
	h, p := lifecycleHandlerConfig(t, 4, backend.URL, pool.Config{MaxConcurrent: 5, MaxPending: 13})
	h.cfg.MaxRotate = 3
	h.cfg.AccountWaitTimeout = 350 * time.Millisecond
	h.cfg.StartupTimeout = 3 * time.Second
	h.cfg.QueueRetryDelay = 10 * time.Millisecond
	h.cfg.QueueMaxAttempts = 2
	accounts := p.Accounts()
	for i, account := range accounts {
		account.Client = upstream.NewWithChatEndpoint(1200*time.Millisecond, backend.URL)
		if i > 0 {
			p.Cooldown(account.Name, pool.CoolSoft, time.Minute, "simulated prior provider rate limit")
		}
	}
	front := httptest.NewServer(h)
	defer front.Close()
	client := &http.Client{Timeout: 4 * time.Second}
	type result struct {
		id      int
		status  int
		elapsed time.Duration
		err     error
	}
	post := func(ctx context.Context, model string) (int, error) {
		payload := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"synthetic probe"}]}`, model)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, front.URL+"/v1/chat/completions", strings.NewReader(payload))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		_, err = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, err
	}
	prefix := fmt.Sprintf("wave-%d", lifecycleSequence.Add(1))
	results := make(chan result, 18)
	var cancelAll []context.CancelFunc
	defer func() {
		for _, cancel := range cancelAll {
			cancel()
		}
	}()
	launch := func(id int, kind string) context.CancelFunc {
		ctx, cancel := context.WithCancel(context.Background())
		cancelAll = append(cancelAll, cancel)
		go func() {
			before := time.Now()
			status, err := post(ctx, fmt.Sprintf("%s-%s-%d", prefix, kind, id))
			results <- result{id: id, status: status, err: err, elapsed: time.Since(before)}
		}()
		return cancel
	}
	for id := 0; id < 5; id++ {
		launch(id, "stall")
	}
	for i := 0; i < 5; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("five requests did not occupy the one schedulable account")
		}
	}
	var canceled []context.CancelFunc
	for id := 5; id < 18; id++ {
		kind := []string{"ok", "unavailable", "retry"}[id%3]
		cancel := launch(id, kind)
		if id < 8 {
			canceled = append(canceled, cancel)
		}
	}
	for _, cancel := range canceled {
		cancel()
	}
	beforeHealth := time.Now()
	resp, err := client.Get(front.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || time.Since(beforeHealth) > 500*time.Millisecond {
		t.Fatalf("wave blocked health: status=%d elapsed=%s", resp.StatusCode, time.Since(beforeHealth))
	}
	counts := map[int]int{}
	for i := 0; i < 18; i++ {
		select {
		case got := <-results:
			if got.id >= 5 && got.id < 8 {
				if !errors.Is(got.err, context.Canceled) {
					t.Errorf("request %d cancellation: status=%d err=%v", got.id, got.status, got.err)
				}
				continue
			}
			want := http.StatusTooManyRequests
			if got.id < 5 {
				want = http.StatusGatewayTimeout
			}
			if got.err != nil || got.status != want || got.elapsed > 3*time.Second {
				t.Errorf("request %d escaped its budget: status=%d want=%d elapsed=%s err=%v", got.id, got.status, want, got.elapsed, got.err)
			}
			counts[got.status]++
		case <-time.After(4 * time.Second):
			t.Fatal("an 18-request wave member remained stuck")
		}
	}
	if counts[http.StatusGatewayTimeout] != 5 || counts[http.StatusTooManyRequests] != 10 || calls.Load() != 5 || maximum.Load() != 5 {
		t.Fatalf("unexpected wave shape: statuses=%v calls=%d peak=%d", counts, calls.Load(), maximum.Load())
	}
	waitLifecycle(t, func() bool { return active.Load() == 0 && !p.Busy(accounts[0].Name) }, "stalled requests retained upstream or account resources")
	for _, state := range p.List() {
		if state["active_concurrent"] != 0 || state["err_count"] != 0 || state["disabled"] != false {
			t.Fatalf("timeouts/cancellations leaked slots or added errors: %#v", state)
		}
		if got, want := state["cooling"].(bool), state["name"] != accounts[0].Name; got != want {
			t.Fatalf("wave changed existing cooldown: %#v", state)
		}
	}
	// Cooling capacity remains part of the existing lifecycle-ticket contract.
	// Fill all 4*5+13 permits, then prove the next request is rejected.
	var releases []func()
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for i := 0; i < 33; i++ {
		release, err := p.AdmitRequest(context.Background())
		if err != nil {
			t.Fatalf("admission ticket %d leaked after wave: %v", i, err)
		}
		releases = append(releases, release)
	}
	if release, err := p.AdmitRequest(context.Background()); !errors.Is(err, pool.ErrQueueFull) {
		if release != nil {
			release()
		}
		t.Fatalf("shared lifecycle cap exceeded: %v", err)
	}
	for _, release := range releases {
		release()
	}
	// Exercise each queued model category after the wave, including a retry that
	// releases and reacquires the same account without penalizing its health.
	for _, check := range []struct {
		kind string
		want int
	}{{"ok", http.StatusOK}, {"unavailable", http.StatusNotFound}, {"retry", http.StatusOK}} {
		model := prefix + "-" + check.kind + "-reuse"
		if status, err := post(context.Background(), model); err != nil || status != check.want {
			t.Fatalf("capacity was not reusable for %s: status=%d err=%v", check.kind, status, err)
		}
	}
	if !p.Healthy(accounts[0].Name) || p.Busy(accounts[0].Name) || calls.Load() != 9 {
		t.Fatalf("recovery requests damaged live capacity: healthy=%t busy=%t calls=%d", p.Healthy(accounts[0].Name), p.Busy(accounts[0].Name), calls.Load())
	}
}
