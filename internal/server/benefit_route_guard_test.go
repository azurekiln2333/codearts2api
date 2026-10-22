package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/upstream"
)

func TestCatalogBenefitDenialDoesNotRetryOnNormalRoute(t *testing.T) {
	f, backend := newCatalogFixture(t)
	h, p := catalogHandler(t, 1, f, backend)
	refreshCatalog(t, h)
	if result := waitCatalog(t, h); result.State != "completed" {
		t.Fatalf("fixture benefit discovery failed: %+v", result)
	}
	account := p.Accounts()[0]
	if !upstream.IsBenefitModelForAuth(account.UID, f.model, account.AuthSnapshot()) {
		t.Fatal("fixture did not establish a benefit route")
	}
	var chatCalls atomic.Int32
	var ordinaryRouteUsed atomic.Bool
	chatBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chatCalls.Add(1)
		if r.Header.Get(upstream.HeaderMaasType) != upstream.MaasBenefit {
			ordinaryRouteUsed.Store(true)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"InferHub.4004.200 benefit not found"}`)
	}))
	defer chatBackend.Close()
	account.Client = upstream.NewWithChatEndpoint(time.Second, chatBackend.URL+"/chat")
	chat := func() int {
		return catalogRequest(h, http.MethodPost, "/v1/chat/completions", `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"synthetic prompt"}]}`, "fixture-key").Code
	}
	if status := chat(); status != http.StatusNotFound || chatCalls.Load() != 1 || ordinaryRouteUsed.Load() || p.Busy(account.Name) {
		t.Fatalf("benefit denial was hidden, retried on another route, or retained its lease: status=%d calls=%d ordinary_route=%v", status, chatCalls.Load(), ordinaryRouteUsed.Load())
	}
	if status := chat(); status != http.StatusNotFound || chatCalls.Load() != 1 || !upstream.IsBenefitModelForAuth(account.UID, f.model, account.AuthSnapshot()) {
		t.Fatal("genuine benefit refusal did not retain its negative cache and source route")
	}
}
