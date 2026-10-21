// Local-only diagnostic harness. All HTTP endpoints use httptest and fake credentials.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/pool"
	"codearts2api/internal/server"
	"codearts2api/internal/upstream"
)

type readPastTerminal struct{}

func (readPastTerminal) Read([]byte) (int, error) {
	return 0, errors.New("READ PAST TERMINAL: a live connection would block here")
}

func main() {
	for _, terminal := range []struct{ name, data string }{
		{"OpenAI DONE", "data: [DONE]\n\n"},
		{"CodeArts event done", "event: done\ndata: {\"error_code\":\"0\"}\n\n"},
		{"CodeArts JSON DONE", "data: {\"text\":\"[DONE]\",\"error_code\":\"0\"}\n\n"},
	} {
		prefix := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" + terminal.data
		_, err := upstream.AggregateRaw(io.MultiReader(strings.NewReader(prefix), readPastTerminal{}))
		fmt.Printf("aggregate / %s: %v\n", terminal.name, err)
		err = upstream.Stream(httptest.NewRecorder(), io.MultiReader(strings.NewReader(prefix), readPastTerminal{}), "local-model")
		fmt.Printf("stream / %s: %v\n", terminal.name, err)
	}
	stalledPreflight()
	canceledAdmission()
	busyFirstAccount()
}

func stalledPreflight() {
	started := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer backend.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	c := upstream.NewWithChatEndpoint(25*time.Millisecond, backend.URL)
	go func() {
		rc, err := c.SendChatV2(ctx, map[string]any{"model": "local-model"}, "local", upstream.SignCredential{}, "local", false)
		if rc != nil {
			_ = rc.Close()
		}
		result <- err
	}()
	<-started
	select {
	case err := <-result:
		fmt.Printf("SSE preflight with constructor timeout 25ms: returned: %v\n", err)
	case <-time.After(150 * time.Millisecond):
		fmt.Println("SSE preflight with constructor timeout 25ms: STILL BLOCKED after 150ms")
		cancel()
		fmt.Printf("SSE preflight cleanup after explicit cancel: %v\n", <-result)
	}
}

func newPool(names ...string) *pool.Pool {
	var auths []*auth.Auth
	for _, name := range names {
		auths = append(auths, auth.New(name, "local", "local", "local", "local", "local", time.Now().Add(time.Hour).Format(time.RFC3339), "", ""))
	}
	p, err := pool.New(auths, pool.Config{MaxConcurrent: 1}, "")
	if err != nil {
		panic(err)
	}
	return p
}

func successBackend() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
}

func canceledAdmission() {
	backend := successBackend()
	defer backend.Close()
	p := newPool("local-canceled")
	p.Get("local-canceled").Client = upstream.NewWithChatEndpoint(time.Second, backend.URL)
	h := server.NewHandler(server.Config{Pool: p, MaxRotate: 1, ErrThreshold: 1})
	if !p.AcquireLock("local-canceled") {
		panic("reserve local slot")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"local"}]}`)).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	select {
	case <-done:
		fmt.Println("already canceled request while account busy: returned promptly")
	case <-time.After(150 * time.Millisecond):
		fmt.Println("already canceled request while account busy: STILL WAITING after 150ms (configured wait 180s)")
	}
	p.ReleaseLock("local-canceled")
	<-done
	_, healthy, _, cooling, _ := p.Stats()
	fmt.Printf("pool health after canceled request: healthy=%d cooling=%d (expected healthy=1 cooling=0)\n", healthy, cooling)
}

func busyFirstAccount() {
	backend := successBackend()
	defer backend.Close()
	p := newPool("local-busy-first", "local-free-second")
	for _, acct := range p.Accounts() {
		acct.Client = upstream.NewWithChatEndpoint(time.Second, backend.URL)
	}
	h := server.NewHandler(server.Config{Pool: p, MaxRotate: 2})
	if !p.AcquireLock("local-busy-first") {
		panic("reserve first slot")
	}
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"local"}]}`)))
		close(done)
	}()
	select {
	case <-done:
		fmt.Println("first account busy, second account free: completed promptly")
	case <-time.After(150 * time.Millisecond):
		fmt.Println("first account busy, second account free: STILL WAITING on first account after 150ms (configured wait 180s)")
	}
	p.ReleaseLock("local-busy-first")
	<-done
}
