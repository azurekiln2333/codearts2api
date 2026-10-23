package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/audit"
	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

// These identity fields reproduce the authorized source capture. All credentials,
// account IDs, content and HTTP services used here are synthetic and local.
const liveIdentityBuiltin = `{"builtinModels":[
{"model_id":"glm-5.3","model_name":"GLM-5.3"},
{"model_id":"deepseek-v4.1-flash","model_name":"DeepSeek-V4.1-Flash"},
{"model_id":"glm-5.2-sft-harmony","model_name":"GLM-5.2-ArkTS-SPARK"},
{"model_id":"GLM-5.2","model_name":"GLM-5.2"},
{"model_id":"Qwen3-VL-235B","model_name":"Qwen3-VL-235B"}]}`

const liveIdentityAgent = `{"gpts":{"models":[
{"model_name":"GLM-5.3","model_alias":"glm-5.3","model_parameters":{"model_id":"glm-5.3"}},
{"model_name":"DeepSeek-V4.1-Flash","model_alias":"deepseek-v4.1-flash","model_parameters":{"model_id":"deepseek-v4.1-flash"}},
{"model_name":"GLM-5.2-ArkTS-SPARK","model_alias":"glm-5.2-sft-harmony","model_parameters":{"model_id":"glm-5.2-sft-harmony"}},
{"model_name":"GLM-5.2","model_parameters":{"model_id":"GLM-5.2"}},
{"model_name":"Qwen3-VL-235B","model_parameters":{"model_id":"Qwen3-VL-235B"}}]}}`

type liveIdentityWire struct {
	model, route string
}

type liveIdentityFixture struct {
	h       *Handler
	p       *pool.Pool
	sink    *audit.Sink
	meta    *catalogFixture
	gateway *httptest.Server
	wire    chan liveIdentityWire
	deny    atomic.Bool
}

func newLiveIdentityFixture(t *testing.T) *liveIdentityFixture {
	t.Helper()
	f := &liveIdentityFixture{wire: make(chan liveIdentityWire, 32)}
	meta, metadata := newCatalogFixture(t)
	meta.bodies[upstream.EpAgentDetail] = liveIdentityAgent
	meta.bodies[upstream.EpModelBuiltin] = liveIdentityBuiltin
	meta.bodies[upstream.EpBenefitGatewayConfig] = `{"enabled":false}`
	f.meta = meta
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		route := r.Header.Get(upstream.HeaderMaasType)
		select {
		case f.wire <- liveIdentityWire{body.Model, route}:
		default:
			t.Error("unexpected excess inference requests")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		valid := body.Model == "glm-5.3" || body.Model == "deepseek-v4.1-flash" || body.Model == "glm-5.2-sft-harmony" || body.Model == "GLM-5.2" || body.Model == "Qwen3-VL-235B"
		if f.deny.Load() || !valid || route != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"InferHub.002002009.404 model not registered"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	}))
	t.Cleanup(backend.Close)
	f.sink = newAuditSink(t)
	// lifecycleHandler supplies a fresh account UID on every invocation, including
	// repeated -count runs in the same process with shared capability caches.
	f.h, f.p = auditHandler(t, backend.URL, 1, f.sink, nil)
	f.h.cfg.APIKey = "fixture-key"
	f.h.cfg.Upstream = upstream.NewWithModelEndpoints(time.Second, metadata.URL, metadata.URL)
	f.h.cfg.Upstream.SetBenefitAutoClaim(false)
	ctx, cancel := context.WithCancel(context.Background())
	f.h.modelCatalog.serviceCtx = ctx
	t.Cleanup(cancel)
	f.gateway = httptest.NewServer(f.h)
	t.Cleanup(f.gateway.Close)
	refreshCatalog(t, f.h)
	if state := waitCatalog(t, f.h); state.State != "completed" {
		t.Fatalf("initial discovery: %+v", state)
	}
	return f
}

func (f *liveIdentityFixture) chat(t *testing.T, model string, stream bool) (int, []byte, audit.Event) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"model": model, "stream": stream, "messages": []map[string]string{{"role": "user", "content": "fixture"}}})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, f.gateway.URL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer fixture-key")
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	ev := waitForRecord(t, f.sink, resp.Header.Get("X-Request-Id"))
	return resp.StatusCode, raw, ev
}

