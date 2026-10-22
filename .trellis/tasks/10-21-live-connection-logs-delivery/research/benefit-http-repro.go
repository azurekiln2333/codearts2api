// Research-only localhost reproduction. No production calls or real credentials.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/upstream"
)

func main() {
	for _, scenario := range []struct {
		name   string
		status int
		body   string
		gate   string
		claim  bool
	}{
		{name: "http-200-business-failure", status: 200, body: `{"error_code":"benefit.synthetic.error","error_msg":"untrusted message must not escape"}`},
		{name: "http-503-transport-failure", status: 503, body: `{"error_code":"benefit.synthetic.error"}`},
		{name: "http-200-valid-empty", status: 200, body: `{"error_code":"0000","result":{"models":[]}}`},
		{name: "http-200-known-channel-refusal", status: 200, body: `{"error_code":"4000","error_msg":"untrusted message must not escape"}`},
		{name: "explicitly-disabled-channel", status: 200, body: `{"error_code":"4000"}`, gate: `{"enabled":false}`, claim: true},
		{name: "unknown-channel-switch", status: 200, body: `{"error_code":"4000"}`, gate: `{}`, claim: true},
	} {
		var mu sync.Mutex
		var request, gateRequest map[string]any
		gateCalls, configCalls, claimCalls := 0, 0, 0
		requestSummary := func(r *http.Request) map[string]any {
			return map[string]any{
				"method": r.Method, "path": r.URL.Path,
				"content_type":       r.Header.Get("Content-Type"),
				"language":           r.Header.Get("X-Language"),
				"agent_type":         r.Header.Get("Agent-Type"),
				"has_security_token": r.Header.Get("X-Security-Token") != "",
				"has_sdk_signing":    strings.HasPrefix(r.Header.Get("Authorization"), "SDK-HMAC-SHA256 "),
			}
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case upstream.EpAgentList:
				_, _ = io.WriteString(w, `{"agents":[]}`)
			case upstream.EpModelBuiltin:
				_, _ = io.WriteString(w, `{"builtinModels":[]}`)
			case upstream.EpBenefitGatewayConfig:
				mu.Lock()
				gateCalls++
				gateRequest = requestSummary(r)
				mu.Unlock()
				gate := scenario.gate
				if gate == "" {
					gate = `{"enabled":true}`
				}
				_, _ = io.WriteString(w, gate)
			case upstream.EpBenefitConfig:
				mu.Lock()
				configCalls++
				request = requestSummary(r)
				mu.Unlock()
				w.WriteHeader(scenario.status)
				_, _ = io.WriteString(w, scenario.body)
			case upstream.EpBenefitClaim:
				mu.Lock()
				claimCalls++
				mu.Unlock()
				_, _ = io.WriteString(w, `{"error_code":"0000"}`)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		c := upstream.NewWithModelEndpoints(time.Second, srv.URL, srv.URL)
		c.SetBenefitAutoClaim(scenario.claim)
		a := &auth.Auth{UserID: "research-" + scenario.name, AccessKeyID: "AK_TEST", SecretAccessKey: "SK_TEST", CloudDragonTok: "ST_TEST"}
		d := c.DiscoverModels(context.Background(), a, true)
		upstream.PublishModelDiscovery(a.UserID, a, d)
		snapshot := upstream.AccountModelDiagnosticsForAuth(a.UserID, a)
		srv.Close()
		mu.Lock()
		for _, source := range snapshot.Sources {
			if source.Source == "benefit" {
				raw, _ := json.Marshal(map[string]any{
					"scenario": scenario.name, "request": request, "source": source,
					"gate_request": gateRequest, "gate_calls": gateCalls,
					"benefit_config_calls": configCalls, "claim_calls": claimCalls, "claim": snapshot.Claim,
				})
				fmt.Println(string(raw))
			}
		}
		mu.Unlock()
	}
}
