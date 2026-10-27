package upstream

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchCreditQuotaSignedStatistics(t *testing.T) {
	const response = `{"package":{"is_credit_package":true},"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":1200,"package_credit_used":400,"package_credit_remain":800},{"name":"usageBasicPackageCredit","package_credit_amount":1200,"package_credit_remain":800}],"end_date":"2026-11-01"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != EpCreditStatistics || r.Header.Get("Agent-Type") != "PromptCenter" || !strings.HasPrefix(r.Header.Get("Authorization"), "SDK-HMAC-SHA256 ") || !strings.Contains(r.Header.Get("Authorization"), "x-auth-token") || r.Header.Get("X-Security-Token") != "fixture-token" || r.Header.Get("X-Auth-Token") != "fixture-token" {
			t.Errorf("unexpected request: method=%s path=%s type=%s", r.Method, r.URL.Path, r.Header.Get("Agent-Type"))
		}
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()
	client := NewWithModelEndpoints(time.Second, server.URL, "")
	quota, err := client.FetchCreditQuota(context.Background(), SignCredential{AccessKeyID: "fixture-ak", SecretAccessKey: "fixture-sk", SecurityToken: "fixture-token"})
	if err != nil || !quota.Available || quota.Total == nil || *quota.Total != 1200 || quota.Used == nil || *quota.Used != 400 || quota.Remaining == nil || *quota.Remaining != 800 || quota.ResetDate != "2026-11-01" {
		t.Fatalf("quota=%+v err=%v", quota, err)
	}
}

func TestParseCreditQuotaMissingAndMalformed(t *testing.T) {
	for _, test := range []struct {
		name          string
		raw           string
		wantAvailable bool
		wantRemaining *float64
		wantError     bool
	}{
		{name: "no credit metric", raw: `{"metrics":[]}`},
		{name: "zero is known", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":0}]}`, wantAvailable: true, wantRemaining: ptrFloat(0)},
		{name: "zero with exponent is known", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":0e-999}]}`, wantAvailable: true, wantRemaining: ptrFloat(0)},
		{name: "calculated", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":100,"package_credit_used":40}]}`, wantAvailable: true, wantRemaining: ptrFloat(60)},
		{name: "overdrawn", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":100,"package_credit_used":140}]}`, wantAvailable: true, wantRemaining: ptrFloat(0)},
		{name: "no remaining", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":100}]}`, wantError: true},
		{name: "missing metrics", raw: `{"package":{}}`, wantError: true},
		{name: "null metrics", raw: `{"metrics":null}`, wantError: true},
		{name: "negative", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":-1}]}`, wantError: true},
		{name: "string", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":"1"}]}`, wantError: true},
		{name: "overflow", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":1e999}]}`, wantError: true},
		{name: "underflow", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":1e-999}]}`, wantError: true},
		{name: "duplicate totals", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":1},{"name":"usageTotalPackageCredit","package_credit_remain":1}]}`, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseCreditQuota([]byte(test.raw))
			if (err != nil) != test.wantError || got.Available != test.wantAvailable {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			if test.wantRemaining != nil && (got.Remaining == nil || *got.Remaining != *test.wantRemaining) {
				t.Fatalf("remaining=%v", got.Remaining)
			}
		})
	}
}

func TestParseCreditQuotaUnlimited(t *testing.T) {
	for _, test := range []struct {
		name      string
		raw       string
		unlimited bool
		used      *float64
		error     bool
	}{
		{name: "infinite total with used", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-1,"package_credit_used":4}]}`, unlimited: true, used: ptrFloat(4)},
		{name: "finite remaining ignored for unlimited", raw: `{"package":{"is_token_package":true,"is_credit_package":false},"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-1.0,"package_credit_used":0,"package_credit_remain":100}]}`, unlimited: true, used: ptrFloat(0)},
		{name: "equivalent exponent", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-10e-1}]}`, unlimited: true},
		{name: "finite zero", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":0,"package_credit_used":0,"package_credit_remain":0}]}`},
		{name: "other negative total", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-2,"package_credit_remain":0}]}`, error: true},
		{name: "near minus one is not the sentinel", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-1.00000000000000001}]}`, error: true},
		{name: "negative used is not a sentinel", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-1,"package_credit_used":-1}]}`, error: true},
		{name: "negative remaining is not a proven sentinel", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-1,"package_credit_remain":-1}]}`, error: true},
		{name: "overflow even with sentinel", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-1,"package_credit_used":1e999}]}`, error: true},
		{name: "underflow even with sentinel", raw: `{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-1,"package_credit_used":-1e-999}]}`, error: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseCreditQuota([]byte(test.raw))
			if (err != nil) != test.error {
				t.Fatalf("quota=%+v err=%v", got, err)
			}
			if err != nil {
				return
			}
			if !got.Available || got.Unlimited != test.unlimited {
				t.Fatalf("incorrect quota: %+v", got)
			}
			if test.unlimited && (got.Total != nil || got.Remaining != nil) {
				t.Fatalf("unlimited quota contains finite numbers: %+v", got)
			}
			if test.used != nil && (got.Used == nil || *got.Used != *test.used) {
				t.Fatalf("incorrect used credits: %+v", got)
			}
		})
	}
}

func ptrFloat(v float64) *float64 { return &v }

func TestFetchCreditQuotaRejectsHTTPAndBoundedResponse(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		kind   string
	}{
		{name: "authentication", status: 401, body: "private-provider-body", kind: "auth"},
		{name: "server", status: 500, body: "private-provider-body", kind: "http"},
		{name: "over limit", status: 200, body: strings.Repeat("x", (4<<20)+1), kind: "parse"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer s.Close()
			_, err := NewWithModelEndpoints(time.Second, s.URL, "").FetchCreditQuota(context.Background(), SignCredential{})
			var source *SourceError
			if !errors.As(err, &source) || source.Kind != test.kind || strings.Contains(err.Error(), "private-provider-body") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
