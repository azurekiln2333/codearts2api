package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"codearts2api/internal/auth"
)

type modelFixture struct {
	mu            sync.Mutex
	bodies        map[string]string
	statuses      map[string]int
	claims        int
	benefitHeader string
}

func newModelFixture(t *testing.T) (*modelFixture, *Client) {
	t.Helper()
	f := &modelFixture{bodies: map[string]string{
		EpBenefitGatewayConfig: `{"enabled":true}`,
		EpAgentList:            `{"agents":[{"agent_id":"code-agent","is_primary_agent":true}]}`,
		EpAgentDetail:          `{"gpts":{"models":[{"model_name":"agent-model"}]}}`,
		EpModelBuiltin:         `{"builtinModels":[{"model_id":"builtin-model"}]}`,
		EpBenefitConfig:        `{"error_code":"0000","result":{"models":[{"model_id":"deepseek-v4.1-flash"}]}}`,
		EpBenefitClaim:         `{"error_code":"0000"}`,
		EpChatV2:               "data: [DONE]\n\n",
	}, statuses: map[string]int{}}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		body, status := f.bodies[r.URL.Path], f.statuses[r.URL.Path]
		if r.URL.Path == EpBenefitClaim {
			f.claims++
		}
		if r.URL.Path == EpChatV2 {
			f.benefitHeader = r.Header.Get(HeaderMaasType)
		}
		f.mu.Unlock()
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(s.Close)
	c := NewWithModelEndpoints(time.Second, s.URL, s.URL)
	c.SetBenefitAutoClaim(false)
	return f, c
}

func fixtureAccount(t *testing.T) *auth.Auth {
	return &auth.Auth{UserID: t.Name(), CloudDragonTok: "fixture-token", AccessKeyID: "fixture-ak", SecretAccessKey: "fixture-sk"}
}

