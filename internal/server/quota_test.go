package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

func quotaServerFixture(t *testing.T, collect bool) (*Handler, *pool.Pool, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != upstream.EpCreditStatistics || r.Method != http.MethodGet {
			t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":100,"package_credit_used":75,"package_credit_remain":25}]}`))
	}))
	t.Cleanup(provider.Close)
	credential := auth.New("account", "account", "", "fixture-token", "fixture-ak", "fixture-sk",
		time.Now().Add(time.Hour).Format(time.RFC3339), "", "")
	p, err := pool.New([]*auth.Auth{credential}, pool.Config{}, "")
	if err != nil {
		t.Fatal(err)
	}
	p.Get("account").Client = upstream.NewWithModelEndpoints(time.Second, provider.URL, "")
	h := NewHandler(Config{Pool: p, APIKey: "fixture-key"})
	if collect {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		h.StartQuotaCollection(ctx)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && !p.QuotaSnapshot().Complete {
			time.Sleep(time.Millisecond)
		}
		if !p.QuotaSnapshot().Complete {
			t.Fatalf("fixture collection did not finish: %+v", p.QuotaSnapshot())
		}
	}
	return h, p, calls
}

func quotaRequest(h *Handler, method, target, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestQuotaHTTPRoutesAndNewAPIConversion(t *testing.T) {
	h, _, calls := quotaServerFixture(t, true)
	for _, path := range []string{"/v1/quota", "/admin/api/quota/refresh"} {
		method := http.MethodGet
		if strings.HasPrefix(path, "/admin") {
			method = http.MethodPost
		}
		if w := quotaRequest(h, method, path, ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
	before := calls.Load()
	w := quotaRequest(h, http.MethodGet, "/v1/quota", "fixture-key")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("quota: status=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["object"] != "codearts_credit_summary" || raw["unit"] != "credits" || raw["remaining"] != float64(25) || raw["complete"] != true || raw["overlap_possible"] != true {
		t.Fatalf("incorrect raw credits: %+v", raw)
	}
	if strings.Contains(w.Body.String(), "fixture-token") || strings.Contains(w.Body.String(), "fixture-sk") || strings.Contains(w.Body.String(), `"account"`) {
		t.Fatalf("external quota leaked account metadata: %s", w.Body.String())
	}
	overview := quotaRequest(h, http.MethodGet, "/admin/api/overview", "fixture-key")
	var overviewBody struct {
		Quota pool.QuotaSnapshot `json:"quota"`
		Stats struct {
			Credits *float64 `json:"credits"`
		} `json:"stats"`
	}
	if overview.Code != http.StatusOK || json.Unmarshal(overview.Body.Bytes(), &overviewBody) != nil ||
		!overviewBody.Quota.Complete || overviewBody.Quota.Total == nil || *overviewBody.Quota.Total != 100 ||
		overviewBody.Quota.Remaining == nil || *overviewBody.Quota.Remaining != 25 ||
		overviewBody.Stats.Credits == nil || *overviewBody.Stats.Credits != 25 {
		t.Fatalf("overview did not expose measured quota: status=%d body=%s", overview.Code, overview.Body.String())
	}
	for _, target := range []string{
		"/v1/quota?credits_per_usd=", "/v1/quota?credits_per_usd=0", "/v1/quota?credits_per_usd=-1",
		"/v1/quota?credits_per_usd=NaN", "/v1/quota?credits_per_usd=Inf", "/v1/quota?credits_per_usd=1&credits_per_usd=2",
	} {
		if response := quotaRequest(h, http.MethodGet, target, "fixture-key"); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid rate %s: status=%d body=%s", target, response.Code, response.Body.String())
		}
	}
	w = quotaRequest(h, http.MethodGet, "/v1/quota?credits_per_usd=100", "fixture-key")
	var converted map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &converted); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || converted["object"] != "credit_summary" || converted["total_available"] != 0.25 || converted["unit"] != "USD" || converted["available_credits"] != float64(25) {
		t.Fatalf("incorrect New API balance: status=%d body=%s", w.Code, w.Body.String())
	}
	w = quotaRequest(h, http.MethodPost, "/admin/api/quota/refresh", "fixture-key")
	if w.Code != http.StatusAccepted || calls.Load() != before {
		t.Fatalf("refresh throttle failed: status=%d calls=%d before=%d", w.Code, calls.Load(), before)
	}
	if calls.Load() != before {
		t.Fatal("cache-only quota GET performed provider requests")
	}
}

func TestQuotaHTTPIncompleteNeverReportsBalance(t *testing.T) {
	h, _, calls := quotaServerFixture(t, false)
	raw := quotaRequest(h, http.MethodGet, "/v1/quota", "fixture-key")
	var body map[string]any
	if err := json.Unmarshal(raw.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if raw.Code != http.StatusOK || body["complete"] != false || body["remaining"] != nil {
		t.Fatalf("unknown quota: status=%d body=%s", raw.Code, raw.Body.String())
	}
	converted := quotaRequest(h, http.MethodGet, "/v1/quota?credits_per_usd=100", "fixture-key")
	if converted.Code != http.StatusServiceUnavailable || strings.Contains(converted.Body.String(), `"total_available"`) || calls.Load() != 0 {
		t.Fatalf("incomplete balance exposed: status=%d body=%s calls=%d", converted.Code, converted.Body.String(), calls.Load())
	}
}

func TestQuotaHTTPUnlimitedAndPartial(t *testing.T) {
	for _, test := range []struct {
		name       string
		secondBody string
		status     int
		complete   bool
	}{
		{name: "unlimited and finite zero", secondBody: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":2,"package_credit_used":2,"package_credit_remain":0}]}`, status: http.StatusOK, complete: true},
		{name: "unlimited and unavailable", secondBody: `{"metrics":[]}`, status: http.StatusOK},
		{name: "unlimited and failed", status: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != upstream.EpCreditStatistics {
					t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("X-Security-Token") == "token-a" {
					_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-1,"package_credit_used":4}]}`))
					return
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.secondBody))
			}))
			defer provider.Close()
			expiry := time.Now().Add(time.Hour).Format(time.RFC3339)
			p, err := pool.New([]*auth.Auth{
				auth.New("a", "a", "", "token-a", "fixture-ak", "fixture-sk", expiry, "", ""),
				auth.New("b", "b", "", "token-b", "fixture-ak", "fixture-sk", expiry, "", ""),
			}, pool.Config{}, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, account := range p.Accounts() {
				account.Client = upstream.NewWithModelEndpoints(time.Second, provider.URL, "")
			}
			h := NewHandler(Config{Pool: p, APIKey: "fixture-key"})
			p.RequestQuotaRefresh()
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				snapshot := p.QuotaSnapshot()
				if !snapshot.Refresh.Running && snapshot.Refresh.FinishedAt != nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if snapshot := p.QuotaSnapshot(); snapshot.Refresh.Running || snapshot.Refresh.FinishedAt == nil {
				t.Fatalf("quota fixture did not finish: %+v", snapshot)
			}
			before := calls.Load()
			response := quotaRequest(h, http.MethodGet, "/v1/quota", "fixture-key")
			var raw map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || raw["complete"] != test.complete || raw["unlimited"] != test.complete || raw["total"] != nil || raw["remaining"] != nil || raw["known_remaining"] != nil {
				t.Fatalf("incorrect raw snapshot: status=%d body=%s", response.Code, response.Body.String())
			}
			if test.complete && raw["used"] != float64(6) || !test.complete && raw["used"] != nil {
				t.Fatalf("incorrect used credits: %s", response.Body.String())
			}
			overview := quotaRequest(h, http.MethodGet, "/admin/api/overview", "fixture-key")
			var result struct {
				Quota pool.QuotaSnapshot `json:"quota"`
			}
			if overview.Code != http.StatusOK || json.Unmarshal(overview.Body.Bytes(), &result) != nil || result.Quota.Complete != test.complete || result.Quota.Unlimited != test.complete {
				t.Fatalf("overview did not preserve quota state: status=%d body=%s", overview.Code, overview.Body.String())
			}
			converted := quotaRequest(h, http.MethodGet, "/v1/quota?credits_per_usd=100", "fixture-key")
			if converted.Code != http.StatusServiceUnavailable || !strings.Contains(converted.Body.String(), "quota_unavailable") || strings.Contains(converted.Body.String(), "total_available") {
				t.Fatalf("invalid converted balance: status=%d body=%s", converted.Code, converted.Body.String())
			}
			if calls.Load() != before {
				t.Fatal("cache-only quota routes performed provider requests")
			}
		})
	}
}
