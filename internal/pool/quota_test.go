package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/upstream"
)

func quotaFixtureAuth(uid, token string) *auth.Auth {
	return auth.New(uid, uid, "", token, "fixture-ak", "fixture-sk",
		time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "", "")
}

func quotaFixturePool(t *testing.T, endpoint string, creds ...*auth.Auth) *Pool {
	t.Helper()
	p, err := New(creds, Config{UpstreamTimeout: time.Second}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, acct := range p.Accounts() {
		acct.Client = upstream.NewWithModelEndpoints(time.Second, endpoint, "")
	}
	return p
}

func quotaWait(t *testing.T, p *Pool) QuotaSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := p.QuotaSnapshot()
		if !snapshot.Refresh.Running && snapshot.Refresh.FinishedAt != nil {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("quota refresh did not finish")
	return QuotaSnapshot{}
}

func quotaRetry(t *testing.T, p *Pool) QuotaSnapshot {
	t.Helper()
	p.quota.mu.Lock()
	p.quota.nextAttempt = time.Time{}
	p.quota.mu.Unlock()
	p.RequestQuotaRefresh()
	return quotaWait(t, p)
}

func TestQuotaSnapshotCoverageAndFailureIsolation(t *testing.T) {
	var mode atomic.Int32
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != upstream.EpCreditStatistics {
			t.Errorf("unexpected provider request: %s", r.URL.Path)
		}
		if r.Header.Get("X-Security-Token") == "b" {
			switch mode.Load() {
			case 1:
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("secret-provider-body"))
				return
			case 2:
				_, _ = w.Write([]byte(`{"metrics":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":80,"package_credit_used":80,"package_credit_remain":0}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":100,"package_credit_used":40,"package_credit_remain":60}]}`))
	}))
	defer server.Close()
	p := quotaFixturePool(t, server.URL, quotaFixtureAuth("a", "a"), quotaFixtureAuth("a", "duplicate"), quotaFixtureAuth("b", "b"))
	before := p.QuotaSnapshot()
	if before.State != "unknown" || before.Remaining != nil || calls.Load() != 0 {
		t.Fatalf("snapshot should be cache-only and unknown: %+v", before)
	}
	if credit, ok := p.List()[0]["credits"].(*float64); !ok || credit != nil {
		t.Fatalf("unknown account credit is not null: %#v", p.List()[0]["credits"])
	}
	p.RequestQuotaRefresh()
	got := quotaWait(t, p)
	if !got.Complete || got.State != "ready" || got.ConfiguredAccounts != 3 || got.CountedAccounts != 2 || got.DuplicateAccounts != 1 ||
		got.Remaining == nil || *got.Remaining != 60 || got.Total == nil || *got.Total != 180 || got.Used == nil || *got.Used != 120 || !got.OverlapPossible || calls.Load() != 2 {
		t.Fatalf("complete aggregate: %+v calls=%d", got, calls.Load())
	}
	for _, item := range p.List() {
		if item["uid"] == "b" {
			credit, ok := item["credits"].(*float64)
			if !ok || credit == nil || *credit != 0 {
				t.Fatalf("known zero missing from account list: %#v", item["credits"])
			}
		}
	}
	// The returned pointers do not alias cached values.
	*got.Accounts[0].Quota.Remaining = 999
	if next := p.QuotaSnapshot(); *next.Remaining != 60 {
		t.Fatalf("caller mutated cached quota: %+v", next)
	}
	mode.Store(1)
	failed := quotaRetry(t, p)
	if failed.Complete || failed.State != "partial" || failed.Remaining != nil || failed.KnownRemaining == nil || *failed.KnownRemaining != 60 || failed.FailedAccounts != 1 || failed.Accounts[1].Quota == nil || failed.Accounts[1].Error != "http" {
		t.Fatalf("partial failure: %+v", failed)
	}
	encoded, err := json.Marshal(failed)
	if err != nil || strings.Contains(string(encoded), "secret-provider-body") || strings.Contains(string(encoded), "fixture-sk") {
		t.Fatalf("unsafe snapshot: %s err=%v", encoded, err)
	}
	_, healthy, disabled, cooling, _ := p.Stats()
	if healthy != 3 || disabled != 0 || cooling != 0 {
		t.Fatalf("quota failure changed health: healthy=%d disabled=%d cooling=%d", healthy, disabled, cooling)
	}
	mode.Store(2)
	unavailable := quotaRetry(t, p)
	if unavailable.Complete || unavailable.Remaining != nil || unavailable.UnavailableAccounts != 1 || unavailable.Accounts[1].State != "unavailable" {
		t.Fatalf("missing metric treated as zero: %+v", unavailable)
	}
}

