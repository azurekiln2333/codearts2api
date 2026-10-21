// Read-only planning harness for task 10-21-production-multimodel-timeouts.
//
// It drives the REAL HTTP chat handler (server.NewHandler) against fake local
// upstreams on loopback only. No production traffic, no credentials, no auths/.
//
// Each scenario states exactly what it does and does not prove.
//
//	go run ./.trellis/tasks/10-21-production-multimodel-timeouts/research/harness
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/pool"
	"codearts2api/internal/server"
	"codearts2api/internal/upstream"
)

const sseOK = "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

func fakeAuth(name string) *auth.Auth {
	return auth.New(name, name, "domain", "token", "ak", "sk",
		time.Now().Add(2*time.Hour).Format(time.RFC3339), "", "verifier")
}

func newPool(names ...string) *pool.Pool {
	var auths []*auth.Auth
	for _, n := range names {
		auths = append(auths, fakeAuth(n))
	}
	p, err := pool.New(auths, pool.Config{MaxConcurrent: 1, MaxPending: 64, ErrThreshold: 3}, "")
	if err != nil {
		panic(err)
	}
	return p
}

// backend answers by ACCOUNT index only; the model payload is ignored on purpose.
// Every account except servingAccount returns a CodeArts capability failure.
func backend(servingAccount int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account := strings.TrimPrefix(r.URL.Path, "/")
		idx := -1
		fmt.Sscanf(account, "acct-%d", &idx)
		_, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if idx != servingAccount {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"InferHub.002002009.404 The model is not registered, please request other model"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseOK)
		w.(http.Flusher).Flush()
	}))
}

func wire(p *pool.Pool, timeout time.Duration, endpoint func(name string) string) {
	for _, a := range p.Accounts() {
		a.Client = upstream.NewWithChatEndpoint(timeout, endpoint(a.Name))
	}
}

