// Package pool 管理 CodeArts 账号池：token 校验/自动刷新、冷却、禁用与轮转。
package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"codearts2api/internal/auth"
	"codearts2api/internal/upstream"
)

// CoolKind 冷却原因。
type CoolKind int

const (
	CoolSoft CoolKind = iota
	CoolErr
	CoolPlan
)

// Account 一个上游账号。
type Account struct {
	Name         string `json:"name"`
	UID          string `json:"uid"`
	UserName     string `json:"user_name"`
	DefaultModel string `json:"default_model"`

	Auth   *auth.Auth       `json:"-"`
	Client *upstream.Client `json:"-"`

	mu               sync.Mutex
	refresh          *tokenRefresh
	checkinMu        sync.Mutex
	checkin          *CheckinStatus
	lastValidated    time.Time
	errCount         int
	coolUntil        time.Time
	coolKind         CoolKind
	disabled         bool
	disabledRevision uint64
	removed          bool
	disabledReason   string
	lastErr          string
	// lastErrAt / lastSuccessAt separate *current* schedulability from
	// *historical* failure. They are presentation-only and never feed the
	// cooldown decision. Zero means unknown (e.g. legacy state.json).
	lastErrAt     time.Time
	lastSuccessAt time.Time
	// Historical failure survives a recovery, so the panel can still explain
	// what went wrong after lastErr is cleared. Class and text come from a
	// fixed safe table; no raw provider error is ever stored here.
	lastFailureAt     time.Time
	lastFailureClass  string
	lastFailureText   string
	activeConcurrent  int       // 当前活跃并发请求数
	maxConcurrent     int       // 最大允许并发数
	keepaliveLastPing time.Time // 最后保活心跳时间
}

// Config 池配置。
type Config struct {
	ErrThreshold    int
	ErrCooldown     time.Duration
	SoftCooldown    time.Duration
	RefreshSkew     time.Duration        // token 到期前多久自动刷新
	MaxConcurrent   int                  // 单账号最大并发数（默认 1）
	MaxPending      int                  // 全池等待请求上限（默认 64）
	UpstreamTimeout time.Duration        // 上游短请求/静默超时（默认 60s）
	KeepaliveWindow time.Duration        // 保活心跳窗口，超过此时间无活动则发送心跳
	LoginConfig     upstream.LoginConfig // OAuth/refresh 端点；零值使用生产默认
	CheckinClient   CheckinClient        // 可选活动客户端；nil 使用账号 Client
}

// Pool 账号池。
type Pool struct {
	cfg              Config
	state            string
	mu               sync.Mutex
	accounts         []*Account
	waiters          []*admissionWaiter
	nextAccount      int
	admittedRequests int
}

// New 构建池。
func New(auths []*auth.Auth, cfg Config, stateFile string) (*Pool, error) {
	if cfg.ErrThreshold <= 0 {
		cfg.ErrThreshold = 3
	}
	if cfg.ErrCooldown <= 0 {
		cfg.ErrCooldown = 10 * time.Minute
	}
	if cfg.SoftCooldown <= 0 {
		cfg.SoftCooldown = 60 * time.Second
	}
	if cfg.RefreshSkew <= 0 {
		cfg.RefreshSkew = 30 * time.Minute
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 1
	}
	if cfg.MaxPending <= 0 {
		cfg.MaxPending = 64
	}
	if cfg.UpstreamTimeout <= 0 {
		cfg.UpstreamTimeout = 60 * time.Second
	}
	if cfg.KeepaliveWindow <= 0 {
		cfg.KeepaliveWindow = 10 * time.Minute // 10 分钟无活动则保活
	}
	if cfg.LoginConfig.ClientID == "" {
		cfg.LoginConfig = upstream.DefaultLoginConfig()
	}
	p := &Pool{cfg: cfg, state: stateFile}
	for _, a := range auths {
		acct := &Account{
			Name:              a.UserID,
			UID:               a.UserID,
			UserName:          a.UserName,
			Auth:              a,
			Client:            upstream.New(cfg.UpstreamTimeout),
			maxConcurrent:     cfg.MaxConcurrent,
			keepaliveLastPing: time.Now(),
		}
		p.accounts = append(p.accounts, acct)
	}
	p.loadState()
	return p, nil
}