func TestQuotaUnlimitedAggregationAndFailures(t *testing.T) {
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Security-Token") == "a" {
			switch mode.Load() {
			case 2:
				_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-1}]}`))
			case 4:
				_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-1,"package_credit_remain":-1}]}`))
			default:
				_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-1,"package_credit_used":4}]}`))
			}
			return
		}
		switch mode.Load() {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 3:
			_, _ = w.Write([]byte(`{"metrics":[]}`))
		default:
			_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":2,"package_credit_used":2,"package_credit_remain":0}]}`))
		}
	}))
	defer server.Close()
	p := quotaFixturePool(t, server.URL, quotaFixtureAuth("a", "a"), quotaFixtureAuth("b", "b"))
	p.RequestQuotaRefresh()
	got := quotaWait(t, p)
	if !got.Complete || !got.Unlimited || got.State != "ready" || got.Total != nil || got.Remaining != nil || got.KnownRemaining != nil || got.Used == nil || *got.Used != 6 || got.ReadyAccounts != 2 {
		t.Fatalf("mixed unlimited and finite zero: %+v", got)
	}
	if got.Accounts[0].Quota == nil || !got.Accounts[0].Quota.Unlimited {
		t.Fatalf("account lost unlimited state: %+v", got.Accounts)
	}
	p.Disable("a", "manual")
	if disabled := p.QuotaSnapshot(); !disabled.Complete || !disabled.Unlimited || disabled.Used == nil || *disabled.Used != 6 {
		t.Fatalf("disabled account quota collection semantics changed: %+v", disabled)
	}
	p.Enable("a")
	mode.Store(1)
	got = quotaRetry(t, p)
	if got.Complete || got.Unlimited || got.State != "partial" || got.KnownRemaining != nil || got.Total != nil || got.Remaining != nil || got.FailedAccounts != 1 {
		t.Fatalf("partial failure exposed an aggregate: %+v", got)
	}
	mode.Store(2)
	got = quotaRetry(t, p)
	if !got.Complete || !got.Unlimited || got.Used != nil {
		t.Fatalf("unknown used credits were fabricated: %+v", got)
	}
	mode.Store(3)
	got = quotaRetry(t, p)
	if got.Complete || got.Unlimited || got.KnownRemaining != nil || got.UnavailableAccounts != 1 {
		t.Fatalf("unavailable account exposed unlimited aggregate: %+v", got)
	}
	mode.Store(4)
	got = quotaRetry(t, p)
	if got.Complete || got.Unlimited || got.FailedAccounts != 1 || got.KnownRemaining == nil || *got.KnownRemaining != 0 {
		t.Fatalf("invalid sentinel changed valid finite zero: %+v", got)
	}
	mode.Store(0)
	got = quotaRetry(t, p)
	if !got.Complete || !got.Unlimited {
		t.Fatalf("unlimited did not recover: %+v", got)
	}
	account := p.Get("a")
	p.quota.mu.Lock()
	entry := p.quota.entries[account]
	entry.successAt = time.Now().Add(-quotaMaxAge - time.Second)
	p.quota.entries[account] = entry
	p.quota.mu.Unlock()
	got = p.QuotaSnapshot()
	if got.Complete || got.Unlimited || got.StaleAccounts != 1 || got.KnownRemaining == nil || *got.KnownRemaining != 0 {
		t.Fatalf("stale unlimited account exposed aggregate: %+v", got)
	}
}

