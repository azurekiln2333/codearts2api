package upstream

import (
	"crypto/sha256"
	"encoding/binary"
	"io"
	"time"

	"codearts2api/internal/auth"
)

type modelSourceCredential struct {
	owner  *auth.Auth
	digest [sha256.Size]byte
}

type modelSourceBackoff struct {
	credential modelSourceCredential
	retryAt    [len(modelSources)]time.Time
}

// Keep only a private digest of the exact captured signing tuple. The owner
// distinguishes replacement logins even when a provider reuses credentials.
func sourceCredential(owner *auth.Auth, cred SignCredential) modelSourceCredential {
	h := sha256.New()
	var size [8]byte
	for _, value := range [...]string{cred.SecurityToken, cred.AccessKeyID, cred.SecretAccessKey} {
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = h.Write(size[:])
		_, _ = io.WriteString(h, value)
	}
	key := modelSourceCredential{owner: owner}
	copy(key.digest[:], h.Sum(nil))
	return key
}

func (c *Client) beginModelSourceBackoff(owner *auth.Auth, cred SignCredential) *modelSourceBackoff {
	credential := sourceCredential(owner, cred)
	c.srcMu.Lock()
	defer c.srcMu.Unlock()
	if c.srcFail == nil {
		c.srcFail = map[string]*modelSourceBackoff{}
	}
	state := c.srcFail[owner.UserID]
	if state == nil || state.credential != credential {
		state = &modelSourceBackoff{credential: credential}
		c.srcFail[owner.UserID] = state
	}
	return state
}

func (c *Client) modelSourceRetry(accountID string, state *modelSourceBackoff, source int) time.Time {
	c.srcMu.Lock()
	defer c.srcMu.Unlock()
	if c.srcFail[accountID] != state {
		return time.Time{}
	}
	return state.retryAt[source]
}

// A late result may update only the generation it started with. In particular,
// an old failure cannot poison a fresh login, nor clear a fresh genuine failure.
func (c *Client) finishModelSource(accountID string, state *modelSourceBackoff, source int, failed bool) time.Time {
	c.srcMu.Lock()
	defer c.srcMu.Unlock()
	if c.srcFail[accountID] != state {
		return time.Time{}
	}
	retry := time.Time{}
	if failed {
		retry = time.Now().Add(srcFailCooldown)
	}
	state.retryAt[source] = retry
	return retry
}

func (c *Client) PruneModelDiscoveryBackoff(current map[string]*auth.Auth) {
	// Resolve synchronized credential snapshots before taking the source lock.
	credentials := make(map[string]modelSourceCredential, len(current))
	for accountID, owner := range current {
		if owner == nil {
			continue
		}
		token, ak, sk := owner.Credentials()
		credentials[accountID] = sourceCredential(owner, SignCredential{AccessKeyID: ak, SecretAccessKey: sk, SecurityToken: token})
	}
	c.srcMu.Lock()
	defer c.srcMu.Unlock()
	now := time.Now()
	for accountID, state := range c.srcFail {
		credential, ok := credentials[accountID]
		if !ok || state.credential != credential {
			delete(c.srcFail, accountID)
			continue
		}
		for i, until := range state.retryAt {
			if !now.Before(until) {
				state.retryAt[i] = time.Time{}
			}
		}
	}
}
