package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

type catalogFixture struct {
	mu            sync.Mutex
	gate          <-chan struct{}
	statuses      map[string]int
	model         string
	calls         atomic.Int32
	active        atomic.Int32
	peak          atomic.Int32
	chatCalls     atomic.Int32
	denyChat      atomic.Bool
	benefitHeader atomic.Bool
}

func newCatalogFixture(t *testing.T) (*catalogFixture, *httptest.Server) {
	t.Helper()
	f := &catalogFixture{statuses: map[string]int{}, model: "deepseek-v4.1-flash"}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		active := f.active.Add(1)
		defer f.active.Add(-1)
		for {
			prev := f.peak.Load()
			if active <= prev || f.peak.CompareAndSwap(prev, active) {
				break
			}
		}
		f.mu.Lock()
		gate, status, model := f.gate, f.statuses[r.URL.Path], f.model
		f.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		if status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, "upstream-secret-body")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case upstream.EpAgentList:
			_, _ = io.WriteString(w, `{"agents":[{"agent_id":"primary","is_primary_agent":true}]}`)
		case upstream.EpAgentDetail:
			_, _ = io.WriteString(w, `{"gpts":{"models":[{"model_name":"GLM-5.2"}]}}`)
		case upstream.EpModelBuiltin:
			_, _ = io.WriteString(w, `{"builtinModels":[]}`)
		case upstream.EpBenefitConfig:
			_, _ = fmt.Fprintf(w, `{"error_code":"0000","result":{"models":[{"model_id":%q}]}}`, model)
		case upstream.EpBenefitClaim:
			_, _ = io.WriteString(w, `{"error_code":"0000"}`)
		case "/chat":
			f.chatCalls.Add(1)
			f.benefitHeader.Store(r.Header.Get(upstream.HeaderMaasType) == upstream.MaasBenefit)
			if f.denyChat.Load() {
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"error":"InferHub.002002009.404 model is not registered"}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, lifecycleSuccess)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(s.Close)
	return f, s
}

