package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixtureBenefitGatewayPath = "/v1/benefit-gateway-config"

func TestBenefitBusinessErrorsHaveSafeKind(t *testing.T) {
	for _, tc := range []struct{ code, kind, safeCode string }{
		{"4000", "business", "4000"},
		{"4004.200", "business", "4004.200"},
		{"InferHub.4004.200", "business", "InferHub.4004.200"},
		{"401", "auth", "401"},
		{"403", "auth", "403"},
		{"secret-key-must-not-escape", "business", ""},
	} {
		t.Run(tc.code, func(t *testing.T) {
			err := benefitEnvelope([]byte(`{"error_code":"` + tc.code + `","error_msg":"secret-provider-body"}`))
			var sourceErr *SourceError
			if !errors.As(err, &sourceErr) || sourceErr.Kind != tc.kind || sourceErr.ProviderCode != tc.safeCode || sourceErr.HTTPStatus != 0 {
				t.Fatalf("HTTP-success provider failure misclassified: %+v", sourceErr)
			}
			encoded, _ := json.Marshal(sourceErr)
			if strings.Contains(string(encoded), "secret-") || strings.Contains(err.Error(), "secret-") {
				t.Fatal("provider body or arbitrary code leaked")
			}
		})
	}
}

func TestDiscoveryDisabledBenefitChannelRemovesOldContributionAndSeed(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	c.SetBenefitAutoClaim(true)
	f.mu.Lock()
	f.bodies[EpBenefitConfig] = `{"error_code":"0000","result":{"models":[{"model_id":"glm-5.3-flash"},{"model_id":"deepseek-v4.1-flash"}]}}`
	f.mu.Unlock()
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if !IsBenefitModelForAuth(a.UserID, "glm-5.3-flash", a) || !IsBenefitModelForAuth(a.UserID, "deepseek-v4.1-flash", a) {
		t.Fatal("fixture has no prior benefit contribution")
	}
	f.mu.Lock()
	claims := f.claims
	f.bodies[fixtureBenefitGatewayPath] = `{"enabled":false}`
	f.bodies[EpAgentDetail] = `{"gpts":{"models":[{"model_name":"deepseek-v4.1-flash"}]}}`
	f.statuses[EpBenefitConfig] = http.StatusServiceUnavailable
	f.mu.Unlock()
	token, ak, sk := a.Credentials()
	backoff := c.beginModelSourceBackoff(a, SignCredential{AccessKeyID: ak, SecretAccessKey: sk, SecurityToken: token})
	c.finishModelSource(a.UserID, backoff, 2, true)
	d := c.DiscoverModels(context.Background(), a, true)
	models, fresh := PublishModelDiscovery(a.UserID, a, d)
	status := AccountModelDiagnosticsForAuth(a.UserID, a).Sources[2]
	if status.Status != "disabled" || status.Error != nil || status.RetryAt != nil || status.Count != 0 || status.Retained != 0 || status.LastSuccessAt == nil || status.LastAttemptAt == nil || status.LastSuccessAt.Before(*status.LastAttemptAt) {
		t.Fatalf("confirmed disabled channel did not authoritatively clear old contribution: %+v", status)
	}
	claimJSON, _ := json.Marshal(d.Claim)
	var claim struct {
		Enabled       bool   `json:"enabled"`
		Attempted     bool   `json:"attempted"`
		SkippedReason string `json:"skipped_reason"`
	}
	_ = json.Unmarshal(claimJSON, &claim)
	if !claim.Enabled || claim.Attempted || claim.SkippedReason != "channel_disabled" || d.Claim.AttemptedAt != nil || d.Claim.Error != nil {
		t.Fatalf("channel skip changed configured claim policy or made an attempt: %s", claimJSON)
	}
	f.mu.Lock()
	claimCalls := f.claims
	f.mu.Unlock()
	if claimCalls != claims {
		t.Fatal("disabled channel still attempted a claim")
	}
	if d.Successes() != 3 || d.FailedSources() != 0 || AccountCatalogStaleForAuth(a.UserID, a) || !c.modelSourceRetry(a.UserID, backoff, 2).IsZero() {
		t.Fatalf("disabled source was counted as a failed/stale pull: %+v", d)
	}
	if present, complete := AccountModelKnowledgeForAuth(a.UserID, "deepseek-v4.1-flash", a); !present || !complete {
		t.Fatal("confirmed disabled source broke complete normal model knowledge")
	}
	if IsBenefitModelForAuth(a.UserID, "glm-5.3-flash", a) || IsBenefitModelForAuth(a.UserID, "deepseek-v4.1-flash", a) || !contains(fresh, "deepseek-v4.1-flash") {
		t.Fatalf("closed channel retained benefit route or lost normal source: models=%+v fresh=%v", models, fresh)
	}
	// Closure is per account/auth, not a global rule for the cold-start seeds.
	if !IsBenefitModel("unrelated-unknown-account", "glm-5.3-flash") {
		t.Fatal("one account's channel closure disabled another account")
	}
	oldSuccess := *status.LastSuccessAt
	f.mu.Lock()
	f.statuses[fixtureBenefitGatewayPath] = http.StatusServiceUnavailable
	f.mu.Unlock()
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	status = AccountModelDiagnosticsForAuth(a.UserID, a).Sources[2]
	if status.Status != "failed" || status.Retained != 0 || status.LastSuccessAt == nil || !status.LastSuccessAt.Equal(oldSuccess) || IsBenefitModelForAuth(a.UserID, "glm-5.3-flash", a) {
		t.Fatalf("unknown later switch resurrected a removed route: %+v", status)
	}
	f.mu.Lock()
	f.statuses[fixtureBenefitGatewayPath] = 0
	f.statuses[EpBenefitConfig] = 0
	f.bodies[fixtureBenefitGatewayPath] = `{"enabled":true}`
	f.mu.Unlock()
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if !IsBenefitModelForAuth(a.UserID, "glm-5.3-flash", a) || AccountModelDiagnosticsForAuth(a.UserID, a).Sources[2].Status != "ok" {
		t.Fatal("reopened channel failed to rediscover its benefit contribution")
	}
}

