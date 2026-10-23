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

type catalogAuthFixture struct {
	h              *Handler
	p              *pool.Pool
	acct           *pool.Account
	owner          *auth.Auth
	refreshCalls   atomic.Int32
	sourceCalls    atomic.Int32
	rejectedSource atomic.Int32
	refreshStarted chan struct{}
	release        func()
	cancelService  context.CancelFunc
}

func newCatalogAuthFixture(t *testing.T, refreshStatus int) *catalogAuthFixture {
	t.Helper()
	f := &catalogAuthFixture{refreshStarted: make(chan struct{})}
	gate := make(chan struct{})
	var releaseOnce, startedOnce sync.Once
	f.release = func() { releaseOnce.Do(func() { close(gate) }) }
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.refreshCalls.Add(1)
		startedOnce.Do(func() { close(f.refreshStarted) })
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if refreshStatus != http.StatusOK {
			w.WriteHeader(refreshStatus)
			_, _ = io.WriteString(w, `{"error":"invalid_grant","error_code":"IAM.ExpiredRefreshToken","error_msg":"fixture refresh rejection"}`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"refresh_token":"new-refresh","credentials":{"security_token":"new-token","access_key_id":"new-ak","secret_access_key":"new-sk","expiration":%q}}`, time.Now().Add(3*time.Hour).Format(time.RFC3339))
	}))
	t.Cleanup(sts.Close)
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.sourceCalls.Add(1)
		if r.Header.Get("X-Security-Token") != "new-token" {
			f.rejectedSource.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"fixture expired STS"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case upstream.EpAgentList:
			_, _ = io.WriteString(w, `{"agents":[]}`)
		case upstream.EpModelBuiltin:
			_, _ = io.WriteString(w, `{"builtinModels":[{"model_id":"glm-5.3","model_name":"GLM-5.3"}]}`)
		case upstream.EpBenefitGatewayConfig:
			_, _ = io.WriteString(w, `{"enabled":false}`)
		default:
			t.Errorf("unexpected model endpoint %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(models.Close)
	jwk, err := upstream.NewDPoPPrivateJWK()
	if err != nil {
		t.Fatal(err)
	}
	f.owner = auth.New(strings.ReplaceAll(t.Name(), "/", "-"), "fixture", "domain", "old-token", "old-ak", "old-sk", time.Now().Add(-time.Hour).Format(time.RFC3339), "old-refresh", "verifier", map[string]string(jwk))
	if err := auth.SaveNew(t.TempDir(), f.owner); err != nil {
		t.Fatal(err)
	}
	login := upstream.DefaultLoginConfig()
	login.STSHost = sts.URL
	f.p, err = pool.New([]*auth.Auth{f.owner}, pool.Config{MaxConcurrent: 1, UpstreamTimeout: time.Second, LoginConfig: login}, "")
	if err != nil {
		t.Fatal(err)
	}
	f.acct = f.p.Accounts()[0]
	client := upstream.NewWithModelEndpoints(time.Second, models.URL, models.URL)
	f.h = NewHandler(Config{Pool: f.p, Upstream: client, APIKey: "fixture-key"})
	ctx, cancel := context.WithCancel(context.Background())
	f.cancelService = cancel
	f.h.modelCatalog.serviceCtx = ctx
	t.Cleanup(func() {
		cancel()
		f.release()
		waitCatalog(t, f.h)
	})
	return f
}

func TestCatalogRefreshValidatesExpiredSTSBeforeSourcesAndSharesRefresh(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprint(force), func(t *testing.T) {
			f := newCatalogAuthFixture(t, http.StatusOK)
			// Admin snapshots with expired STS still perform no validation/I/O.
			for _, path := range []string{"/admin/api/models", "/admin/api/overview"} {
				if rec := catalogRequest(f.h, http.MethodGet, path, "", "fixture-key"); rec.Code != http.StatusOK {
					t.Fatalf("cached snapshot failed: %d", rec.Code)
				}
			}
			if f.sourceCalls.Load() != 0 || f.refreshCalls.Load() != 0 {
				t.Fatal("cached GET refreshed or discovered with expired credentials")
			}
			if !force {
				// Reproduce old-STS source failure/backoff left by the former
				// worker. A newly refreshed generation must get a real attempt.
				d := f.h.cfg.Upstream.DiscoverModels(context.Background(), f.owner, false)
				if d.FailedSources() != 3 || f.rejectedSource.Load() != 3 {
					t.Fatal("fixture did not establish old-STS source backoff")
				}
			}
			if force {
				refreshCatalog(t, f.h)
			} else {
				f.h.StartModelDiscovery(f.h.modelCatalog.serviceCtx)
			}
			select {
			case <-f.refreshStarted:
			case <-time.After(time.Second):
				t.Fatal("catalog worker did not validate expired STS")
			}
			validation := make(chan error, 1)
			go func() {
				valid, err := f.p.ValidateContext(context.Background(), f.acct)
				if err == nil && !valid {
					err = fmt.Errorf("shared validation returned false")
				}
				validation <- err
			}()
			f.release()
			if err := <-validation; err != nil {
				t.Fatal(err)
			}
			status := waitCatalog(t, f.h)
			if status.State != "completed" || status.Completed != 1 || status.Failed != 0 || f.refreshCalls.Load() != 1 {
				t.Fatalf("shared credential/discovery job: %+v refreshes=%d", status, f.refreshCalls.Load())
			}
			wantSources, wantRejected := int32(3), int32(0)
			if !force {
				wantSources, wantRejected = 6, 3
			}
			if f.sourceCalls.Load() != wantSources || f.rejectedSource.Load() != wantRejected {
				t.Fatalf("job used old STS or retained old backoff: sources=%d rejected=%d", f.sourceCalls.Load(), f.rejectedSource.Load())
			}
			if exact, _ := upstream.ResolveModelForAuth(f.acct.UID, "GLM-5.3", f.owner); exact != "glm-5.3" {
				t.Fatalf("freshly authenticated source identity was not published: %s", exact)
			}
		})
	}
}

func TestCatalogCredentialFailureIsNotASourceHTTPFailure(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprint(disabled), func(t *testing.T) {
			f := newCatalogAuthFixture(t, http.StatusBadRequest)
			if disabled {
				f.p.Disable(f.acct.Name, "manual disable")
			}
			f.release()
			refreshCatalog(t, f.h)
			status := waitCatalog(t, f.h)
			if status.State != "failed" || status.Completed+status.Skipped != 1 || f.sourceCalls.Load() != 0 {
				t.Fatalf("failed credentials reached sources or broke counts: %+v", status)
			}
			if disabled {
				if f.refreshCalls.Load() != 0 || status.Skipped != 1 {
					t.Fatal("disabled account was implicitly refreshed")
				}
			} else if f.refreshCalls.Load() != 1 || status.Completed != 1 || status.Failed != 1 || f.p.Enabled(f.acct.Name) {
				t.Fatalf("terminal refresh failure was concealed: %+v refresh=%d", status, f.refreshCalls.Load())
			}
			for _, source := range upstream.AccountModelDiagnosticsForAuth(f.acct.UID, f.owner).Sources {
				if source.Status != "unknown" || source.Error != nil || source.LastAttemptAt != nil {
					t.Fatalf("refresh failure fabricated model-source HTTP error: %+v", source)
				}
			}
		})
	}
}

func TestCatalogCredentialWaitCancellationDisableAndReplacement(t *testing.T) {
	for _, mode := range []string{"service-cancel", "job-timeout", "disable", "relogin", "remove-readd"} {
		t.Run(mode, func(t *testing.T) {
			f := newCatalogAuthFixture(t, http.StatusOK)
			if mode == "job-timeout" {
				f.h.modelCatalog.jobTimeout = 40 * time.Millisecond
			}
			refreshCatalog(t, f.h)
			select {
			case <-f.refreshStarted:
			case <-time.After(time.Second):
				t.Fatal("catalog refresh did not enter shared credential wait")
			}
			switch mode {
			case "service-cancel":
				f.cancelService()
			case "disable":
				f.p.Disable(f.acct.Name, "manual disable")
			case "relogin", "remove-readd":
				if mode == "remove-readd" {
					f.p.SyncToDir(nil)
				}
				f.p.AddAccount(auth.New(f.acct.UID, "replacement", "domain", "replacement-token", "replacement-ak", "replacement-sk", time.Now().Add(time.Hour).Format(time.RFC3339), "new-login-refresh", "verifier"))
			}
			if mode != "service-cancel" && mode != "job-timeout" {
				f.release()
			}
			status := waitCatalog(t, f.h)
			if status.State != "failed" || status.Completed+status.Skipped != 1 || f.sourceCalls.Load() != 0 {
				t.Fatalf("stale/canceled credential work reached sources: %+v calls=%d", status, f.sourceCalls.Load())
			}
			if mode == "service-cancel" || mode == "job-timeout" {
				// The canceled job waiter ends promptly; the one bounded shared
				// refresh still persists a provider-rotated token when it returns.
				f.release()
				waitLifecycle(t, func() bool { return f.owner.Token() == "new-token" }, "shared rotated credentials were lost after waiter cancellation")
			}
			if mode == "disable" && f.p.Enabled(f.acct.Name) {
				t.Fatal("credential refresh undid a manual disable")
			}
			if mode == "relogin" || mode == "remove-readd" {
				current := f.p.Get(f.acct.Name).AuthSnapshot()
				if current.Token() != "replacement-token" || len(upstream.AccountModelDiagnosticsForAuth(f.acct.UID, current).Models) != 0 {
					t.Fatal("old login's refresh or catalog overwrote replacement")
				}
			}
			page := catalogRequest(f.h, http.MethodGet, "/admin/api/models", "", "fixture-key")
			var decoded catalogPage
			if err := json.Unmarshal(page.Body.Bytes(), &decoded); err != nil || len(decoded.Accounts) != 1 {
				t.Fatal("credential boundary broke cached diagnostics")
			}
		})
	}
}

func TestCatalogSnapshotFiltersNegativesByCurrentWireAndLogin(t *testing.T) {
	f, backend := newCatalogFixture(t)
	f.bodies[upstream.EpModelBuiltin] = `{"builtinModels":[{"model_id":"nonseed-wire-id","model_name":"Nonseed-Wire-ID"}]}`
	h, p := catalogHandler(t, 1, f, backend)
	acct := p.Accounts()[0]
	owner := acct.AuthSnapshot()
	refreshCatalog(t, h)
	waitCatalog(t, h)
	markAvailability(acct.UID, "Nonseed-Wire-ID", owner, availUnusable, "not registered")
	if page := h.catalogSnapshot(); len(page.Exclusions) != 0 {
		t.Fatalf("wrong-wire negative survived corrected source identity: %+v", page.Exclusions)
	}
	markAvailability(acct.UID, "nonseed-wire-id", owner, availUnusable, "not registered")
	page := h.catalogSnapshot()
	if len(page.Exclusions) != 1 || page.Exclusions[0].Model != "nonseed-wire-id" {
		t.Fatalf("same-wire denial was hidden: %+v", page.Exclusions)
	}
	blocked := 0
	for _, model := range page.Models {
		if model.ID == "nonseed-wire-id" {
			blocked = model.BlockedAccounts
		}
	}
	if blocked != 1 {
		t.Fatalf("current same-wire negative did not count: %d", blocked)
	}
	replacement := auth.New(acct.UID, "replacement", "domain", "new-token", "new-ak", "new-sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", "")
	p.AddAccount(replacement)
	if page := h.catalogSnapshot(); len(page.Exclusions) != 0 {
		t.Fatalf("old-login rejection appeared on replacement: %+v", page.Exclusions)
	}
	// Unknown mixed-case IDs preserve their literal wire identity; the folded
	// map key must not erase an otherwise authoritative current-login denial.
	markAvailability(acct.UID, "MiXeD-Unlisted-ID", replacement, availUnusable, "not registered")
	if page := h.catalogSnapshot(); len(page.Exclusions) != 1 {
		t.Fatalf("literal unknown wire denial was lost: %+v", page.Exclusions)
	}
}
