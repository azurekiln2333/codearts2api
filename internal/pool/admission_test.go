package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"codearts2api/internal/auth"
)

func admissionPool(t *testing.T, cfg Config, names ...string) *Pool {
	t.Helper()
	auths := make([]*auth.Auth, 0, len(names))
	for _, name := range names {
		auths = append(auths, auth.New(name, name, "domain", "token", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", "verifier"))
	}
	p, err := New(auths, cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAcquireLockRejectsDisabledAccount(t *testing.T) {
	p := admissionPool(t, Config{}, "one")
	p.Disable("one", "test")
	if p.AcquireLock("one") {
		p.ReleaseLock("one")
		t.Fatal("disabled account received an active slot")
	}
}

func TestAccountsReturnsIndependentSlice(t *testing.T) {
	p := admissionPool(t, Config{}, "one", "two")
	snapshot := p.Accounts()
	snapshot[0] = snapshot[1]
	if p.Get("one") == nil {
		t.Fatal("mutating an account slice snapshot changed pool membership")
	}
}

func candidates(names ...string) []AccountCandidate {
	out := make([]AccountCandidate, 0, len(names))
	for _, name := range names {
		out = append(out, AccountCandidate{Name: name})
	}
	return out
}

func acquireForTest(t *testing.T, p *Pool, names ...string) *Lease {
	t.Helper()
	lease, err := p.AcquireAccount(context.Background(), candidates(names...), 0)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func waitPending(t *testing.T, p *Pool, want int) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		p.mu.Lock()
		got := len(p.waiters)
		p.mu.Unlock()
		if got == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("pending=%d, want %d", got, want)
		case <-tick.C:
		}
	}
}

func receiveLease(t *testing.T, ch <-chan *Lease) *Lease {
	t.Helper()
	select {
	case lease := <-ch:
		if lease == nil {
			t.Fatal("expected lease")
		}
		return lease
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for lease")
		return nil
	}
}

func TestAcquireUsesFreeAccountBeforeWaiting(t *testing.T) {
	p := admissionPool(t, Config{}, "one", "two")
	first := acquireForTest(t, p, "one")
	defer first.Release()
	lease, err := p.AcquireAccount(context.Background(), candidates("one", "two"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if lease.Account.Name != "two" {
		t.Fatalf("selected=%s, want free account two", lease.Account.Name)
	}
}

func TestAcquirePriorityUtilizationAndRoundRobin(t *testing.T) {
	p := admissionPool(t, Config{MaxConcurrent: 4}, "one", "two", "three")
	for i, want := range []string{"one", "two", "three", "one", "two", "three"} {
		lease := acquireForTest(t, p, "one", "two", "three")
		if lease.Account.Name != want {
			t.Fatalf("round %d selected=%s, want %s", i, lease.Account.Name, want)
		}
		lease.Release()
	}
	busy := acquireForTest(t, p, "one")
	defer busy.Release()
	lease, err := p.AcquireAccount(context.Background(), []AccountCandidate{{Name: "one", Priority: 0}, {Name: "two", Priority: 1}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Account.Name != "one" {
		t.Fatalf("capability priority lost to utilization: %s", lease.Account.Name)
	}
	lease.Release()
	p.SetMaxConcurrent("two", 2)
	lease = acquireForTest(t, p, "one", "two")
	if lease.Account.Name != "two" {
		t.Fatalf("selected=%s, want account with least utilization two", lease.Account.Name)
	}
	lease.Release()
}

func TestAcquireQueueBoundCancellationAndFIFO(t *testing.T) {
	p := admissionPool(t, Config{MaxPending: 2}, "one")
	held := acquireForTest(t, p, "one")
	defer held.Release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	canceled := make(chan error, 1)
	go func() {
		lease, err := p.AcquireAccount(ctx, candidates("one"), time.Second)
		if lease != nil {
			lease.Release()
		}
		canceled <- err
	}()
	waitPending(t, p, 1)
	second := make(chan *Lease, 1)
	go func() {
		lease, _ := p.AcquireAccount(context.Background(), candidates("one"), time.Second)
		second <- lease
	}()
	waitPending(t, p, 2)
	start := time.Now()
	if _, err := p.AcquireAccount(context.Background(), candidates("one"), time.Second); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("queue overflow error=%v, want ErrQueueFull", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("queue overflow blocked")
	}
	cancel()
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled acquisition error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled acquisition did not wake")
	}
	waitPending(t, p, 1)
	third := make(chan *Lease, 1)
	go func() {
		lease, _ := p.AcquireAccount(context.Background(), candidates("one"), time.Second)
		third <- lease
	}()
	waitPending(t, p, 2)
	held.Release()
	secondLease := receiveLease(t, second)
	select {
	case lease := <-third:
		if lease != nil {
			lease.Release()
		}
		t.Fatal("later waiter bypassed the older compatible waiter")
	default:
	}
	secondLease.Release()
	thirdLease := receiveLease(t, third)
	thirdLease.Release()
	waitPending(t, p, 0)
	if p.Busy("one") {
		t.Fatal("queue cleanup leaked an account slot")
	}
}

func TestAcquireCancellationExpiryAndIneligibleAccounts(t *testing.T) {
	p := admissionPool(t, Config{}, "one")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.AcquireAccount(ctx, candidates("one"), time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled request error=%v", err)
	}
	if p.Busy("one") {
		t.Fatal("pre-canceled request reserved capacity")
	}
	held := acquireForTest(t, p, "one")
	if _, err := p.AcquireAccount(context.Background(), candidates("one"), 20*time.Millisecond); !errors.Is(err, ErrAcquireTimeout) {
		t.Fatalf("wait expiry error=%v", err)
	}
	waitPending(t, p, 0)
	held.Release()
	p.Disable("one", "test")
	for _, cs := range [][]AccountCandidate{nil, candidates("missing"), candidates("one")} {
		if _, err := p.AcquireAccount(context.Background(), cs, time.Second); !errors.Is(err, ErrNoHealthyAccount) {
			t.Fatalf("ineligible candidate error=%v", err)
		}
	}
}

func TestQueuedAcquireRechecksDisableRemoveAndCooldown(t *testing.T) {
	for _, action := range []string{"disable", "remove", "cooldown"} {
		t.Run(action, func(t *testing.T) {
			p := admissionPool(t, Config{}, "one")
			held := acquireForTest(t, p, "one")
			defer held.Release()
			result := make(chan error, 1)
			go func() {
				lease, err := p.AcquireAccount(context.Background(), candidates("one"), time.Second)
				if lease != nil {
					lease.Release()
				}
				result <- err
			}()
			waitPending(t, p, 1)
			switch action {
			case "disable":
				p.Disable("one", "test")
			case "remove":
				p.SyncToDir(nil)
			case "cooldown":
				p.Cooldown("one", CoolSoft, time.Minute, "test")
			}
			select {
			case err := <-result:
				if !errors.Is(err, ErrNoHealthyAccount) {
					t.Fatalf("invalidated acquisition error=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("state change failed to wake the waiter")
			}
			waitPending(t, p, 0)
		})
	}
}

func TestAcquireCapacityIncreaseWakesWaiter(t *testing.T) {
	p := admissionPool(t, Config{}, "one")
	held := acquireForTest(t, p, "one")
	defer held.Release()
	result := make(chan *Lease, 1)
	go func() {
		lease, _ := p.AcquireAccount(context.Background(), candidates("one"), time.Second)
		result <- lease
	}()
	waitPending(t, p, 1)
	p.SetMaxConcurrent("one", 2)
	lease := receiveLease(t, result)
	lease.Release()
}

func TestLeaseReloadAndConcurrentIdempotentRelease(t *testing.T) {
	p := admissionPool(t, Config{}, "one")
	old := acquireForTest(t, p, "one")
	oldAccount := old.Account
	reloadedAuth := auth.New("one", "changed", "domain", "new", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", "verifier")
	p.SyncToDir([]*auth.Auth{reloadedAuth})
	if p.Get("one") != oldAccount || oldAccount.AuthSnapshot() != reloadedAuth {
		t.Fatal("normal reload did not preserve account ownership and replace auth")
	}
	if _, err := p.AcquireAccount(context.Background(), candidates("one"), 0); !errors.Is(err, ErrAcquireTimeout) {
		t.Fatalf("reload lost active slot: %v", err)
	}
	p.SyncToDir(nil)
	p.AddAccount(reloadedAuth)
	replacement := acquireForTest(t, p, "one")
	if replacement.Account == oldAccount {
		t.Fatal("removed account was reused as replacement")
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); old.Release() }()
	}
	wg.Wait()
	if !p.Busy("one") {
		t.Fatal("old lease released replacement account's active slot")
	}
	oldAccount.mu.Lock()
	oldActive := oldAccount.activeConcurrent
	oldAccount.mu.Unlock()
	if oldActive != 0 {
		t.Fatalf("removed account active=%d, want 0", oldActive)
	}
	replacement.Release()
	if p.Busy("one") {
		t.Fatal("replacement lease did not release")
	}
}

func TestConcurrentAdmissionNeverExceedsCapacity(t *testing.T) {
	p := admissionPool(t, Config{MaxConcurrent: 2, MaxPending: 12}, "one", "two", "three")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var tracking sync.Mutex
	active := make(map[string]int)
	used := make(map[string]int)
	problems := make(chan error, 96)
	start := make(chan struct{})
	for i := 0; i < 48; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 8; j++ {
				lease, err := p.AcquireAccount(ctx, candidates("one", "two", "three"), time.Second)
				if errors.Is(err, ErrQueueFull) {
					return
				}
				if err != nil {
					problems <- err
					return
				}
				tracking.Lock()
				active[lease.Account.Name]++
				used[lease.Account.Name]++
				if active[lease.Account.Name] > 2 {
					problems <- fmt.Errorf("account %s active=%d exceeds 2", lease.Account.Name, active[lease.Account.Name])
				}
				tracking.Unlock()
				p.mu.Lock()
				pending := len(p.waiters)
				p.mu.Unlock()
				if pending > 12 {
					problems <- fmt.Errorf("pending=%d exceeds 12", pending)
				}
				tracking.Lock()
				active[lease.Account.Name]--
				tracking.Unlock()
				lease.Release()
			}
		}()
	}
	close(start)
	wg.Wait()
	close(problems)
	for err := range problems {
		t.Error(err)
	}
	for _, name := range []string{"one", "two", "three"} {
		if used[name] == 0 || p.Busy(name) {
			t.Fatalf("account %s use=%d, still busy=%t", name, used[name], p.Busy(name))
		}
	}
	waitPending(t, p, 0)
}

func TestLifecycleAdmissionBoundsRequestsAcrossRetrySleeps(t *testing.T) {
	p := admissionPool(t, Config{MaxConcurrent: 1, MaxPending: 2}, "one")
	var releases []func()
	for i := 0; i < 3; i++ {
		release, err := p.AdmitRequest(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
		defer release()
	}
	// Even with no held account slots or queued acquisitions, the three live
	// request tickets represent handlers sleeping between attempts.
	if p.Busy("one") {
		t.Fatal("ticket admission unexpectedly reserved a slot")
	}
	if _, err := p.AdmitRequest(context.Background()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("retry sleeper overflow error=%v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); releases[0]() }()
	}
	wg.Wait()
	next, err := p.AdmitRequest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer next()
	if _, err := p.AdmitRequest(context.Background()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("double-release corrupted lifecycle count: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.AdmitRequest(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ticket error=%v", err)
	}
}

func TestConcurrentSaturationBoundsWaitersAndCancellationDrains(t *testing.T) {
	p := admissionPool(t, Config{MaxConcurrent: 2, MaxPending: 8}, "one", "two", "three")
	var held []*Lease
	for i := 0; i < 6; i++ {
		lease := acquireForTest(t, p, "one", "two", "three")
		held = append(held, lease)
		defer lease.Release()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func() {
			<-start
			lease, err := p.AcquireAccount(ctx, candidates("one", "two", "three"), 3*time.Second)
			if lease != nil {
				lease.Release()
			}
			results <- err
		}()
	}
	close(start)
	waitPending(t, p, 8)
	for i := 0; i < 12; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, ErrQueueFull) {
				t.Fatalf("saturated waiter result=%v, want queue overflow", err)
			}
		case <-time.After(time.Second):
			t.Fatal("overflow requests remained blocked behind full queue")
		}
	}
	for _, status := range p.GetConcurrentStats() {
		if status["active_concurrent"].(int) != 2 {
			t.Fatalf("saturation exceeded or lost an active slot: %#v", status)
		}
	}
	cancel()
	for i := 0; i < 8; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled saturation waiter result=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("cancellation did not drain saturated queue")
		}
	}
	waitPending(t, p, 0)
	for _, lease := range held {
		lease.Release()
	}
	for _, name := range []string{"one", "two", "three"} {
		if p.Busy(name) {
			t.Fatalf("cancellation/release leaked capacity on %s", name)
		}
	}
	lease := acquireForTest(t, p, "one", "two", "three")
	lease.Release()
}

func TestConcurrentLifecycleAdmissionBoundAndRelease(t *testing.T) {
	p := admissionPool(t, Config{MaxConcurrent: 1, MaxPending: 2}, "one")
	start := make(chan struct{})
	releaseAll := make(chan struct{})
	results := make(chan error, 32)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, err := p.AdmitRequest(context.Background())
			results <- err
			if err == nil {
				<-releaseAll
				release()
				release()
			}
		}()
	}
	close(start)
	accepted, rejected := 0, 0
	for i := 0; i < 32; i++ {
		select {
		case err := <-results:
			if err == nil {
				accepted++
			} else if errors.Is(err, ErrQueueFull) {
				rejected++
			} else {
				t.Errorf("unexpected admission error: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("lifecycle admission blocked instead of failing promptly")
		}
	}
	close(releaseAll)
	wg.Wait()
	if accepted != 3 || rejected != 29 {
		t.Fatalf("accepted=%d rejected=%d, want 3/29", accepted, rejected)
	}
	p.mu.Lock()
	left := p.admittedRequests
	p.mu.Unlock()
	if left != 0 {
		t.Fatalf("lifecycle tickets leaked: %d", left)
	}
}

func TestConcurrentReloadPreservesLeasesAndSnapshots(t *testing.T) {
	p := admissionPool(t, Config{MaxConcurrent: 2}, "one")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	problems := make(chan error, 16)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 120; i++ {
			a := auth.New("one", fmt.Sprintf("name-%d", i), "domain", fmt.Sprintf("token-%d", i), "ak", "sk", time.Now().Add(2*time.Hour).Format(time.RFC3339), "", "verifier")
			p.SyncToDir([]*auth.Auth{a})
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 120; i++ {
			p.Stats()
			p.List()
			p.GetConcurrentStats()
			for _, a := range p.Accounts() {
				a.AuthSnapshot().Credentials()
			}
		}
	}()
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 80; j++ {
				lease, err := p.AcquireAccount(ctx, candidates("one"), time.Second)
				if err != nil {
					problems <- err
					return
				}
				ok, err := p.ValidateContext(ctx, lease.Account)
				lease.Release()
				if !ok || err != nil {
					problems <- fmt.Errorf("validation during reload: ok=%t err=%v", ok, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(problems)
	for err := range problems {
		t.Error(err)
	}
	if p.Busy("one") {
		t.Fatal("reload stress leaked an active lease")
	}
	waitPending(t, p, 0)
}
