package pool

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sync"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/upstream"
)

const (
	quotaAccountLimit = 512
	quotaWorkers      = 2
	quotaJobTimeout   = 30 * time.Second
	quotaCallTimeout  = 8 * time.Second
	quotaInterval     = 5 * time.Minute
	quotaMinInterval  = 30 * time.Second
	quotaMaxAge       = 15 * time.Minute
)

var errQuotaCallTimeout = errors.New("quota call timeout")

type quotaManager struct {
	mu          sync.Mutex
	entries     map[*Account]quotaEntry
	serviceCtx  context.Context
	collecting  bool
	running     bool
	startedAt   time.Time
	finishedAt  time.Time
	nextAttempt time.Time
	nextTarget  int
}

type quotaEntry struct {
	owner       *auth.Auth
	fingerprint [sha256.Size]byte
	value       upstream.CreditQuota
	hasValue    bool
	attemptedAt time.Time
	successAt   time.Time
	errorKind   string
}

// QuotaRefresh describes the one shared background collection job.
type QuotaRefresh struct {
	Running    bool       `json:"running"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	NextAt     *time.Time `json:"next_at"`
}

// AccountQuota separates a missing or failed measurement from a known zero.
type AccountQuota struct {
	Account       string                `json:"account"`
	UID           string                `json:"uid"`
	State         string                `json:"state"`
	Error         string                `json:"error,omitempty"`
	LastAttemptAt *time.Time            `json:"last_attempt_at"`
	LastSuccessAt *time.Time            `json:"last_success_at"`
	Quota         *upstream.CreditQuota `json:"quota"`
}

// QuotaSnapshot sums the latest complete measurements by distinct account UID.
// Providers can share a package across different UIDs, so overlap is possible.
type QuotaSnapshot struct {
	Unit                 string         `json:"unit"`
	Scope                string         `json:"scope"`
	CreditPackagesOnly   bool           `json:"credit_packages_only"`
	ExcludesBenefitQuota bool           `json:"excludes_benefit_quota"`
	OverlapPossible      bool           `json:"overlap_possible"`
	State                string         `json:"state"`
	Complete             bool           `json:"complete"`
	Total                *float64       `json:"total"`
	Used                 *float64       `json:"used"`
	Remaining            *float64       `json:"remaining"`
	KnownRemaining       *float64       `json:"known_remaining"`
	ConfiguredAccounts   int            `json:"configured_accounts"`
	CountedAccounts      int            `json:"counted_accounts"`
	ReadyAccounts        int            `json:"ready_accounts"`
	FailedAccounts       int            `json:"failed_accounts"`
	UnknownAccounts      int            `json:"unknown_accounts"`
	UnavailableAccounts  int            `json:"unavailable_accounts"`
	StaleAccounts        int            `json:"stale_accounts"`
	DuplicateAccounts    int            `json:"duplicate_accounts"`
	UnprocessedAccounts  int            `json:"unprocessed_accounts"`
	OldestSuccessAt      *time.Time     `json:"oldest_success_at"`
	Accounts             []AccountQuota `json:"accounts"`
	Refresh              QuotaRefresh   `json:"refresh"`
}

type quotaTarget struct {
	account     *Account
	uid         string
	owner       *auth.Auth
	client      *upstream.Client
	credential  upstream.SignCredential
	fingerprint [sha256.Size]byte
	expired     bool
}

func quotaFingerprint(cred upstream.SignCredential) [sha256.Size]byte {
	h := sha256.New()
	var size [8]byte
	for _, value := range [...]string{cred.SecurityToken, cred.AccessKeyID, cred.SecretAccessKey} {
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = h.Write(size[:])
		_, _ = io.WriteString(h, value)
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

func quotaCredential(owner *auth.Auth) upstream.SignCredential {
	if owner == nil {
		return upstream.SignCredential{}
	}
	token, ak, sk := owner.Credentials()
	return upstream.SignCredential{SecurityToken: token, AccessKeyID: ak, SecretAccessKey: sk}
}

func quotaTargetRank(target quotaTarget, entries map[*Account]quotaEntry) int {
	if target.owner == nil || target.client == nil || target.expired ||
		target.credential.SecurityToken == "" || target.credential.AccessKeyID == "" || target.credential.SecretAccessKey == "" {
		return 0
	}
	if entry, ok := entries[target.account]; ok && entry.owner == target.owner &&
		entry.fingerprint == target.fingerprint && entry.errorKind == "auth" {
		return 1
	}
	return 2
}

func quotaTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	v := t.UTC()
	return &v
}

func quotaCopyFloat(v *float64) *float64 {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}

func quotaCopyValue(v upstream.CreditQuota) upstream.CreditQuota {
	v.Total = quotaCopyFloat(v.Total)
	v.Used = quotaCopyFloat(v.Used)
	v.Remaining = quotaCopyFloat(v.Remaining)
	return v
}

func (q *quotaManager) refreshLocked() QuotaRefresh {
	return QuotaRefresh{
		Running: q.running, StartedAt: quotaTime(q.startedAt),
		FinishedAt: quotaTime(q.finishedAt), NextAt: quotaTime(q.nextAttempt),
	}
}

// StartQuotaCollection ties periodic, read-only quota collection to the service.
// It never validates, refreshes or persists credentials and never claims benefits.
func (p *Pool) StartQuotaCollection(ctx context.Context) {
	if p == nil || ctx == nil || ctx.Err() != nil {
		return
	}
	p.quota.mu.Lock()
	if p.quota.collecting {
		p.quota.mu.Unlock()
		return
	}
	p.quota.collecting = true
	p.quota.serviceCtx = ctx
	p.quota.mu.Unlock()
	p.RequestQuotaRefresh()
	go func() {
		ticker := time.NewTicker(quotaInterval)
		defer ticker.Stop()
		defer func() {
			p.quota.mu.Lock()
			p.quota.collecting = false
			p.quota.serviceCtx = nil
			p.quota.mu.Unlock()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.RequestQuotaRefresh()
			}
		}
	}()
}

// RequestQuotaRefresh starts or joins the shared bounded job. A rate limit
// prevents repeated calls from amplifying upstream traffic.
func (p *Pool) RequestQuotaRefresh() QuotaRefresh {
	if p == nil {
		return QuotaRefresh{}
	}
	p.quota.mu.Lock()
	now := time.Now()
	ctx := p.quota.serviceCtx
	if ctx == nil {
		ctx = context.Background()
	}
	if p.quota.running || now.Before(p.quota.nextAttempt) || ctx.Err() != nil {
		status := p.quota.refreshLocked()
		p.quota.mu.Unlock()
		return status
	}
	p.quota.running = true
	p.quota.startedAt = now
	p.quota.finishedAt = time.Time{}
	p.quota.nextAttempt = now.Add(quotaMinInterval)
	status := p.quota.refreshLocked()
	p.quota.mu.Unlock()
	go p.runQuotaRefresh(ctx)
	return status
}

// quotaTargetsLocked is called with p.mu held; entries are bounded by the
// number of inspected account slots, and identical account UIDs are counted once.
func (p *Pool) quotaTargetsLocked() ([]quotaTarget, int, int) {
	limit := len(p.accounts)
	unprocessed := 0
	if limit > quotaAccountLimit {
		unprocessed = limit - quotaAccountLimit
		limit = quotaAccountLimit
	}
	seen := make(map[string]int, limit)
	duplicates := 0
	targets := make([]quotaTarget, 0, limit)
	p.quota.mu.Lock()
	defer p.quota.mu.Unlock()
	for _, acct := range p.accounts[:limit] {
		acct.mu.Lock()
		uid, owner, client := acct.UID, acct.Auth, acct.Client
		acct.mu.Unlock()
		cred := quotaCredential(owner)
		target := quotaTarget{account: acct, uid: uid, owner: owner, client: client,
			credential: cred, fingerprint: quotaFingerprint(cred)}
		if owner != nil {
			target.expired = owner.Expired()
		}
		if uid != "" {
			if index, ok := seen[uid]; ok {
				duplicates++
				if quotaTargetRank(target, p.quota.entries) > quotaTargetRank(targets[index], p.quota.entries) {
					targets[index] = target
				}
				continue
			}
			seen[uid] = len(targets)
		}
		targets = append(targets, target)
	}
	return targets, duplicates, unprocessed
}

func (p *Pool) runQuotaRefresh(parent context.Context) {
	defer func() {
		p.quota.mu.Lock()
		p.quota.running = false
		p.quota.finishedAt = time.Now()
		p.quota.mu.Unlock()
	}()
	p.runQuotaSweep(parent, quotaJobTimeout, quotaMinInterval, quotaCallTimeout)
}

func (p *Pool) quotaSweepTargets() ([]quotaTarget, []int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	targets, _, _ := p.quotaTargetsLocked()
	limit := len(p.accounts)
	if limit > quotaAccountLimit {
		limit = quotaAccountLimit
	}
	current := make(map[*Account]struct{}, limit)
	for _, acct := range p.accounts[:limit] {
		current[acct] = struct{}{}
	}
	p.quota.mu.Lock()
	defer p.quota.mu.Unlock()
	for acct := range p.quota.entries {
		if _, ok := current[acct]; !ok {
			delete(p.quota.entries, acct)
		}
	}
	start := 0
	if len(targets) > 0 {
		start = p.quota.nextTarget % len(targets)
	}
	pending := make([]int, len(targets))
	for i := range pending {
		pending[i] = (start + i) % len(targets)
	}
	return targets, pending
}

// A sweep processes each captured target once, in bounded batches. Only targets
// interrupted by the batch deadline are continued; real upstream failures count
// as attempts and are not retried until another refresh.
func (p *Pool) runQuotaSweep(parent context.Context, batchTimeout, interval, callTimeout time.Duration) {
	targets, pending := p.quotaSweepTargets()
	for len(pending) > 0 && parent.Err() == nil {
		started := time.Now()
		ctx, cancel := context.WithTimeout(parent, batchTimeout)
		pending = p.collectQuotaBatch(ctx, parent, targets, pending, callTimeout)
		cancel()
		if len(pending) == 0 || parent.Err() != nil {
			return
		}
		if delay := time.Until(started.Add(interval)); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-parent.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		p.quota.mu.Lock()
		p.quota.nextAttempt = time.Now().Add(interval)
		p.quota.mu.Unlock()
	}
}

func (p *Pool) collectQuotaBatch(ctx, parent context.Context, targets []quotaTarget, pending []int, callTimeout time.Duration) []int {
	jobs := make(chan int)
	completed := make([]bool, len(targets))
	var wg sync.WaitGroup
	for i := 0; i < quotaWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				completed[index] = p.collectAccountQuota(ctx, parent, targets[index], callTimeout)
			}
		}()
	}
	// The unbuffered queue limits work to available workers. Interrupted and
	// undispatched targets keep their old cache and are retried first.
dispatch:
	for _, index := range pending {
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- index:
		}
	}
	close(jobs)
	wg.Wait()
	next := make([]int, 0, len(pending))
	for _, index := range pending {
		if !completed[index] {
			next = append(next, index)
		}
	}
	p.quota.mu.Lock()
	if len(next) > 0 {
		p.quota.nextTarget = next[0]
	} else if len(pending) > 0 {
		p.quota.nextTarget = (pending[len(pending)-1] + 1) % len(targets)
	}
	p.quota.mu.Unlock()
	return next
}

func (p *Pool) collectAccountQuota(ctx, parent context.Context, target quotaTarget, callTimeout time.Duration) bool {
	if ctx.Err() != nil || parent.Err() != nil {
		return false
	}
	if !p.quotaTargetCurrent(target) {
		return true
	}
	var value upstream.CreditQuota
	var kind string
	switch {
	case target.uid == "":
		kind = "missing_identity"
	case target.owner == nil || target.client == nil || target.credential.SecurityToken == "" || target.credential.AccessKeyID == "" || target.credential.SecretAccessKey == "":
		kind = "missing_credentials"
	case target.expired || target.owner.Expired():
		kind = "credential_expired"
	default:
		if ctx.Err() != nil {
			return false
		}
		callCtx, cancel := context.WithTimeoutCause(ctx, callTimeout, errQuotaCallTimeout)
		var err error
		value, err = target.client.FetchCreditQuota(callCtx, target.credential)
		cause := context.Cause(callCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) && !errors.Is(cause, errQuotaCallTimeout) {
				return false
			}
			kind = quotaErrorKind(err)
		}
	}
	if parent.Err() == nil {
		p.publishQuota(target, value, kind)
		return true
	}
	return false
}

func quotaErrorKind(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errQuotaCallTimeout) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var source *upstream.SourceError
	if errors.As(err, &source) {
		switch source.Kind {
		case "auth", "http", "parse":
			return source.Kind
		}
	}
	return "upstream_error"
}

func (p *Pool) quotaTargetCurrent(target quotaTarget) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, acct := range p.accounts {
		if acct != target.account {
			continue
		}
		acct.mu.Lock()
		defer acct.mu.Unlock()
		return !acct.removed && acct.Auth == target.owner && quotaFingerprint(quotaCredential(acct.Auth)) == target.fingerprint
	}
	return false
}

func (p *Pool) publishQuota(target quotaTarget, value upstream.CreditQuota, kind string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, acct := range p.accounts {
		if acct != target.account {
			continue
		}
		acct.mu.Lock()
		defer acct.mu.Unlock()
		if acct.removed || acct.Auth != target.owner || quotaFingerprint(quotaCredential(acct.Auth)) != target.fingerprint {
			return
		}
		p.quota.mu.Lock()
		defer p.quota.mu.Unlock()
		if p.quota.entries == nil {
			p.quota.entries = make(map[*Account]quotaEntry)
		}
		entry := p.quota.entries[acct]
		if entry.owner != target.owner || entry.fingerprint != target.fingerprint {
			entry = quotaEntry{owner: target.owner, fingerprint: target.fingerprint}
		}
		entry.attemptedAt = time.Now()
		entry.errorKind = kind
		if kind == "" {
			entry.value = quotaCopyValue(value)
			entry.hasValue = true
			entry.successAt = entry.attemptedAt
		}
		p.quota.entries[acct] = entry
		return
	}
}

// accountQuotaRemainingLocked is used by List while holding pool and account
// locks. It returns an owned pointer only for a current, successful measurement.
func (p *Pool) accountQuotaRemainingLocked(acct *Account, now time.Time) *float64 {
	if acct.Auth == nil || acct.Auth.Expired() {
		return nil
	}
	fingerprint := quotaFingerprint(quotaCredential(acct.Auth))
	p.quota.mu.Lock()
	defer p.quota.mu.Unlock()
	entry, ok := p.quota.entries[acct]
	if !ok || entry.owner != acct.Auth || entry.fingerprint != fingerprint || entry.errorKind != "" ||
		!entry.hasValue || !entry.value.Available || now.Sub(entry.successAt) > quotaMaxAge {
		return nil
	}
	return quotaCopyFloat(entry.value.Remaining)
}

// QuotaSnapshot is a cache-only read and never starts provider traffic.
func (p *Pool) QuotaSnapshot() QuotaSnapshot {
	out := QuotaSnapshot{Unit: "credits", Scope: "distinct_account_uids", OverlapPossible: true,
		CreditPackagesOnly: true, ExcludesBenefitQuota: true,
		State: "unknown", Accounts: []AccountQuota{}}
	if p == nil {
		return out
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	targets, duplicates, unprocessed := p.quotaTargetsLocked()
	out.ConfiguredAccounts, out.CountedAccounts = len(p.accounts), len(targets)
	out.DuplicateAccounts, out.UnprocessedAccounts = duplicates, unprocessed
	p.quota.mu.Lock()
	defer p.quota.mu.Unlock()
	out.Refresh = p.quota.refreshLocked()
	var remaining, total, used float64
	allTotal, allUsed := true, true
	now := time.Now()
	for _, target := range targets {
		item := AccountQuota{Account: target.account.Name, UID: target.uid, State: "unknown"}
		entry, ok := p.quota.entries[target.account]
		if ok && entry.owner == target.owner && entry.fingerprint == target.fingerprint {
			item.LastAttemptAt, item.LastSuccessAt = quotaTime(entry.attemptedAt), quotaTime(entry.successAt)
			if entry.hasValue {
				v := quotaCopyValue(entry.value)
				item.Quota = &v
			}
			switch {
			case entry.errorKind != "":
				item.State, item.Error = "failed", entry.errorKind
			case !entry.hasValue:
				item.State = "unknown"
			case !entry.value.Available || entry.value.Remaining == nil:
				item.State = "unavailable"
			case now.Sub(entry.successAt) > quotaMaxAge:
				item.State = "stale"
			default:
				item.State = "ready"
			}
		}
		if target.expired {
			item.State, item.Error = "failed", "credential_expired"
		}
		switch item.State {
		case "ready":
			out.ReadyAccounts++
			remaining += *item.Quota.Remaining
			if item.Quota.Total == nil {
				allTotal = false
			} else {
				total += *item.Quota.Total
			}
			if item.Quota.Used == nil {
				allUsed = false
			} else {
				used += *item.Quota.Used
			}
			if out.OldestSuccessAt == nil || entry.successAt.Before(*out.OldestSuccessAt) {
				out.OldestSuccessAt = quotaTime(entry.successAt)
			}
		case "failed":
			out.FailedAccounts++
		case "unavailable":
			out.UnavailableAccounts++
		case "stale":
			out.StaleAccounts++
		default:
			out.UnknownAccounts++
		}
		out.Accounts = append(out.Accounts, item)
	}
	out.Complete = len(targets) > 0 && unprocessed == 0 && out.ReadyAccounts == len(targets) &&
		!math.IsInf(remaining, 0) && !math.IsNaN(remaining)
	if out.ReadyAccounts > 0 && !math.IsInf(remaining, 0) && !math.IsNaN(remaining) {
		out.KnownRemaining = quotaCopyFloat(&remaining)
	}
	switch {
	case out.ConfiguredAccounts == 0:
		out.State = "empty"
	case out.Complete:
		out.State = "ready"
		out.Remaining = &remaining
		if allTotal && !math.IsInf(total, 0) && !math.IsNaN(total) {
			out.Total = &total
		}
		if allUsed && !math.IsInf(used, 0) && !math.IsNaN(used) {
			out.Used = &used
		}
	case out.ReadyAccounts > 0:
		out.State = "partial"
	case out.Refresh.Running:
		out.State = "loading"
	case out.FailedAccounts > 0:
		out.State = "failed"
	case out.StaleAccounts > 0:
		out.State = "stale"
	case out.UnavailableAccounts > 0:
		out.State = "unavailable"
	}
	return out
}