func TestQuotaUnlimitedWithUnknownAccount(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Security-Token") == "b" {
			close(started)
			<-release
			_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":1}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_amount":-1}]}`))
	}))
	defer server.Close()
	p := quotaFixturePool(t, server.URL, quotaFixtureAuth("a", "a"), quotaFixtureAuth("b", "b"))
	p.RequestQuotaRefresh()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("second account did not start")
	}
	deadline := time.Now().Add(3 * time.Second)
	var partial QuotaSnapshot
	for time.Now().Before(deadline) {
		partial = p.QuotaSnapshot()
		if partial.ReadyAccounts == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if partial.ReadyAccounts != 1 || partial.UnknownAccounts != 1 || partial.Complete || partial.Unlimited || partial.Total != nil || partial.Remaining != nil || partial.KnownRemaining != nil {
		t.Fatalf("unknown account exposed unlimited aggregate: %+v", partial)
	}
	if got := quotaWait(t, p); !got.Complete || !got.Unlimited {
		t.Fatalf("completed collection did not expose unlimited: %+v", got)
	}
}

func TestQuotaDuplicateSelectsUsableCredential(t *testing.T) {
	for _, test := range []struct {
		name   string
		modify func(*Account)
	}{
		{name: "expired", modify: func(acct *Account) {
			acct.Auth = auth.New("same", "same", "", "expired", "fixture-ak", "fixture-sk", time.Now().Add(-time.Hour).Format(time.RFC3339), "", "")
		}},
		{name: "missing token", modify: func(acct *Account) {
			acct.Auth = auth.New("same", "same", "", "", "fixture-ak", "fixture-sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", "")
		}},
		{name: "missing access key", modify: func(acct *Account) {
			acct.Auth = auth.New("same", "same", "", "bad", "", "fixture-sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", "")
		}},
		{name: "missing secret key", modify: func(acct *Account) {
			acct.Auth = auth.New("same", "same", "", "bad", "fixture-ak", "", time.Now().Add(time.Hour).Format(time.RFC3339), "", "")
		}},
		{name: "missing auth", modify: func(acct *Account) { acct.Auth = nil }},
		{name: "missing client", modify: func(acct *Account) { acct.Client = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("X-Security-Token") != "valid" {
					t.Errorf("used unavailable duplicate credential")
				}
				_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":7}]}`))
			}))
			defer server.Close()
			p := quotaFixturePool(t, server.URL, quotaFixtureAuth("same", "bad"), quotaFixtureAuth("same", "valid"))
			test.modify(p.accounts[0])
			p.RequestQuotaRefresh()
			got := quotaWait(t, p)
			if !got.Complete || got.CountedAccounts != 1 || got.DuplicateAccounts != 1 ||
				got.Remaining == nil || *got.Remaining != 7 || calls.Load() != 1 {
				t.Fatalf("valid duplicate was not selected: %+v calls=%d", got, calls.Load())
			}
		})
	}
}

func TestQuotaDuplicateAuthFailureAndCredentialGeneration(t *testing.T) {
	var badCalls, goodCalls, recoveredCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Security-Token") {
		case "bad":
			badCalls.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		case "good":
			goodCalls.Add(1)
			_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":7}]}`))
		case "recovered":
			recoveredCalls.Add(1)
			_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":9}]}`))
		default:
			t.Error("unexpected synthetic credential")
		}
	}))
	defer server.Close()
	bad := quotaFixtureAuth("same", "bad")
	bad.SetPath(filepath.Join(t.TempDir(), "synthetic.json"))
	p := quotaFixturePool(t, server.URL, bad, quotaFixtureAuth("same", "good"))
	p.RequestQuotaRefresh()
	first := quotaWait(t, p)
	if first.Complete || badCalls.Load() != 1 || goodCalls.Load() != 0 {
		t.Fatalf("first attempt did not record the auth rejection: %+v", first)
	}
	good := quotaRetry(t, p)
	if !good.Complete || good.Remaining == nil || *good.Remaining != 7 || goodCalls.Load() != 1 {
		t.Fatalf("valid duplicate did not recover the balance: %+v", good)
	}
	good = quotaRetry(t, p)
	if !good.Complete || goodCalls.Load() != 2 || badCalls.Load() != 1 {
		t.Fatalf("rejected duplicate was repeatedly selected: %+v", good)
	}
	if err := bad.UpdateCredentials("recovered", "fixture-ak", "fixture-sk", time.Now().Add(time.Hour).Format(time.RFC3339), ""); err != nil {
		t.Fatal(err)
	}
	changed := p.QuotaSnapshot()
	if changed.Complete || changed.Remaining != nil || changed.KnownRemaining != nil {
		t.Fatalf("previous duplicate generation leaked into the new credential: %+v", changed)
	}
	recovered := quotaRetry(t, p)
	if !recovered.Complete || recovered.Remaining == nil || *recovered.Remaining != 9 || recoveredCalls.Load() != 1 {
		t.Fatalf("updated credential did not get its own balance: %+v", recovered)
	}
}