func TestDiscoveryGateMustBeExplicitBeforeRemovingBenefit(t *testing.T) {
	for _, tc := range []struct {
		name, body, kind string
		status           int
	}{
		{"missing", `{}`, "parse", 200},
		{"null", `{"enabled":null}`, "parse", 200},
		{"string", `{"enabled":"false"}`, "parse", 200},
		{"number", `{"enabled":0}`, "parse", 200},
		{"duplicate", `{"enabled":true,"enabled":false}`, "parse", 200},
		{"case", `{"Enabled":false}`, "parse", 200},
		{"null-object", `null`, "parse", 200},
		{"array", `[{"enabled":false}]`, "parse", 200},
		{"trailing", `{"enabled":false} {}`, "parse", 200},
		{"malformed", `{"enabled":false`, "parse", 200},
		{"unauthorized", `{"enabled":false}`, "auth", 401},
		{"forbidden", `{"enabled":false}`, "auth", 403},
		{"unavailable", `{"enabled":false}`, "http", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newModelFixture(t)
			a := fixtureAccount(t)
			want, _ := PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
			f.mu.Lock()
			f.bodies[fixtureBenefitGatewayPath], f.statuses[fixtureBenefitGatewayPath] = tc.body, tc.status
			f.mu.Unlock()
			d := c.DiscoverModels(context.Background(), a, true)
			got, fresh := PublishModelDiscovery(a.UserID, a, d)
			s := AccountModelDiagnostics(a.UserID).Sources[2]
			if s.Status != "failed" || s.Error == nil || s.Error.Kind != tc.kind || s.Retained != 1 || s.LastSuccessAt == nil || s.RetryAt == nil || !reflect.DeepEqual(got, want) || contains(fresh, "deepseek-v4.1-flash") {
				t.Fatalf("uncertain switch incorrectly revoked/refreshed benefit: source=%+v models=%+v fresh=%v", s, got, fresh)
			}
			if tc.status != 200 && s.Error.HTTPStatus != tc.status {
				t.Fatal("real HTTP failure lost its status")
			}
			if !IsBenefitModelForAuth(a.UserID, "deepseek-v4.1-flash", a) || d.Claim.Attempted {
				t.Fatal("uncertain switch erased evidence or permitted claim")
			}
		})
	}
}

