package server

import (
	"context"
	"net/http"
	"testing"
)

// Chat or a watcher may finish the shared credential refresh before a catalog
// job begins. A source failure from the old STS must not suppress that job's
// first actual metadata request using the refreshed credential generation.
func TestCatalogRefreshAfterOtherCallerRotatesCredentials(t *testing.T) {
	f := newCatalogAuthFixture(t, http.StatusOK)
	old := f.h.cfg.Upstream.DiscoverModels(context.Background(), f.owner, false)
	if old.FailedSources() != 3 || f.rejectedSource.Load() != 3 {
		t.Fatal("fixture did not establish source failures with expired STS")
	}
	f.release()
	valid, err := f.p.ValidateContext(context.Background(), f.acct)
	if err != nil || !valid {
		t.Fatalf("other caller did not complete credential refresh: valid=%v err=%v", valid, err)
	}
	f.h.StartModelDiscovery(f.h.modelCatalog.serviceCtx)
	state := waitCatalog(t, f.h)
	if state.State != "completed" || state.Completed != 1 || state.Failed != 0 || f.sourceCalls.Load() != 6 || f.rejectedSource.Load() != 3 {
		t.Fatalf("old credential backoff suppressed refreshed metadata job: state=%+v sources=%d rejected=%d", state, f.sourceCalls.Load(), f.rejectedSource.Load())
	}
}
