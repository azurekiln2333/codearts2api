package upstream

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"codearts2api/internal/auth"
)

func TestResolveModelForAuthOwnsExactCaseAndRoute(t *testing.T) {
	f, c := newModelFixture(t)
	a := fixtureAccount(t)
	f.mu.Lock()
	f.bodies[EpAgentDetail] = `{"gpts":{"models":[]}}`
	f.bodies[EpModelBuiltin] = `{"builtinModels":[{"model_id":"GLM-5.3","context_window":32768}]}`
	f.bodies[EpBenefitConfig] = `{"error_code":"0000","result":{"models":[{"model_id":"glm-5.3"}]}}`
	f.mu.Unlock()
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	SetAccountModels(t.Name()+"-other", []ModelInfo{{ID: "gLM-5.3"}, {ID: "Foreign-Only"}})
	for _, input := range []string{"glm-5.3", "GLM-5.3", "gLm-5.3"} {
		id, benefit := ResolveModelForAuth(a.UserID, input, a)
		if id != "glm-5.3" || !benefit {
			t.Fatalf("input=%q id=%q benefit=%v", input, id, benefit)
		}
	}
	if id, benefit := ResolveModelForAuth(a.UserID, "foreign-only", a); id != "foreign-only" || benefit {
		t.Fatalf("foreign model leaked: %q %v", id, benefit)
	}
	replacement := &auth.Auth{UserID: a.UserID}
	if id, benefit := ResolveModelForAuth(a.UserID, "Glm-5.3", replacement); id != "Glm-5.3" || benefit {
		t.Fatalf("previous auth leaked: %q %v", id, benefit)
	}
	f.mu.Lock()
	f.bodies[EpBenefitGatewayConfig] = `{"enabled":false}`
	f.mu.Unlock()
	PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
	if id, benefit := ResolveModelForAuth(a.UserID, "glm-5.3", a); id != "GLM-5.3" || benefit {
		t.Fatalf("closed route: %q %v", id, benefit)
	}
}

func TestResolveModelForAuthStaticAliasesAndConcurrentPublication(t *testing.T) {
	a := fixtureAccount(t)
	SetAccountModels(a.UserID, []ModelInfo{{ID: "glm-5.2"}})
	for _, input := range []string{"snap-chat", "SNAP-CHAT", "GLM-5.2"} {
		if id, _ := ResolveModelForAuth(a.UserID, input, a); id != "glm-5.2" {
			t.Fatalf("alias=%q sent=%q", input, id)
		}
	}
	var wg sync.WaitGroup
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			uid := fmt.Sprintf("%s-%d", t.Name(), n)
			exact := []string{"Model-Mixed", "model-mixed"}[n%2]
			for i := 0; i < 50; i++ {
				SetAccountModels(uid, []ModelInfo{{ID: exact, Benefit: n%2 == 0}})
				id, benefit := ResolveModelForAuth(uid, "MODEL-MIXED", nil)
				if id != exact || benefit != (n%2 == 0) {
					t.Errorf("cross-account result=%q/%v want=%q/%v", id, benefit, exact, n%2 == 0)
					return
				}
			}
		}(n)
	}
	wg.Wait()
}