func postChat(client *http.Client, base, model string, timeout time.Duration) (int, string, time.Duration) {
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"diag"}],"stream":false,"max_tokens":8}`, model)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		return 0, "BUILD_ERROR: " + err.Error(), 0
	}
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, "CLIENT_ERROR: " + err.Error(), time.Since(start)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode, string(strings.TrimSpace(string(raw))), time.Since(start)
}

func summarize(statuses []int, times []int64) string {
	byStatus := map[int][]int64{}
	for i, s := range statuses {
		byStatus[s] = append(byStatus[s], times[i])
	}
	keys := make([]int, 0, len(byStatus))
	for k := range byStatus {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var sb strings.Builder
	for _, k := range keys {
		v := byStatus[k]
		sort.Slice(v, func(a, b int) bool { return v[a] < v[b] })
		label := fmt.Sprint(k)
		if k == 0 {
			label = "no-status"
		}
		fmt.Fprintf(&sb, "%s x%d [min=%dms median=%dms max=%dms] ", label, len(v), v[0], v[len(v)/2], v[len(v)-1])
	}
	return strings.TrimSpace(sb.String())
}

func heldSlots(p *pool.Pool) int {
	held := 0
	for _, a := range p.Accounts() {
		if p.Busy(a.Name) {
			held++
		}
	}
	return held
}

// ---------------------------------------------------------------- scenarios

// S1: a model served only by the LAST of five unknown-catalog accounts.
//
// A FRESH model id is used per trial because availability negatives are cached
// process-wide by accountID|model for 30 minutes: reusing one id would let the
// first trial's markUnusable() results suppress accounts in the control run.
func scenarioRotationCap() {
	fmt.Println("== S1 capability false-negative vs MaxRotate (fresh model id per trial) ==")
	for _, maxRotate := range []int{3, 5} {
		model := fmt.Sprintf("only-on-acct-4-rot%d", maxRotate)
		p := newPool("acct-0", "acct-1", "acct-2", "acct-3", "acct-4")
		be := backend(4)
		wire(p, 2*time.Second, func(name string) string { return be.URL + "/" + name })
		h := server.NewHandler(server.Config{Pool: p, MaxRotate: maxRotate, QueueMaxAttempts: 1, ErrThreshold: 3})
		front := httptest.NewServer(h)
		status, body, elapsed := postChat(front.Client(), front.URL, model, 10*time.Second)
		fmt.Printf("  model=%s MaxRotate=%d -> status=%d elapsed=%s\n    body=%.170s\n",
			model, maxRotate, status, elapsed.Round(time.Millisecond), body)
		front.Close()
		be.Close()
	}
}

// S2: 18 concurrent mixed-model requests, three of four accounts cooling,
// max_concurrent=5, healthy fake upstream. Control case only: it shows wiring,
// alias handling and slot reuse when the upstream answers instantly. It does not
// measure provider latency or queue pressure.
func scenarioMixedWave() {
	fmt.Println("== S2 18 mixed-model requests, 3/4 accounts cooling, healthy upstream ==")
	p := newPool("acct-0", "acct-1", "acct-2", "acct-3")
	for _, a := range p.Accounts() {
		p.SetMaxConcurrent(a.Name, 5)
	}
	for _, n := range []string{"acct-1", "acct-2", "acct-3"} {
		p.Cooldown(n, pool.CoolSoft, 10*time.Minute, "planning harness: simulated cooling")
	}
	be := backend(0)
	wire(p, 2*time.Second, func(name string) string { return be.URL + "/" + name })
	h := server.NewHandler(server.Config{Pool: p, MaxRotate: 3, QueueMaxAttempts: 3, QueueRetryDelay: 2 * time.Second, ErrThreshold: 3})
	front := httptest.NewServer(h)
	client := front.Client()

	models := []string{"GLM-5.2", "glm-5.2", "glm-5.2-sft-harmony", "GLM-5.3", "glm-5.3", "kimi-k2.6-vl",
		"OpenPangu-2.0-Flash", "openpangu-2.0-flash", "OpenPangu-2.0-Pro", "openpangu-2.0-pro",
		"Qwen3-VL-235B", "qwen3-vl-235b", "deepseek-v4-flash-0731", "deepseek-v4-pro-0813", "glm-5.3-flash",
		"glm-5.2", "deepseek-v4-flash-0731", "openpangu-2.0-flash"}
	statuses := make([]int, len(models))
	times := make([]int64, len(models))
	var wg sync.WaitGroup
	start := time.Now()
	for i, model := range models {
		wg.Add(1)
		go func(i int, model string) {
			defer wg.Done()
			status, _, elapsed := postChat(client, front.URL, model, 60*time.Second)
			statuses[i] = status
			times[i] = elapsed.Milliseconds()
		}(i, model)
	}
	wg.Wait()
	fmt.Printf("  wall=%s %s\n", time.Since(start).Round(time.Millisecond), summarize(statuses, times))
	fmt.Printf("  accounts still holding slots: %d\n", heldSlots(p))
	front.Close()
	be.Close()
}

// S3: lifecycle ticket cap while three of four accounts are cooling.
func scenarioAdmissionCap() {
	fmt.Println("== S3 lifecycle ticket cap with cooling accounts ==")
	p := newPool("acct-0", "acct-1", "acct-2", "acct-3")
	for _, a := range p.Accounts() {
		p.SetMaxConcurrent(a.Name, 5)
	}
	for _, n := range []string{"acct-1", "acct-2", "acct-3"} {
		p.Cooldown(n, pool.CoolSoft, 10*time.Minute, "harness")
	}
	var releases []func()
	for {
		release, err := p.AdmitRequest(context.Background())
		if err != nil {
			break
		}
		releases = append(releases, release)
		if len(releases) > 500 {
			break
		}
	}
	schedulable := 0
	for _, a := range p.Accounts() {
		if p.Healthy(a.Name) {
			schedulable += 5
		}
	}
	fmt.Printf("  admitted tickets=%d (= MaxPending 64 + all non-disabled capacities 20)\n", len(releases))
	fmt.Printf("  schedulable slots=%d -> cap is bounded but looser than live capacity by %d\n",
		schedulable, len(releases)-schedulable)
	for _, r := range releases {
		r()
	}
}

// S4: three of four accounts cooling and an upstream that accepts the connection
// but never sends response headers. Scaled budgets (upstream idle 800ms, account
// wait 500ms, startup 3s) stand in for 60s/10s/90s so the harness stays fast.
// Expected mixture: holders of the 5 real slots end at the upstream idle bound
// (~800ms, 504) and queued requests end at the account-wait bound (~500ms, 429).
func scenarioStalledUpstream() {
	fmt.Println("== S4 18 requests, 3/4 accounts cooling, silent upstream (scaled budgets) ==")
	p := newPool("acct-0", "acct-1", "acct-2", "acct-3")
	for _, a := range p.Accounts() {
		p.SetMaxConcurrent(a.Name, 5)
	}
	for _, n := range []string{"acct-1", "acct-2", "acct-3"} {
		p.Cooldown(n, pool.CoolSoft, 10*time.Minute, "planning harness: simulated cooling")
	}
	stall := make(chan struct{})
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-stall:
		case <-r.Context().Done():
		}
	}))
	defer func() { close(stall); be.Close() }()
	wire(p, 800*time.Millisecond, func(string) string { return be.URL })
	h := server.NewHandler(server.Config{
		Pool: p, MaxRotate: 3, QueueMaxAttempts: 3, QueueRetryDelay: 2 * time.Second, ErrThreshold: 3,
		StartupTimeout: 3 * time.Second, AccountWaitTimeout: 500 * time.Millisecond,
		StreamWriteTimeout: time.Second,
	})
	front := httptest.NewServer(h)
	defer front.Close()
	client := front.Client()

	statuses := make([]int, 18)
	times := make([]int64, 18)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < 18; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			status, _, elapsed := postChat(client, front.URL, "glm-5.2", 20*time.Second)
			statuses[i] = status
			times[i] = elapsed.Milliseconds()
		}(i)
	}
	wg.Wait()
	fmt.Printf("  wall=%s %s\n", time.Since(start).Round(time.Millisecond), summarize(statuses, times))
	fmt.Printf("  accounts still holding slots: %d (want 0)\n", heldSlots(p))
	fmt.Printf("  schedulable accounts after the wave: %d (cooling accounts must stay untouched)\n",
		healthyCount(p))
}

func healthyCount(p *pool.Pool) int {
	n := 0
	for _, a := range p.Accounts() {
		if p.Healthy(a.Name) {
			n++
		}
	}
	return n
}

// S5: every attempt returns upstream 429. 18 requests stay BELOW the admission cap
// (MaxPending 64 + 2 slots = 66), so this measures finite retry behaviour and
// cleanup only. Saturation rejection is covered by the existing regressions
// (internal/server/concurrency_test.go, internal/pool/admission_test.go).
func scenarioRetrySleepers() {
	fmt.Println("== S5 18 requests, upstream 429 on every attempt (below the admission cap) ==")
	p := newPool("acct-0")
	p.SetMaxConcurrent("acct-0", 2)
	var attempts int64
	var mu sync.Mutex
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"TPM limit"}`)
	}))
	defer be.Close()
	wire(p, time.Second, func(string) string { return be.URL })
	h := server.NewHandler(server.Config{
		Pool: p, MaxRotate: 1, QueueMaxAttempts: 3, QueueRetryDelay: 400 * time.Millisecond, ErrThreshold: 3,
		StartupTimeout: 5 * time.Second, AccountWaitTimeout: 300 * time.Millisecond,
		StreamWriteTimeout: time.Second,
	})
	front := httptest.NewServer(h)
	defer front.Close()
	client := front.Client()

	statuses := make([]int, 18)
	times := make([]int64, 18)
	var wg sync.WaitGroup
	for i := 0; i < 18; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			status, _, elapsed := postChat(client, front.URL, "glm-5.2", 20*time.Second)
			statuses[i] = status
			times[i] = elapsed.Milliseconds()
		}(i)
	}
	wg.Wait()
	mu.Lock()
	upstreamAttempts := attempts
	mu.Unlock()
	fmt.Printf("  %s\n", summarize(statuses, times))
	fmt.Printf("  upstream attempts=%d (18 requests x <=3 attempts x <=1 rotation is the hard bound)\n", upstreamAttempts)
	fmt.Printf("  accounts still holding slots: %d (want 0)\n", heldSlots(p))
}

