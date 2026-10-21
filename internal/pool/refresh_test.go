package pool

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/upstream"
)

func refreshFixture(t *testing.T, handler http.HandlerFunc, timeout time.Duration) (*Pool, *Account, string) {
	t.Helper()
	sts := httptest.NewServer(handler)
	t.Cleanup(sts.Close)
	jwk, err := upstream.NewDPoPPrivateJWK()
	if err != nil {
		t.Fatal(err)
	}
	a := auth.New("refresh-one", "user", "domain", "old-token", "ak", "sk", time.Now().Add(20*time.Minute).Format(time.RFC3339), "old-refresh", "verifier", map[string]string(jwk))
	a.SetClientID("original-oauth-client")
	dir := t.TempDir()
	if err := auth.SaveNew(dir, a); err != nil {
		t.Fatal(err)
	}
	login := upstream.DefaultLoginConfig()
	login.STSHost = sts.URL
	login.ClientID = "new-configured-client"
	p, err := New([]*auth.Auth{a}, Config{RefreshSkew: time.Hour, UpstreamTimeout: timeout, LoginConfig: login}, "")
	if err != nil {
		t.Fatal(err)
	}
	return p, p.Get("refresh-one"), dir
}

func writeRefreshSuccess(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"refresh_token":"rotated-refresh","credentials":{"security_token":"rotated-token","access_key_id":"rotated-ak","secret_access_key":"rotated-sk","expiration":%q}}`, time.Now().Add(3*time.Hour).Format(time.RFC3339))
}

func TestRefreshCanceledWaiterReleasesSlotAndPersistsRotatedToken(t *testing.T) {
	started := make(chan struct{})
	providerRelease := make(chan struct{})
	var releaseOnce sync.Once
	releaseProvider := func() { releaseOnce.Do(func() { close(providerRelease) }) }
	defer releaseProvider()
	var requests atomic.Int32
	clientIDs := make(chan string, 1)
	p, account, dir := refreshFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			_ = r.ParseForm()
			clientIDs <- r.PostForm.Get("client_id")
			close(started)
		}
		select {
		case <-providerRelease:
			writeRefreshSuccess(w)
		case <-r.Context().Done():
		}
	}, 3*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lease := acquireForTest(t, p, account.Name)
	first := make(chan error, 1)
	go func() {
		defer lease.Release()
		_, err := p.ValidateContext(ctx, account)
		first <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	if got := <-clientIDs; got != "original-oauth-client" {
		t.Fatalf("refresh client_id=%q, want original OAuth identity", got)
	}
	cancel()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled validation error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled refresh waiter was blocked behind the network")
	}
	// Wait for the deferred lease release in the validation goroutine.
	lease.Release()
	if p.Busy(account.Name) {
		t.Fatal("canceled refresh waiter leaked its active slot")
	}
	statsDone := make(chan struct{})
	go func() { p.Stats(); close(statsDone) }()
	select {
	case <-statsDone:
	case <-time.After(time.Second):
		t.Fatal("refresh network call held the pool mutex")
	}
	_, healthy, disabled, cooling, _ := p.Stats()
	if healthy != 1 || disabled != 0 || cooling != 0 {
		t.Fatalf("canceled caller changed health: healthy=%d disabled=%d cooling=%d", healthy, disabled, cooling)
	}
	second := make(chan error, 1)
	go func() {
		ok, err := p.ValidateContext(context.Background(), account)
		if err == nil && !ok {
			err = errors.New("shared validation returned false")
		}
		second <- err
	}()
	releaseProvider()
	select {
	case err := <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shared refresh did not finish")
	}
	if requests.Load() != 1 {
		t.Fatalf("provider requests=%d, want one shared exchange", requests.Load())
	}
	if got := account.AuthSnapshot().Refresh(); got != "rotated-refresh" {
		t.Fatalf("in-memory refresh token=%q", got)
	}
	saved, err := auth.LoadDir(dir)
	if err != nil || len(saved) != 1 {
		t.Fatalf("load persisted auth: count=%d err=%v", len(saved), err)
	}
	if saved[0].Refresh() != "rotated-refresh" || saved[0].Token() != "rotated-token" {
		t.Fatal("canceled caller lost provider-rotated credentials on disk")
	}
}

func TestSharedRefreshIsBoundedAndCancellationDoesNotDisableAccount(t *testing.T) {
	started := make(chan struct{}, 1)
	serverRelease := make(chan struct{})
	defer close(serverRelease)
	p, account, _ := refreshFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-serverRelease:
		}
	}, 40*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := p.ValidateContext(ctx, account)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	account.mu.Lock()
	flight := account.refresh
	account.mu.Unlock()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled refresh waiter error=%v", err)
	}
	select {
	case <-flight.done:
		if !errors.Is(flight.err, context.DeadlineExceeded) {
			t.Fatalf("shared refresh expiry error=%v", flight.err)
		}
	case <-time.After(time.Second):
		t.Fatal("shared refresh exceeded its independent timeout")
	}
	_, healthy, disabled, cooling, _ := p.Stats()
	if healthy != 1 || disabled != 0 || cooling != 0 {
		t.Fatalf("failed shared refresh changed account health: %d %d %d", healthy, disabled, cooling)
	}
}

func TestRefreshReloadPreservesCurrentLoginIdentity(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("new-login=%t", changed), func(t *testing.T) {
			started := make(chan struct{})
			providerRelease := make(chan struct{})
			var once sync.Once
			releaseProvider := func() { once.Do(func() { close(providerRelease) }) }
			defer releaseProvider()
			p, account, dir := refreshFixture(t, func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-providerRelease
				writeRefreshSuccess(w)
			}, 3*time.Second)
			result := make(chan error, 1)
			go func() { result <- p.RefreshTokenContext(context.Background(), account.Name) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("refresh did not start")
			}
			reloaded, err := auth.LoadDir(dir)
			if err != nil || len(reloaded) != 1 {
				t.Fatalf("reload auth: count=%d err=%v", len(reloaded), err)
			}
			if changed {
				if err := reloaded[0].UpdateCredentials("new-login-token", "new-ak", "new-sk", time.Now().Add(4*time.Hour).Format(time.RFC3339), "new-login-refresh"); err != nil {
					t.Fatal(err)
				}
			}
			p.SyncToDir(reloaded)
			if account != p.Get(account.Name) {
				t.Fatal("normal reload replaced account object")
			}
			releaseProvider()
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("refresh did not finish after reload")
			}
			want := "rotated-refresh"
			if changed {
				want = "new-login-refresh"
			}
			if got := account.AuthSnapshot().Refresh(); got != want {
				t.Fatalf("current login refresh=%q, want %q", got, want)
			}
			persisted, err := auth.LoadDir(dir)
			if err != nil || len(persisted) != 1 || persisted[0].Refresh() != want {
				t.Fatalf("current login persistence mismatch: err=%v", err)
			}
		})
	}
}

func TestRefreshDoesNotUndoConcurrentDisable(t *testing.T) {
	started := make(chan struct{})
	providerRelease := make(chan struct{})
	var once sync.Once
	releaseProvider := func() { once.Do(func() { close(providerRelease) }) }
	defer releaseProvider()
	p, account, _ := refreshFixture(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-providerRelease
		writeRefreshSuccess(w)
	}, 3*time.Second)
	result := make(chan error, 1)
	go func() { result <- p.RefreshTokenContext(context.Background(), account.Name) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	p.Disable(account.Name, "manual disable while refreshing")
	releaseProvider()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not finish")
	}
	if p.Enabled(account.Name) {
		t.Fatal("refresh undid a concurrent administrative disable")
	}
	if account.AuthSnapshot().Refresh() != "rotated-refresh" {
		t.Fatal("disabled account lost the provider-rotated credentials")
	}
}

func TestValidateExpiredTokenDoesNotUseValidationCache(t *testing.T) {
	a := auth.New("expired", "user", "domain", "token", "ak", "sk", time.Now().Add(-time.Minute).Format(time.RFC3339), "", "verifier")
	p, err := New([]*auth.Auth{a}, Config{}, "")
	if err != nil {
		t.Fatal(err)
	}
	account := p.Get("expired")
	account.mu.Lock()
	account.lastValidated = time.Now()
	account.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ok, err := p.ValidateContext(ctx, account); ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled expired validation: ok=%t err=%v", ok, err)
	}
	if !p.Enabled("expired") {
		t.Fatal("canceled validation disabled the account")
	}
	if ok, err := p.ValidateContext(context.Background(), account); ok || err != nil {
		t.Fatalf("expired cached validation: ok=%t err=%v", ok, err)
	}
	if p.Enabled("expired") {
		t.Fatal("expired token without refresh stayed enabled through cache")
	}
}

func TestUpstreamTimeoutPropagatesToInitialAddAndReloadClients(t *testing.T) {
	serverRelease := make(chan struct{})
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		select {
		case <-r.Context().Done():
		case <-serverRelease:
		}
	}))
	defer sts.Close()
	defer close(serverRelease)
	jwk, err := upstream.NewDPoPPrivateJWK()
	if err != nil {
		t.Fatal(err)
	}
	login := upstream.DefaultLoginConfig()
	login.STSHost = sts.URL
	for _, mode := range []string{"initial", "add", "reload"} {
		t.Run(mode, func(t *testing.T) {
			a := auth.New(mode, mode, "domain", "token", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "refresh", "verifier", map[string]string(jwk))
			var initial []*auth.Auth
			if mode == "initial" {
				initial = []*auth.Auth{a}
			}
			p, err := New(initial, Config{UpstreamTimeout: 30 * time.Millisecond}, "")
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "add":
				p.AddAccount(a)
			case "reload":
				p.SyncToDir([]*auth.Auth{a})
			}
			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err = p.Get(mode).Client.RefreshToken(ctx, login, a.Refresh(), a.Verifier(), jwk)
			if err == nil || time.Since(start) > 500*time.Millisecond {
				t.Fatalf("%s client ignored timeout: elapsed=%s err=%v", mode, time.Since(start), err)
			}
		})
	}
}
