package pool

import (
	"errors"
	"time"

	"codearts2api/internal/auth"
)

var ErrUpstreamBusy = errors.New("candidate accounts recently rejected upstream concurrency")

// UpstreamBusyError distinguishes remembered provider refusal from local slots.
// RetryAfter is advice to the caller, not a claim about the provider's release time.
type UpstreamBusyError struct{ RetryAfter time.Duration }

func (*UpstreamBusyError) Error() string { return ErrUpstreamBusy.Error() }
func (*UpstreamBusyError) Unwrap() error { return ErrUpstreamBusy }

const maxUpstreamBusyDelay = 30 * time.Second

// NoteUpstreamBusy records each explicit concurrency refusal before releasing its
// lease. Results from removed accounts or replaced logins cannot delay the new owner.
func (p *Pool) NoteUpstreamBusy(a *Account, owner *auth.Auth, base time.Duration) time.Duration {
	if a == nil || owner == nil {
		return 0
	}
	if base <= 0 {
		base = 2 * time.Second
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.containsAccountLocked(a) {
		return 0
	}
	a.mu.Lock()
	if a.removed || a.Auth != owner {
		a.mu.Unlock()
		return 0
	}
	if a.busyAuth != owner {
		a.busyStreak = 0
	}
	delay := base
	for n := uint(0); n < a.busyStreak && delay < maxUpstreamBusyDelay; n++ {
		if delay > maxUpstreamBusyDelay/2 {
			delay = maxUpstreamBusyDelay
			break
		}
		delay *= 2
	}
	if delay > maxUpstreamBusyDelay {
		delay = maxUpstreamBusyDelay
	}
	if a.busyStreak < 32 {
		a.busyStreak++
	}
	a.busyAuth, a.busyUntil = owner, time.Now().Add(delay)
	a.mu.Unlock()
	p.signalAdmissionChangedLocked()
	p.dispatchWaitersLocked()
	return delay
}

// UpstreamBusy reports only transient admission; Healthy deliberately stays true.
func (p *Pool) UpstreamBusy(a *Account) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.busyAuth == a.Auth && time.Now().Before(a.busyUntil)
}

// NoteSuccessForAuth applies completion only to the account and login that sent
// it. A late result cannot clear a replacement login's backoff or health history.
func (p *Pool) NoteSuccessForAuth(a *Account, owner *auth.Auth) {
	if a == nil || owner == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.containsAccountLocked(a) {
		return
	}
	a.mu.Lock()
	if a.removed || a.Auth != owner {
		a.mu.Unlock()
		return
	}
	a.busyAuth, a.busyUntil, a.busyStreak = nil, time.Time{}, 0
	now := time.Now()
	recovering := a.errCount > 0 || a.lastErr != ""
	blocked := a.disabled || now.Before(a.coolUntil)
	a.lastSuccessAt = now
	if recovering && !blocked {
		a.errCount, a.lastErr = 0, ""
	}
	a.mu.Unlock()
	p.signalAdmissionChangedLocked()
	p.dispatchWaitersLocked()
	if recovering && !blocked {
		p.saveState()
	}
}

func (p *Pool) containsAccountLocked(a *Account) bool {
	for _, current := range p.accounts {
		if current == a {
			return true
		}
	}
	return false
}

func (p *Pool) signalAdmissionChangedLocked() {
	if p.admissionChanged != nil {
		close(p.admissionChanged)
	}
	p.admissionChanged = make(chan struct{})
}

// admissionWaitLocked reports the next busy expiry and whether all healthy
// candidates are blocked by remembered provider refusal. The caller holds p.mu.
func (p *Pool) admissionWaitLocked(candidates map[string]int, sticky string, now time.Time) (time.Time, error) {
	var earliest time.Time
	healthy, allBusy := false, true
	sticky = p.effectiveStickyLocked(sticky, now)
	for _, a := range p.accounts {
		if _, ok := candidates[a.Name]; !ok || (sticky != "" && a.Name != sticky) {
			continue
		}
		a.mu.Lock()
		eligible := !a.removed && !a.disabled && !now.Before(a.coolUntil) && a.Auth != nil
		busy := a.busyAuth == a.Auth && now.Before(a.busyUntil)
		until := a.busyUntil
		a.mu.Unlock()
		if !eligible {
			continue
		}
		healthy = true
		if !busy {
			allBusy = false
			continue
		}
		if earliest.IsZero() || until.Before(earliest) {
			earliest = until
		}
	}
	if healthy && allBusy {
		return earliest, &UpstreamBusyError{RetryAfter: earliest.Sub(now)}
	}
	return earliest, ErrAcquireTimeout
}

// The pool lock makes the sticky eligibility decision atomic with dispatch.
// Ordinary local occupancy preserves affinity; explicit provider busy does not.
func (p *Pool) effectiveStickyLocked(sticky string, now time.Time) string {
	if sticky == "" {
		return ""
	}
	for _, a := range p.accounts {
		if a.Name != sticky {
			continue
		}
		a.mu.Lock()
		eligible := !a.removed && !a.disabled && !now.Before(a.coolUntil) && a.Auth != nil
		busy := a.busyAuth == a.Auth && now.Before(a.busyUntil)
		a.mu.Unlock()
		if eligible && !busy {
			return sticky
		}
		break
	}
	return ""
}