func catalogHandler(t *testing.T, count int, f *catalogFixture, backend *httptest.Server) (*Handler, *pool.Pool) {
	t.Helper()
	var credentials []*auth.Auth
	for i := 0; i < count; i++ {
		credentials = append(credentials, auth.New(fmt.Sprintf("%s-acct-%02d", t.Name(), i), "test", "domain", "test-token", "test-ak", "test-sk", time.Now().Add(24*time.Hour).Format(time.RFC3339), "", ""))
	}
	p, err := pool.New(credentials, pool.Config{MaxConcurrent: 1}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range p.Accounts() {
		a.Client = upstream.NewWithChatEndpoint(time.Second, backend.URL+"/chat")
		noteTraffic(a.UID)
	}
	c := upstream.NewWithModelEndpoints(2*time.Second, backend.URL, backend.URL)
	c.SetBenefitAutoClaim(false)
	h := NewHandler(Config{Pool: p, Upstream: c, APIKey: "fixture-key", QueueMaxAttempts: 1})
	ctx, cancel := context.WithCancel(context.Background())
	h.modelCatalog.serviceCtx = ctx
	t.Cleanup(func() {
		cancel()
		deadline := time.Now().Add(2 * time.Second)
		for h.modelRefreshSnapshot().State == "running" && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	})
	return h, p
}

func catalogRequest(h *Handler, method, path, body, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func refreshCatalog(t *testing.T, h *Handler) modelRefreshStatus {
	t.Helper()
	w := catalogRequest(h, http.MethodPost, "/admin/api/models/refresh", "{}", "fixture-key")
	if w.Code != 202 {
		t.Fatalf("refresh status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Refresh modelRefreshStatus `json:"refresh"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Refresh
}

func waitCatalog(t *testing.T, h *Handler) modelRefreshStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := h.modelRefreshSnapshot()
		if s.State != "running" {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("bounded catalog job did not finish")
	return modelRefreshStatus{}
}

func TestCatalogRoutesAuthenticatedCachedAndStrictBody(t *testing.T) {
	f, backend := newCatalogFixture(t)
	h, _ := catalogHandler(t, 1, f, backend)
	for _, path := range []string{"/admin/api/models", "/admin/api/models/refresh"} {
		method := http.MethodGet
		body := ""
		if strings.HasSuffix(path, "refresh") {
			method = http.MethodPost
			body = "{}"
		}
		for _, key := range []string{"", "wrong"} {
			if w := catalogRequest(h, method, path, body, key); w.Code != 401 {
				t.Fatalf("unauthorized %s leaked diagnostics: %d", path, w.Code)
			}
		}
	}
	w := catalogRequest(h, http.MethodGet, "/admin/api/models", "", "fixture-key")
	if w.Code != 200 || f.calls.Load() != 0 {
		t.Fatalf("cached GET performed upstream I/O: %d calls=%d", w.Code, f.calls.Load())
	}
	for _, body := range []string{"", "null", "[]", "{", `{"model":"x"}`, "{} {}", `{"x":"` + strings.Repeat("a", 2000) + `"}`} {
		if w := catalogRequest(h, http.MethodPost, "/admin/api/models/refresh", body, "fixture-key"); w.Code != 400 {
			t.Fatalf("invalid body accepted: %q status=%d", body, w.Code)
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("invalid/unauthenticated requests triggered upstream")
	}
	refreshCatalog(t, h)
	s := waitCatalog(t, h)
	if s.State != "completed" || s.Total != 1 || s.Completed != 1 || s.Failed != 0 {
		t.Fatalf("wrong job result: %+v", s)
	}
	w = catalogRequest(h, http.MethodGet, "/admin/api/models", "", "fixture-key")
	var page catalogPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Accounts) != 1 || page.Accounts[0].Sources[2].Status != "ok" || page.Accounts[0].Claim.Enabled || page.Accounts[0].Claim.Attempted {
		t.Fatalf("wrong account source/claim metadata: %+v", page.Accounts)
	}
	found := false
	for _, m := range page.Models {
		if m.ID == f.model {
			found = m.Origin == "discovered" && m.CatalogAccounts == 1 && m.BenefitAccounts == 1
		}
	}
	if !found {
		t.Fatalf("exact dynamic model omitted: %+v", page.Models)
	}
	calls := f.calls.Load()
	second := refreshCatalog(t, h)
	if second.ID != s.ID || f.calls.Load() != calls {
		t.Fatal("15-second minimum did not coalesce early refresh")
	}
	for i := 0; i < 8; i++ {
		catalogRequest(h, http.MethodGet, "/admin/api/models", "", "fixture-key")
	}
	if f.calls.Load() != calls {
		t.Fatal("snapshot polling performed provider work")
	}
}

func TestOverviewCatalogReadsDoNotLaunchDiscoveryOrProbes(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached-%t", cached), func(t *testing.T) {
			f, backend := newCatalogFixture(t)
			h, p := catalogHandler(t, 1, f, backend)
			h.cfg.Upstream.SetBenefitAutoClaim(true)
			acct := p.Accounts()[0]
			if cached {
				upstream.SetAccountModels(acct.UID, []upstream.ModelInfo{{ID: f.model, Benefit: true}})
				// Make the one cached model eligible for a real probe. A read-only
				// admin snapshot must not initiate it, even on an idle account.
				availability.Lock()
				delete(availability.lastTraffic, acct.UID)
				availability.Unlock()
			}
			for i := 0; i < 3; i++ {
				w := catalogRequest(h, http.MethodGet, "/admin/api/overview", "", "fixture-key")
				if w.Code != http.StatusOK {
					t.Fatalf("overview status=%d body=%s", w.Code, w.Body.String())
				}
				var overview struct {
					Models []struct{ ID string } `json:"models"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &overview); err != nil {
					t.Fatal(err)
				}
				if cached {
					found := false
					for _, model := range overview.Models {
						found = found || model.ID == f.model
					}
					if !found {
						t.Fatal("cached overview omitted its discovered model")
					}
				}
			}
			if state := h.modelRefreshSnapshot().State; state != "idle" {
				t.Fatalf("overview started a metadata/claim job: %s", state)
			}
			time.Sleep(20 * time.Millisecond)
			if f.calls.Load() != 0 || f.chatCalls.Load() != 0 {
				t.Fatalf("overview performed metadata, claim or chat I/O: calls=%d chat=%d", f.calls.Load(), f.chatCalls.Load())
			}
		})
	}
}

func TestOpenAIModelsStillSchedulesDiscoveryAndIdleProbe(t *testing.T) {
	f, backend := newCatalogFixture(t)
	h, p := catalogHandler(t, 1, f, backend)
	h.cfg.Upstream.SetBenefitAutoClaim(true)
	w := catalogRequest(h, http.MethodGet, "/v1/models", "", "fixture-key")
	if w.Code != http.StatusOK || h.modelRefreshSnapshot().ID == "" {
		t.Fatalf("OpenAI listing did not schedule its background refresh: status=%d refresh=%+v", w.Code, h.modelRefreshSnapshot())
	}
	if result := waitCatalog(t, h); result.State != "completed" || f.calls.Load() != 5 {
		t.Fatalf("OpenAI listing lost metadata/claim discovery: refresh=%+v calls=%d", result, f.calls.Load())
	}
	acct := p.Accounts()[0]
	markUsable(acct.UID, "GLM-5.2") // Leave exactly one unknown model; no probe-gap sleeper.
	availability.Lock()
	delete(availability.lastTraffic, acct.UID)
	availability.Unlock()
	w = catalogRequest(h, http.MethodGet, "/v1/models", "", "fixture-key")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), f.model) {
		t.Fatalf("cached OpenAI listing omitted the discovered model: status=%d body=%s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, _ := modelAvailability(acct.UID, f.model)
		if state == availUsable && !p.Busy(acct.Name) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if state, _ := modelAvailability(acct.UID, f.model); state != availUsable || f.chatCalls.Load() != 1 || !f.benefitHeader.Load() || p.Busy(acct.Name) {
		t.Fatalf("OpenAI listing lost its bounded idle probe: state=%d calls=%d benefit=%t busy=%t", state, f.chatCalls.Load(), f.benefitHeader.Load(), p.Busy(acct.Name))
	}
}

func TestCatalogColdPartialMissDoesNotPreventRealChat(t *testing.T) {
	f, backend := newCatalogFixture(t)
	f.statuses[upstream.EpBenefitConfig] = http.StatusServiceUnavailable
	h, _ := catalogHandler(t, 1, f, backend)
	refreshCatalog(t, h)
	if result := waitCatalog(t, h); result.State != "partial" {
		t.Fatalf("fixture did not produce a partial cold discovery: %+v", result)
	}
	if candidates := h.chatCandidates(nil, f.model, ""); len(candidates) != 1 || candidates[0].Priority != 1 {
		t.Fatalf("missing model in partial discovery must remain unknown: %+v", candidates)
	}
	if candidates := h.chatCandidates(nil, "GLM-5.2", ""); len(candidates) != 1 || candidates[0].Priority != 0 {
		t.Fatalf("confirmed partial catalog member lost its selection priority: %+v", candidates)
	}
	calls := f.calls.Load()
	w := catalogRequest(h, http.MethodPost, "/v1/chat/completions", `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"fixture"}]}`, "fixture-key")
	if w.Code != http.StatusOK || f.chatCalls.Load() != 1 || f.calls.Load() != calls+1 {
		t.Fatalf("partial catalog miss prevented the real chat or synchronously discovered: status=%d chat=%d calls=%d before=%d body=%s", w.Code, f.chatCalls.Load(), f.calls.Load(), calls, w.Body.String())
	}
	if f.benefitHeader.Load() {
		t.Fatal("missing source metadata invented a benefit route")
	}
}

func TestCatalogConcurrentRefreshCoalescesWorkersAndKeepsHealthResponsive(t *testing.T) {
	f, backend := newCatalogFixture(t)
	h, _ := catalogHandler(t, 5, f, backend)
	gate := make(chan struct{})
	f.mu.Lock()
	f.gate = gate
	f.mu.Unlock()
	first := refreshCatalog(t, h)
	var wg sync.WaitGroup
	results := make(chan string, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := catalogRequest(h, http.MethodPost, "/admin/api/models/refresh", "{}", "fixture-key")
			var b struct {
				Refresh modelRefreshStatus `json:"refresh"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &b)
			results <- b.Refresh.ID
		}()
	}
	wg.Wait()
	close(results)
	for id := range results {
		if id != first.ID {
			t.Fatal("concurrent callers created different discovery flights")
		}
	}
	deadline := time.Now().Add(time.Second)
	for f.active.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.peak.Load() > 2 || f.active.Load() != 2 {
		t.Fatalf("worker bound violated: active=%d peak=%d", f.active.Load(), f.peak.Load())
	}
	start := time.Now()
	w := catalogRequest(h, http.MethodGet, "/healthz", "", "")
	if w.Code != 200 || time.Since(start) > 100*time.Millisecond {
		t.Fatal("stalled refresh blocked health")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodPost, "/admin/api/models/refresh", strings.NewReader("{}")).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer fixture-key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	cancel()
	if h.modelRefreshSnapshot().State != "running" {
		t.Fatal("caller cancellation canceled shared job")
	}
	close(gate)
	s := waitCatalog(t, h)
	if s.ID != first.ID || s.State != "completed" || s.Completed != 5 || f.peak.Load() > 2 {
		t.Fatalf("shared job wrong: %+v peak=%d", s, f.peak.Load())
	}
}