// Accounts 返回全部账号。
func (p *Pool) Accounts() []*Account {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*Account(nil), p.accounts...)
}

// AuthSnapshot returns the current credential object safely across reloads.
// Credential fields must still be read through auth.Auth's synchronized methods.
func (a *Account) AuthSnapshot() *auth.Auth {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Auth
}

// Get 按名字取账号。
func (p *Pool) Get(name string) *Account {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Name == name {
			return a
		}
	}
	return nil
}

// AddAccount 动态添加账号（WebUI 登录成功后调用）。
func (p *Pool) AddAccount(a *auth.Auth) *Account {
	acct := &Account{
		Name:              a.UserID,
		UID:               a.UserID,
		UserName:          a.UserName,
		Auth:              a,
		Client:            upstream.New(p.cfg.UpstreamTimeout),
		maxConcurrent:     p.cfg.MaxConcurrent,
		keepaliveLastPing: time.Now(),
	}
	p.mu.Lock()
	for _, existing := range p.accounts {
		if existing.Name == acct.Name {
			existing.mu.Lock()
			existing.Auth = a
			existing.UserName = a.UserName
			existing.maxConcurrent = p.cfg.MaxConcurrent
			existing.lastValidated = time.Time{}
			existing.disabled = false
			existing.disabledRevision++
			existing.disabledReason = ""
			existing.lastErr = ""
			existing.mu.Unlock()
			p.dispatchWaitersLocked()
			p.saveState()
			p.mu.Unlock()
			return existing
		}
	}
	p.accounts = append(p.accounts, acct)
	p.dispatchWaitersLocked()
	p.saveState()
	p.mu.Unlock()
	log.Printf("pool account added name=%s uid=%s", acct.Name, a.UserID)
	return acct
}

// List 状态快照（脱敏；字段对齐 WorkBuddy 面板）。
func (p *Pool) List() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, 0, len(p.accounts))
	now := time.Now()
	for _, a := range p.accounts {
		a.mu.Lock()
		cooling := !a.disabled && now.Before(a.coolUntil)
		until := ""
		if cooling {
			until = a.coolUntil.Format(time.RFC3339)
		}
		reason := a.disabledReason
		if reason == "" {
			reason = a.lastErr
		}
		lastErrAt, lastSuccessAt := "", ""
		if !a.lastErrAt.IsZero() {
			lastErrAt = a.lastErrAt.Format(time.RFC3339)
		}
		if !a.lastSuccessAt.IsZero() {
			lastSuccessAt = a.lastSuccessAt.Format(time.RFC3339)
		}
		lastFailureAt := ""
		if !a.lastFailureAt.IsZero() {
			lastFailureAt = a.lastFailureAt.Format(time.RFC3339)
		}
		out = append(out, map[string]any{
			"name":          a.Name,
			"uid":           a.UID,
			"nickname":      a.UserName,
			"user_name":     a.UserName,
			"default_model": a.DefaultModel,
			"credits":       int64(0), // 保留旧字段；未查询真实余额
			"checkin":       a.checkin,
			"disabled":      a.disabled,
			"cooling":       cooling,
			"until":         until,
			"err_count":     a.errCount,
			"reason":        reason,
			"last_error":    a.lastErr,
			// Current schedulability, separate from failure history below.
			"schedulable":     !a.disabled && !cooling,
			"last_error_at":   lastErrAt,
			"last_success_at": lastSuccessAt,
			// Historical failure survives recovery; empty means unknown.
			"last_failure_at":    lastFailureAt,
			"last_failure_class": a.lastFailureClass,
			"last_failure_text":  a.lastFailureText,
			"active_concurrent":  a.activeConcurrent,
			"max_concurrent":     a.maxConcurrent,
			"token_remaining":    a.Auth.Remaining().Round(time.Minute).String(),
			"expires_at":         a.Auth.ExpiresAt().Format(time.RFC3339),
		})
		a.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["uid"].(string) < out[j]["uid"].(string) })
	return out
}

// Stats 汇总。
// Config returns the effective pool configuration (read-only copy).
func (p *Pool) Config() Config { return p.cfg }

