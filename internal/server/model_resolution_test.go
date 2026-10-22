package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/upstream"
)

func TestChatResolvesSelectedAccountModelCaseOnWire(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		mu.Lock()
		sent = append(sent, body.Model)
		mu.Unlock()
		if r.URL.Path == "/first" || body.Model != "glm-5.3" {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":"InferHub.002002009.404 model not registered"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	}))
	defer backend.Close()
	sink := newAuditSink(t)
	h, p := auditHandler(t, backend.URL, 2, sink, nil)
	a, b := p.Accounts()[0], p.Accounts()[1]
	a.Client = upstream.NewWithChatEndpoint(time.Second, backend.URL+"/first")
	upstream.SetAccountModels(a.UID, []upstream.ModelInfo{{ID: "GLM-5.3"}})
	upstream.SetAccountModels(b.UID, []upstream.ModelInfo{{ID: "glm-5.3"}})
	rec, id := doChat(h, "gLm-5.3")
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 || sent[0] != "GLM-5.3" || sent[1] != "glm-5.3" {
		t.Fatalf("wire models=%q", sent)
	}
	ev := waitForRecord(t, sink, id)
	if ev.Model != "gLm-5.3" || ev.ModelCanonical != "glm-5.3" || ev.Attempts != len(sent) {
		t.Fatalf("audit=%+v", ev)
	}
	assertLifecycleHealthy(t, p)
}

func TestChatStaticAliasNegativeUsesSentIdentity(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"error":"InferHub.002002009.404 model not registered"}`)
	}))
	defer backend.Close()
	h, p := lifecycleHandler(t, 1, backend.URL)
	for _, model := range []string{"snap-chat", "GLM-5.2", "SNAP-CHAT"} {
		rec, _ := doChat(h, model)
		if rec.Code != 404 {
			t.Fatalf("%s: status=%d", model, rec.Code)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("same model alias bypassed denial: calls=%d", calls)
	}
	assertLifecycleHealthy(t, p)
}

func TestChatAndProbeResolveOwnCaseDefaultsAndUnknowns(t *testing.T) {
	for _, tc := range []struct {
		name, own, input, fallback, want string
		benefit                          bool
	}{
		{"lowercase", "GLM-5.3", "glm-5.3", "", "GLM-5.3", false},
		{"mixed", "gLm-5.3", "GLM-5.3", "", "gLm-5.3", false},
		{"benefit", "glm-5.3", "Glm-5.3", "", "glm-5.3", true},
		{"auto", "glm-5.3", "auto", "GLM-5.3", "glm-5.3", false},
		{"default", "glm-5.3", "", "GLM-5.3", "glm-5.3", false},
		{"unknown", "", "foreign-only", "", "foreign-only", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan string, 2)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				seen <- body.Model
				if body.Model != tc.want || (r.Header.Get(upstream.HeaderMaasType) == upstream.MaasBenefit) != tc.benefit {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":"InferHub.002002009.404 model not registered"}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, lifecycleSuccess)
			}))
			defer backend.Close()
			sink := newAuditSink(t)
			h, p := auditHandler(t, backend.URL, 1, sink, nil)
			acct := p.Accounts()[0]
			if tc.own != "" {
				upstream.SetAccountModels(acct.UID, []upstream.ModelInfo{{ID: tc.own, Benefit: tc.benefit}})
			}
			upstream.SetAccountModels(t.Name()+"-foreign", []upstream.ModelInfo{{ID: "GLM-5.3"}, {ID: "Foreign-Only"}})
			if tc.fallback != "" {
				h.cfg.DefaultModel = tc.fallback
			}
			probeModel := tc.input
			if probeModel == "" || probeModel == "auto" {
				probeModel = h.cfg.DefaultModel
			}
			h.probeModel(acct, probeModel)
			select {
			case got := <-seen:
				if got != tc.want {
					t.Fatalf("probe sent %q, want %q", got, tc.want)
				}
			default:
				t.Fatal("probe made no HTTP request")
			}
			if state, _ := modelAvailability(acct.UID, tc.want); state != availUsable {
				t.Fatal("probe used another model identity or route")
			}
			rec, id := doChat(h, tc.input)
			if rec.Code != http.StatusOK {
				t.Fatalf("chat status=%d body=%s", rec.Code, rec.Body.String())
			}
			if got := <-seen; got != tc.want {
				t.Fatalf("chat sent %q, want %q", got, tc.want)
			}
			ev := waitForRecord(t, sink, id)
			if ev.Model != tc.input || ev.ModelCanonical != tc.want {
				t.Fatalf("audit requested/sent identity: %+v", ev)
			}
			assertLifecycleHealthy(t, p)
		})
	}
}

func TestChatSourceCaseSwitchAndAuthReplacementOnWire(t *testing.T) {
	f, discoveryBackend := newCatalogFixture(t)
	f.bodies[upstream.EpAgentDetail] = `{"gpts":{"models":[]}}`
	f.bodies[upstream.EpModelBuiltin] = `{"builtinModels":[{"model_id":"GLM-5.3","context_window":32768}]}`
	f.bodies[upstream.EpBenefitConfig] = `{"error_code":"0000","result":{"models":[{"model_id":"glm-5.3"}]}}`
	h, p := catalogHandler(t, 1, f, discoveryBackend)
	acct := p.Accounts()[0]
	type sentModel struct {
		id      string
		benefit bool
	}
	seen := make(chan sentModel, 3)
	chatBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		seen <- sentModel{body.Model, r.Header.Get(upstream.HeaderMaasType) == upstream.MaasBenefit}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
	}))
	defer chatBackend.Close()
	acct.Client = upstream.NewWithChatEndpoint(time.Second, chatBackend.URL)
	refreshCatalog(t, h)
	waitCatalog(t, h)
	chat := func(want string, benefit bool) {
		t.Helper()
		rec := catalogRequest(h, http.MethodPost, "/v1/chat/completions", `{"model":"gLm-5.3","messages":[{"role":"user","content":"hi"}]}`, "fixture-key")
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		got := <-seen
		if got.id != want || got.benefit != benefit {
			t.Fatalf("sent=%+v want=%s/%v", got, want, benefit)
		}
	}
	chat("glm-5.3", true)
	f.mu.Lock()
	f.bodies[upstream.EpBenefitGatewayConfig] = `{"enabled":false}`
	f.mu.Unlock()
	owner := acct.AuthSnapshot()
	upstream.PublishModelDiscovery(acct.UID, owner, h.cfg.Upstream.DiscoverModels(context.Background(), owner, true))
	chat("GLM-5.3", false)
	p.AddAccount(auth.New(acct.UID, "replacement", "domain", "new-token", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", ""))
	chat("gLm-5.3", false)
	assertLifecycleHealthy(t, p)
}