func TestCatalogBudgetAndServiceCancellationStopQueuedWork(t *testing.T) {
	for _, serviceCancel := range []bool{false, true} {
		t.Run(fmt.Sprint(serviceCancel), func(t *testing.T) {
			f, backend := newCatalogFixture(t)
			h, _ := catalogHandler(t, 6, f, backend)
			gate := make(chan struct{})
			f.mu.Lock()
			f.gate = gate
			f.mu.Unlock()
			h.modelCatalog.jobTimeout = 150 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h.modelCatalog.serviceCtx = ctx
			refreshCatalog(t, h)
			deadline := time.Now().Add(time.Second)
			for f.active.Load() < 2 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if serviceCancel {
				cancel()
			}
			s := waitCatalog(t, h)
			if s.State != "failed" || s.Completed+s.Skipped != 6 || s.Skipped < 4 || s.Failed < 1 {
				t.Fatalf("deadline unattempted work reported as success: %+v", s)
			}
			if f.calls.Load() > 2 || f.peak.Load() > 2 {
				t.Fatalf("deadline leaked queued work: calls=%d peak=%d", f.calls.Load(), f.peak.Load())
			}
		})
	}
}

func TestCatalogRefreshRecoversOldNegativeThroughRealChat(t *testing.T) {
	f, backend := newCatalogFixture(t)
	h, p := catalogHandler(t, 1, f, backend)
	acct := p.Accounts()[0]
	f.denyChat.Store(true)
	chat := func() *httptest.ResponseRecorder {
		return catalogRequest(h, http.MethodPost, "/v1/chat/completions", `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"synthetic prompt"}]}`, "fixture-key")
	}
	if w := chat(); w.Code != 404 {
		t.Fatalf("initial provider denial status=%d body=%s", w.Code, w.Body.String())
	}
	if w := chat(); w.Code != 404 || f.chatCalls.Load() != 1 {
		t.Fatal("negative cache did not prevent repeated provider denial")
	}
	markUnusable(acct.UID, "unrelated-model", "not registered")
	markUsable(acct.UID, "known-working-model")
	f.denyChat.Store(false)
	refreshCatalog(t, h)
	s := waitCatalog(t, h)
	if s.NegativeResetCount != 1 {
		availability.Lock()
		entry := availability.byKey[availKey(acct.UID, "deepseek-v4.1-flash")]
		availability.Unlock()
		t.Fatalf("scoped negative reset count wrong: %+v entry=%+v sources=%+v", s, entry, upstream.AccountModelDiagnostics(acct.UID))
	}
	if st, _ := modelAvailability(acct.UID, "deepseek-v4.1-flash"); st != availUnknown {
		t.Fatal("discovery marked entitlement usable instead of unknown")
	}
	if st, _ := modelAvailability(acct.UID, "unrelated-model"); st != availUnusable {
		t.Fatal("unrelated negative was cleared")
	}
	if st, _ := modelAvailability(acct.UID, "known-working-model"); st != availUsable {
		t.Fatal("positive history was altered")
	}
	if w := chat(); w.Code != 200 || f.chatCalls.Load() != 2 || !f.benefitHeader.Load() {
		t.Fatalf("real chat failed after rediscovery: status=%d body=%s hits=%d benefit=%v", w.Code, w.Body.String(), f.chatCalls.Load(), f.benefitHeader.Load())
	}
}