func (p *Pool) Stats() (total, healthy, disabled, cooling int, credits int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for _, a := range p.accounts {
		total++
		a.mu.Lock()
		if a.disabled {
			disabled++
		} else if now.Before(a.coolUntil) {
			cooling++
		} else {
			healthy++
		}
		a.mu.Unlock()
	}
	credits = int64(healthy) // 面板「积分合计」位复用为健康数
	return
}

// Enable 解禁账号。
func (p *Pool) Enable(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Name == name || a.UID == name {
			a.mu.Lock()
			a.disabled = false
			a.disabledRevision++
			a.disabledReason = ""
			a.lastErr = ""
			a.mu.Unlock()
			p.dispatchWaitersLocked()
			p.saveState()
			return true
		}
	}
	return false
}

// ClearCooldown 清冷却。
func (p *Pool) ClearCooldown(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Name == name || a.UID == name {
			a.mu.Lock()
			a.coolUntil = time.Time{}
			a.coolKind = CoolSoft
			a.mu.Unlock()
			p.dispatchWaitersLocked()
			p.saveState()
			return true
		}
	}
	return false
}

// SyncToDir 用磁盘 auths 全量对齐。
func (p *Pool) SyncToDir(auths []*auth.Auth) {
	p.mu.Lock()
	defer p.mu.Unlock()
	byUID := map[string]*Account{}
	for _, a := range p.accounts {
		byUID[a.UID] = a
	}
	next := make([]*Account, 0, len(auths))
	for _, a := range auths {
		if existing, ok := byUID[a.UserID]; ok {
			existing.mu.Lock()
			existing.Auth = a
			existing.UserName = a.UserName
			existing.maxConcurrent = p.cfg.MaxConcurrent
			existing.lastValidated = time.Time{}
			existing.mu.Unlock()
			next = append(next, existing)
			delete(byUID, a.UserID)
		} else {
			next = append(next, &Account{
				Name:              a.UserID,
				UID:               a.UserID,
				UserName:          a.UserName,
				Auth:              a,
				Client:            upstream.New(p.cfg.UpstreamTimeout),
				maxConcurrent:     p.cfg.MaxConcurrent,
				keepaliveLastPing: time.Now(),
			})
		}
	}
	for _, removed := range byUID {
		removed.mu.Lock()
		removed.removed = true
		removed.mu.Unlock()
	}
	p.accounts = next
	p.dispatchWaitersLocked()
}

// PickExcluding 挑一个健康账号。
func (p *Pool) PickExcluding(tried map[string]bool) *Account {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for _, a := range p.accounts {
		if tried != nil && tried[a.Name] {
			continue
		}
		a.mu.Lock()
		healthy := !a.removed && !a.disabled && !now.Before(a.coolUntil)
		a.mu.Unlock()
		if healthy {
			return a
		}
	}
	return nil
}

// Validate 校验并（必要时）自动刷新 token。带 5 分钟缓存。
func (p *Pool) Validate(a *Account) (bool, error) {
	return p.ValidateContext(context.Background(), a)
}

// ValidateContext allows callers waiting for a shared refresh to disconnect.
func (p *Pool) ValidateContext(ctx context.Context, a *Account) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if a == nil {
			return false, ErrNoHealthyAccount
		}
		a.mu.Lock()
		authz, disabled, removed, lastValidated := a.Auth, a.disabled, a.removed, a.lastValidated
		a.mu.Unlock()
		if disabled || removed || authz == nil {
			return false, nil
		}
		expired := authz.Expired()
		if !expired && time.Since(lastValidated) < 5*time.Minute {
			return true, nil
		}
		if expired && authz.Refresh() == "" {
			p.disableAuth(ctx, a, authz, "token expired; re-login required")
			return false, ctx.Err()
		}
		if authz.ExpiringSoon(p.cfg.RefreshSkew) || expired {
			if authz.Refresh() == "" {
				log.Printf("pool token account=%s expiring soon (remaining=%s) but no refresh_token, re-login required", a.Name, authz.Remaining().Round(time.Minute))
			} else if err := p.refreshAccountContext(ctx, a); err != nil {
				if ctx.Err() != nil {
					return false, ctx.Err()
				}
				var refreshExpired *upstream.RefreshTokenExpiredError
				if errors.As(err, &refreshExpired) {
					p.disableAuth(ctx, a, authz, "refresh_token expired; re-login required")
					return false, ctx.Err()
				}
				return false, err
			}
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		a.mu.Lock()
		if a.Auth != authz {
			a.mu.Unlock()
			continue
		}
		ok := !a.disabled && !a.removed
		if ok {
			a.lastValidated = time.Now()
		}
		a.mu.Unlock()
		return ok, nil
	}
}

