package upstream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/auth"
)

func writeBackoffFixtureSuccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case EpAgentList:
		_, _ = io.WriteString(w, `{"agents":[]}`)
	case EpModelBuiltin:
		_, _ = io.WriteString(w, `{"builtinModels":[]}`)
	case EpBenefitGatewayConfig:
		_, _ = io.WriteString(w, `{"enabled":false}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func rotateBackoffFixture(t *testing.T, a *auth.Auth, token, ak, sk string) {
	t.Helper()
	if err := a.UpdateCredentials(token, ak, sk, time.Now().Add(time.Hour).Format(time.RFC3339), "fixture-refresh"); err != nil {
		t.Fatal(err)
	}
}

func newBackoffFixtureAccount(t *testing.T) *auth.Auth {
	t.Helper()
	a := fixtureAccount(t)
	a.UserID = strings.ReplaceAll(a.UserID, "/", "-")
	if err := auth.SaveNew(t.TempDir(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestDiscoveryBackoffBelongsToSigningCredentialGeneration(t *testing.T) {
	for _, changed := range []string{"token", "ak", "sk"} {
		t.Run(changed, func(t *testing.T) {
			var calls atomic.Int32
			var recoverSource atomic.Bool
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if !recoverSource.Load() {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				writeBackoffFixtureSuccess(w, r)
			}))
			defer s.Close()
			c := NewWithModelEndpoints(time.Second, s.URL, s.URL)
			a := newBackoffFixtureAccount(t)
			assertFailed := func(force bool, wantCalls int32) {
				t.Helper()
				d := c.DiscoverModels(context.Background(), a, force)
				if d.FailedSources() != 3 || calls.Load() != wantCalls {
					t.Fatalf("failed/backoff discovery force=%v successes=%d calls=%d want=%d", force, d.Successes(), calls.Load(), wantCalls)
				}
				for _, source := range d.sources {
					if source.RetryAt == nil || !source.RetryAt.After(time.Now()) {
						t.Fatalf("source failure lost bounded retry: %+v", source.SourceStatus)
					}
				}
			}
			assertFailed(false, 3)
			assertFailed(false, 3)
			token, ak, sk := a.Credentials()
			switch changed {
			case "token":
				token = "next-token"
			case "ak":
				ak = "next-ak"
			case "sk":
				sk = "next-sk"
			}
			rotateBackoffFixture(t, a, token, ak, sk)
			assertFailed(false, 6)
			assertFailed(false, 6)
			assertFailed(true, 9)
			recoverSource.Store(true)
			if d := c.DiscoverModels(context.Background(), a, true); d.Successes() != 3 || calls.Load() != 12 {
				t.Fatalf("force did not recover sources: successes=%d calls=%d", d.Successes(), calls.Load())
			}
			if d := c.DiscoverModels(context.Background(), a, false); d.Successes() != 3 || calls.Load() != 15 {
				t.Fatalf("success failed to clear generation backoff: successes=%d calls=%d", d.Successes(), calls.Load())
			}
		})
	}
}

func TestDiscoveryLateOldGenerationCannotChangeCurrentBackoff(t *testing.T) {
	for _, replaceOwner := range []bool{false, true} {
		for _, oldStatus := range []int{http.StatusOK, http.StatusServiceUnavailable} {
			t.Run(fmt.Sprintf("replace=%v/old=%d", replaceOwner, oldStatus), func(t *testing.T) {
				var newCalls atomic.Int32
				started, release := make(chan struct{}), make(chan struct{})
				var startedOnce, releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-Security-Token") == "fixture-token" {
						if r.URL.Path == EpAgentList {
							startedOnce.Do(func() { close(started) })
							select {
							case <-release:
							case <-r.Context().Done():
								return
							}
						}
						if oldStatus != http.StatusOK {
							w.WriteHeader(oldStatus)
							return
						}
						writeBackoffFixtureSuccess(w, r)
						return
					}
					newCalls.Add(1)
					w.WriteHeader(http.StatusServiceUnavailable)
				}))
				defer s.Close()
				defer unblock()
				c := NewWithModelEndpoints(time.Second, s.URL, s.URL)
				oldOwner := newBackoffFixtureAccount(t)
				oldDone := make(chan ModelDiscovery, 1)
				go func() { oldDone <- c.DiscoverModels(context.Background(), oldOwner, false) }()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("old discovery did not reach blocked HTTP boundary")
				}
				current := oldOwner
				if replaceOwner {
					current = &auth.Auth{UserID: oldOwner.UserID, CloudDragonTok: "next-token", AccessKeyID: "next-ak", SecretAccessKey: "next-sk"}
				} else {
					rotateBackoffFixture(t, current, "next-token", "next-ak", "next-sk")
				}
				if d := c.DiscoverModels(context.Background(), current, false); d.FailedSources() != 3 || newCalls.Load() != 3 {
					t.Fatalf("new generation did not attempt every source: successes=%d calls=%d", d.Successes(), newCalls.Load())
				}
				unblock()
				select {
				case <-oldDone:
				case <-time.After(time.Second):
					t.Fatal("old discovery did not finish")
				}
				if d := c.DiscoverModels(context.Background(), current, false); d.FailedSources() != 3 || newCalls.Load() != 3 {
					t.Fatalf("late old result changed fresh failure backoff: successes=%d calls=%d", d.Successes(), newCalls.Load())
				}
			})
		}
	}
}

func TestDiscoveryBackoffReplacementOwnerAndBoundedPruning(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer s.Close()
	c := NewWithModelEndpoints(time.Second, s.URL, s.URL)
	a := newBackoffFixtureAccount(t)
	c.DiscoverModels(context.Background(), a, false)
	// A replacement auth object must not inherit failure even if its STS is equal.
	token, ak, sk := a.Credentials()
	b := &auth.Auth{UserID: a.UserID, CloudDragonTok: token, AccessKeyID: ak, SecretAccessKey: sk}
	c.DiscoverModels(context.Background(), b, false)
	if calls.Load() != 6 {
		t.Fatalf("replacement login inherited backoff: calls=%d", calls.Load())
	}
	for i := 0; i < 8; i++ {
		rotateBackoffFixture(t, a, fmt.Sprintf("token-%d", i), ak, sk)
		c.DiscoverModels(context.Background(), a, false)
	}
	if len(c.srcFail) != 1 {
		t.Fatalf("rotations accumulated obsolete generations: retained=%d", len(c.srcFail))
	}
	state := c.srcFail[a.UserID]
	state.retryAt[0] = time.Now().Add(-time.Second)
	c.PruneModelDiscoveryBackoff(map[string]*auth.Auth{a.UserID: a})
	if !c.modelSourceRetry(a.UserID, state, 0).IsZero() || c.modelSourceRetry(a.UserID, state, 1).IsZero() {
		t.Fatal("prune did not clear only expired source retries")
	}
	c.PruneModelDiscoveryBackoff(map[string]*auth.Auth{a.UserID: b})
	if len(c.srcFail) != 0 {
		t.Fatal("prune retained previous login's generation")
	}
	c.DiscoverModels(context.Background(), b, false)
	c.PruneModelDiscoveryBackoff(nil)
	if len(c.srcFail) != 0 {
		t.Fatal("prune retained removed account")
	}
}