func TestCatalogRefreshPreservesConcurrentDenialRetainedRouteAndReplacement(t *testing.T) {
	for _, mode := range []string{"new-denial", "stale-benefit", "same-uid-relogin", "removed-readded"} {
		t.Run(mode, func(t *testing.T) {
			f, backend := newCatalogFixture(t)
			h, p := catalogHandler(t, 1, f, backend)
			acct := p.Accounts()[0]
			oldAuth := acct.AuthSnapshot()
			if mode == "stale-benefit" {
				refreshCatalog(t, h)
				waitCatalog(t, h)
				h.modelCatalog.mu.Lock()
				h.modelCatalog.refresh.RetryAt = nil
				h.modelCatalog.mu.Unlock()
				f.mu.Lock()
				f.statuses[upstream.EpBenefitConfig] = 503
				f.mu.Unlock()
			}
			markUnusable(acct.UID, f.model, "not registered")
			gate := make(chan struct{})
			f.mu.Lock()
			f.gate = gate
			f.mu.Unlock()
			refreshCatalog(t, h)
			deadline := time.Now().Add(time.Second)
			for f.active.Load() < 1 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			switch mode {
			case "new-denial":
				markUnusable(acct.UID, f.model, "new denial")
			case "same-uid-relogin", "removed-readded":
				if mode == "removed-readded" {
					p.SyncToDir(nil)
				}
				p.AddAccount(auth.New(acct.UID, "replacement", "domain", "new-token", "new-ak", "new-sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", ""))
			}
			close(gate)
			s := waitCatalog(t, h)
			if s.NegativeResetCount != 0 {
				t.Fatalf("unsafe negative reset: %+v", s)
			}
			if st, _ := modelAvailability(acct.UID, f.model); st != availUnusable {
				t.Fatalf("new/concurrently replaced or retained-only negative cleared: %v", st)
			}
			if mode == "same-uid-relogin" || mode == "removed-readded" {
				if s.Completed != 0 || s.Skipped != 1 || p.Get(acct.Name).AuthSnapshot() == oldAuth {
					t.Fatalf("old-login result published over replacement: %+v", s)
				}
				if _, known := upstream.AccountModels(acct.UID); known {
					t.Fatal("old-login catalog published to replacement")
				}
			}
		})
	}
}