func (f *liveIdentityFixture) expectWire(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-f.wire:
		if got.model != want || got.route != "" {
			t.Fatalf("wire=%+v, want normal-route %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("expected an actual upstream HTTP request")
	}
}

func assertLiveIdentityContent(t *testing.T, raw []byte, stream bool) {
	t.Helper()
	if stream {
		completion, err := upstream.AggregateRaw(bytes.NewReader(raw))
		if err != nil || completion.Content != "ok" || !bytes.Contains(raw, []byte("[DONE]")) {
			t.Fatalf("incomplete stream: completion=%+v error=%v body=%s", completion, err, raw)
		}
		return
	}
	var response struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || len(response.Choices) != 1 || response.Choices[0].Message.Content != "ok" {
		t.Fatalf("missing successful content: %s", raw)
	}
}

func TestLiveIdentityDiscoveryChatProbeAndRepeatedStream(t *testing.T) {
	f := newLiveIdentityFixture(t)
	metadataCalls := f.meta.calls.Load()
	for _, tc := range []struct{ input, wire string }{
		{"glm-5.3", "glm-5.3"}, {"gLm-5.3", "glm-5.3"},
		{"deepseek-v4.1-flash", "deepseek-v4.1-flash"}, {"DeEpSeek-V4.1-Flash", "deepseek-v4.1-flash"},
		{"GLM-5.2-ArkTS-SPARK", "glm-5.2-sft-harmony"}, {"glm-5.2-sft-harmony", "glm-5.2-sft-harmony"},
		{"glm-5.2", "GLM-5.2"}, {"qwen3-vl-235b", "Qwen3-VL-235B"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				status, raw, ev := f.chat(t, tc.input, stream)
				if status != http.StatusOK {
					t.Fatalf("status=%d body=%s audit=%+v", status, raw, ev)
				}
				f.expectWire(t, tc.wire)
				assertLiveIdentityContent(t, raw, stream)
				if ev.Model != tc.input || ev.ModelCanonical != tc.wire || ev.Stream != stream || ev.Attempts != 1 || ev.Outcome != "ok" || !ev.Committed || ev.Status != 200 {
					t.Fatalf("requested/wire identity or outcome differs: %+v", ev)
				}
				if len(f.sink.Connections().Active) != 0 {
					t.Fatal("completed request retained active tracking")
				}
				assertLifecycleHealthy(t, f.p)
			}
			acct := f.p.Accounts()[0]
			availability.Lock()
			delete(availability.lastTraffic, acct.UID)
			availability.Unlock()
			f.h.probeModel(acct, tc.input)
			f.expectWire(t, tc.wire)
			if state, _ := modelAvailability(acct.UID, tc.wire); state != availUsable {
				t.Fatal("probe did not record availability for the actual inference ID")
			}
			assertLifecycleHealthy(t, f.p)
		})
	}
	if f.meta.calls.Load() != metadataCalls {
		t.Fatal("chat/probe performed metadata discovery")
	}
}

func TestLiveIdentityDisplayAliasDenialScope(t *testing.T) {
	f := newLiveIdentityFixture(t)
	f.deny.Store(true)
	status, _, first := f.chat(t, "GLM-5.2-ArkTS-SPARK", false)
	if status != 404 || first.Attempts != 1 || first.ModelCanonical != "glm-5.2-sft-harmony" || first.Cause.Class != audit.CauseCapability {
		t.Fatalf("actual alias denial: status=%d event=%+v", status, first)
	}
	f.expectWire(t, "glm-5.2-sft-harmony")
	f.deny.Store(false)
	for _, input := range []string{"glm-5.2-sft-harmony", "gLm-5.2-ArkTS-SPARK"} {
		status, _, ev := f.chat(t, input, false)
		if status != 404 || ev.Attempts != 0 {
			t.Fatalf("display alias bypassed inference-ID denial: status=%d event=%+v", status, ev)
		}
	}
	if status, _, ev := f.chat(t, "GLM-5.4-ArkTS-SPARK", false); status == 200 || ev.Attempts != 0 {
		t.Fatalf("unlisted version substituted a known model: status=%d event=%+v", status, ev)
	}
	select {
	case got := <-f.wire:
		t.Fatalf("cached denial or unknown version reached another inference ID: %+v", got)
	default:
	}
	status, raw, ev := f.chat(t, "glm-5.2", false)
	if status != 200 || ev.ModelCanonical != "GLM-5.2" {
		t.Fatalf("one alias denial blocked another model: status=%d event=%+v", status, ev)
	}
	f.expectWire(t, "GLM-5.2")
	assertLiveIdentityContent(t, raw, false)
	assertLifecycleHealthy(t, f.p)
}