func (p *Pool) disableAuth(ctx context.Context, a *Account, authz *auth.Auth, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	a.mu.Lock()
	changed := !a.removed && a.Auth == authz
	if changed {
		a.disabled, a.disabledReason, a.lastErr = true, reason, reason
		a.lastErrAt = time.Now()
		setFailureLocked(a, reason)
		a.disabledRevision++
	}
	a.mu.Unlock()
	if changed {
		p.dispatchWaitersLocked()
		p.saveState()
	}
}

// Cooldown 冷却账号。
func (p *Pool) Cooldown(name string, kind CoolKind, dur time.Duration, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Name != name {
			continue
		}
		a.mu.Lock()
		a.coolUntil = time.Now().Add(dur)
		a.coolKind = kind
		a.lastErr = reason
		a.lastErrAt = time.Now()
		setFailureLocked(a, reason)
		a.mu.Unlock()
		log.Printf("pool cooldown account=%s kind=%d dur=%s reason=%s", name, kind, dur, reason)
	}
	p.dispatchWaitersLocked()
	p.saveState()
}

// NoteError 累计错误。
func (p *Pool) NoteError(name string, threshold int, cooldown time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Name != name {
			continue
		}
		a.mu.Lock()
		a.errCount++
		// Every counted error refreshes the failure timestamp, including counts
		// below the threshold: "failing now" must not depend on cooling.
		a.lastErrAt = time.Now()
		setFailureLocked(a, "consecutive errors")
		if a.errCount >= threshold {
			a.coolUntil = time.Now().Add(cooldown)
			a.lastErr = "consecutive errors"
		}
		a.mu.Unlock()
	}
	p.dispatchWaitersLocked()
	p.saveState()
}

// setFailureLocked records the historical failure for an account. Class and
// text come from a fixed table, so no raw provider error text can be persisted.
// The historical record survives a later recovery; the caller keeps updating
// lastErr separately for *current* state.
func setFailureLocked(a *Account, reason string) {
	class, text := failureClass(reason)
	a.lastFailureAt = time.Now()
	a.lastFailureClass = class
	a.lastFailureText = text
}

// failureClass maps an internally generated failure reason to a fixed
// (class, description) pair. Unknown input collapses to a bounded generic
// description rather than being echoed back.
func failureClass(reason string) (string, string) {
	low := strings.ToLower(reason)
	switch {
	case strings.Contains(low, "429"), strings.Contains(low, "rate limit"):
		return "rate_limit", "upstream rate limited"
	case strings.Contains(low, "401"), strings.Contains(low, "authentication"),
		strings.Contains(low, "credential"), strings.Contains(low, "token invalid"):
		return "auth", "upstream rejected credentials"
	case strings.Contains(low, "consecutive"):
		return "unknown", "consecutive upstream errors"
	case strings.Contains(low, "http 5"), strings.Contains(low, "server error"):
		return "upstream_5xx", "upstream server error"
	case strings.Contains(low, "refresh"):
		return "auth", "token refresh failed"
	case strings.Contains(low, "manual"), strings.Contains(low, "禁用"):
		return "manual", "manually disabled"
	}
	return "unknown", "unclassified account failure"
}

