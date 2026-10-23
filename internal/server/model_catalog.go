package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

const (
	catalogAccountLimit    = 512
	catalogModelLimit      = 2048
	catalogExclusionLimit  = 512
	catalogWorkerLimit     = 2
	catalogJobTimeout      = 60 * time.Second
	catalogRefreshInterval = 15 * time.Second
)

type modelRefreshStatus struct {
	ID                 string     `json:"id"`
	State              string     `json:"state"`
	StartedAt          *time.Time `json:"started_at"`
	FinishedAt         *time.Time `json:"finished_at"`
	RetryAt            *time.Time `json:"retry_at"`
	Total              int        `json:"total"`
	Completed          int        `json:"completed"`
	Failed             int        `json:"failed"`
	Skipped            int        `json:"skipped"`
	NegativeResetCount int        `json:"negative_reset_count"`
}

type modelCatalogState struct {
	mu         sync.Mutex
	refresh    modelRefreshStatus
	serviceCtx context.Context
	// Private test overrides remain bounded by the production maxima.
	jobTimeout  time.Duration
	minInterval time.Duration
}

func copyRefresh(s modelRefreshStatus) modelRefreshStatus {
	copyTime := func(p *time.Time) *time.Time {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	s.StartedAt, s.FinishedAt, s.RetryAt = copyTime(s.StartedAt), copyTime(s.FinishedAt), copyTime(s.RetryAt)
	if s.State == "" {
		s.State = "idle"
	}
	return s
}

func (h *Handler) modelRefreshSnapshot() modelRefreshStatus {
	h.modelCatalog.mu.Lock()
	defer h.modelCatalog.mu.Unlock()
	return copyRefresh(h.modelCatalog.refresh)
}

// StartModelDiscovery attaches shared discovery to the service lifetime. Clients
// only receive snapshots; a disconnected refresh caller cannot cancel the job.
func (h *Handler) StartModelDiscovery(ctx context.Context) {
	h.modelCatalog.mu.Lock()
	h.modelCatalog.serviceCtx = ctx
	h.modelCatalog.mu.Unlock()
	h.requestModelRefresh(false)
}

func (h *Handler) catalogAccounts() []*pool.Account {
	if h.cfg.Pool == nil {
		return nil
	}
	accounts := h.cfg.Pool.Accounts()
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Name < accounts[j].Name })
	return accounts
}

func (h *Handler) requestModelRefresh(force bool) modelRefreshStatus {
	accounts := h.catalogAccounts()
	if !force {
		needed := false
		for i, acct := range accounts {
			if i >= catalogAccountLimit {
				break
			}
			if h.cfg.Pool.Healthy(acct.Name) && upstream.AccountCatalogStaleForAuth(acct.UID, acct.AuthSnapshot()) {
				needed = true
				break
			}
		}
		if !needed {
			return h.modelRefreshSnapshot()
		}
	}
	h.modelCatalog.mu.Lock()
	current := h.modelCatalog.refresh
	now := time.Now().UTC()
	if current.State == "running" || (current.RetryAt != nil && now.Before(*current.RetryAt)) {
		h.modelCatalog.mu.Unlock()
		return copyRefresh(current)
	}
	ctx := h.modelCatalog.serviceCtx
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		h.modelCatalog.mu.Unlock()
		return copyRefresh(current)
	}
	negativeRevision := availabilityRevision()
	now = time.Now().UTC()
	interval := catalogRefreshInterval
	if h.modelCatalog.minInterval > 0 && h.modelCatalog.minInterval < interval {
		interval = h.modelCatalog.minInterval
	}
	retry := now.Add(interval)
	current = modelRefreshStatus{ID: randHex(24), State: "running", StartedAt: &now, RetryAt: &retry, Total: len(accounts)}
	h.modelCatalog.refresh = current
	timeout := catalogJobTimeout
	if h.modelCatalog.jobTimeout > 0 && h.modelCatalog.jobTimeout < timeout {
		timeout = h.modelCatalog.jobTimeout
	}
	h.modelCatalog.mu.Unlock()
	go h.runModelRefresh(ctx, current, accounts, force, timeout, negativeRevision)
	return copyRefresh(current)
}

