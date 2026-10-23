package pool

import (
	"context"
	"errors"
	"testing"
	"time"

	"codearts2api/internal/auth"
)

func TestUpstreamBusyBackoffIsBoundedNeutralAndLoginScoped(t *testing.T) {
	p := admissionPool(t, Config{}, "one")
	a := p.Get("one")
	owner := a.AuthSnapshot()
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second} {
		if got := p.NoteUpstreamBusy(a, owner, time.Second); got != want {
			t.Fatalf("backoff=%s want=%s", got, want)
		}
	}
	if !p.Healthy(a.Name) || !p.UpstreamBusy(a) {
		t.Fatal("provider busy must block admission while retaining health")
	}
	if s := p.List()[0]; s["err_count"] != 0 || s["cooling"] != false || s["disabled"] != false {
		t.Fatalf("busy changed health: %#v", s)
	}
	replacement := auth.New("one", "new-login", "domain", "new-token", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", "")
	p.AddAccount(replacement)
	if p.UpstreamBusy(a) {
		t.Fatal("replacement login inherited old busy state")
	}
	if got := p.NoteUpstreamBusy(a, owner, time.Second); got != 0 {
		t.Fatal("stale result changed replacement admission")
	}
	if got := p.NoteUpstreamBusy(a, replacement, time.Second); got != time.Second {
		t.Fatalf("new login did not start fresh: %s", got)
	}
	p.NoteSuccessForAuth(a, owner)
	if !p.UpstreamBusy(a) {
		t.Fatal("old success cleared new login's busy state")
	}
	p.NoteSuccessForAuth(a, replacement)
	if p.UpstreamBusy(a) {
		t.Fatal("actual completion did not clear busy state")
	}
	if got := p.NoteUpstreamBusy(a, replacement, time.Second); got != time.Second {
		t.Fatalf("completion did not reset backoff: %s", got)
	}
	p.SyncToDir(nil)
	current := p.AddAccount(replacement)
	if p.NoteUpstreamBusy(a, replacement, time.Second) != 0 || p.UpstreamBusy(current) {
		t.Fatal("removed object's late result delayed replacement account")
	}
}

func TestAcquireSkipsUpstreamBusyAndWakesAtExpiry(t *testing.T) {
	p := admissionPool(t, Config{}, "one", "two")
	a := p.Get("one")
	p.NoteUpstreamBusy(a, a.AuthSnapshot(), 80*time.Millisecond)
	backup := acquireForTest(t, p, "one", "two")
	if backup.Account.Name != "two" {
		t.Fatal("recent provider refusal was preferred over usable backup")
	}
	backup.Release()
	started := time.Now()
	lease, err := p.AcquireAccount(context.Background(), candidates("one"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if time.Since(started) < 50*time.Millisecond || lease.Account != a {
		t.Fatal("busy expiry did not govern admission")
	}
}

func TestUpstreamBusyWaitIsCancellableAndReportsProviderSaturation(t *testing.T) {
	p := admissionPool(t, Config{}, "one", "two")
	a := p.Get("one")
	p.NoteUpstreamBusy(a, a.AuthSnapshot(), time.Second)
	for _, wait := range []time.Duration{0, 10 * time.Millisecond} {
		lease, err := p.AcquireAccount(context.Background(), candidates("one"), wait)
		var busy *UpstreamBusyError
		if lease != nil || !errors.Is(err, ErrUpstreamBusy) || !errors.As(err, &busy) || busy.RetryAfter <= 0 {
			t.Fatalf("known busy misreported: lease=%v error=%v", lease, err)
		}
	}
	local := acquireForTest(t, p, "two")
	defer local.Release()
	if _, err := p.AcquireAccount(context.Background(), candidates("one", "two"), 0); !errors.Is(err, ErrAcquireTimeout) {
		t.Fatalf("mixed local occupancy was claimed as all-upstream busy: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.AcquireAccount(ctx, candidates("one"), time.Hour)
		done <- err
	}()
	waitPending(t, p, 1)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("busy waiter retained cancellation")
	}
	waitPending(t, p, 0)
}

func TestBusyRecordedDuringSlotWaitUpdatesWakeDeadline(t *testing.T) {
	p := admissionPool(t, Config{}, "one")
	a := p.Get("one")
	holder := acquireForTest(t, p, "one")
	done := make(chan *Lease, 1)
	go func() {
		lease, _ := p.AcquireAccount(context.Background(), candidates("one"), time.Second)
		done <- lease
	}()
	waitPending(t, p, 1)
	p.NoteUpstreamBusy(a, a.AuthSnapshot(), 60*time.Millisecond)
	holder.Release()
	select {
	case lease := <-done:
		if lease != nil {
			lease.Release()
		}
		t.Fatal("waiter bypassed newly recorded refusal")
	case <-time.After(20 * time.Millisecond):
	}
	lease := receiveLease(t, done)
	lease.Release()
	waitPending(t, p, 0)
}

func TestStickyWaitKeepsLocalAffinityButReleasesItOnProviderBusy(t *testing.T) {
	p := admissionPool(t, Config{}, "one", "two")
	a := p.Get("one")
	holder := acquireForTest(t, p, "one")
	defer holder.Release()
	done := make(chan *Lease, 1)
	go func() {
		lease, _ := p.AcquireAccount(context.Background(), []AccountCandidate{{Name: "one", Sticky: true}, {Name: "two"}}, time.Second)
		done <- lease
	}()
	// A locally occupied sticky account must keep affinity even with idle B.
	waitPending(t, p, 1)
	select {
	case lease := <-done:
		if lease != nil {
			lease.Release()
		}
		t.Fatal("local occupancy unexpectedly discarded sticky affinity")
	default:
	}
	// Explicit provider refusal changes that policy atomically with dispatch;
	// A remains locally occupied, so its release cannot be the wake trigger.
	p.NoteUpstreamBusy(a, a.AuthSnapshot(), time.Second)
	backup := receiveLease(t, done)
	defer backup.Release()
	if backup.Account.Name != "two" {
		t.Fatalf("busy sticky waiter selected %s, want two", backup.Account.Name)
	}
	waitPending(t, p, 0)
}