func TestCatalogAllFilteredModelsRemainEmptyAndDiagnosticsExplain(t *testing.T) {
	f, backend := newCatalogFixture(t)
	h, p := catalogHandler(t, 1, f, backend)
	acct := p.Accounts()[0]
	upstream.SetAccountModels(acct.UID, []upstream.ModelInfo{{ID: f.model, Benefit: true}})
	for _, m := range modelEntries([]upstream.ModelInfo{{ID: f.model, Benefit: true}}) {
		markUnusable(acct.UID, m["id"].(string), "模型未在当前账号注册")
	}
	w := catalogRequest(h, http.MethodGet, "/v1/models", "", "fixture-key")
	var body struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 0 || !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("all-excluded list restored original models: %s", w.Body.String())
	}
	page := h.catalogSnapshot()
	if len(page.Exclusions) == 0 || len(page.Models) == 0 {
		t.Fatal("diagnostic catalog hid excluded model information")
	}
	for _, e := range page.Exclusions {
		if e.Reason != "not_registered" {
			t.Fatalf("unexplained exclusion: %+v", e)
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("known catalog listing fetched upstream")
	}
}

func TestCatalogSnapshotLimitsAndTruthfulTruncation(t *testing.T) {
	f, backend := newCatalogFixture(t)
	h, p := catalogHandler(t, 10, f, backend)
	for i, acct := range p.Accounts() {
		infos := make([]upstream.ModelInfo, 256)
		for j := range infos {
			infos[j] = upstream.ModelInfo{ID: fmt.Sprintf("limit-%02d-%03d", i, j)}
		}
		upstream.SetAccountModels(acct.UID, infos)
	}
	for i := 0; i < 600; i++ {
		markUnusable(p.Accounts()[0].UID, fmt.Sprintf("excluded-%03d", i), "not registered")
	}
	page := h.catalogSnapshot()
	if len(page.Models) != catalogModelLimit || page.Truncated.Models != 10*256+len(staticModels)-catalogModelLimit {
		t.Fatalf("model union not bounded with distinct truncation count: len=%d truncated=%+v", len(page.Models), page.Truncated)
	}
	if len(page.Exclusions) != catalogExclusionLimit || page.Truncated.Exclusions != 600-catalogExclusionLimit {
		t.Fatalf("exclusions not bounded: len=%d truncated=%+v", len(page.Exclusions), page.Truncated)
	}
	if f.calls.Load() != 0 {
		t.Fatal("bounded snapshot performed upstream I/O")
	}

	h2, _ := catalogHandler(t, catalogAccountLimit+3, f, backend)
	page = h2.catalogSnapshot()
	if len(page.Accounts) != catalogAccountLimit || page.Truncated.Accounts != 3 {
		t.Fatalf("account snapshot limit wrong: len=%d truncated=%+v", len(page.Accounts), page.Truncated)
	}
}