// NoteSuccess records a completed request.
//
// It clears obsolete *current* error state only on a real error-to-success
// transition and persists exactly once for that transition, so a restart cannot
// resurrect a stale errCount. Routine successes perform no disk write at all.
// An active cooldown or a manual disable is never overridden.
func (p *Pool) NoteSuccess(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for _, a := range p.accounts {
		if a.Name != name {
			continue
		}
		a.mu.Lock()
		recovering := a.errCount > 0 || a.lastErr != ""
		cooling := now.Before(a.coolUntil)
		blocked := a.disabled || cooling
		a.lastSuccessAt = now
		if recovering && !blocked {
			a.errCount = 0
			a.lastErr = ""
		}
		a.mu.Unlock()
		if recovering && !blocked {
			p.saveState()
		}
	}
}

// Disable 禁用账号。
func (p *Pool) Disable(name, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Name == name {
			a.mu.Lock()
			a.disabled = true
			a.disabledRevision++
			a.disabledReason = reason
			a.lastErr = reason
			a.lastErrAt = time.Now()
			setFailureLocked(a, reason)
			a.mu.Unlock()
			log.Printf("pool disable account=%s reason=%s", name, reason)
		}
	}
	p.dispatchWaitersLocked()
	p.saveState()
}

// Healthy 查询账号可用。
// Busy 报告账号当前是否有活跃请求占用并发槽位（供探测等后台任务让位）。
func (p *Pool) Busy(name string) bool {
	p.mu.Lock()
	var target *Account
	for _, a := range p.accounts {
		if a.Name == name {
			target = a
			break
		}
	}
	p.mu.Unlock()
	if target == nil {
		return false
	}
	target.mu.Lock()
	defer target.mu.Unlock()
	return target.activeConcurrent > 0
}

func (p *Pool) Healthy(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Name == name {
			a.mu.Lock()
			h := !a.disabled && time.Now().After(a.coolUntil)
			a.mu.Unlock()
			return h
		}
	}
	return false
}

// Enabled 报告账号是否启用；冷却期间仍需续期。
func (p *Pool) Enabled(name string) bool {
	a := p.Get(name)
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.disabled
}

// AcquireLock 尝试获取账号的并发请求锁。返回 true 表示成功，false 表示已达上限。
func (p *Pool) AcquireLock(name string) bool {
	_, err := p.AcquireAccount(context.Background(), []AccountCandidate{{Name: name}}, 0)
	return err == nil
}

// ReleaseLock 释放账号的并发请求锁，并唤醒等待槽位的请求。
func (p *Pool) ReleaseLock(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Name == name {
			p.releaseAccountLocked(a)
			break
		}
	}
	p.dispatchWaitersLocked()
}

// AcquireLockWait 阻塞等待账号并发槽位，超时返回 false。
//
// Compatibility helper; request handlers should retain an AcquireAccount lease.
func (p *Pool) AcquireLockWait(name string, timeout time.Duration) bool {
	_, err := p.AcquireAccount(context.Background(), []AccountCandidate{{Name: name}}, timeout)
	return err == nil
}

// NeedKeepalive 检查账号是否需要保活心跳。
func (p *Pool) NeedKeepalive(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Name != name {
			continue
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		// 超过 KeepaliveWindow 无活动则需保活
		return time.Since(a.keepaliveLastPing) > p.cfg.KeepaliveWindow
	}
	return false
}

// PingKeepalive 更新账号的保活心跳时间。
func (p *Pool) PingKeepalive(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Name == name {
			a.mu.Lock()
			a.keepaliveLastPing = time.Now()
			a.mu.Unlock()
			break
		}
	}
}

// CheckAndRefreshTokenWithin 在剩余有效期低于 skew 时主动刷新 token。
//
// skew 来自 scheduler 的 refresh_skew（配置项 watch.refresh_skew_minutes）。
// 此前该配置从未生效：原实现硬编码 1 小时阈值，调度器传进来的 RefreshSkew
// 只出现在日志里，README 写的「提前刷新时间」实际不起作用。
func (p *Pool) CheckAndRefreshTokenWithin(name string, skew time.Duration) error {
	return p.CheckAndRefreshTokenWithinContext(context.Background(), name, skew)
}

