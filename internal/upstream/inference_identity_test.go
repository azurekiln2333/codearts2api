package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"codearts2api/internal/auth"
)

func TestInferenceIdentityCapturedSourceFields(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	f.bodies[EpBenefitGatewayConfig] = `{"enabled":false}`
	f.bodies[EpAgentDetail] = `{"gpts":{"models":[
		{"model_id":"opaque-glm-record","model_name":"GLM-5.3","model_alias":"glm-5.3","model_parameters":{"model_id":"glm-5.3","context_window":202752}},
		{"model_name":"DeepSeek-V4.1-Flash","model_alias":"deepseek-v4.1-flash","model_parameters":{"model_id":"deepseek-v4.1-flash"}},
		{"model_name":"GLM-5.2-ArkTS-SPARK","model_alias":"glm-5.2-sft-harmony","model_parameters":{"model_id":"glm-5.2-sft-harmony"}},
		{"model_name":"GLM-5.2","model_parameters":{"model_id":"GLM-5.2"}},
		{"model_name":"Qwen3-VL-235B","model_parameters":{"model_id":"Qwen3-VL-235B"}}
	]}}`
	f.bodies[EpModelBuiltin] = `{"builtinModels":[
		{"model_id":"glm-5.3","model_name":"GLM-5.3"},
		{"model_id":"deepseek-v4.1-flash","model_name":"DeepSeek-V4.1-Flash"},
		{"model_id":"glm-5.2-sft-harmony","model_name":"GLM-5.2-ArkTS-SPARK"},
		{"model_id":"GLM-5.2","model_name":"GLM-5.2"},
		{"model_id":"Qwen3-VL-235B","model_name":"Qwen3-VL-235B"}
	]}`
	d := c.DiscoverModels(context.Background(), a, true)
	models, fresh := PublishModelDiscovery(a.UserID, a, d)
	if d.Successes() != 3 || len(models) != 5 || len(fresh) != 5 {
		t.Fatalf("discovery lost explicit identities: successes=%d models=%+v fresh=%v", d.Successes(), models, fresh)
	}
	for _, source := range []string{"agent", "builtin"} {
		for _, model := range d.sources[source].models {
			if model.ID == "glm-5.2-sft-harmony" && model.Name != "GLM-5.2-ArkTS-SPARK" {
				t.Fatalf("%s discarded display name: %+v", source, model)
			}
			if model.ID == "GLM-5.3" || model.ID == "DeepSeek-V4.1-Flash" || model.ID == "GLM-5.2-ArkTS-SPARK" {
				t.Fatalf("%s promoted display name to inference ID: %+v", source, model)
			}
		}
	}
	for input, want := range map[string]string{
		"gLm-5.3": "glm-5.3", "DEEPSEEK-V4.1-FLASH": "deepseek-v4.1-flash",
		"GLM-5.2-ArkTS-SPARK": "glm-5.2-sft-harmony", " GLM-5.2-SFT-HARMONY ": "glm-5.2-sft-harmony",
		"glm-5.2": "GLM-5.2", "qwen3-vl-235b": "Qwen3-VL-235B",
	} {
		id, benefit := ResolveModelForAuth(a.UserID, input, a)
		present, complete := AccountModelKnowledgeForAuth(a.UserID, input, a)
		if id != want || benefit || !present || !complete || IsBenefitModelForAuth(a.UserID, input, a) {
			t.Errorf("input=%q resolved=%q benefit=%t knowledge=%t/%t want=%q", input, id, benefit, present, complete, want)
		}
	}
	for _, input := range []string{"glm-5.4", "deepseek-v4.1-pro", "opaque-glm-record"} {
		if id, _ := ResolveModelForAuth(a.UserID, input, a); id != input {
			t.Errorf("unknown %q was guessed as %q", input, id)
		}
		if present, _ := AccountModelKnowledgeForAuth(a.UserID, input, a); present {
			t.Errorf("unknown %q became a catalog member", input)
		}
	}
	replacement := &auth.Auth{UserID: a.UserID}
	if id, _ := ResolveModelForAuth(a.UserID, "GLM-5.2-ArkTS-SPARK", replacement); id != "GLM-5.2-ArkTS-SPARK" {
		t.Fatalf("replacement login inherited display alias: %q", id)
	}
}

