package pool

import (
	"time"

	"codearts2api/internal/auth"
)

// WithCurrentAuth validates the exact account/login snapshot and runs only a
// bounded in-memory publication. fn must not do I/O or call back into the pool.
// The pool -> account lock order matches reload, removal and token publication.
func (p *Pool) WithCurrentAuth(acct *Account, authz *auth.Auth, fn func()) bool {
	if p == nil || acct == nil || authz == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	found := false
	for _, current := range p.accounts {
		if current == acct {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	acct.mu.Lock()
	defer acct.mu.Unlock()
	if acct.Auth != authz || acct.removed || acct.disabled || time.Now().Before(acct.coolUntil) {
		return false
	}
	fn()
	return true
}

// ModelDiscoveryEligibility contains safe fixed metadata for the admin catalog.
func (p *Pool) ModelDiscoveryEligibility(acct *Account, authz *auth.Auth) (bool, string) {
	if p == nil || acct == nil || authz == nil {
		return false, "missing_credentials"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	found := false
	for _, current := range p.accounts {
		if current == acct {
			found = true
			break
		}
	}
	if !found {
		return false, "removed"
	}
	acct.mu.Lock()
	defer acct.mu.Unlock()
	switch {
	case acct.removed:
		return false, "removed"
	case acct.Auth != authz:
		return false, "replaced"
	case acct.disabled:
		return false, "disabled"
	case time.Now().Before(acct.coolUntil):
		return false, "cooling"
	default:
		return true, ""
	}
}