type catalogWork struct {
	acct  *pool.Account
	authz *auth.Auth
}

func (h *Handler) runModelRefresh(parent context.Context, initial modelRefreshStatus, accounts []*pool.Account, force bool, timeout time.Duration, negativeRevision uint64) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// Snapshot at most 512 configured accounts, including exclusions. Discard
	// removed/old-login source snapshots before any fresh result is published.
	limit := len(accounts)
	if limit > catalogAccountLimit {
		limit = catalogAccountLimit
	}
	current := make(map[string]*auth.Auth, limit)
	work := make([]catalogWork, 0, limit)
	skipped := len(accounts) - limit
	for _, acct := range accounts[:limit] {
		authz := acct.AuthSnapshot()
		current[acct.UID] = authz
		if eligible, _ := h.cfg.Pool.ModelDiscoveryEligibility(acct, authz); !eligible {
			skipped++
			continue
		}
		if !force && !upstream.AccountCatalogStaleForAuth(acct.UID, acct.AuthSnapshot()) {
			skipped++
			continue
		}
		work = append(work, catalogWork{acct, authz})
	}
	upstream.PruneModelCatalogs(current)
	h.cfg.Upstream.PruneModelDiscoveryBackoff(current)
	h.modelCatalog.mu.Lock()
	h.modelCatalog.refresh.Skipped = skipped
	h.modelCatalog.mu.Unlock()
	jobs := make(chan catalogWork)
	var wg sync.WaitGroup
	successful := 0
	for i := 0; i < catalogWorkerLimit; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					h.catalogSkip()
					continue
				}
				// Only bounded jobs validate credentials; cached admin GETs never
				// reach this path. Share the pool's refresh with chat/watch callers
				// rather than sending expired STS to every model source.
				if eligible, _ := h.cfg.Pool.ModelDiscoveryEligibility(item.acct, item.authz); !eligible {
					h.catalogSkip()
					continue
				}
				valid, err := h.cfg.Pool.ValidateContext(ctx, item.acct)
				if ctx.Err() != nil {
					h.catalogSkip()
					continue
				}
				if err != nil || !valid {
					h.catalogCredentialFailure()
					continue
				}
				if eligible, _ := h.cfg.Pool.ModelDiscoveryEligibility(item.acct, item.authz); !eligible {
					h.catalogSkip()
					continue
				}
				d := h.cfg.Upstream.DiscoverModels(ctx, item.authz, force)
				resets := 0
				published := h.cfg.Pool.WithCurrentAuth(item.acct, item.authz, func() {
					_, fresh := upstream.PublishModelDiscovery(item.acct.UID, item.authz, d)
					if force {
						resets = invalidateCatalogNegatives(item.acct.UID, fresh, *initial.StartedAt, negativeRevision)
					}
				})
				h.modelCatalog.mu.Lock()
				if !published {
					h.modelCatalog.refresh.Skipped++
				} else {
					h.modelCatalog.refresh.Completed++
					if d.FailedSources() > 0 {
						h.modelCatalog.refresh.Failed++
					}
					if d.Successes() > 0 {
						successful++
					}
					h.modelCatalog.refresh.NegativeResetCount += resets
				}
				h.modelCatalog.mu.Unlock()
			}
		}()
	}
	for i, item := range work {
		select {
		case jobs <- item:
		case <-ctx.Done():
			h.modelCatalog.mu.Lock()
			h.modelCatalog.refresh.Skipped += len(work) - i
			h.modelCatalog.mu.Unlock()
			close(jobs)
			wg.Wait()
			h.finishModelRefresh(successful)
			return
		}
	}
	close(jobs)
	wg.Wait()
	h.finishModelRefresh(successful)
}

