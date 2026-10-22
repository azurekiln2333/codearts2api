package upstream

import (
	"context"
	"fmt"
	"testing"
)

func TestDiscoveryBenefitGateRequiresHTTPOK(t *testing.T) {
	for _, status := range []int{201, 202, 203, 204, 205, 206, 207} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f, c := newModelFixture(t)
			a := fixtureAccount(t)
			PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
			f.mu.Lock()
			f.statuses[EpBenefitGatewayConfig] = status
			f.bodies[EpBenefitGatewayConfig] = `{"enabled":false}`
			f.mu.Unlock()
			PublishModelDiscovery(a.UserID, a, c.DiscoverModels(context.Background(), a, true))
			source := AccountModelDiagnosticsForAuth(a.UserID, a).Sources[2]
			if source.Status != "failed" || source.Error == nil || source.Error.Kind != "http" || source.Error.HTTPStatus != status || source.Retained != 1 || source.RetryAt == nil || !IsBenefitModelForAuth(a.UserID, "deepseek-v4.1-flash", a) {
				t.Fatalf("HTTP %d removed authoritative prior route: %+v", status, source)
			}
		})
	}
}

func TestOtherModelRequestsKeepExistingHTTPSuccessRange(t *testing.T) {
	f, c := newModelFixture(t)
	f.mu.Lock()
	f.statuses[EpModelBuiltin] = 206
	f.mu.Unlock()
	a := fixtureAccount(t)
	d := c.DiscoverModels(context.Background(), a, true)
	if d.sources["builtin"].Status != "ok" || d.Successes() != 3 {
		t.Fatalf("gate status constraint changed another directory's success contract: %+v", d)
	}
}