func TestQuotaSnapshotCredentialExpiryAndStaleness(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":0}]}`))
	}))
	defer server.Close()
	cred := quotaFixtureAuth("a", "a")
	p := quotaFixturePool(t, server.URL, cred)
	p.RequestQuotaRefresh()
	got := quotaWait(t, p)
	if !got.Complete || got.Remaining == nil || *got.Remaining != 0 {
		t.Fatalf("known zero lost: %+v", got)
	}
	p.quota.mu.Lock()
	entry := p.quota.entries[p.accounts[0]]
	entry.successAt = time.Now().Add(-quotaMaxAge - time.Second)
	p.quota.entries[p.accounts[0]] = entry
	p.quota.mu.Unlock()
	stale := p.QuotaSnapshot()
	if stale.Complete || stale.Remaining != nil || stale.StaleAccounts != 1 || stale.State != "stale" {
		t.Fatalf("stale value exposed as current: %+v", stale)
	}
	expired := auth.New("a", "a", "", "expired", "fixture-ak", "fixture-sk", time.Now().Add(-time.Hour).Format(time.RFC3339), "", "")
	p.AddAccount(expired)
	expiredSnapshot := quotaRetry(t, p)
	if expiredSnapshot.Complete || expiredSnapshot.Accounts[0].Error != "credential_expired" || calls.Load() != 1 {
		t.Fatalf("expired credential sent or treated as balance: %+v calls=%d", expiredSnapshot, calls.Load())
	}
	_, healthy, disabled, _, _ := p.Stats()
	if healthy != 1 || disabled != 0 {
		t.Fatalf("quota collection changed health: healthy=%d disabled=%d", healthy, disabled)
	}
}

func TestQuotaNaturallyExpiredCredentialInvalidatesCachedBalance(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":7}]}`))
	}))
	defer server.Close()
	expiresAt := time.Now().Add(2 * time.Second)
	cred := auth.New("a", "a", "", "a", "fixture-ak", "fixture-sk", expiresAt.Format(time.RFC3339Nano), "", "")
	p := quotaFixturePool(t, server.URL, cred)
	p.RequestQuotaRefresh()
	initial := quotaWait(t, p)
	if !initial.Complete || initial.Remaining == nil || *initial.Remaining != 7 {
		t.Fatalf("initial measurement missing: %+v", initial)
	}
	if delay := time.Until(expiresAt); delay > 0 {
		time.Sleep(delay + 10*time.Millisecond)
	}
	got := p.QuotaSnapshot()
	if got.Complete || got.Remaining != nil || got.KnownRemaining != nil || got.FailedAccounts != 1 ||
		got.Accounts[0].State != "failed" || got.Accounts[0].Error != "credential_expired" {
		t.Fatalf("expired credential retained a current aggregate: %+v", got)
	}
	credit, ok := p.List()[0]["credits"].(*float64)
	if !ok || credit != nil {
		t.Fatalf("account list retained expired credit: %#v", p.List()[0]["credits"])
	}
	if calls.Load() != 1 {
		t.Fatalf("reading expired quota contacted provider: calls=%d", calls.Load())
	}
}