// S6: non-streaming aggregation of an ACTIVE upstream that keeps sending data well
// past the startup budget. This is the legitimate long-generation case: the startup
// timer stops after preflight, so the client sees no response headers until the
// aggregation finishes. It is why "no headers for N seconds" alone cannot identify a
// stalled request.
func scenarioLongAggregation() {
	fmt.Println("== S6 non-stream aggregation of an active upstream past the startup budget ==")
	p := newPool("acct-0")
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		for i := 0; i < 12; i++ {
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-time.After(60 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}))
	defer be.Close()
	wire(p, 200*time.Millisecond, func(string) string { return be.URL })
	h := server.NewHandler(server.Config{
		Pool: p, MaxRotate: 1, QueueMaxAttempts: 1, ErrThreshold: 3,
		StartupTimeout: 150 * time.Millisecond, // deliberately shorter than the stream
		AccountWaitTimeout: 100 * time.Millisecond, StreamWriteTimeout: time.Second,
	})
	front := httptest.NewServer(h)
	defer front.Close()
	status, body, elapsed := postChat(front.Client(), front.URL, "glm-5.2", 20*time.Second)
	content := strings.Count(body, "\"content\":\"")
	fmt.Printf("  status=%d elapsed=%s startup=150ms stream~720ms content_parts=%d\n",
		status, elapsed.Round(time.Millisecond), content)
	fmt.Printf("  body=%.120s\n", body)
	fmt.Printf("  accounts still holding slots: %d (want 0)\n", heldSlots(p))
}

func main() {
	scenarioRotationCap()
	fmt.Println()
	scenarioMixedWave()
	fmt.Println()
	scenarioAdmissionCap()
	fmt.Println()
	scenarioStalledUpstream()
	fmt.Println()
	scenarioRetrySleepers()
	fmt.Println()
	scenarioLongAggregation()
}
