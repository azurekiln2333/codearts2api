package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

func updateLifecycleMax(maximum *atomic.Int32, value int32) {
	for previous := maximum.Load(); value > previous; previous = maximum.Load() {
		if maximum.CompareAndSwap(previous, value) {
			return
		}
	}
}

func TestChatConcurrentSaturationCancellationFairnessAndHealth(t *testing.T) {
	var hold atomic.Bool
	hold.Store(true)
	var active, maximum, calls [2]atomic.Int32
	started := make(chan int, 32)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil || index < 0 || index > 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		current := active[index].Add(1)
		updateLifecycleMax(&maximum[index], current)
		defer active[index].Add(-1)
		calls[index].Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		w.(http.Flusher).Flush()
		started <- index
		if hold.Load() {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer backend.Close()
	h, p := lifecycleHandlerConfig(t, 2, backend.URL, pool.Config{MaxConcurrent: 1, MaxPending: 2, ErrThreshold: 1})
	for index, a := range p.Accounts() {
		a.Client = upstream.NewWithChatEndpoint(time.Second, fmt.Sprintf("%s/%d", backend.URL, index))
	}
	h.cfg.AccountWaitTimeout = time.Second
	h.cfg.StartupTimeout = 2 * time.Second
	other := NewHandler(h.cfg)
	front := httptest.NewServer(h)
	defer front.Close()
	secondFront := httptest.NewServer(other)
	defer secondFront.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	request := func(ctx context.Context, base string, id int) (*http.Response, error) {
		payload := fmt.Sprintf(`{"messages":[{"role":"user","content":"concurrent %d"}],"stream":true}`, id)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", strings.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return client.Do(req)
	}
	type pending struct {
		cancel context.CancelFunc
		done   chan struct{}
	}
	var live []pending
	defer func() {
		for _, item := range live {
			item.cancel()
		}
	}()
	launch := func(base string, id int) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		live = append(live, pending{cancel: cancel, done: done})
		go func() {
			defer close(done)
			resp, err := request(ctx, base, id)
			if err == nil {
				defer resp.Body.Close()
				_, _ = io.Copy(io.Discard, resp.Body)
			}
		}()
	}
	launch(front.URL, 0)
	launch(secondFront.URL, 1)
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("two accounts did not run concurrently")
		}
	}
	launch(front.URL, 2)
	launch(secondFront.URL, 3)
	// A temporary admission probe releases immediately. QueueFull proves all
	// four lifecycle permits are retained by the two active and two queued calls.
	waitLifecycle(t, func() bool {
		release, err := p.AdmitRequest(context.Background())
		if err == nil {
			release()
		}
		return errors.Is(err, pool.ErrQueueFull)
	}, "two waiting requests did not fill the shared admission queue")

	rejections := make(chan error, 24)
	for id := 4; id < 28; id++ {
		go func(id int) {
			before := time.Now()
			resp, err := request(context.Background(), front.URL, id)
			if err != nil {
				rejections <- err
				return
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" || time.Since(before) > 500*time.Millisecond {
				rejections <- fmt.Errorf("excess request retained: status=%d retry=%q elapsed=%s", resp.StatusCode, resp.Header.Get("Retry-After"), time.Since(before))
				return
			}
			rejections <- nil
		}(id)
	}
	for i := 0; i < 24; i++ {
		if err := <-rejections; err != nil {
			t.Error(err)
		}
	}
	beforeHealth := time.Now()
	resp, err := client.Get(secondFront.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || time.Since(beforeHealth) > 300*time.Millisecond {
		t.Fatalf("saturation blocked health: status=%d elapsed=%s", resp.StatusCode, time.Since(beforeHealth))
	}
	for _, item := range live[2:] {
		item.cancel()
		select {
		case <-item.done:
		case <-time.After(300 * time.Millisecond):
			t.Fatal("queued caller did not cancel promptly")
		}
	}
	for _, item := range live[:2] {
		item.cancel()
		select {
		case <-item.done:
		case <-time.After(300 * time.Millisecond):
			t.Fatal("active caller did not cancel promptly")
		}
	}
	waitLifecycle(t, func() bool {
		return !p.Busy(p.Accounts()[0].Name) && !p.Busy(p.Accounts()[1].Name) && active[0].Load() == 0 && active[1].Load() == 0
	}, "canceled streams did not release account capacity")
	assertLifecycleHealthy(t, p)
	if maximum[0].Load() != 1 || maximum[1].Load() != 1 || calls[0].Load() != 1 || calls[1].Load() != 1 {
		t.Fatalf("per-account limit or queue bound violated: maximum=[%d %d] calls=[%d %d]", maximum[0].Load(), maximum[1].Load(), calls[0].Load(), calls[1].Load())
	}

	hold.Store(false)
	for i := 0; i < 12; i++ {
		resp, err := request(context.Background(), front.URL, i+100)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || !strings.Contains(string(data), "[DONE]") {
			t.Fatalf("freed slot was not reused: status=%d err=%v body=%s", resp.StatusCode, err, data)
		}
	}
	if calls[0].Load() != 7 || calls[1].Load() != 7 {
		t.Fatalf("unaffined traffic starved an eligible account: calls=[%d %d]", calls[0].Load(), calls[1].Load())
	}
	assertLifecycleHealthy(t, p)
}

func TestChatRetrySleeperWavesRespectSharedLifecycleBound(t *testing.T) {
	var attempts atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"TPM limit"}`)
	}))
	defer backend.Close()
	h, p := lifecycleHandlerConfig(t, 1, backend.URL, pool.Config{MaxConcurrent: 1, MaxPending: 2})
	h.cfg.QueueRetryDelay = time.Second
	h.cfg.QueueMaxAttempts = 3
	other := NewHandler(h.cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{}, 3)
	for i := 0; i < 3; i++ {
		go func(i int) {
			target := h
			if i%2 == 1 {
				target = other
			}
			target.ServeHTTP(httptest.NewRecorder(), lifecycleRequest(ctx, false))
			done <- struct{}{}
		}(i)
	}
	waitLifecycle(t, func() bool { return attempts.Load() == 3 && !p.Busy(p.Accounts()[0].Name) }, "first wave did not enter retry sleeps")
	for i := 0; i < 20; i++ {
		rec := httptest.NewRecorder()
		other.ServeHTTP(rec, lifecycleRequest(context.Background(), false))
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
			t.Fatalf("retry sleepers escaped shared limit: %d %s", rec.Code, rec.Body.String())
		}
	}
	if attempts.Load() != 3 {
		t.Fatalf("rejected sleeper wave reached upstream: attempts=%d", attempts.Load())
	}
	cancel()
	for i := 0; i < 3; i++ {
		select {
		case <-done:
		case <-time.After(300 * time.Millisecond):
			t.Fatal("cancellation retained a sleeper permit")
		}
	}
	assertLifecycleHealthy(t, p)
	// All three permits must be reusable after cancellation.
	var releases []func()
	for i := 0; i < 3; i++ {
		release, err := p.AdmitRequest(context.Background())
		if err != nil {
			t.Fatalf("lifecycle permit leaked after cancellation: %v", err)
		}
		releases = append(releases, release)
	}
	for _, release := range releases {
		release()
	}
}