func TestQuotaTimeoutResumesAtUnprocessedAccounts(t *testing.T) {
	var mode atomic.Int32
	started := make(chan string, 16)
	release := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 1:
			started <- r.Header.Get("X-Security-Token")
			<-r.Context().Done()
			return
		case 2:
			started <- r.Header.Get("X-Security-Token")
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":1}]}`))
	}))
	defer server.Close()
	defer releaseOnce.Do(func() { close(release) })
	var creds []*auth.Auth
	for i := 0; i < 6; i++ {
		uid := fmt.Sprintf("acct-%d", i)
		creds = append(creds, quotaFixtureAuth(uid, uid))
	}
	p := quotaFixturePool(t, server.URL, creds...)
	for _, acct := range p.Accounts() {
		acct.Client = upstream.NewWithModelEndpoints(5*time.Second, server.URL, "")
	}
	p.RequestQuotaRefresh()
	initial := quotaWait(t, p)
	if !initial.Complete || initial.Remaining == nil || *initial.Remaining != 6 {
		t.Fatalf("initial cache not complete: %+v", initial)
	}

	mode.Store(1)
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	targets, pending := p.quotaSweepTargets()
	done := make(chan []int, 1)
	go func() {
		done <- p.collectQuotaBatch(ctx, context.Background(), targets, pending, 5*time.Second)
	}()
	first := make(map[string]bool)
	for i := 0; i < quotaWorkers; i++ {
		select {
		case uid := <-started:
			first[uid] = true
		case <-time.After(3 * time.Second):
			t.Fatal("initial slow requests did not start")
		}
	}
	if !first["acct-0"] || !first["acct-1"] || len(first) != 2 {
		t.Fatalf("unexpected first wave: %v", first)
	}
	select {
	case pending = <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("timed-out quota job did not finish")
	}
	select {
	case uid := <-started:
		t.Fatalf("unstarted request ran after timeout: %s", uid)
	default:
	}
	partial := p.QuotaSnapshot()
	if !partial.Complete || partial.FailedAccounts != 0 || partial.ReadyAccounts != 6 ||
		partial.Remaining == nil || *partial.Remaining != 6 {
		t.Fatalf("batch deadline discarded cached measurements: %+v", partial)
	}
	for _, item := range partial.Accounts {
		if item.State != "ready" || item.Quota == nil || item.Quota.Remaining == nil || *item.Quota.Remaining != 1 {
			t.Fatalf("interrupted or unstarted account lost its cached measurement: %+v", item)
		}
	}

	mode.Store(2)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	done2 := make(chan struct{})
	go func() {
		p.collectQuotaBatch(ctx2, context.Background(), targets, pending, 5*time.Second)
		close(done2)
	}()
	next := make(map[string]bool)
	for i := 0; i < quotaWorkers; i++ {
		select {
		case uid := <-started:
			next[uid] = true
		case <-time.After(3 * time.Second):
			t.Fatal("rotated requests did not start")
		}
	}
	if !next["acct-0"] || !next["acct-1"] || len(next) != 2 {
		t.Fatalf("next job did not resume at interrupted accounts: %v", next)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-done2:
	case <-time.After(5 * time.Second):
		t.Fatal("rotated quota job did not finish")
	}
	final := p.QuotaSnapshot()
	if !final.Complete || final.Remaining == nil || *final.Remaining != 6 {
		t.Fatalf("quota did not recover after rotation: %+v", final)
	}
}

func TestQuotaSweepAutomaticallyFinishesInterruptedAccounts(t *testing.T) {
	var mu sync.Mutex
	calls := make(map[string]int)
	interrupted := make(chan string, 2)
	retried := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := r.Header.Get("X-Security-Token")
		mu.Lock()
		calls[uid]++
		attempt := calls[uid]
		mu.Unlock()
		if uid == "acct-14" || uid == "acct-15" {
			if attempt == 1 {
				<-r.Context().Done()
				interrupted <- uid
				return
			}
			if attempt == 2 {
				retried <- uid
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
		}
		_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":1}]}`))
	}))
	defer server.Close()
	defer releaseOnce.Do(func() { close(release) })
	var creds []*auth.Auth
	for i := 0; i < 18; i++ {
		uid := fmt.Sprintf("acct-%d", i)
		creds = append(creds, quotaFixtureAuth(uid, uid))
	}
	p := quotaFixturePool(t, server.URL, creds...)
	p.mu.Lock()
	targets, _, _ := p.quotaTargetsLocked()
	p.quota.mu.Lock()
	p.quota.entries = make(map[*Account]quotaEntry, len(targets))
	remaining := float64(1)
	for _, target := range targets {
		p.quota.entries[target.account] = quotaEntry{owner: target.owner, fingerprint: target.fingerprint,
			value: upstream.CreditQuota{Available: true, Remaining: &remaining}, hasValue: true,
			attemptedAt: time.Now().Add(-14 * time.Minute), successAt: time.Now().Add(-14 * time.Minute)}
	}
	p.quota.mu.Unlock()
	p.mu.Unlock()

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		p.runQuotaSweep(parent, 400*time.Millisecond, 400*time.Millisecond, 2*time.Second)
		close(done)
	}()
	first := make(map[string]bool)
	for i := 0; i < 2; i++ {
		select {
		case uid := <-interrupted:
			first[uid] = true
		case <-time.After(3 * time.Second):
			t.Fatal("first batch did not interrupt both pending requests")
		}
	}
	if !first["acct-14"] || !first["acct-15"] {
		t.Fatalf("unexpected interrupted accounts: %v", first)
	}
	for i := 0; i < 2; i++ {
		select {
		case uid := <-retried:
			if !first[uid] {
				t.Fatalf("retried unexpected account %q", uid)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("interrupted accounts were not automatically retried")
		}
	}
	before := p.QuotaSnapshot()
	if !before.Complete || before.FailedAccounts != 0 || before.Remaining == nil || *before.Remaining != 18 {
		t.Fatalf("batch deadline lost current cached values: %+v", before)
	}
	// Simulate the five-minute scheduling delay without waiting in real time.
	p.quota.mu.Lock()
	for account, entry := range p.quota.entries {
		entry.successAt = entry.successAt.Add(-5 * time.Minute)
		p.quota.entries[account] = entry
	}
	p.quota.mu.Unlock()
	aged := p.QuotaSnapshot()
	if aged.Complete || aged.StaleAccounts != 4 || aged.ReadyAccounts != 14 {
		t.Fatalf("freshness fixture did not age only interrupted and unstarted accounts: %+v", aged)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("automatic sweep did not finish")
	}
	final := p.QuotaSnapshot()
	if !final.Complete || final.ReadyAccounts != 18 || final.Remaining == nil || *final.Remaining != 18 {
		t.Fatalf("automatic sweep did not restore a complete fresh snapshot: %+v", final)
	}
	mu.Lock()
	defer mu.Unlock()
	for i := 0; i < 18; i++ {
		uid := fmt.Sprintf("acct-%d", i)
		want := 1
		if i == 14 || i == 15 {
			want = 2
		}
		if calls[uid] != want {
			t.Fatalf("unexpected attempts for %s: got %d, want %d", uid, calls[uid], want)
		}
	}
}

