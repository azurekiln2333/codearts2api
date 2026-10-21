package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/pool"
)

// The background model probe must hold an owned lease, not a name-based lock:
// after an administrative remove plus a fresh login the replacement account is a
// different object, and a name-based release would decrement the replacement's
// active count — silently freeing a slot that a real chat request is using and
// allowing per-account capacity to be exceeded.
func TestProbeLeaseSurvivesAccountRemoveAndReAdd(t *testing.T) {
	releaseProbe := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, lifecycleSuccess)
		w.(http.Flusher).Flush()
		select {
		case <-releaseProbe:
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()

	h, p := lifecycleHandler(t, 1, backend.URL)
	acct := p.Accounts()[0]
	probeDone := make(chan struct{})
	go func() {
		defer close(probeDone)
		h.probeModel(acct, "glm-5.2")
	}()
	waitLifecycle(t, func() bool { return p.Busy(acct.Name) }, "probe did not take an owned slot")

	// Admin removes the account; the user logs in again. The replacement has the
	// same name and UID but is a different *pool.Account.
	reloaded := auth.New(acct.UID, acct.UserName, "domain", "new-token", "ak", "sk",
		time.Now().Add(time.Hour).Format(time.RFC3339), "", "verifier")
	p.SyncToDir(nil)
	replacement := p.AddAccount(reloaded)
	if replacement == acct {
		t.Fatal("remove plus re-add reused the old account object")
	}
	// A real chat request now owns the replacement account's only slot.
	lease, err := p.AcquireAccount(context.Background(), []pool.AccountCandidate{{Name: acct.Name}}, 0)
	if err != nil {
		t.Fatalf("real request could not acquire the replacement slot: %v", err)
	}
	chatOwns := true
	defer func() {
		if chatOwns {
			lease.Release()
		}
	}()

	close(releaseProbe)
	select {
	case <-probeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("probe did not finish")
	}
	if !p.Busy(acct.Name) {
		t.Fatal("probe release freed a slot owned by another request on the replacement account")
	}
	if _, err := p.AcquireAccount(context.Background(), []pool.AccountCandidate{{Name: acct.Name}}, 0); err == nil {
		t.Fatal("per-account capacity was exceeded while the real request still held its slot")
	}
	lease.Release()
	chatOwns = false
	if p.Busy(acct.Name) {
		t.Fatal("slot leak after the real request finished")
	}
	assertLifecycleHealthy(t, p)
}

// A probe that loses the derived availability decision must not mark the model
// usable/unusable for a different account object; this is the observable half of
// the same ownership contract and guards the lease wiring above.
func TestProbeRecordsAvailabilityOnlyForItsOwnAccount(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.TrimSpace(lifecycleSuccess)+"\n")
	}))
	defer backend.Close()
	h, p := lifecycleHandler(t, 1, backend.URL)
	acct := p.Accounts()[0]
	h.probeModel(acct, "glm-5.2")
	if state, _ := modelAvailability(acct.UID, "glm-5.2"); state != availUsable {
		t.Fatalf("probe did not record availability: state=%v", state)
	}
	if p.Busy(acct.Name) {
		t.Fatal("probe leaked its owned slot")
	}
}