// CheckAndRefreshTokenWithinContext makes background watchdog shutdown and
// cancellation independent of a shared account refresh already in progress.
func (p *Pool) CheckAndRefreshTokenWithinContext(ctx context.Context, name string, skew time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if skew <= 0 {
		skew = time.Hour
	}
	acct := p.Get(name)
	if acct == nil {
		return fmt.Errorf("account not found: %s", name)
	}

	authz := acct.AuthSnapshot()
	if authz == nil {
		return ErrNoHealthyAccount
	}
	remaining := authz.Remaining()
	hasRefresh := authz.Refresh() != ""

	if remaining > skew {
		return nil
	}
	if !hasRefresh {
		// ticket 登录不返回 refresh_token，跳过无意义的周期刷新。
		return nil
	}
	log.Printf("pool proactive refresh account=%s remaining=%s skew=%s",
		name, remaining.Round(time.Minute), skew)
	return p.refreshAccountContext(ctx, acct)
}

// CheckAndRefreshToken 检查 token 是否快过期并主动刷新（比 scheduler 更积极）。
func (p *Pool) CheckAndRefreshToken(name string) error {
	return p.CheckAndRefreshTokenWithin(name, time.Hour)
}

// RefreshToken 手动刷新指定账号的 token。
func (p *Pool) RefreshToken(name string) error {
	return p.RefreshTokenContext(context.Background(), name)
}

// RefreshAllTokens 批量刷新所有临期 token（用于 cron 或脚本）。
func (p *Pool) RefreshAllTokens() int {
	count := 0
	for _, acct := range p.Accounts() {
		if err := p.CheckAndRefreshToken(acct.Name); err == nil {
			count++
		}
	}
	return count
}

// Acquire 尝试获取账号并发 slot（返回 true 表示成功，调用方需随后 Release）。
func (p *Pool) Acquire(name string) bool {
	return p.AcquireLock(name)
}

// Release 释放账号并发 slot。
func (p *Pool) Release(name string) {
	p.ReleaseLock(name)
}

// CheckKeepalive 检查是否需要保活心跳。
func (p *Pool) CheckKeepalive(name string) bool {
	p.mu.Lock()
	var target *Account
	for _, a := range p.accounts {
		if a.Name == name {
			target = a
			break
		}
	}
	p.mu.Unlock()
	if target == nil {
		return false
	}
	target.mu.Lock()
	defer target.mu.Unlock()

	// 如果超过保活窗口无活动，需要保活
	needsKeepalive := time.Since(target.keepaliveLastPing) > p.cfg.KeepaliveWindow
	if needsKeepalive {
		target.keepaliveLastPing = time.Now()
	}
	return needsKeepalive
}

// SetKeepalive 手动更新保活时间。
func (p *Pool) SetKeepalive(name string) {
	p.mu.Lock()
	var target *Account
	for _, a := range p.accounts {
		if a.Name == name {
			target = a
			break
		}
	}
	p.mu.Unlock()
	if target == nil {
		return
	}
	target.mu.Lock()
	defer target.mu.Unlock()
	target.keepaliveLastPing = time.Now()
}

// GetConcurrentStats 获取账号并发统计信息。
func (p *Pool) GetConcurrentStats() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]map[string]any, 0, len(p.accounts))
	now := time.Now()
	for _, a := range p.accounts {
		a.mu.Lock()
		cooling := !a.disabled && now.Before(a.coolUntil)
		concurrent := a.activeConcurrent
		maxConc := a.maxConcurrent
		lastPing := a.keepaliveLastPing
		a.mu.Unlock()

		out = append(out, map[string]any{
			"name":              a.Name,
			"uid":               a.UID,
			"user_name":         a.UserName,
			"active_concurrent": concurrent,
			"max_concurrent":    maxConc,
			"utilization":       float64(concurrent) / float64(maxConc),
			"last_keepalive":    lastPing.Format(time.RFC3339),
			"cooling":           cooling,
			"disabled":          a.disabled,
		})
	}
	return out
}

// TryAcquire 尝试获取账号的并发槽位，成功返回 true 并增加计数。
func (p *Pool) TryAcquire(name string) bool {
	return p.AcquireLock(name)
}

// NeedsKeepalive 检查账号是否需要保活心跳。
func (p *Pool) NeedsKeepalive(name string) bool {
	p.mu.Lock()
	var target *Account
	for _, a := range p.accounts {
		if a.Name == name {
			target = a
			break
		}
	}
	p.mu.Unlock()

	if target == nil {
		return false
	}

	target.mu.Lock()
	defer target.mu.Unlock()

	// 超过窗口期无活动则需保活
	return time.Since(target.keepaliveLastPing) > p.cfg.KeepaliveWindow
}

