package pool

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrQueueFull        = errors.New("account admission queue full")
	ErrNoHealthyAccount = errors.New("no healthy candidate account")
	ErrAcquireTimeout   = errors.New("account admission timed out")
)

// AccountCandidate ranks accounts with cached model capabilities. Lower wins.
type AccountCandidate struct {
	Name     string
	Priority int
}

// Lease owns a slot on the actual acquired account, including across reloads.
type Lease struct {
	Account *Account
	account *Account
	pool    *Pool
	once    sync.Once
}

// Release is safe to call more than once, including from concurrent cleanup.
func (l *Lease) Release() {
	if l == nil || l.pool == nil || l.account == nil {
		return
	}
	l.once.Do(func() {
		p := l.pool
		p.mu.Lock()
		p.releaseAccountLocked(l.account)
		p.dispatchWaitersLocked()
		p.mu.Unlock()
	})
}

type admissionWaiter struct {
	ctx        context.Context
	candidates map[string]int
	deadline   time.Time
	done       chan struct{}
	lease      *Lease
	err        error
	completed  bool
}

// AdmitRequest bounds all live chat handlers, including those between attempts.
// A ticket stays owned during retry sleeps, while account leases may be released.
func (p *Pool) AdmitRequest(ctx context.Context) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit := p.cfg.MaxPending
	for _, a := range p.accounts {
		a.mu.Lock()
		if !a.disabled && !a.removed && a.maxConcurrent > 0 {
			limit += a.maxConcurrent
		}
		a.mu.Unlock()
	}
	if p.admittedRequests >= limit {
		return nil, ErrQueueFull
	}
	p.admittedRequests++
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			p.admittedRequests--
			p.mu.Unlock()
		})
	}, nil
}

// AcquireAccount atomically selects and reserves capacity. Only blocked callers
// count against MaxPending. Existing compatible waiters get released slots first.
func (p *Pool) AcquireAccount(ctx context.Context, candidates []AccountCandidate, wait time.Duration) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	priorities := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		if old, ok := priorities[candidate.Name]; !ok || candidate.Priority < old {
			priorities[candidate.Name] = candidate.Priority
		}
	}
	p.mu.Lock()
	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	p.dispatchWaitersLocked()
	lease, healthy := p.reserveAccountLocked(priorities)
	if lease != nil {
		p.mu.Unlock()
		if err := ctx.Err(); err != nil {
			lease.Release()
			return nil, err
		}
		return lease, nil
	}
	if !healthy {
		p.mu.Unlock()
		return nil, ErrNoHealthyAccount
	}
	if wait <= 0 {
		p.mu.Unlock()
		return nil, ErrAcquireTimeout
	}
	if len(p.waiters) >= p.cfg.MaxPending {
		p.mu.Unlock()
		return nil, ErrQueueFull
	}
	w := &admissionWaiter{ctx: ctx, candidates: priorities, deadline: time.Now().Add(wait), done: make(chan struct{})}
	p.waiters = append(p.waiters, w)
	p.mu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	var waitErr error
	select {
	case <-ctx.Done():
		waitErr = ctx.Err()
	case <-timer.C:
		waitErr = ErrAcquireTimeout
	case <-w.done:
	}
	p.mu.Lock()
	// Cancellation wins even if a release dispatched a slot simultaneously.
	if err := ctx.Err(); err != nil {
		waitErr = err
	}
	if !w.completed {
		p.removeWaiterLocked(w)
		w.err = waitErr
		w.completed = true
	} else if waitErr != nil {
		if w.lease != nil {
			p.releaseAccountLocked(w.lease.account)
			w.lease = nil
		}
		w.err = waitErr
	}
	lease, err := w.lease, w.err
	p.dispatchWaitersLocked()
	p.mu.Unlock()
	return lease, err
}

// reserveAccountLocked checks all candidates before waiting. The pool mutex
// serializes selection with every reservation, health mutation and reload.
func (p *Pool) reserveAccountLocked(candidates map[string]int) (*Lease, bool) {
	healthy := false
	best, bestPriority, bestActive, bestMax := -1, 0, 0, 1
	now := time.Now()
	for offset := 0; offset < len(p.accounts); offset++ {
		index := (p.nextAccount + offset) % len(p.accounts)
		a := p.accounts[index]
		priority, ok := candidates[a.Name]
		if !ok {
			continue
		}
		a.mu.Lock()
		eligible := !a.removed && !a.disabled && !now.Before(a.coolUntil) && a.Auth != nil
		active, max := a.activeConcurrent, a.maxConcurrent
		a.mu.Unlock()
		if !eligible {
			continue
		}
		healthy = true
		if max <= 0 || active >= max {
			continue
		}
		if best < 0 || priority < bestPriority || (priority == bestPriority && int64(active)*int64(bestMax) < int64(bestActive)*int64(max)) {
			best, bestPriority, bestActive, bestMax = index, priority, active, max
		}
	}
	if best < 0 {
		return nil, healthy
	}
	a := p.accounts[best]
	a.mu.Lock()
	a.activeConcurrent++
	a.keepaliveLastPing = now
	a.mu.Unlock()
	p.nextAccount = (best + 1) % len(p.accounts)
	return &Lease{Account: a, account: a, pool: p}, true
}

func (p *Pool) releaseAccountLocked(a *Account) {
	a.mu.Lock()
	if a.activeConcurrent > 0 {
		a.activeConcurrent--
	}
	a.mu.Unlock()
}

func (p *Pool) removeWaiterLocked(target *admissionWaiter) {
	for i, w := range p.waiters {
		if w == target {
			copy(p.waiters[i:], p.waiters[i+1:])
			p.waiters[len(p.waiters)-1] = nil
			p.waiters = p.waiters[:len(p.waiters)-1]
			return
		}
	}
}

func (p *Pool) dispatchWaitersLocked() {
	remaining := p.waiters[:0]
	for _, w := range p.waiters {
		w.err = w.ctx.Err()
		if w.err == nil && !time.Now().Before(w.deadline) {
			w.err = ErrAcquireTimeout
		}
		if w.err == nil {
			var healthy bool
			w.lease, healthy = p.reserveAccountLocked(w.candidates)
			if w.lease == nil && !healthy {
				w.err = ErrNoHealthyAccount
			}
		}
		if w.lease == nil && w.err == nil {
			remaining = append(remaining, w)
			continue
		}
		w.completed = true
		close(w.done)
	}
	for i := len(remaining); i < len(p.waiters); i++ {
		p.waiters[i] = nil
	}
	p.waiters = remaining
}