func (h *Handler) catalogSkip() {
	h.modelCatalog.mu.Lock()
	h.modelCatalog.refresh.Skipped++
	h.modelCatalog.mu.Unlock()
}

func (h *Handler) catalogCredentialFailure() {
	h.modelCatalog.mu.Lock()
	h.modelCatalog.refresh.Completed++
	h.modelCatalog.refresh.Failed++
	h.modelCatalog.mu.Unlock()
}

func (h *Handler) finishModelRefresh(successful int) {
	h.modelCatalog.mu.Lock()
	defer h.modelCatalog.mu.Unlock()
	s := &h.modelCatalog.refresh
	now := time.Now().UTC()
	s.FinishedAt = &now
	switch {
	case s.Total == 0:
		s.State = "completed"
	case successful == 0:
		s.State = "failed"
	case s.Failed > 0 || s.Skipped > 0:
		s.State = "partial"
	default:
		s.State = "completed"
	}
}

func (h *Handler) adminModelsRefresh(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	defer r.Body.Close()
	var body map[string]json.RawMessage
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil || body == nil || len(body) != 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "expected an empty JSON object")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "expected one JSON object")
		return
	}
	if r.Context().Err() != nil {
		return
	}
	s := h.requestModelRefresh(true)
	if s.RetryAt != nil {
		seconds := int(time.Until(*s.RetryAt).Seconds()) + 1
		if seconds > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"refresh": s})
}

type catalogModelSummary struct {
	ID              string `json:"id"`
	Origin          string `json:"origin"`
	CatalogAccounts int    `json:"catalog_accounts"`
	BenefitAccounts int    `json:"benefit_accounts"`
	BlockedAccounts int    `json:"blocked_accounts"`
}

type catalogAccountSummary struct {
	Account        string                  `json:"account"`
	Eligible       bool                    `json:"eligible"`
	ExcludedReason *string                 `json:"excluded_reason"`
	LastSuccessAt  *time.Time              `json:"last_success_at"`
	Sources        []upstream.SourceStatus `json:"sources"`
	Claim          upstream.ClaimStatus    `json:"claim"`
}