func TestDiscoveryRetainsEachFailedSourceAndExactBenefitRoute(t *testing.T) {
	for _, failed := range [][]string{{EpModelBuiltin, EpBenefitConfig}, {EpAgentList}, {EpModelBuiltin}, {EpBenefitConfig}, {EpAgentList, EpModelBuiltin}} {
		t.Run(strings.Join(failed, "-"), func(t *testing.T) {
			f, c := newModelFixture(t)
			a := fixtureAccount(t)
			seed := c.DiscoverModels(context.Background(), a, true)
			want, _ := PublishModelDiscovery(a.UserID, a, seed)
			f.mu.Lock()
			for _, path := range failed {
				f.statuses[path] = 503
				f.bodies[path] = "secret upstream body must not escape"
			}
			f.mu.Unlock()
			d := c.DiscoverModels(context.Background(), a, true)
			got, fresh := PublishModelDiscovery(a.UserID, a, d)
			stored, ok := AccountModels(a.UserID)
			if !ok || !reflect.DeepEqual(got, stored) || !reflect.DeepEqual(got, want) {
				t.Fatalf("returned/stored retained source union differs: got=%+v stored=%+v want=%+v", got, stored, want)
			}
			if !IsBenefitModel(a.UserID, "deepseek-v4.1-flash") {
				t.Fatal("partial discovery lost non-seed benefit routing")
			}
			diag := AccountModelDiagnostics(a.UserID)
			serialized, _ := json.Marshal(diag)
			if strings.Contains(string(serialized), "secret upstream") {
				t.Fatal("diagnostic leaked arbitrary upstream text")
			}
			for _, s := range diag.Sources {
				if s.Status == "failed" && (s.Retained != 1 || s.Count != 1 || s.LastSuccessAt == nil || s.LastAttemptAt == nil || s.Error.HTTPStatus != 503) {
					t.Errorf("retained source metadata is inaccurate: %+v", s)
				}
			}
			if contains(failed, EpBenefitConfig) && contains(fresh, "deepseek-v4.1-flash") {
				t.Fatal("retained benefit route was reported freshly authorized")
			}
			rc, err := c.SendChatV2(context.Background(), map[string]any{"model": "deepseek-v4.1-flash", "stream": true}, "", SignCredential{}, "", IsBenefitModel(a.UserID, "deepseek-v4.1-flash"))
			if err != nil {
				t.Fatal(err)
			}
			_ = rc.Close()
			f.mu.Lock()
			header := f.benefitHeader
			f.mu.Unlock()
			if header != MaasBenefit {
				t.Fatalf("actual request lost benefit header: %q", header)
			}
		})
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestDiscoveryAuthoritativeEmptyCannotResurrectBenefit(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	f.mu.Lock()
	f.bodies[EpBenefitConfig] = `{"error_code":"0000","result":{"models":[]}}`
	f.bodies[EpAgentDetail] = `{"gpts":{"models":[{"model_name":"deepseek-v4.1-flash"}]}}`
	f.mu.Unlock()
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if IsBenefitModel(a.UserID, "deepseek-v4.1-flash") {
		t.Fatal("successful empty benefit source retained removed benefit flag")
	}
	f.mu.Lock()
	f.statuses[EpBenefitConfig] = 503
	f.mu.Unlock()
	got, _ := PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if IsBenefitModel(a.UserID, "deepseek-v4.1-flash") {
		t.Fatalf("historical benefit resurrected after subsequent failure: %+v", got)
	}
	diag := AccountModelDiagnostics(a.UserID)
	if diag.Sources[2].LastSuccessAt == nil || diag.Sources[2].Retained != 0 {
		t.Fatalf("empty successful snapshot not retained honestly: %+v", diag.Sources[2])
	}
}

func TestDiscoveryAuthoritativeBenefitRemovalOverridesSeed(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	f.mu.Lock()
	f.bodies[EpBenefitConfig] = `{"error_code":"0000","result":{"models":[{"model_id":"glm-5.3-flash"}]}}`
	f.mu.Unlock()
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if !IsBenefitModel(a.UserID, "glm-5.3-flash") {
		t.Fatal("seed fixture not initially benefit-routed")
	}
	f.mu.Lock()
	f.bodies[EpBenefitConfig] = `{"error_code":"0000","result":{"models":[]}}`
	f.mu.Unlock()
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if IsBenefitModel(a.UserID, "glm-5.3-flash") {
		t.Fatal("cold-start seed overrode authoritative removal")
	}
	f.mu.Lock()
	f.statuses[EpBenefitConfig] = 503
	f.mu.Unlock()
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if IsBenefitModel(a.UserID, "glm-5.3-flash") {
		t.Fatal("failed benefit refresh resurrected removed cold-start seed")
	}
}

func TestDiscoveryAgentPresenceCannotOverrideRetainedBenefit(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	f.mu.Lock()
	f.bodies[EpAgentDetail] = `{"gpts":{"models":[{"model_name":"deepseek-v4.1-flash"}]}}`
	f.statuses[EpBenefitConfig] = 503
	f.mu.Unlock()
	_, fresh := PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if !IsBenefitModel(a.UserID, "deepseek-v4.1-flash") || contains(fresh, "deepseek-v4.1-flash") {
		t.Fatal("agent source incorrectly replaced or refreshed retained governing benefit route")
	}
}

func TestDiscoveryFreshBenefitRouteUsesMergedModelIdentity(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	const mergedID = "DeepSeek-V4.1-Flash"
	f.mu.Lock()
	f.bodies[EpAgentDetail] = `{"gpts":{"models":[{"model_name":"DeepSeek-V4.1-Flash","model_parameters":{"context_window":128000}}]}}`
	f.mu.Unlock()
	models, fresh := PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	var merged ModelInfo
	for _, model := range models {
		if strings.EqualFold(model.ID, mergedID) {
			merged = model
		}
	}
	if merged.ID != mergedID || !merged.Benefit || !contains(fresh, "deepseek-v4.1-flash") {
		t.Fatalf("fresh governing benefit source lost the merged identity: merged=%+v fresh=%v", merged, fresh)
	}
	f.mu.Lock()
	f.statuses[EpBenefitConfig] = http.StatusServiceUnavailable
	f.mu.Unlock()
	_, fresh = PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if contains(fresh, "deepseek-v4.1-flash") {
		t.Fatal("agent casing match incorrectly refreshed a retained failed benefit route")
	}
}

func TestModelKnowledgeRequiresCompleteCurrentDiscovery(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	assertKnowledge := func(model string, wantPresent, wantComplete bool) {
		t.Helper()
		present, complete := AccountModelKnowledgeForAuth(a.UserID, model, a)
		if present != wantPresent || complete != wantComplete {
			t.Fatalf("model %q knowledge=%t/%t want=%t/%t", model, present, complete, wantPresent, wantComplete)
		}
	}
	assertKnowledge("new-model", false, false)
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	assertKnowledge("deepseek-v4.1-flash", true, true)
	assertKnowledge("new-model", false, true)
	f.mu.Lock()
	f.statuses[EpBenefitConfig] = http.StatusServiceUnavailable
	f.mu.Unlock()
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	assertKnowledge("DEEPSEEK-V4.1-FLASH", true, false)
	assertKnowledge("new-model", false, false)
	f.mu.Lock()
	f.statuses[EpBenefitConfig] = 0
	f.bodies[EpBenefitConfig] = `{"error_code":"0000","result":{"models":[]}}`
	f.mu.Unlock()
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	assertKnowledge("deepseek-v4.1-flash", false, true)
	catalog.Lock()
	catalog.accounts[a.UserID].fetched = time.Now().Add(-CatalogTTL)
	catalog.Unlock()
	assertKnowledge("agent-model", true, false)
	assertKnowledge("new-model", false, false)
	if present, complete := AccountModelKnowledgeForAuth(a.UserID, "agent-model", &auth.Auth{UserID: a.UserID}); present || complete {
		t.Fatal("replacement login inherited old source knowledge")
	}
	SetAccountModels(a.UserID, []ModelInfo{{ID: "compatibility-model"}})
	assertKnowledge("compatibility-model", true, true)
	assertKnowledge("new-model", false, true)
}

func TestDiscoveryStrictEnvelopeAndModelParsers(t *testing.T) {
	cases := []struct {
		name, path, body string
		success          bool
		expected         string
	}{
		{"missing-gpts", EpAgentDetail, `{}`, false, ""},
		{"missing-agent-models", EpAgentDetail, `{"gpts":{}}`, false, ""},
		{"null-agent-models", EpAgentDetail, `{"gpts":{"models":null}}`, false, ""},
		{"empty-agent-models", EpAgentDetail, `{"gpts":{"models":[]}}`, true, ""},
		{"agent-top-id", EpAgentDetail, `{"gpts":{"models":[{"model_id":"exact-model"}]}}`, true, "exact-model"},
		{"agent-param-id", EpAgentDetail, `{"gpts":{"models":[{"model_parameters":{"model_id":"exact-model"}}]}}`, true, "exact-model"},
		{"agent-alias", EpAgentDetail, `{"gpts":{"models":[{"model_alias":"exact-model"}]}}`, true, "exact-model"},
		{"preserve-existing-name-precedence", EpAgentDetail, `{"gpts":{"models":[{"model_name":"inference-name","model_id":"opaque-internal-id"}]}}`, true, "inference-name"},
		{"agent-empty-id", EpAgentDetail, `{"gpts":{"models":[{}]}}`, false, ""},
		{"agent-malformed-number", EpAgentDetail, `{"gpts":{"models":[{"model_name":"x","model_parameters":{"max_tokens":"bad"}}]}}`, false, ""},
		{"missing-builtin", EpModelBuiltin, `{"error":{"message":"secret"}}`, false, ""},
		{"null-builtin", EpModelBuiltin, `{"builtinModels":null}`, false, ""},
		{"empty-builtin", EpModelBuiltin, `{"builtinModels":[]}`, true, ""},
		{"missing-benefit-result", EpBenefitConfig, `{"error_code":"0000"}`, false, ""},
		{"missing-benefit-models", EpBenefitConfig, `{"error_code":"0000","result":{}}`, false, ""},
		{"empty-benefit", EpBenefitConfig, `{"error_code":"0000","result":{"models":[]}}`, true, ""},
		{"benefit-business-error", EpBenefitConfig, `{"error_code":"secret-key-must-not-escape","result":{"models":[]}}`, false, ""},
		{"overlong-exact-id", EpModelBuiltin, `{"builtinModels":[{"model_id":"` + strings.Repeat("a", 193) + `"}]}`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newModelFixture(t)
			a := fixtureAccount(t)
			f.mu.Lock()
			f.bodies[tc.path] = tc.body
			f.mu.Unlock()
			d := c.DiscoverModels(context.Background(), a, true)
			name := "agent"
			if tc.path == EpModelBuiltin {
				name = "builtin"
			}
			if tc.path == EpBenefitConfig {
				name = "benefit"
			}
			s := d.sources[name]
			valid := s.Status == "ok" || s.Status == "empty"
			if valid != tc.success {
				t.Fatalf("status=%s error=%+v", s.Status, s.Error)
			}
			if tc.expected != "" && (len(s.models) != 1 || s.models[0].ID != tc.expected) {
				t.Fatalf("exact identifier parser changed: %+v", s.models)
			}
		})
	}
}

func TestDiscoveryReadFailureOverflowAndCollectionLimit(t *testing.T) {
	for _, mode := range []string{"read-error", "byte-overflow", "model-overflow"} {
		t.Run(mode, func(t *testing.T) {
			c := New(time.Second)
			c.SetBenefitAutoClaim(false)
			c.http.Transport = discoveryRoundTripper(func(r *http.Request) (*http.Response, error) {
				var body io.ReadCloser
				switch mode {
				case "read-error":
					body = &failedMetadataBody{}
				case "byte-overflow":
					body = io.NopCloser(strings.NewReader(strings.Repeat(" ", 4<<20) + `{}`))
				case "model-overflow":
					entries := make([]string, maxSourceModels+1)
					for i := range entries {
						entries[i] = fmt.Sprintf(`{"model_id":"model-%d"}`, i)
					}
					body = io.NopCloser(strings.NewReader(`{"builtinModels":[` + strings.Join(entries, ",") + `]}`))
				}
				return &http.Response{StatusCode: 200, Body: body, Header: http.Header{}}, nil
			})
			_, err := c.fetchBuiltinModelsContext(context.Background(), SignCredential{})
			if err == nil {
				t.Fatal("incomplete/overlimit source accepted as authoritative")
			}
		})
	}
}

type discoveryRoundTripper func(*http.Request) (*http.Response, error)

func (f discoveryRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failedMetadataBody struct{ sent bool }

func (b *failedMetadataBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		return copy(p, `{"builtinModels":[]}`), nil
	}
	return 0, errors.New("synthetic body read failure")
}
func (b *failedMetadataBody) Close() error { return nil }

func TestDiscoveryContextBackoffAndClaimRemainSeparate(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	c.SetBenefitAutoClaim(true)
	f.mu.Lock()
	f.statuses[EpBenefitClaim] = 403
	f.statuses[EpBenefitConfig] = 503
	f.mu.Unlock()
	d := c.DiscoverModels(context.Background(), a, true)
	if !d.Claim.Enabled || !d.Claim.Attempted || d.Claim.Succeeded || d.Claim.Error.Kind != "auth" {
		t.Fatalf("claim failure lost: %+v", d.Claim)
	}
	d = c.DiscoverModels(context.Background(), a, false)
	if d.sources["benefit"].Status != "backoff" || d.sources["benefit"].LastAttemptAt != nil || d.Claim.Attempted {
		t.Fatalf("backoff manufactured an attempt: %+v", d)
	}
	f.mu.Lock()
	f.statuses[EpBenefitConfig] = 0
	f.mu.Unlock()
	d = c.DiscoverModels(context.Background(), a, true)
	if d.sources["benefit"].Status != "ok" || d.Claim.Succeeded {
		t.Fatal("force retry failed or config discovery incorrectly claimed claim success")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d = c.DiscoverModels(ctx, a, true)
	for _, s := range d.sources {
		if s.Status != "canceled" || s.LastAttemptAt != nil || s.RetryAt != nil {
			t.Fatalf("caller cancel manufactured provider attempt/backoff: %+v", s)
		}
	}
}

func TestDiscoveryEmptySourcesKnownButAllFailureUnknownAndCopiesOwned(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	f.mu.Lock()
	f.statuses[EpAgentList] = 503
	f.statuses[EpModelBuiltin] = 503
	f.statuses[EpBenefitConfig] = 503
	f.mu.Unlock()
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if _, known := AccountModels(a.UserID); known {
		t.Fatal("complete cold discovery failure created authoritative empty catalog")
	}
	f.mu.Lock()
	f.statuses = map[string]int{}
	f.bodies[EpAgentList] = `{"agents":[]}`
	f.bodies[EpModelBuiltin] = `{"builtinModels":[]}`
	f.bodies[EpBenefitConfig] = `{"error_code":"0000","result":{"models":[]}}`
	f.mu.Unlock()
	infos, _ := PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if stored, known := AccountModels(a.UserID); !known || len(stored) != 0 || len(infos) != 0 {
		t.Fatal("authoritative valid empty lost")
	}
	d := AccountModelDiagnostics(a.UserID)
	d.Sources[0].Status = "mutated"
	*d.Sources[0].LastSuccessAt = time.Time{}
	if next := AccountModelDiagnostics(a.UserID); next.Sources[0].Status != "empty" || next.Sources[0].LastSuccessAt.IsZero() {
		t.Fatal("returned metadata aliases stored catalog")
	}
}