func TestCatalogReplacementImmediatelyHidesOldOwnedSnapshot(t *testing.T) {
	f, backend := newCatalogFixture(t)
	h, p := catalogHandler(t, 1, f, backend)
	acct := p.Accounts()[0]
	refreshCatalog(t, h)
	waitCatalog(t, h)
	p.AddAccount(auth.New(acct.UID, "replacement", "domain", "new-token", "new-ak", "new-sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", ""))
	page := h.catalogSnapshot()
	if len(page.Accounts) != 1 || page.Accounts[0].Sources[2].Status != "unknown" || page.Accounts[0].LastSuccessAt != nil {
		t.Fatalf("old-login source metadata leaked into replacement: %+v", page.Accounts)
	}
	for _, m := range page.Models {
		if m.ID == f.model {
			t.Fatal("old-login model contribution leaked into replacement")
		}
	}
	if len(h.cachedDynamicModels()) != 0 {
		t.Fatal("cached public catalog used old-login contribution")
	}
}

func TestCatalogReplacementChatIgnoresOldCatalogAndBenefitRoute(t *testing.T) {
	f, backend := newCatalogFixture(t)
	f.model = "old-login-benefit-only"
	h, p := catalogHandler(t, 1, f, backend)
	acct := p.Accounts()[0]
	refreshCatalog(t, h)
	waitCatalog(t, h)
	chat := func(model string) *httptest.ResponseRecorder {
		return catalogRequest(h, http.MethodPost, "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"synthetic"}]}`, model), "fixture-key")
	}
	if w := chat(f.model); w.Code != 200 || !f.benefitHeader.Load() {
		t.Fatalf("original account fixture did not use discovered benefit route: %d %s", w.Code, w.Body.String())
	}
	p.AddAccount(auth.New(acct.UID, "replacement", "domain", "new-token", "new-ak", "new-sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", ""))
	if w := chat("new-login-model-absent-from-old-catalog"); w.Code != 200 {
		t.Fatalf("old-login catalog excluded replacement chat: %d %s", w.Code, w.Body.String())
	}
	if w := chat(f.model); w.Code != 200 || f.benefitHeader.Load() {
		t.Fatalf("old-login benefit route leaked to new credentials: %d %s benefit=%v", w.Code, w.Body.String(), f.benefitHeader.Load())
	}
}