type catalogExclusion struct {
	Account    string    `json:"account"`
	Model      string    `json:"model"`
	Reason     string    `json:"reason"`
	RecordedAt time.Time `json:"recorded_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type catalogTruncation struct {
	Accounts   int `json:"accounts"`
	Models     int `json:"models"`
	Exclusions int `json:"exclusions"`
}
type catalogPage struct {
	Models     []catalogModelSummary   `json:"models"`
	Accounts   []catalogAccountSummary `json:"accounts"`
	Exclusions []catalogExclusion      `json:"exclusions"`
	Refresh    modelRefreshStatus      `json:"refresh"`
	Truncated  catalogTruncation       `json:"truncated"`
}

func catalogText(s string) string {
	if len(s) > 192 {
		s = s[:192]
	}
	return strings.ToValidUTF8(strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s), "")
}

func (h *Handler) catalogSnapshot() catalogPage {
	page := catalogPage{Models: []catalogModelSummary{}, Accounts: []catalogAccountSummary{}, Exclusions: []catalogExclusion{}, Refresh: h.modelRefreshSnapshot()}
	accounts := h.catalogAccounts()
	if len(accounts) > catalogAccountLimit {
		page.Truncated.Accounts = len(accounts) - catalogAccountLimit
		accounts = accounts[:catalogAccountLimit]
	}
	models := map[string]*catalogModelSummary{}
	omittedModels := map[string]struct{}{}
	accountNames := map[string]string{}
	accountAuths := map[string]*auth.Auth{}
	for _, acct := range accounts {
		authz := acct.AuthSnapshot()
		eligible, reason := h.cfg.Pool.ModelDiscoveryEligibility(acct, authz)
		d := upstream.AccountModelDiagnosticsForAuth(acct.UID, authz)
		d.Claim.Enabled = h.cfg.Upstream.BenefitAutoClaim()
		a := catalogAccountSummary{Account: catalogText(acct.Name), Eligible: eligible, LastSuccessAt: d.LastSuccessAt, Sources: d.Sources, Claim: d.Claim}
		if reason != "" {
			a.ExcludedReason = &reason
		}
		page.Accounts = append(page.Accounts, a)
		accountNames[acct.UID] = a.Account
		accountAuths[acct.UID] = authz
		for _, mi := range d.Models {
			key := strings.ToLower(mi.ID)
			m := models[key]
			if m == nil {
				if len(models) >= catalogModelLimit {
					omittedModels[key] = struct{}{}
					continue
				}
				m = &catalogModelSummary{ID: mi.ID, Origin: "discovered"}
				models[key] = m
			}
			m.CatalogAccounts++
			if mi.Benefit {
				m.BenefitAccounts++
			}
		}
	}
	for _, sm := range staticModels {
		key := strings.ToLower(sm.ID)
		if models[key] == nil {
			if len(models) >= catalogModelLimit {
				omittedModels[key] = struct{}{}
				continue
			}
			models[key] = &catalogModelSummary{ID: sm.ID, Origin: "static"}
		}
	}
	page.Truncated.Models = len(omittedModels)
	// Availability is read after all pool/account snapshots; no lock inversion
	// with the atomic pool -> account -> catalog/availability publisher.
	now := time.Now()
	type negativeSnapshot struct {
		accountID string
		model     string
		entry     availEntry
	}
	var negatives []negativeSnapshot
	availability.Lock()
	for key, entry := range availability.byKey {
		if entry.state != availUnusable || now.Sub(entry.at) > availabilityTTL {
			continue
		}
		sep := strings.LastIndexByte(key, '|')
		if sep < 0 {
			continue
		}
		if _, ok := accountNames[key[:sep]]; !ok {
			continue
		}
		negatives = append(negatives, negativeSnapshot{accountID: key[:sep], model: key[sep+1:], entry: entry})
	}
	availability.Unlock()
	// Resolution reads account catalog locks, so it must run after releasing
	// availability: publication holds pool -> catalog -> availability.
	for _, snapshot := range negatives {
		entry, model := snapshot.entry, snapshot.model
		owner := accountAuths[snapshot.accountID]
		exact, _ := upstream.ResolveModelForAuth(snapshot.accountID, entry.wireID, owner)
		if !availabilityEntryMatches(entry, exact, owner) {
			continue
		}
		if m := models[model]; m != nil {
			m.BlockedAccounts++
		}
		if len(page.Exclusions) >= catalogExclusionLimit {
			page.Truncated.Exclusions++
			continue
		}
		page.Exclusions = append(page.Exclusions, catalogExclusion{Account: accountNames[snapshot.accountID], Model: catalogText(model), Reason: availabilityReasonCode(entry.reason), RecordedAt: entry.at, ExpiresAt: entry.at.Add(availabilityTTL)})
	}
	for _, m := range models {
		page.Models = append(page.Models, *m)
	}
	sort.Slice(page.Models, func(i, j int) bool { return strings.ToLower(page.Models[i].ID) < strings.ToLower(page.Models[j].ID) })
	sort.Slice(page.Exclusions, func(i, j int) bool {
		if page.Exclusions[i].Account != page.Exclusions[j].Account {
			return page.Exclusions[i].Account < page.Exclusions[j].Account
		}
		return page.Exclusions[i].Model < page.Exclusions[j].Model
	})
	return page
}

func (h *Handler) adminModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.catalogSnapshot())
}

func (h *Handler) cachedDynamicModels() []upstream.ModelInfo {
	var sets [][]upstream.ModelInfo
	for i, acct := range h.catalogAccounts() {
		if i >= catalogAccountLimit {
			break
		}
		if infos, known := upstream.AccountModelsForAuth(acct.UID, acct.AuthSnapshot()); known {
			sets = append(sets, infos)
		}
	}
	return upstream.MergeModels(sets...)
}
