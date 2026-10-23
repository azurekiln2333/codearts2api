package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"codearts2api/internal/audit"
	"codearts2api/internal/upstream"
)

func newColdIdentityFixture(t *testing.T, agent, builtin string, valid map[string]bool, deniedGate <-chan struct{}, deniedStarted chan<- struct{}) *liveIdentityFixture {
	t.Helper()
	f := &liveIdentityFixture{wire: make(chan liveIdentityWire, 32)}
	meta, metadata := newCatalogFixture(t)
	meta.bodies[upstream.EpAgentDetail] = agent
	meta.bodies[upstream.EpModelBuiltin] = builtin
	meta.bodies[upstream.EpBenefitGatewayConfig] = `{"enabled":false}`
	f.meta = meta
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case f.wire <- liveIdentityWire{body.Model, r.Header.Get(upstream.HeaderMaasType)}:
		default:
			t.Error("unexpected excess inference requests")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if !valid[body.Model] {
			if deniedStarted != nil {
				select {
				case deniedStarted <- struct{}{}:
				default:
				}
			}
			if deniedGate != nil {
				select {
				case <-deniedGate:
				case <-r.Context().Done():
					return
				}
			}
		}
		if f.deny.Load() || !valid[body.Model] {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"InferHub.002002009.404 model not registered"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	}))
	t.Cleanup(backend.Close)
	f.sink = newAuditSink(t)
	f.h, f.p = auditHandler(t, backend.URL, 1, f.sink, nil)
	f.h.cfg.APIKey = "fixture-key"
	f.h.cfg.Upstream = upstream.NewWithModelEndpoints(time.Second, metadata.URL, metadata.URL)
	f.h.cfg.Upstream.SetBenefitAutoClaim(false)
	ctx, cancel := context.WithCancel(context.Background())
	f.h.modelCatalog.serviceCtx = ctx
	t.Cleanup(cancel)
	f.gateway = httptest.NewServer(f.h)
	t.Cleanup(f.gateway.Close)
	// Deliberately do not refresh: the first chat must exercise the real
	// account-without-catalog path, without a hidden model-list prewarm.
	return f
}

func TestColdIdentityLateWrongCaseCannotOverwriteCorrectWireDenial(t *testing.T) {
	const display, exact = "Fixture-Late-Identity-V1", "fixture-late-identity-v1"
	const agent = `{"gpts":{"models":[{"model_name":"Fixture-Late-Identity-V1","model_parameters":{"model_id":"fixture-late-identity-v1"}}]}}`
	const builtin = `{"builtinModels":[{"model_name":"Fixture-Late-Identity-V1","model_id":"fixture-late-identity-v1"}]}`
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	f := newColdIdentityFixture(t, agent, builtin, map[string]bool{exact: true}, gate, started)
	acct := f.p.Accounts()[0]
	f.p.SetMaxConcurrent(acct.Name, 2)
	done := make(chan coldIdentityCall, 1)
	go func() { done <- requestColdIdentity(f.gateway.URL, display) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old wire request did not reach provider")
	}
	f.expectWire(t, display)
	f.h.requestModelRefresh(false)
	if state := waitCatalog(t, f.h); state.State != "completed" {
		t.Fatalf("background discovery failed: %+v", state)
	}
	// A newer, real refusal of the correct wire ID is authoritative. The stale
	// request below must neither replace its identity nor make it disappear.
	f.deny.Store(true)
	status, _, current := f.chat(t, exact, false)
	if status != http.StatusNotFound || current.Attempts != 1 || current.ModelCanonical != exact {
		t.Fatalf("correct wire denial was not actually sent: status=%d event=%+v", status, current)
	}
	f.expectWire(t, exact)
	f.deny.Store(false)
	close(gate)
	old := <-done
	if old.err != nil || old.status != http.StatusNotFound {
		t.Fatalf("old literal wire failure: status=%d err=%v", old.status, old.err)
	}
	status, _, cached := f.chat(t, display, false)
	if status != http.StatusNotFound || cached.Attempts != 0 {
		t.Fatalf("old mapping erased correct-wire rejection: status=%d event=%+v", status, cached)
	}
	select {
	case wire := <-f.wire:
		t.Fatalf("authoritative current denial was bypassed: %+v", wire)
	default:
	}
	assertLifecycleHealthy(t, f.p)
}

