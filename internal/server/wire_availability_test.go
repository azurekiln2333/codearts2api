package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/upstream"
)

// A corrected registration may receive a genuine denial before an old wrong-ID
// attempt returns. That late result must not overwrite the new wire's evidence.
func TestLateOldWireDenialPreservesCurrentWireProviderDenial(t *testing.T) {
	const oldWire, currentWire = "Late-Wire-Identity", "late-wire-identity"
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseOld := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseOld()
	var currentCalls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		switch input.Model {
		case oldWire:
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		case currentWire:
			currentCalls.Add(1)
		default:
			t.Errorf("unexpected inferred model substitution: %q", input.Model)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"InferHub.002002009.404 model not registered"}`)
	}))
	defer func() { releaseOld(); backend.Close() }()
	f, metadata := newCatalogFixture(t)
	f.bodies[upstream.EpAgentDetail] = `{"gpts":{"models":[]}}`
	f.bodies[upstream.EpModelBuiltin] = fmt.Sprintf(`{"builtinModels":[{"model_id":%q,"model_name":%q}]}`, currentWire, oldWire)
	f.bodies[upstream.EpBenefitGatewayConfig] = `{"enabled":false}`
	h, p := catalogHandler(t, 1, f, metadata)
	acct := p.Accounts()[0]
	owner := acct.AuthSnapshot()
	p.SetMaxConcurrent(acct.Name, 2)
	acct.Client = upstream.NewWithChatEndpoint(time.Second, backend.URL)
	chat := func(model string) *httptest.ResponseRecorder {
		return catalogRequest(h, http.MethodPost, "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"fixture"}]}`, model), "fixture-key")
	}
	oldDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { oldDone <- chat(oldWire) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old cold-start wire never reached actual HTTP")
	}
	if state := h.requestModelRefresh(false); state.State != "running" {
		t.Fatalf("ordinary discovery did not start: %+v", state)
	}
	if state := waitCatalog(t, h); state.State != "completed" || state.NegativeResetCount != 0 {
		t.Fatalf("ordinary discovery was not authoritative: %+v", state)
	}
	if rec := chat(oldWire); rec.Code != http.StatusNotFound || currentCalls.Load() != 1 {
		t.Fatalf("corrected exact wire did not receive its own real denial: status=%d calls=%d", rec.Code, currentCalls.Load())
	}
	releaseOld()
	select {
	case rec := <-oldDone:
		if rec.Code != http.StatusNotFound {
			t.Fatalf("old provider refusal changed outcome: %d", rec.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("old request retained local resources")
	}
	if state, _ := modelAvailabilityForAuth(acct.UID, oldWire, owner); state != availUnusable {
		t.Fatal("late wrong-wire denial overwrote the genuine current-wire denial")
	}
	availability.Lock()
	entry := availability.byKey[availKey(acct.UID, currentWire)]
	availability.Unlock()
	if entry.wireID != currentWire || entry.owner != owner {
		t.Fatalf("negative retained the wrong actual identity: wire=%q", entry.wireID)
	}
	if !h.modelKnownUnusable(oldWire) || h.pickByCatalog(nil, oldWire) != nil {
		t.Fatal("model list or compatibility selection bypassed current exact denial")
	}
	if rec := chat(oldWire); rec.Code != http.StatusNotFound || currentCalls.Load() != 1 {
		t.Fatalf("same-wire provider denial was discarded: status=%d calls=%d", rec.Code, currentCalls.Load())
	}
	assertLifecycleHealthy(t, p)
}

func TestAvailabilityResultsStayWithCapturedLoginAndWire(t *testing.T) {
	h, p := lifecycleHandler(t, 1, "http://127.0.0.1:1")
	acct := p.Accounts()[0]
	oldOwner := acct.AuthSnapshot()
	const wire = "login-wire-model"
	h.markModelUnusable(acct, oldOwner, wire, "not registered")
	if state, _ := modelAvailabilityForAuth(acct.UID, wire, oldOwner); state != availUnusable {
		t.Fatal("actual login denial was not recorded")
	}
	replacement := auth.New(acct.UID, "replacement", "domain", "new-token", "new-ak", "new-sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", "")
	p.AddAccount(replacement)
	if state, _ := modelAvailabilityForAuth(acct.UID, wire, replacement); state != availUnknown {
		t.Fatal("replacement login inherited old availability")
	}
	h.markModelUnusable(acct, replacement, wire, "current not registered")
	h.markModelUsable(acct, oldOwner, wire)
	if state, reason := modelAvailabilityForAuth(acct.UID, wire, replacement); state != availUnusable || reason != "current not registered" {
		t.Fatal("late old-login success cleared replacement's genuine denial")
	}
	h.markModelUsable(acct, replacement, wire)
	h.markModelUnusable(acct, oldOwner, wire, "late old denial")
	if state, _ := modelAvailabilityForAuth(acct.UID, wire, replacement); state != availUsable {
		t.Fatal("late old-login denial poisoned replacement's actual success")
	}
}
