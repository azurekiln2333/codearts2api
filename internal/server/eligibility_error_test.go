package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codearts2api/internal/audit"
	"codearts2api/internal/auth"
	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

const eligibilityModel = "glm-5.3-flash"

// Exercise the registered chat route over HTTP, including its correlated audit.
func assertEligibilityChat(t *testing.T, h *Handler, status int, code, outcome, cause string) audit.Event {
	t.Helper()
	frontend := httptest.NewServer(h)
	defer frontend.Close()
	req, err := http.NewRequest(http.MethodPost, frontend.URL+"/v1/chat/completions", strings.NewReader(`{"model":"glm-5.3-flash","messages":[{"role":"user","content":"synthetic"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if h.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.cfg.APIKey)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != status {
		t.Errorf("status=%d, want %d; body=%s", res.StatusCode, status, raw)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != code {
		t.Errorf("error code=%q, want %q; body=%s", body.Error.Code, code, raw)
	}
	if status == http.StatusOK && !strings.Contains(string(raw), `"content":"ok"`) {
		t.Errorf("eligible account did not return content: %s", raw)
	}
	id := res.Header.Get("X-Request-Id")
	if id == "" {
		t.Fatal("missing request correlation ID")
	}
	ev := waitForRecord(t, h.cfg.Audit, id)
	if !ev.Committed || ev.Status != status || ev.Outcome != outcome || ev.ErrorCode != code || ev.Cause.Class != cause || ev.Model != eligibilityModel {
		t.Errorf("incorrect correlated audit: %+v", ev)
	}
	return ev
}

func TestChatCompleteCatalogExclusionReturnsModelUnavailable(t *testing.T) {
	for _, mode := range []string{"all-catalog-absent", "catalog-and-provider-denial"} {
		t.Run(mode, func(t *testing.T) {
			f, backend := newCatalogFixture(t)
			f.model = "other-benefit-model"
			f.bodies[upstream.EpModelBuiltin] = `{"builtinModels":[{"model_id":"glm-5.3"},{"model_id":"deepseek-v4.1-flash"}]}`
			h, p := catalogHandler(t, 2, f, backend)
			h.cfg.Audit = newAuditSink(t)
			refreshCatalog(t, h)
			if state := waitCatalog(t, h); state.State != "completed" {
				t.Fatalf("fixture did not establish complete current catalogs: %+v", state)
			}
			for _, acct := range p.Accounts() {
				if present, complete := upstream.AccountModelKnowledgeForAuth(acct.UID, eligibilityModel, acct.AuthSnapshot()); present || !complete {
					t.Fatalf("fixture must authoritatively lack the distinct flash model: present=%t complete=%t", present, complete)
				}
			}
			if mode == "catalog-and-provider-denial" {
				acct := p.Accounts()[1]
				h.markModelUnusable(acct, acct.AuthSnapshot(), eligibilityModel, "not registered")
			}
			calls := f.calls.Load()
			ev := assertEligibilityChat(t, h, http.StatusNotFound, "model_not_available", "model_unavailable", audit.CauseCapability)
			if ev.Attempts != 0 || ev.Account != nil || ev.FirstDataMS != nil || len(ev.Tried) != 0 || ev.Cause.Code != "" {
				t.Errorf("cached model exclusion fabricated an upstream attempt: %+v", ev)
			}
			listed := map[string]bool{}
			for _, entry := range h.modelList() {
				id, _ := entry["id"].(string)
				listed[strings.ToLower(id)] = true
				if strings.EqualFold(id, eligibilityModel) {
					t.Error("cached public list still advertises the authoritatively excluded model")
				}
			}
			if !listed["glm-5.3"] || !listed["deepseek-v4.1-flash"] {
				t.Errorf("catalog exclusion removed supported distinct models: %v", listed)
			}
			if f.calls.Load() != calls || f.chatCalls.Load() != 0 {
				t.Errorf("cached exclusion/list triggered discovery or inference: before=%d after=%d chat=%d", calls, f.calls.Load(), f.chatCalls.Load())
			}
			assertLifecycleHealthy(t, p)
		})
	}
}

func TestChatUnavailablePoolRetainsNoHealthyAccount(t *testing.T) {
	for _, mode := range []string{"empty", "disabled", "cooling", "expired-without-refresh"} {
		t.Run(mode, func(t *testing.T) {
			f, backend := newCatalogFixture(t)
			count := 2
			if mode == "empty" {
				count = 0
			}
			h, p := catalogHandler(t, count, f, backend)
			h.cfg.Audit = newAuditSink(t)
			for _, acct := range p.Accounts() {
				if mode != "expired-without-refresh" {
					// Even authoritative absence cannot make a disabled/cooling
					// pool into a model-capability result.
					upstream.SetAccountModels(acct.UID, []upstream.ModelInfo{{ID: "glm-5.3"}})
				}
				switch mode {
				case "disabled":
					p.Disable(acct.Name, "manual disable")
				case "cooling":
					p.Cooldown(acct.Name, pool.CoolSoft, time.Minute, "HTTP 503")
				case "expired-without-refresh":
					p.AddAccount(auth.New(acct.UID, "expired", "domain", "synthetic-token", "synthetic-ak", "synthetic-sk", time.Now().Add(-time.Hour).Format(time.RFC3339), "", ""))
				}
			}
			cause := audit.CauseNone
			if mode == "expired-without-refresh" {
				cause = audit.CauseUnknown
			}
			ev := assertEligibilityChat(t, h, http.StatusServiceUnavailable, "no_healthy_account", "no_healthy_account", cause)
			if ev.Attempts != 0 || ev.FirstDataMS != nil || f.calls.Load() != 0 {
				t.Errorf("unavailable pool made an upstream call: audit=%+v calls=%d", ev, f.calls.Load())
			}
			for _, acct := range p.Accounts() {
				if p.Busy(acct.Name) {
					t.Error("unavailable pool retained a lease")
				}
			}
		})
	}
}

func TestChatNonauthoritativeCatalogMissRemainsEligible(t *testing.T) {
	for _, mode := range []string{"unknown", "cold-partial", "retained-after-failure", "previous-login", "actual-success-overrides-absence"} {
		t.Run(mode, func(t *testing.T) {
			f, backend := newCatalogFixture(t)
			f.model = "other-benefit-model"
			h, p := catalogHandler(t, 1, f, backend)
			h.cfg.Audit = newAuditSink(t)
			h.modelCatalog.minInterval = time.Nanosecond
			acct := p.Accounts()[0]
			if mode == "cold-partial" {
				f.statuses[upstream.EpBenefitConfig] = http.StatusServiceUnavailable
			}
			if mode != "unknown" {
				refreshCatalog(t, h)
				state := waitCatalog(t, h)
				if state.State != "completed" && !(mode == "cold-partial" && state.State == "partial") {
					t.Fatalf("unexpected initial discovery: %+v", state)
				}
			}
			switch mode {
			case "retained-after-failure":
				f.mu.Lock()
				f.statuses[upstream.EpBenefitConfig] = http.StatusServiceUnavailable
				f.mu.Unlock()
				refreshCatalog(t, h)
				if state := waitCatalog(t, h); state.State != "partial" {
					t.Fatalf("fixture did not retain stale discovery: %+v", state)
				}
			case "previous-login":
				p.AddAccount(auth.New(acct.UID, "replacement", "domain", "new-token", "new-ak", "new-sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", ""))
			case "actual-success-overrides-absence":
				h.markModelUsable(acct, acct.AuthSnapshot(), eligibilityModel)
			}
			present, complete := upstream.AccountModelKnowledgeForAuth(acct.UID, eligibilityModel, acct.AuthSnapshot())
			if present || complete != (mode == "actual-success-overrides-absence") {
				t.Fatalf("fixture has incorrect absence authority: present=%t complete=%t", present, complete)
			}
			if mode == "retained-after-failure" {
				source := upstream.AccountModelDiagnosticsForAuth(acct.UID, acct.AuthSnapshot()).Sources[2]
				if source.Retained == 0 || source.LastSuccessAt == nil || source.Error == nil {
					t.Fatalf("fixture did not retain previously successful stale source evidence: %+v", source)
				}
			}
			calls := f.calls.Load()
			ev := assertEligibilityChat(t, h, http.StatusOK, "", "ok", audit.CauseNone)
			if ev.Attempts != 1 || ev.Account == nil || ev.ModelCanonical != eligibilityModel || ev.FirstDataMS == nil {
				t.Errorf("eligible account was not actually attempted with the unchanged ID: %+v", ev)
			}
			if f.chatCalls.Load() != 1 || f.calls.Load() != calls+1 {
				t.Errorf("chat did not perform exactly one cached-selection inference: before=%d after=%d chat=%d", calls, f.calls.Load(), f.chatCalls.Load())
			}
			assertLifecycleHealthy(t, p)
		})
	}
}

func TestChatPartialCatalogPreservesActualProviderDenial(t *testing.T) {
	f, backend := newCatalogFixture(t)
	f.statuses[upstream.EpBenefitConfig] = http.StatusServiceUnavailable
	h, p := catalogHandler(t, 1, f, backend)
	h.cfg.Audit = newAuditSink(t)
	refreshCatalog(t, h)
	if state := waitCatalog(t, h); state.State != "partial" {
		t.Fatalf("fixture must have nonauthoritative absence: %+v", state)
	}
	acct := p.Accounts()[0]
	h.markModelUnusable(acct, acct.AuthSnapshot(), eligibilityModel, "not registered")
	calls := f.calls.Load()
	ev := assertEligibilityChat(t, h, http.StatusNotFound, "model_not_available", "model_unavailable", audit.CauseCapability)
	if ev.Attempts != 0 || f.calls.Load() != calls {
		t.Errorf("partial discovery bypassed actual provider denial: audit=%+v before=%d after=%d", ev, calls, f.calls.Load())
	}
	assertLifecycleHealthy(t, p)
}