func (f *liveIdentityFixture) gatedRefresh(t *testing.T) func() {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	f.meta.mu.Lock()
	f.meta.gate = gate
	f.meta.mu.Unlock()
	f.h.modelCatalog.mu.Lock()
	f.h.modelCatalog.refresh.RetryAt = nil
	f.h.modelCatalog.mu.Unlock()
	refreshCatalog(t, f.h)
	until := time.Now().Add(time.Second)
	for f.meta.active.Load() == 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if f.meta.active.Load() == 0 {
		t.Fatal("refresh did not reach the blocked local metadata server")
	}
	return release
}

func TestLiveIdentityNegativeResetRequiresBuiltinFreshness(t *testing.T) {
	f := newLiveIdentityFixture(t)
	acct := f.p.Accounts()[0]
	f.deny.Store(true)
	deny := func(input, wire string) {
		t.Helper()
		status, _, ev := f.chat(t, input, false)
		if status != 404 || ev.Attempts != 1 || ev.ModelCanonical != wire {
			t.Fatalf("expected actual provider denial: status=%d event=%+v", status, ev)
		}
		f.expectWire(t, wire)
	}
	deny("gLm-5.3", "glm-5.3")
	f.meta.mu.Lock()
	f.meta.statuses[upstream.EpModelBuiltin] = http.StatusServiceUnavailable
	f.meta.mu.Unlock()
	release := f.gatedRefresh(t)
	deny("DeepSeek-V4.1-Flash", "deepseek-v4.1-flash")
	release()
	if state := waitCatalog(t, f.h); state.State != "partial" || state.NegativeResetCount != 0 {
		t.Fatalf("agent-only success cleared a builtin-owned denial: %+v", state)
	}
	for _, model := range []string{"glm-5.3", "deepseek-v4.1-flash"} {
		if state, _ := modelAvailability(acct.UID, model); state != availUnusable {
			t.Fatalf("old/fresh real denial lost for %s", model)
		}
	}
	f.meta.mu.Lock()
	delete(f.meta.statuses, upstream.EpModelBuiltin)
	f.meta.mu.Unlock()
	release = f.gatedRefresh(t)
	deny("GLM-5.2-ArkTS-SPARK", "glm-5.2-sft-harmony")
	release()
	if state := waitCatalog(t, f.h); state.State != "completed" || state.NegativeResetCount != 2 {
		t.Fatalf("fresh builtin did not reset only pre-job denials: %+v", state)
	}
	for _, model := range []string{"glm-5.3", "deepseek-v4.1-flash"} {
		if state, _ := modelAvailability(acct.UID, model); state != availUnknown {
			t.Fatalf("rediscovery did not leave %s unknown: %d", model, state)
		}
	}
	if state, _ := modelAvailability(acct.UID, "glm-5.2-sft-harmony"); state != availUnusable {
		t.Fatal("successful discovery cleared a new concurrent alias denial")
	}
	f.deny.Store(false)
	status, raw, ev := f.chat(t, "GLM-5.3", true)
	if status != 200 || ev.ModelCanonical != "glm-5.3" {
		t.Fatalf("rediscovered inference ID cannot recover: status=%d event=%+v", status, ev)
	}
	f.expectWire(t, "glm-5.3")
	assertLiveIdentityContent(t, raw, true)
	if active := f.sink.Connections().Active; len(active) != 0 {
		t.Fatalf("retained %d active requests", len(active))
	}
	assertLifecycleHealthy(t, f.p)
}