func TestInferenceIdentityBuiltinAuthorityAndFreshness(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	f.bodies[EpBenefitGatewayConfig] = `{"enabled":false}`
	// Legacy agent metadata has only a display name, richer than builtin.
	f.bodies[EpAgentDetail] = `{"gpts":{"models":[{"model_name":"GLM-5.3","model_parameters":{"context_window":128000}}]}}`
	f.bodies[EpModelBuiltin] = `{"builtinModels":[{"model_id":"glm-5.3","model_name":"GLM-5.3"}]}`
	models, fresh := PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if models[0].ID != "GLM-5.3" || !contains(fresh, "glm-5.3") {
		t.Fatalf("display merge and fresh wire identity diverged: models=%+v fresh=%v", models, fresh)
	}
	if id, benefit := ResolveModelForAuth(a.UserID, "GLM-5.3", a); id != "glm-5.3" || benefit {
		t.Fatalf("richer agent overrode builtin inference ID: %q/%t", id, benefit)
	}
	f.mu.Lock()
	f.statuses[EpModelBuiltin] = http.StatusServiceUnavailable
	f.mu.Unlock()
	_, fresh = PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if id, _ := ResolveModelForAuth(a.UserID, "GLM-5.3", a); id != "glm-5.3" || len(fresh) != 0 {
		t.Fatalf("fresh agent reset retained failed builtin identity: id=%q fresh=%v", id, fresh)
	}
	if source := AccountModelDiagnosticsForAuth(a.UserID, a).Sources[1]; source.Status != "failed" || source.Retained != 1 {
		t.Fatalf("builtin retention not reported: %+v", source)
	}
	f.mu.Lock()
	f.statuses[EpModelBuiltin] = 0
	f.mu.Unlock()
	_, fresh = PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if !contains(fresh, "glm-5.3") {
		t.Fatalf("fresh builtin did not authorize exact scoped reset: %v", fresh)
	}
	f.mu.Lock()
	f.bodies[EpModelBuiltin] = `{"builtinModels":[]}`
	f.mu.Unlock()
	_, fresh = PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if id, _ := ResolveModelForAuth(a.UserID, "glm-5.3", a); id != "GLM-5.3" || !contains(fresh, "GLM-5.3") {
		t.Fatalf("authoritative builtin removal did not hand identity to agent: %q fresh=%v", id, fresh)
	}
	f.mu.Lock()
	f.statuses[EpModelBuiltin] = http.StatusServiceUnavailable
	f.mu.Unlock()
	_, fresh = PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if id, _ := ResolveModelForAuth(a.UserID, "glm-5.3", a); id != "GLM-5.3" || !contains(fresh, "GLM-5.3") {
		t.Fatalf("failed builtin resurrected removed identity: %q fresh=%v", id, fresh)
	}
}

func TestInferenceIdentityAliasCollisions(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	f.bodies[EpBenefitGatewayConfig] = `{"enabled":false}`
	f.bodies[EpAgentDetail] = `{"gpts":{"models":[
		{"model_name":"Shared Display","model_parameters":{"model_id":"target-a"}},
		{"model_name":"Specific Display","model_parameters":{"model_id":"target-a","context_window":128000}},
		{"model_name":"Shared Display","model_parameters":{"model_id":"target-b"}},
		{"model_name":"Other Display","model_parameters":{"model_id":"target-c"}}
	]}}`
	f.bodies[EpModelBuiltin] = `{"builtinModels":[{"model_id":"Shared Display"},{"model_id":"target-d","model_name":"Other Display"}]}`
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if id, _ := ResolveModelForAuth(a.UserID, "shared display", a); id != "Shared Display" {
		t.Fatalf("display alias overrode exact registered ID: %q", id)
	}
	if id, _ := ResolveModelForAuth(a.UserID, "Specific Display", a); id != "target-a" {
		t.Fatalf("unique explicit alias did not resolve: %q", id)
	}
	if id, _ := ResolveModelForAuth(a.UserID, "Other Display", a); id != "Other Display" {
		t.Fatalf("cross-source ambiguous alias resolved: %q", id)
	}
	f.mu.Lock()
	f.bodies[EpModelBuiltin] = `{"builtinModels":[]}`
	f.mu.Unlock()
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if id, _ := ResolveModelForAuth(a.UserID, "Shared Display", a); id != "Shared Display" {
		t.Fatalf("deduplication hid a conflicting source alias: %q", id)
	}
	if present, complete := AccountModelKnowledgeForAuth(a.UserID, "Shared Display", a); present || !complete {
		t.Fatalf("ambiguous alias became eligible: present=%t complete=%t", present, complete)
	}
	if id, _ := ResolveModelForAuth(a.UserID, "Other Display", a); id != "target-c" {
		t.Fatalf("removed collision did not restore unique source alias: %q", id)
	}
	SetAccountModels(a.UserID, []ModelInfo{{ID: "legacy-id", Name: "Legacy Display"}})
	if id, _ := ResolveModelForAuth(a.UserID, "Legacy Display", a); id != "Legacy Display" {
		t.Fatalf("legacy SetAccountModels invented display mapping: %q", id)
	}
}

