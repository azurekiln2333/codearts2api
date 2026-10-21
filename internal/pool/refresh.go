package pool

import (
	"context"
	"fmt"
	"log"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/upstream"
)

type tokenRefresh struct {
	done             chan struct{}
	err              error
	disabledRevision uint64
}

// RefreshTokenContext shares one bounded refresh per account. Canceling a
// waiter does not discard a provider response after its refresh token rotates.
func (p *Pool) RefreshTokenContext(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a := p.Get(name)
	if a == nil {
		return fmt.Errorf("account not found: %s", name)
	}
	return p.refreshAccountContext(ctx, a)
}

func (p *Pool) refreshAccountContext(ctx context.Context, a *Account) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	if err := ctx.Err(); err != nil {
		a.mu.Unlock()
		return err
	}
	if a.removed || a.Auth == nil {
		a.mu.Unlock()
		return ErrNoHealthyAccount
	}
	flight := a.refresh
	if flight == nil {
		authz := a.Auth
		if authz.Refresh() == "" {
			a.mu.Unlock()
			return fmt.Errorf("no refresh_token available")
		}
		if authz.DPoPPrivateJWK()["d"] == "" {
			a.mu.Unlock()
			return fmt.Errorf("no DPoP private key available; re-login required")
		}
		flight = &tokenRefresh{done: make(chan struct{}), disabledRevision: a.disabledRevision}
		a.refresh = flight
		go p.runRefresh(a, authz, flight)
	}
	a.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-flight.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		return flight.err
	}
}

func (p *Pool) runRefresh(a *Account, authz *auth.Auth, flight *tokenRefresh) {
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.UpstreamTimeout)
	defer cancel()
	login := p.cfg.LoginConfig
	login.ClientID = authz.ClientIDOr(login.ClientID)
	refreshToken := authz.Refresh()
	resp, err := a.Client.RefreshToken(ctx, login, refreshToken, authz.Verifier(), upstream.DPoPPrivateJWK(authz.DPoPPrivateJWK()))
	if err != nil {
		err = fmt.Errorf("refresh failed: %w", err)
	} else {
		// Reload may replace the auth object while preserving the account. Update
		// an unchanged refresh identity; never overwrite a newer login or recreate
		// credentials for a removed account. No pool lock covers the network call.
		p.mu.Lock()
		a.mu.Lock()
		current := a.Auth
		if a.removed || current == nil {
			err = ErrNoHealthyAccount
		} else if current == authz || (current.Refresh() == refreshToken && current.ClientIDOr(login.ClientID) == login.ClientID) {
			err = current.UpdateCredentials(resp.Credentials.SecurityToken, resp.Credentials.AccessKeyID, resp.Credentials.SecretAccessKey, resp.Credentials.Expiration, resp.RefreshToken)
			if err != nil {
				err = fmt.Errorf("save token: %w", err)
			} else {
				if a.disabledRevision == flight.disabledRevision {
					a.disabled = false
					a.disabledReason = ""
					a.lastErr = ""
				}
				a.lastValidated = time.Now()
			}
		}
		a.mu.Unlock()
		p.dispatchWaitersLocked()
		p.mu.Unlock()
	}
	a.mu.Lock()
	flight.err = err
	a.refresh = nil
	close(flight.done)
	a.mu.Unlock()
	if err == nil {
		log.Printf("pool token refreshed account=%s", a.Name)
	}
}