func TestDiscoveryBenefitGateSignedAndSkipsLegacyEndpoints(t *testing.T) {
	if EpBenefitGatewayConfig != fixtureBenefitGatewayPath {
		t.Fatal("benefit gate endpoint changed from the documented protocol")
	}
	var mu sync.Mutex
	var paths []string
	gateCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case EpAgentList:
			_, _ = io.WriteString(w, `{"agents":[]}`)
		case EpModelBuiltin:
			_, _ = io.WriteString(w, `{"builtinModels":[]}`)
		case fixtureBenefitGatewayPath:
			mu.Lock()
			gateCalls++
			mu.Unlock()
			if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("Agent-Type") != "PromptCenter" || r.Header.Get(HeaderMaasType) != "" || r.Header.Get("X-Security-Token") != "fixture-token" || r.Header.Get("X-Auth-Token") != "" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Language") != "zh-cn" {
				t.Errorf("benefit gate did not use its documented request protocol")
			}
			authorization := r.Header.Get("Authorization")
			if !strings.HasPrefix(authorization, "SDK-HMAC-SHA256 Access=fixture-ak, SignedHeaders=") {
				t.Error("benefit gate was not signed")
			}
			for _, header := range []string{"agent-type;", "content-type;", "host;", "x-language;", "x-sdk-content-sha256;", "x-sdk-date;", "x-security-token,"} {
				if !strings.Contains(authorization, header) {
					t.Errorf("gate signature omitted %s", header)
				}
			}
			_, _ = io.WriteString(w, `{"enabled":false}`)
		default:
			t.Errorf("disabled gate called legacy endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	c := NewWithModelEndpoints(time.Second, srv.URL, srv.URL)
	a := fixtureAccount(t)
	for _, autoClaim := range []bool{false, true} {
		c.SetBenefitAutoClaim(autoClaim)
		d := c.DiscoverModels(context.Background(), a, true)
		if d.Successes() != 3 || d.FailedSources() != 0 || d.Claim.Enabled != autoClaim || d.Claim.Attempted {
			t.Fatalf("confirmed channel state failed: %+v", d)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if gateCalls != 2 || len(paths) != 6 {
		t.Fatalf("disabled discovery did not use exactly one gate per pull: %v", paths)
	}
}

func TestDiscoveryBenefitGateTimeoutRetainsContribution(t *testing.T) {
	_, c := newModelFixture(t)
	a := fixtureAccount(t)
	want, _ := PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	originalTransport := c.http.Transport
	c.http.Transport = discoveryRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == EpBenefitGatewayConfig {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return originalTransport.RoundTrip(r)
	})
	c.SetBenefitAutoClaim(true)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	d := c.DiscoverModels(ctx, a, true)
	got, fresh := PublishModelDiscovery(a.UserID, a, d)
	s := AccountModelDiagnosticsForAuth(a.UserID, a).Sources[2]
	if s.Status != "failed" || s.Error == nil || s.Error.Kind != "timeout" || s.LastSuccessAt == nil || s.RetryAt == nil || s.Retained != 1 || !reflect.DeepEqual(want, got) || contains(fresh, "deepseek-v4.1-flash") || d.Claim.Attempted || time.Since(started) >= time.Second {
		t.Fatalf("stalled switch was not bounded/retained as unknown: source=%+v models=%+v claim=%+v", s, got, d.Claim)
	}
	if !IsBenefitModelForAuth(a.UserID, "deepseek-v4.1-flash", a) {
		t.Fatal("switch timeout was inferred to mean channel closure")
	}
}

func TestBenefitHTTPFailuresKeepStatusWhenBodyIsUnreadable(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable} {
		c := New(time.Second)
		c.http.Transport = discoveryRoundTripper(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: &failedMetadataBody{}, Header: http.Header{}}, nil
		})
		for _, endpoint := range []string{EpBenefitGatewayConfig, EpBenefitConfig} {
			_, err := c.modelRequest(context.Background(), http.MethodGet, "https://fixture.invalid"+endpoint, "", SignCredential{}, 8)
			var sourceErr *SourceError
			kind := "http"
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				kind = "auth"
			}
			if !errors.As(err, &sourceErr) || sourceErr.Kind != kind || sourceErr.HTTPStatus != status {
				t.Fatalf("real non-2xx response lost its status due to error-body contents: %+v", sourceErr)
			}
		}
	}
}

func TestBenefitGateClaimAndConfigShareSourceBudget(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	c.SetBenefitAutoClaim(true)
	// Leave the source context as the governing deadline rather than the
	// fixture client's shorter per-request timeout.
	c.http.Timeout = time.Minute
	originalTransport := c.http.Transport
	deadlines := map[string]time.Time{}
	c.http.Transport = discoveryRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case EpBenefitGatewayConfig, EpBenefitClaim, EpBenefitConfig:
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) > modelSourceTimeout {
				t.Error("benefit request escaped the source deadline")
			}
			deadlines[r.URL.Path] = deadline
		}
		return originalTransport.RoundTrip(r)
	})
	if d := c.DiscoverModels(context.Background(), a, true); d.Successes() != 3 || !d.Claim.Succeeded {
		t.Fatalf("enabled channel no longer discovers/claims: %+v", d)
	}
	gate, claim, config := deadlines[EpBenefitGatewayConfig], deadlines[EpBenefitClaim], deadlines[EpBenefitConfig]
	if gate.IsZero() || !gate.Equal(config) || claim.IsZero() || !claim.Before(gate) || time.Until(claim) > 5*time.Second {
		t.Fatalf("gate/claim/config did not share the bounded source context: gate=%v claim=%v config=%v", gate, claim, config)
	}
	f.mu.Lock()
	claimCalls := f.claims
	f.mu.Unlock()
	if claimCalls != 1 {
		t.Fatalf("enabled channel did not preserve configured claim policy: %d", claimCalls)
	}
}

func TestDiscoveryBusinessRefusalDoesNotInferDisabledChannel(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	want, _ := PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	f.mu.Lock()
	f.bodies[EpBenefitConfig] = `{"error_code":"4000","error_msg":"secret-provider-body"}`
	f.mu.Unlock()
	d := c.DiscoverModels(context.Background(), a, true)
	got, fresh := PublishModelDiscovery(a.UserID, a, d)
	s := AccountModelDiagnostics(a.UserID).Sources[2]
	if s.Status != "failed" || s.Error == nil || s.Error.Kind != "business" || s.Error.ProviderCode != "4000" || s.Error.HTTPStatus != 0 || s.Retained != 1 || s.RetryAt == nil || !reflect.DeepEqual(want, got) || contains(fresh, "deepseek-v4.1-flash") || !IsBenefitModelForAuth(a.UserID, "deepseek-v4.1-flash", a) {
		t.Fatalf("provider business code was inferred to revoke the channel: %+v models=%+v", s, got)
	}
}
