package server

import (
	"encoding/json"
	"fmt"
	"io"
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

// capabilityBackend answers per account path suffix: every account except
// `serving` returns a CodeArts "model not registered" capability failure, which
// is an account/model capability problem, not an account fault.
func capabilityBackend(serving int, hits *[8]atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var index int
		_, _ = fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/"), "%d", &index)
		if index >= 0 && index < len(hits) {
			hits[index].Add(1)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
		if index != serving {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"InferHub.002002009.404 The model is not registered, please request other model"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
		w.(http.Flusher).Flush()
	}))
}

func capabilityHandler(t *testing.T, accounts int, backend *httptest.Server, maxRotate int) (*Handler, *pool.Pool) {
	t.Helper()
	var auths []*auth.Auth
	sequence := lifecycleSequence.Add(1)
	for i := 0; i < accounts; i++ {
		id := fmt.Sprintf("%s-%d-%d", t.Name(), sequence, i)
		auths = append(auths, auth.New(id, "tester", "domain", "token", "ak", "sk",
			time.Now().Add(time.Hour).Format(time.RFC3339), "", ""))
	}
	p, err := pool.New(auths, pool.Config{MaxConcurrent: 1, ErrThreshold: 1}, "")
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range p.Accounts() {
		a.Client = upstream.NewWithChatEndpoint(time.Second, fmt.Sprintf("%s/%d", backend.URL, i))
	}
	return NewHandler(Config{
		Pool: p, DefaultModel: "glm-5.2", MaxRotate: maxRotate,
		QueueMaxAttempts: 1, ErrThreshold: 1,
		StartupTimeout: 10 * time.Second, AccountWaitTimeout: 5 * time.Second,
		StreamWriteTimeout: 5 * time.Second,
	}), p
}

// A capability rejection must not consume the generic rotation budget: with more
// eligible accounts than MaxRotate, a model served only by a later account must
// still succeed. Each subtest uses a fresh model id because availability
// negatives are cached process-wide per account|model.
func TestChatCapabilityRejectionDoesNotConsumeRotationBudget(t *testing.T) {
	var hits [8]atomic.Int32
	backend := capabilityBackend(4, &hits)
	defer backend.Close()

	// Five accounts, only the fifth can serve. MaxRotate=3 is the production
	// default: three capability refusals must not end the request.
	h, p := capabilityHandler(t, 5, backend, 3)
	front := httptest.NewServer(h)
	defer front.Close()

	model := "only-on-acct-4-rot3"
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":false}`, model)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("capability refusals must not exhaust MaxRotate: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (%s)", err, rec.Body.String())
	}
	if len(body.Choices) == 0 || body.Choices[0].Message.Content != "ok" {
		t.Fatalf("unexpected completion: %s", rec.Body.String())
	}
	if hits[4].Load() == 0 {
		t.Fatal("the only capable account was never reached")
	}
	assertLifecycleHealthy(t, p)
}

// The truthful 404 is only allowed once every eligible candidate was evaluated.
func TestChatModelUnavailableOnlyAfterAllCandidatesEvaluated(t *testing.T) {
	var hits [8]atomic.Int32
	backend := capabilityBackend(-1, &hits) // nothing can serve
	defer backend.Close()

	h, p := capabilityHandler(t, 5, backend, 3)
	model := "nobody-serves-this-rot3"
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":false}`, model)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected a truthful 404 after evaluating all candidates, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "is not available on eligible configured accounts") {
		t.Fatalf("unexpected 404 wording: %s", rec.Body.String())
	}
	for i := 0; i < 5; i++ {
		if hits[i].Load() == 0 {
			t.Fatalf("account %d was never evaluated but the response claims unavailability", i)
		}
	}
	// Capability failures are not account faults: no cooldown, no error count.
	assertLifecycleHealthy(t, p)
}

// Generic provider failures keep their existing bound: a 5xx still consumes the
// rotation budget, so the exemption cannot silently widen retries.
func TestChatGenericFailuresStillBoundedByMaxRotate(t *testing.T) {
	var hits [8]atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var index int
		_, _ = fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/"), "%d", &index)
		if index >= 0 && index < len(hits) {
			hits[index].Add(1)
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"upstream exploded"}`)
	}))
	defer backend.Close()

	h, _ := capabilityHandler(t, 5, backend, 3)
	model := "generic-5xx-rot3"
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":false}`, model)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("5xx must not succeed")
	}
	total := int32(0)
	for i := 0; i < 5; i++ {
		total += hits[i].Load()
	}
	if total != 3 {
		t.Fatalf("generic failures must stay bounded by MaxRotate=3, saw %d attempts", total)
	}
}