// UpdateKeepalive 更新账号的最后保活时间。
func (p *Pool) UpdateKeepalive(name string) {
	p.mu.Lock()
	var target *Account
	for _, a := range p.accounts {
		if a.Name == name {
			target = a
			break
		}
	}
	p.mu.Unlock()

	if target != nil {
		target.mu.Lock()
		target.keepaliveLastPing = time.Now()
		target.mu.Unlock()
	}
}

// SetMaxConcurrent 设置账号的最大并发数。
func (p *Pool) SetMaxConcurrent(name string, max int) {
	if max <= 0 {
		max = 1
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var target *Account
	for _, a := range p.accounts {
		if a.Name == name {
			target = a
			break
		}
	}
	if target != nil {
		target.mu.Lock()
		target.maxConcurrent = max
		target.mu.Unlock()
		p.dispatchWaitersLocked()
	}
}

func (p *Pool) loadState() {
	if p.state == "" {
		return
	}
	raw, err := os.ReadFile(p.state)
	if err != nil {
		return
	}
	var data []struct {
		Name             string    `json:"name"`
		ErrCount         int       `json:"err_count"`
		CoolUntil        time.Time `json:"cool_until"`
		Disabled         bool      `json:"disabled"`
		Reason           string    `json:"reason"`
		LastErr          string    `json:"last_error"`
		LastErrAt        time.Time `json:"last_error_at"`
		LastSuccessAt    time.Time `json:"last_success_at"`
		LastFailureAt    time.Time `json:"last_failure_at"`
		LastFailureClass string    `json:"last_failure_class"`
		LastFailureText  string    `json:"last_failure_text"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return
	}
	for _, a := range p.accounts {
		for _, s := range data {
			if s.Name == a.Name {
				a.mu.Lock()
				a.errCount = s.ErrCount
				a.coolUntil = s.CoolUntil
				a.disabled = s.Disabled
				a.disabledReason = s.Reason
				a.lastErr = s.LastErr
				// Missing in legacy state files: stays zero (unknown), never
				// inferred from cool_until or the journal.
				a.lastErrAt = s.LastErrAt
				a.lastSuccessAt = s.LastSuccessAt
				a.lastFailureAt = s.LastFailureAt
				a.lastFailureClass = s.LastFailureClass
				a.lastFailureText = s.LastFailureText
				a.mu.Unlock()
			}
		}
	}
}

func (p *Pool) saveState() {
	if p.state == "" {
		return
	}
	type entry struct {
		Name             string    `json:"name"`
		ErrCount         int       `json:"err_count"`
		CoolUntil        time.Time `json:"cool_until"`
		Disabled         bool      `json:"disabled"`
		Reason           string    `json:"reason"`
		LastErr          string    `json:"last_error"`
		LastErrAt        time.Time `json:"last_error_at,omitempty"`
		LastSuccessAt    time.Time `json:"last_success_at,omitempty"`
		LastFailureAt    time.Time `json:"last_failure_at,omitempty"`
		LastFailureClass string    `json:"last_failure_class,omitempty"`
		LastFailureText  string    `json:"last_failure_text,omitempty"`
	}
	out := make([]entry, 0, len(p.accounts))
	for _, a := range p.accounts {
		a.mu.Lock()
		out = append(out, entry{
			Name:             a.Name,
			ErrCount:         a.errCount,
			CoolUntil:        a.coolUntil,
			Disabled:         a.disabled,
			Reason:           a.disabledReason,
			LastErr:          a.lastErr,
			LastErrAt:        a.lastErrAt,
			LastSuccessAt:    a.lastSuccessAt,
			LastFailureAt:    a.lastFailureAt,
			LastFailureClass: a.lastFailureClass,
			LastFailureText:  a.lastFailureText,
		})
		a.mu.Unlock()
	}
	raw, _ := json.MarshalIndent(out, "", "  ")
	tmp := p.state + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, p.state)
}