func TestQuotaSweepRecordsSingleCallTimeoutWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-Security-Token") == "slow" {
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":7}]}`))
	}))
	defer server.Close()
	p := quotaFixturePool(t, server.URL, quotaFixtureAuth("slow", "slow"), quotaFixtureAuth("good", "good"))
	parent, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.runQuotaSweep(parent, time.Second, time.Second, 50*time.Millisecond)
	got := p.QuotaSnapshot()
	if got.Complete || got.FailedAccounts != 1 || got.ReadyAccounts != 1 ||
		got.Accounts[0].Error != "timeout" || calls.Load() != 2 {
		t.Fatalf("single-call timeout was hidden or retried: %+v calls=%d", got, calls.Load())
	}
}

func TestQuotaSweepStopsWhileWaitingForNextBatch(t *testing.T) {
	var calls atomic.Int32
	aborted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
		aborted <- struct{}{}
	}))
	defer server.Close()
	p := quotaFixturePool(t, server.URL, quotaFixtureAuth("a", "a"))
	parent, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.runQuotaSweep(parent, 50*time.Millisecond, 5*time.Second, 2*time.Second)
		close(done)
	}()
	select {
	case <-aborted:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("batch did not reach its deadline")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel the continuation delay")
	}
	got := p.QuotaSnapshot()
	if got.Complete || got.FailedAccounts != 0 || calls.Load() != 1 {
		t.Fatalf("shutdown published an error or continued the sweep: %+v calls=%d", got, calls.Load())
	}
}

func TestQuotaRefreshCoalescesAndBoundsWorkers(t *testing.T) {
	var calls, active, peak atomic.Int32
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		current := active.Add(1)
		for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		active.Add(-1)
		_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":1}]}`))
	}))
	defer server.Close()
	var creds []*auth.Auth
	for i := 0; i < 6; i++ {
		uid := fmt.Sprintf("acct-%d", i)
		creds = append(creds, quotaFixtureAuth(uid, uid))
	}
	p := quotaFixturePool(t, server.URL, creds...)
	var wg sync.WaitGroup
	for i := 0; i < 18; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.RequestQuotaRefresh() }()
	}
	wg.Wait()
	for i := 0; i < quotaWorkers; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	if calls.Load() != quotaWorkers || peak.Load() != quotaWorkers {
		t.Fatalf("unbounded or missing workers: calls=%d peak=%d", calls.Load(), peak.Load())
	}
	close(release)
	got := quotaWait(t, p)
	if !got.Complete || got.Remaining == nil || *got.Remaining != 6 || calls.Load() != 6 || peak.Load() > quotaWorkers {
		t.Fatalf("refresh was duplicated: %+v calls=%d peak=%d", got, calls.Load(), peak.Load())
	}
	p.RequestQuotaRefresh()
	if calls.Load() != 6 {
		t.Fatalf("refresh throttle did not apply: %d", calls.Load())
	}
}