func TestInferenceIdentityAliasUsesEffectiveBenefitRoute(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	f.bodies[EpAgentDetail] = `{"gpts":{"models":[{"model_name":"GLM-5.2-ArkTS-SPARK","model_parameters":{"model_id":"glm-5.2-sft-harmony","context_window":128000}}]}}`
	f.bodies[EpModelBuiltin] = `{"builtinModels":[{"model_id":"glm-5.2-sft-harmony","model_name":"GLM-5.2-ArkTS-SPARK"}]}`
	f.bodies[EpBenefitConfig] = `{"error_code":"0000","result":{"models":[{"model_id":"GLM-5.2-SFT-HARMONY","model_name":"Benefit Spark"}]}}`
	_, fresh := PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if !contains(fresh, "GLM-5.2-SFT-HARMONY") {
		t.Fatalf("fresh identity did not follow governing benefit source: %v", fresh)
	}
	for _, input := range []string{"GLM-5.2-ArkTS-SPARK", "Benefit Spark", "glm-5.2-sft-harmony"} {
		id, benefit := ResolveModelForAuth(a.UserID, input, a)
		present, complete := AccountModelKnowledgeForAuth(a.UserID, input, a)
		if id != "GLM-5.2-SFT-HARMONY" || !benefit || !IsBenefitModelForAuth(a.UserID, input, a) || !present || !complete {
			t.Errorf("alias route/membership mismatch: %q -> %q/%t knowledge=%t/%t", input, id, benefit, present, complete)
		}
	}
	f.mu.Lock()
	f.statuses[EpBenefitConfig] = http.StatusServiceUnavailable
	f.mu.Unlock()
	_, fresh = PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if id, benefit := ResolveModelForAuth(a.UserID, "GLM-5.2-ArkTS-SPARK", a); id != "GLM-5.2-SFT-HARMONY" || !benefit || len(fresh) != 0 {
		t.Fatalf("normal success replaced stale benefit authority: %q/%t fresh=%v", id, benefit, fresh)
	}
	f.mu.Lock()
	f.bodies[EpBenefitGatewayConfig] = `{"enabled":false}`
	f.mu.Unlock()
	_, fresh = PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if id, benefit := ResolveModelForAuth(a.UserID, "GLM-5.2-ArkTS-SPARK", a); id != "glm-5.2-sft-harmony" || benefit || !contains(fresh, id) {
		t.Fatalf("confirmed closure did not restore normal alias identity: %q/%t fresh=%v", id, benefit, fresh)
	}
	if present, _ := AccountModelKnowledgeForAuth(a.UserID, "Benefit Spark", a); present {
		t.Fatal("removed benefit display alias survived authoritative closure")
	}
}

func TestInferenceIdentityExplicitParameterValidationAndDisplayBounds(t *testing.T) {
	for _, badID := range []string{strings.Repeat("x", 193), "bad\nidentifier"} {
		raw, err := json.Marshal(map[string]any{"model_name": "Valid Display", "model_parameters": map[string]any{"model_id": badID}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseModelEntries([]json.RawMessage{raw}, "agent"); err == nil {
			t.Fatal("invalid explicit inference parameter silently fell back to display name")
		}
	}
	for _, name := range []string{strings.Repeat("x", 193), "unsafe\ndisplay"} {
		raw, _ := json.Marshal(map[string]any{"model_name": name, "model_parameters": map[string]any{"model_id": "real-id"}})
		models, err := parseModelEntries([]json.RawMessage{raw}, "agent")
		if err != nil || len(models) != 1 || len(models[0].Name) > 192 || models[0].displayAlias != "" {
			t.Fatalf("sanitized display became an invented alias: models=%+v err=%v", models, err)
		}
	}
}
