package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"codearts2api/internal/auth"
)

const maxSourceModels = 256
const modelSourceTimeout = 15 * time.Second

var modelSources = [...]string{"agent", "builtin", "benefit"}

// NewWithModelEndpoints replaces only metadata boundaries (e.g. a localhost
// protocol fixture). Endpoints are constructor inputs, never accepted by admin APIs.
func NewWithModelEndpoints(timeout time.Duration, snapBase, benefitBase string) *Client {
	c := New(timeout)
	if snapBase != "" {
		c.snapBase = strings.TrimRight(snapBase, "/")
	}
	if benefitBase != "" {
		c.benefitBase = strings.TrimRight(benefitBase, "/")
	}
	return c
}

// SourceError deliberately contains no arbitrary upstream text or response data.
type SourceError struct {
	Kind         string `json:"kind"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	ProviderCode string `json:"provider_code,omitempty"`
}

func (e *SourceError) Error() string { return "model discovery " + e.Kind }

type SourceStatus struct {
	Source        string       `json:"source"`
	Status        string       `json:"status"`
	LastAttemptAt *time.Time   `json:"last_attempt_at"`
	LastSuccessAt *time.Time   `json:"last_success_at"`
	RetryAt       *time.Time   `json:"retry_at"`
	Count         int          `json:"count"`
	Retained      int          `json:"retained"`
	Error         *SourceError `json:"error,omitempty"`
}

type ClaimStatus struct {
	Enabled       bool         `json:"enabled"`
	Attempted     bool         `json:"attempted"`
	AttemptedAt   *time.Time   `json:"attempted_at,omitempty"`
	Succeeded     bool         `json:"succeeded,omitempty"`
	Error         *SourceError `json:"error,omitempty"`
	SkippedReason string       `json:"skipped_reason,omitempty"`
}

type sourceSnapshot struct {
	SourceStatus
	models []ModelInfo
}

// ModelDiscovery is an unpublished result. The server validates account/auth
// identity before atomically publishing it; network calls never run in pool locks.
type ModelDiscovery struct {
	sources map[string]sourceSnapshot
	Claim   ClaimStatus
}

func (d ModelDiscovery) Successes() int {
	n := 0
	for _, s := range d.sources {
		if modelSourceSucceeded(s.Status) {
			n++
		}
	}
	return n
}

func (d ModelDiscovery) FailedSources() int { return len(modelSources) - d.Successes() }

// An explicitly disabled channel is a successful, authoritative empty source.
// Failed/unknown switches must retain the last successful contribution instead.
func modelSourceSucceeded(status string) bool {
	return status == "ok" || status == "empty" || status == "disabled"
}

// ModelCatalogSnapshot owns its returned slices and timestamp/error pointers.
type ModelCatalogSnapshot struct {
	Models        []ModelInfo
	Sources       []SourceStatus
	Claim         ClaimStatus
	LastSuccessAt *time.Time
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

func cloneSource(s SourceStatus) SourceStatus {
	s.LastAttemptAt, s.LastSuccessAt, s.RetryAt = cloneTime(s.LastAttemptAt), cloneTime(s.LastSuccessAt), cloneTime(s.RetryAt)
	if s.Error != nil {
		e := *s.Error
		s.Error = &e
	}
	return s
}

func cloneClaim(c ClaimStatus) ClaimStatus {
	c.AttemptedAt = cloneTime(c.AttemptedAt)
	if c.Error != nil {
		e := *c.Error
		c.Error = &e
	}
	return c
}

func AccountModelDiagnostics(accountID string) ModelCatalogSnapshot {
	return accountModelDiagnostics(accountID, nil, false)
}

func AccountModelDiagnosticsForAuth(accountID string, owner *auth.Auth) ModelCatalogSnapshot {
	return accountModelDiagnostics(accountID, owner, true)
}

func accountModelDiagnostics(accountID string, owner *auth.Auth, checkOwner bool) ModelCatalogSnapshot {
	catalog.RLock()
	defer catalog.RUnlock()
	out := ModelCatalogSnapshot{Models: []ModelInfo{}, Sources: []SourceStatus{}}
	ac := catalog.accounts[accountID]
	if ac != nil && checkOwner && ac.owner != nil && ac.owner != owner {
		ac = nil
	}
	for _, name := range modelSources {
		s := SourceStatus{Source: name, Status: "unknown"}
		if ac != nil {
			if snap, ok := ac.sources[name]; ok {
				s = cloneSource(snap.SourceStatus)
			}
		}
		out.Sources = append(out.Sources, s)
		if s.LastSuccessAt != nil && (out.LastSuccessAt == nil || s.LastSuccessAt.After(*out.LastSuccessAt)) {
			out.LastSuccessAt = cloneTime(s.LastSuccessAt)
		}
	}
	if ac != nil {
		out.Models = append(out.Models, ac.infos...)
		out.Claim = cloneClaim(ac.claim)
	}
	return out
}

// PruneModelCatalogs also drops old-login snapshots. Direct compatibility
// SetAccountModels snapshots have no owner until the next real discovery.
func PruneModelCatalogs(current map[string]*auth.Auth) {
	catalog.Lock()
	defer catalog.Unlock()
	for id, ac := range catalog.accounts {
		owner, ok := current[id]
		if !ok || (ac.owner != nil && owner != ac.owner) {
			delete(catalog.accounts, id)
		}
	}
	rebuildKnownLocked()
}

func indexCatalog(ac *accountCatalog) {
	ac.knows, ac.benefit = map[string]bool{}, map[string]bool{}
	ac.exact = make(map[string]string, len(ac.infos))
	for _, mi := range ac.infos {
		key := strings.ToLower(mi.ID)
		ac.knows[key] = true
		ac.exact[key] = mi.ID
		if mi.Benefit {
			ac.benefit[key] = true
		}
	}
	// Display merging may prefer a richer normal-source record with different
	// casing. A benefit-routed request must use the benefit source's exact ID.
	for _, mi := range ac.sources["benefit"].models {
		key := strings.ToLower(mi.ID)
		if ac.benefit[key] {
			ac.exact[key] = mi.ID
		}
	}
}

func rebuildKnownLocked() {
	catalog.known = map[string]string{}
	for _, id := range seedKnownModels {
		catalog.known[strings.ToLower(id)] = id
	}
	for _, id := range seedBenefitModels {
		catalog.known[strings.ToLower(id)] = id
	}
	ids := make([]string, 0, len(catalog.accounts))
	for id := range catalog.accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ac := catalog.accounts[id]
		for _, mi := range ac.infos {
			key := strings.ToLower(mi.ID)
			if _, ok := catalog.known[key]; !ok || mi.ID != key {
				catalog.known[key] = mi.ID
			}
		}
	}
}

func (c *Client) PruneModelDiscoveryBackoff(current map[string]*auth.Auth) {
	c.srcMu.Lock()
	defer c.srcMu.Unlock()
	now := time.Now()
	for key, until := range c.srcFail {
		sep := strings.LastIndexByte(key, '|')
		if sep < 0 {
			delete(c.srcFail, key)
			continue
		}
		if _, ok := current[key[:sep]]; !ok || !now.Before(until) {
			delete(c.srcFail, key)
		}
	}
}

// PublishModelDiscovery returns exactly the committed effective catalog and the
// exact fresh IDs whose governing route source succeeded. A retained benefit
// route requires a fresh benefit response before a negative can be invalidated.
func PublishModelDiscovery(accountID string, owner *auth.Auth, d ModelDiscovery) ([]ModelInfo, []string) {
	if accountID == "" || owner == nil {
		return nil, nil
	}
	catalog.Lock()
	defer catalog.Unlock()
	ac := &accountCatalog{sources: map[string]sourceSnapshot{}, owner: owner, claim: cloneClaim(d.Claim), fetched: time.Now()}
	prev := catalog.accounts[accountID]
	if prev != nil && prev.owner != nil && prev.owner != owner {
		prev = nil
	}
	ac.known = d.Successes() > 0 || (prev != nil && prev.known)
	var sets [][]ModelInfo
	for _, name := range modelSources {
		s := d.sources[name]
		s.SourceStatus = cloneSource(s.SourceStatus)
		if !modelSourceSucceeded(s.Status) && prev != nil {
			if old, ok := prev.sources[name]; ok {
				s.models = append([]ModelInfo(nil), old.models...)
				s.LastSuccessAt = cloneTime(old.LastSuccessAt)
				if s.LastAttemptAt == nil {
					s.LastAttemptAt = cloneTime(old.LastAttemptAt)
				}
				s.Retained = len(s.models)
			}
		}
		s.Count = len(s.models)
		ac.sources[name] = s
		sets = append(sets, s.models)
	}
	ac.infos = MergeModels(sets...)
	indexCatalog(ac)
	catalog.accounts[accountID] = ac
	rebuildKnownLocked()
	fresh := make([]string, 0, len(ac.infos))
	for _, mi := range ac.infos {
		for _, name := range modelSources {
			if mi.Benefit && name != "benefit" {
				continue
			}
			s := ac.sources[name]
			if !modelSourceSucceeded(s.Status) {
				continue
			}
			found := false
			for _, item := range s.models {
				if strings.EqualFold(item.ID, mi.ID) {
					found = true
					break
				}
			}
			if found {
				fresh = append(fresh, mi.ID)
				break
			}
		}
	}
	return append([]ModelInfo(nil), ac.infos...), fresh
}

// DiscoverModels carries one deadline through every source and optional claim.
// It does not mutate account catalogs. force bypasses each source's backoff once.
func (c *Client) DiscoverModels(ctx context.Context, acct *auth.Auth, force bool) ModelDiscovery {
	d := ModelDiscovery{sources: map[string]sourceSnapshot{}, Claim: ClaimStatus{Enabled: c.BenefitAutoClaim()}}
	if acct == nil {
		return d
	}
	token, ak, sk := acct.Credentials()
	cred := SignCredential{AccessKeyID: ak, SecretAccessKey: sk, SecurityToken: token}
	for _, name := range modelSources {
		s := sourceSnapshot{SourceStatus: SourceStatus{Source: name}}
		key := acct.UserID + "|" + name
		if ctx.Err() != nil {
			s.Status, s.Error = "canceled", discoveryError(ctx.Err())
			d.sources[name] = s
			continue
		}
		c.srcMu.Lock()
		retry := c.srcFail[key]
		c.srcMu.Unlock()
		if !force && time.Now().Before(retry) {
			s.Status, s.RetryAt, s.Error = "backoff", &retry, &SourceError{Kind: "backoff"}
			d.sources[name] = s
			continue
		}
		attempt := time.Now().UTC()
		s.LastAttemptAt = &attempt
		sourceCtx, cancelSource := context.WithTimeout(ctx, modelSourceTimeout)
		var err error
		channelDisabled := false
		switch name {
		case "agent":
			s.models, err = c.fetchAgentModelsContext(sourceCtx, cred)
		case "builtin":
			s.models, err = c.fetchBuiltinModelsContext(sourceCtx, cred)
		case "benefit":
			var enabled bool
			enabled, err = c.fetchBenefitGatewayEnabled(sourceCtx, cred)
			if err != nil {
				break
			}
			if !enabled {
				channelDisabled = true
				d.Claim.SkippedReason = "channel_disabled"
				break
			}
			if d.Claim.Enabled {
				claimAttempt := time.Now().UTC()
				d.Claim.Attempted, d.Claim.AttemptedAt = true, &claimAttempt
				claimCtx, cancelClaim := context.WithTimeout(sourceCtx, 5*time.Second)
				claimErr := c.ClaimBenefitContext(claimCtx, cred)
				cancelClaim()
				d.Claim.Succeeded = claimErr == nil
				if claimErr != nil {
					d.Claim.Error = discoveryError(claimErr)
				}
			}
			s.models, err = c.fetchBenefitModelsContext(sourceCtx, cred)
		}
		cancelSource()
		if err == nil {
			s.Status = "ok"
			if channelDisabled {
				s.Status = "disabled"
			} else if len(s.models) == 0 {
				s.Status = "empty"
			}
			success := time.Now().UTC()
			s.LastSuccessAt, s.Count = &success, len(s.models)
			c.clearSrcFail(key)
		} else {
			s.Status, s.Error = "failed", discoveryError(err)
			if errors.Is(ctx.Err(), context.Canceled) {
				s.Status = "canceled"
			} else {
				c.noteSrcFail(key)
				c.srcMu.Lock()
				retry = c.srcFail[key]
				c.srcMu.Unlock()
				s.RetryAt = &retry
			}
		}
		d.sources[name] = s
	}
	return d
}

func discoveryError(err error) *SourceError {
	var safe *SourceError
	if errors.As(err, &safe) {
		e := *safe
		return &e
	}
	if errors.Is(err, context.Canceled) {
		return &SourceError{Kind: "canceled"}
	}
	var nerr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &nerr) && nerr.Timeout()) {
		return &SourceError{Kind: "timeout"}
	}
	return &SourceError{Kind: "unknown"}
}

// Every metadata request checks read errors and overflow before accepting JSON.
func (c *Client) modelRequest(ctx context.Context, method, endpoint, agentType string, cred SignCredential, maxBytes int64) ([]byte, error) {
	return c.modelRequestWithStatus(ctx, method, endpoint, agentType, cred, maxBytes, 0)
}

// requiredStatus optionally narrows a source's success contract. In particular,
// a pending or partial switch response must never revoke confirmed routes.
func (c *Client) modelRequestWithStatus(ctx context.Context, method, endpoint, agentType string, cred SignCredential, maxBytes int64, requiredStatus int) ([]byte, error) {
	var payload []byte
	if method == http.MethodPost {
		payload = []byte("{}")
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Language", "zh-cn")
	if agentType != "" {
		req.Header.Set("Agent-Type", agentType)
	}
	if cred.SecurityToken != "" {
		req.Header.Set("X-Security-Token", cred.SecurityToken)
	}
	signRequest(req, payload, cred)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || (requiredStatus != 0 && resp.StatusCode != requiredStatus) {
		kind := "http"
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			kind = "auth"
		}
		return nil, &SourceError{Kind: kind, HTTPStatus: resp.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes || !utf8.Valid(raw) {
		return nil, &SourceError{Kind: "parse"}
	}
	return raw, nil
}

func requiredObject(raw []byte, key string) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return nil, &SourceError{Kind: "parse"}
	}
	value, ok := obj[key]
	if !ok || len(value) == 0 || value[0] != '{' {
		return nil, &SourceError{Kind: "parse"}
	}
	return value, nil
}

func requiredArray(raw []byte, key string) ([]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return nil, &SourceError{Kind: "parse"}
	}
	value, ok := obj[key]
	if !ok || len(value) == 0 || value[0] != '[' {
		return nil, &SourceError{Kind: "parse"}
	}
	var entries []json.RawMessage
	if json.Unmarshal(value, &entries) != nil || len(entries) > maxSourceModels {
		return nil, &SourceError{Kind: "parse"}
	}
	return entries, nil
}

type wireModel struct {
	ModelAlias    string `json:"model_alias"`
	ModelName     string `json:"model_name"`
	ModelID       string `json:"model_id"`
	ModelDesc     string `json:"model_desc"`
	ContextWindow int64  `json:"context_window"`
	MaxTokens     int64  `json:"max_tokens"`
	Params        struct {
		ContextWindow int64  `json:"context_window"`
		MaxTokens     int64  `json:"max_tokens"`
		ModelID       string `json:"model_id"`
		ModelDesc     string `json:"model_desc"`
	} `json:"model_parameters"`
}

func boundedModelText(s string, max int) string {
	if len(s) > max {
		s = s[:max]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
}

func parseModelEntries(entries []json.RawMessage, source string) ([]ModelInfo, error) {
	out := make([]ModelInfo, 0, len(entries))
	for _, raw := range entries {
		var m wireModel
		if json.Unmarshal(raw, &m) != nil {
			return nil, &SourceError{Kind: "parse"}
		}
		id := firstNonEmpty(m.ModelID, m.ModelName)
		if source == "agent" {
			// Existing captured client fixture prefers model_name. Do not guess a
			// different inference identifier when opaque IDs and names coexist.
			id = firstNonEmpty(m.ModelName, m.ModelID, m.ModelAlias, m.Params.ModelID)
			m.ContextWindow, m.MaxTokens, m.ModelDesc = m.Params.ContextWindow, m.Params.MaxTokens, m.Params.ModelDesc
		}
		if id == "" || len(id) > 192 || !utf8.ValidString(id) || boundedModelText(id, 192) != id || m.ContextWindow < 0 || m.MaxTokens < 0 {
			return nil, &SourceError{Kind: "parse"}
		}
		out = append(out, ModelInfo{ID: id, Name: boundedModelText(id, 192), ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens, Desc: boundedModelText(m.ModelDesc, 512), Benefit: source == "benefit"})
	}
	return MergeModels(out), nil
}

func (c *Client) fetchAgentModelsContext(ctx context.Context, cred SignCredential) ([]ModelInfo, error) {
	raw, err := c.modelRequest(ctx, http.MethodGet, c.snapURL(EpAgentList)+"?offset=0&limit=100&is_primary_agent=true", "AgentCenter", cred, 4<<20)
	if err != nil {
		return nil, err
	}
	entries, err := requiredArray(raw, "agents")
	if err != nil {
		return nil, err
	}
	// The official request is paginated at 100. A full page without following
	// pages cannot establish which agent is primary, so never publish it as a
	// complete source (or interpret a missing later CodeAgent as removal).
	if len(entries) >= 100 {
		return nil, &SourceError{Kind: "parse"}
	}
	type wireAgent struct {
		ID      string `json:"agent_id"`
		Name    string `json:"agent_name"`
		Primary bool   `json:"is_primary_agent"`
		Show    bool   `json:"show_in_ide"`
		Alias   struct {
			Zh string `json:"alias_zh_cn"`
		} `json:"alias"`
	}
	agents := make([]wireAgent, 0, len(entries))
	for _, entry := range entries {
		var a wireAgent
		if json.Unmarshal(entry, &a) != nil || a.ID == "" || len(a.ID) > 192 {
			return nil, &SourceError{Kind: "parse"}
		}
		agents = append(agents, a)
	}
	if len(agents) == 0 {
		return []ModelInfo{}, nil
	}
	chosen := agents[0].ID
	for _, a := range agents {
		if a.Primary {
			chosen = a.ID
			break
		}
	}
	for _, a := range agents {
		if a.Name == "CodeAgent" && a.Show && a.Alias.Zh == "智能体" {
			chosen = a.ID
			break
		}
	}
	raw, err = c.modelRequest(ctx, http.MethodGet, c.snapURL(EpAgentDetail)+"?agent_id="+url.QueryEscape(chosen), "AgentCenter", cred, 4<<20)
	if err != nil {
		return nil, err
	}
	gpts, err := requiredObject(raw, "gpts")
	if err != nil {
		return nil, err
	}
	entries, err = requiredArray(gpts, "models")
	if err != nil {
		return nil, err
	}
	return parseModelEntries(entries, "agent")
}

func (c *Client) fetchBuiltinModelsContext(ctx context.Context, cred SignCredential) ([]ModelInfo, error) {
	raw, err := c.modelRequest(ctx, http.MethodGet, c.snapURL(EpModelBuiltin), "PromptCenter", cred, 4<<20)
	if err != nil {
		return nil, err
	}
	entries, err := requiredArray(raw, "builtinModels")
	if err != nil {
		return nil, err
	}
	return parseModelEntries(entries, "builtin")
}

func (c *Client) fetchBenefitGatewayEnabled(ctx context.Context, cred SignCredential) (bool, error) {
	raw, err := c.modelRequestWithStatus(ctx, http.MethodGet, c.snapURL(EpBenefitGatewayConfig), "PromptCenter", cred, 1<<20, http.StatusOK)
	if err != nil {
		return false, err
	}
	// A missing/null/ambiguous switch cannot authoritatively revoke a route.
	// Read the exact key once; unrelated future metadata fields remain allowed.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return false, &SourceError{Kind: "parse"}
	}
	found, enabled := false, false
	for decoder.More() {
		key, keyErr := decoder.Token()
		var value json.RawMessage
		if keyErr != nil || decoder.Decode(&value) != nil {
			return false, &SourceError{Kind: "parse"}
		}
		if key != "enabled" {
			continue
		}
		value = bytes.TrimSpace(value)
		if found || (!bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false"))) {
			return false, &SourceError{Kind: "parse"}
		}
		found, enabled = true, bytes.Equal(value, []byte("true"))
	}
	end, err := decoder.Token()
	var extra any
	if err != nil || end != json.Delim('}') || !found || decoder.Decode(&extra) != io.EOF {
		return false, &SourceError{Kind: "parse"}
	}
	return enabled, nil
}

func benefitEnvelope(raw []byte) error {
	var envelope struct {
		Code string `json:"error_code"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Code == "" {
		return &SourceError{Kind: "parse"}
	}
	if envelope.Code == "0000" {
		return nil
	}
	// Codes are allowlisted rather than arbitrary provider strings.
	code := ""
	switch envelope.Code {
	case "4000", "4004.200", "InferHub.4004.200", "401", "403":
		code = envelope.Code
	}
	kind := "business"
	if envelope.Code == "401" || envelope.Code == "403" {
		kind = "auth"
	}
	return &SourceError{Kind: kind, ProviderCode: code}
}

func (c *Client) fetchBenefitModelsContext(ctx context.Context, cred SignCredential) ([]ModelInfo, error) {
	raw, err := c.modelRequest(ctx, http.MethodGet, c.benefitURL(EpBenefitConfig), "", cred, 4<<20)
	if err != nil {
		return nil, err
	}
	if err = benefitEnvelope(raw); err != nil {
		return nil, err
	}
	result, err := requiredObject(raw, "result")
	if err != nil {
		return nil, err
	}
	entries, err := requiredArray(result, "models")
	if err != nil {
		return nil, err
	}
	return parseModelEntries(entries, "benefit")
}

// ClaimBenefit preserves the configured opt-in/out policy and CLI entry point.
func (c *Client) ClaimBenefit(cred SignCredential) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.ClaimBenefitContext(ctx, cred)
}

func (c *Client) ClaimBenefitContext(ctx context.Context, cred SignCredential) error {
	raw, err := c.modelRequest(ctx, http.MethodPost, c.benefitURL(EpBenefitClaim), "", cred, 1<<20)
	if err != nil {
		return err
	}
	return benefitEnvelope(raw)
}