func TestQuotaOldAccountAndCredentialCannotPublish(t *testing.T) {
	for _, rotate := range []bool{false, true} {
		t.Run(fmt.Sprintf("same_auth_rotation_%t", rotate), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Security-Token") == "old" {
					close(started)
					<-release
					_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":999}]}`))
					return
				}
				_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":7}]}`))
			}))
			defer server.Close()
			old := quotaFixtureAuth("a", "old")
			old.SetPath(filepath.Join(t.TempDir(), "synthetic.json"))
			p := quotaFixturePool(t, server.URL, old)
			p.RequestQuotaRefresh()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("provider was not called")
			}
			if rotate {
				if err := old.UpdateCredentials("new", "fixture-ak", "fixture-sk", time.Now().Add(time.Hour).Format(time.RFC3339), ""); err != nil {
					t.Fatal(err)
				}
			} else {
				p.AddAccount(quotaFixtureAuth("a", "new"))
			}
			close(release)
			got := quotaWait(t, p)
			if got.Complete || got.Remaining != nil || got.Accounts[0].Quota != nil {
				t.Fatalf("old credentials published: %+v", got)
			}
			got = quotaRetry(t, p)
			if !got.Complete || got.Remaining == nil || *got.Remaining != 7 {
				t.Fatalf("new credentials not collected: %+v", got)
			}
		})
	}
}

func TestQuotaRemovedAccountCannotPublish(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Security-Token") == "old" {
			close(started)
			<-release
			_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":999}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"metrics":[{"name":"usageTotalPackageCredit","package_credit_remain":5}]}`))
	}))
	defer server.Close()
	p := quotaFixturePool(t, server.URL, quotaFixtureAuth("a", "old"))
	p.RequestQuotaRefresh()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider was not called")
	}
	p.SyncToDir(nil)
	newAccount := p.AddAccount(quotaFixtureAuth("a", "new"))
	newAccount.Client = upstream.NewWithModelEndpoints(time.Second, server.URL, "")
	close(release)
	got := quotaWait(t, p)
	if got.Complete || got.Accounts[0].Quota != nil {
		t.Fatalf("removed account result published: %+v", got)
	}
	got = quotaRetry(t, p)
	if !got.Complete || got.Remaining == nil || *got.Remaining != 5 {
		t.Fatalf("new account result missing: %+v", got)
	}
}

func TestQuotaAccountLimitNeverPresentsPartialSumAsComplete(t *testing.T) {
	var creds []*auth.Auth
	for i := 0; i <= quotaAccountLimit; i++ {
		uid := fmt.Sprintf("acct-%d", i)
		creds = append(creds, quotaFixtureAuth(uid, uid))
	}
	p := quotaFixturePool(t, "http://127.0.0.1", creds...)
	remaining := float64(1)
	value := upstream.CreditQuota{Available: true, Remaining: &remaining}
	p.mu.Lock()
	targets, _, _ := p.quotaTargetsLocked()
	p.quota.mu.Lock()
	p.quota.entries = make(map[*Account]quotaEntry, len(targets))
	for _, target := range targets {
		p.quota.entries[target.account] = quotaEntry{owner: target.owner, fingerprint: target.fingerprint,
			value: quotaCopyValue(value), hasValue: true, successAt: time.Now(), attemptedAt: time.Now()}
	}
	p.quota.mu.Unlock()
	p.mu.Unlock()
	got := p.QuotaSnapshot()
	if got.Complete || got.Remaining != nil || got.UnprocessedAccounts != 1 || got.ReadyAccounts != quotaAccountLimit || got.KnownRemaining == nil || *got.KnownRemaining != quotaAccountLimit {
		t.Fatalf("account limit hid incomplete coverage: %+v", got)
	}
}

func TestQuotaCollectionStopsOnServiceCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	p := quotaFixturePool(t, server.URL, quotaFixtureAuth("a", "a"))
	ctx, cancel := context.WithCancel(context.Background())
	p.StartQuotaCollection(ctx)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("provider was not called")
	}
	cancel()
	got := quotaWait(t, p)
	if got.Complete || got.FailedAccounts != 0 || got.Remaining != nil {
		t.Fatalf("shutdown misreported as upstream failure: %+v", got)
	}
}