func TestColdIdentityCapturedDisplayNamesBeforeDiscovery(t *testing.T) {
	f := newColdIdentityFixture(t, liveIdentityAgent, liveIdentityBuiltin, map[string]bool{"glm-5.3": true, "deepseek-v4.1-flash": true}, nil, nil)
	for _, tc := range []struct{ input, wire string }{
		{"GLM-5.3", "glm-5.3"},
		{"DeepSeek-V4.1-Flash", "deepseek-v4.1-flash"},
	} {
		for _, stream := range []bool{false, true} {
			status, raw, ev := f.chat(t, tc.input, stream)
			if status != http.StatusOK {
				t.Fatalf("cold request %q: status=%d body=%s event=%+v", tc.input, status, raw, ev)
			}
			f.expectWire(t, tc.wire)
			assertLiveIdentityContent(t, raw, stream)
			if ev.Model != tc.input || ev.ModelCanonical != tc.wire || ev.Attempts != 1 || ev.Outcome != "ok" {
				t.Fatalf("cold audit disagrees with actual request: %+v", ev)
			}
		}
	}
	if f.meta.calls.Load() != 0 {
		t.Fatal("cold chat unexpectedly fetched model metadata")
	}
	if len(f.sink.Connections().Active) != 0 {
		t.Fatal("cold requests retained active tracking")
	}
	assertLifecycleHealthy(t, f.p)
}

type coldIdentityCall struct {
	status int
	raw    []byte
	id     string
	err    error
}

func requestColdIdentity(base, model string) coldIdentityCall {
	raw, err := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "fixture"}}})
	if err != nil {
		return coldIdentityCall{err: err}
	}
	req, err := http.NewRequest(http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return coldIdentityCall{err: err}
	}
	req.Header.Set("Authorization", "Bearer fixture-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return coldIdentityCall{err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return coldIdentityCall{resp.StatusCode, body, resp.Header.Get("X-Request-Id"), err}
}

func TestColdIdentityDynamicWireCorrectionIgnoresOldCaseDenial(t *testing.T) {
	const display, exact = "Fixture-Cold-Identity-V1", "fixture-cold-identity-v1"
	const agent = `{"gpts":{"models":[{"model_name":"Fixture-Cold-Identity-V1","model_parameters":{"model_id":"fixture-cold-identity-v1"}},{"model_name":"GLM-5.2","model_parameters":{"model_id":"GLM-5.2"}}]}}`
	const builtin = `{"builtinModels":[{"model_name":"Fixture-Cold-Identity-V1","model_id":"fixture-cold-identity-v1"},{"model_name":"GLM-5.2","model_id":"GLM-5.2"}]}`
	for _, late := range []bool{false, true} {
		name := "denial_before_discovery"
		if late {
			name = "denial_after_discovery"
		}
		t.Run(name, func(t *testing.T) {
			gate := make(chan struct{})
			started := make(chan struct{}, 1)
			if !late {
				close(gate)
			}
			f := newColdIdentityFixture(t, agent, builtin, map[string]bool{exact: true, "GLM-5.2": true}, gate, started)
			acct := f.p.Accounts()[0]
			markUnusable(acct.UID, "GLM-5.2", "fixture unrelated denial")
			done := make(chan coldIdentityCall, 1)
			go func() { done <- requestColdIdentity(f.gateway.URL, display) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("unknown cold model did not reach its literal wire ID")
			}
			f.expectWire(t, display)
			var first coldIdentityCall
			if !late {
				first = <-done
			}
			f.h.requestModelRefresh(false)
			if state := waitCatalog(t, f.h); state.State != "completed" || state.NegativeResetCount != 0 {
				t.Fatalf("background discovery did a broad negative reset: %+v", state)
			}
			if late {
				close(gate)
				first = <-done
			}
			if first.err != nil || first.status != http.StatusNotFound {
				t.Fatalf("literal unknown-case denial: status=%d err=%v body=%s", first.status, first.err, first.raw)
			}
			if ev := waitForRecord(t, f.sink, first.id); ev.Attempts != 1 || ev.ModelCanonical != display || ev.Cause.Class != audit.CauseCapability {
				t.Fatalf("original denial lost the actual wire identity: %+v", ev)
			}
			status, raw, ev := f.chat(t, display, true)
			if status != http.StatusOK || ev.Attempts != 1 || ev.ModelCanonical != exact {
				t.Fatalf("corrected ID inherited old wire denial: status=%d body=%s event=%+v", status, raw, ev)
			}
			f.expectWire(t, exact)
			assertLiveIdentityContent(t, raw, true)
			if state, _ := modelAvailability(acct.UID, "GLM-5.2"); state != availUnusable {
				t.Fatal("identity correction cleared an unrelated exact-wire denial")
			}
			assertLifecycleHealthy(t, f.p)
		})
	}
}
