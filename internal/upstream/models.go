// 模型目录与限时福利路由。
//
// 上游可用模型来自三路（对齐官方客户端 ModelService）：
//  1. agent-center：useragents 找 CodeAgent（alias 智能体 + show_in_ide）→ detail 的 gpts.models
//  2. 内置：GET /v1/model/builtin（Agent-Type: PromptCenter）
//  3. 账号福利开关确认开启后：GET {opengw}/api/v1/gateway/config
//
// 福利来源确认的模型聊天必须追加请求头 maas_type: benefit。
// 开关关闭后的正常目录模型按 agent/builtin 证据路由，不沿用旧福利标记。
//
// 目录按账号隔离：福利是按账号授予的，池里 A 账号有、B 账号没有时不允许互相污染，
// 因此 IsBenefitModel 必须带账号身份查询（见 SetAccountModels）。
package upstream

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"codearts2api/internal/auth"
)

// CatalogTTL 账号模型目录的有效期（超过则建议重新发现）。
const CatalogTTL = time.Hour

// 福利路由头：官方 renderer 检测到 isFreeBenefit 即添加，经 kernel 转发到上游聊天请求。
const (
	HeaderMaasType = "maas_type"
	MaasBenefit    = "benefit"
)

// ModelInfo 模型信息（与 workbuddy/trae 一致）。
type ModelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextWindow int64  `json:"contextWindow,omitempty"`
	MaxTokens     int64  `json:"maxTokens,omitempty"`
	// Benefit 限时福利（免费套餐）模型：聊天需带 maas_type: benefit 头。
	Benefit bool   `json:"benefit,omitempty"`
	Desc    string `json:"desc,omitempty"`
	// displayAlias is populated only from an unchanged, bounded source name.
	// It is account-local compatibility evidence, never a union-wide alias.
	displayAlias string
}

// 冷启动种子：没有账号权威福利快照时回退；成功空目录或明确关闭
// 福利通道会覆盖该回退，后续拉取失败也不能恢复已移除的福利路由。
var seedBenefitModels = []string{
	"deepseek-v4-flash-0731", "deepseek-v4-pro-0813", "glm-5.3-flash",
}

// 内置模型种子（大小写归一用，不标记福利）。
var seedKnownModels = []string{
	// Captured normal-source inference IDs (2026-10-22), distinct from their
	// GLM-5.3 / DeepSeek-V4.1-Flash display names. These are not benefit seeds.
	"glm-5.3", "deepseek-v4.1-flash",
	"GLM-5.2", "GLM-5.2-ArkTS-SPARK", "GLM-5.1", "GLM-4.7",
	"OpenPangu-2.0-Pro", "OpenPangu-2.0-Flash",
	"Qwen3-VL-235B", "Qwen3.5-397B-A17B-VL", "Qwen3.6-27B-VL",
}

var seedBenefit = func() map[string]bool {
	m := make(map[string]bool, len(seedBenefitModels))
	for _, id := range seedBenefitModels {
		m[strings.ToLower(id)] = true
	}
	return m
}()

// accountCatalog 一个账号的模型目录快照。
type accountCatalog struct {
	infos      []ModelInfo
	benefit    map[string]bool   // lower(id) -> true（本轮发现确认的福利）
	knows      map[string]bool   // lower(id) -> true（该账号目录里登记过的全部模型）
	exact      map[string]string // lower(id) -> this account's exact upstream ID
	aliases    map[string]string // lower(display name) -> unambiguous lower(id)
	wireSource map[string]string // lower(id) -> source governing exact ID and route
	sources    map[string]sourceSnapshot
	claim      ClaimStatus
	owner      *auth.Auth
	fetched    time.Time
	known      bool
}

var catalog = struct {
	sync.RWMutex
	accounts map[string]*accountCatalog // accountID -> 目录
	known    map[string]string          // lower(id) -> 精确 ID（跨账号，大小写归一）
}{accounts: map[string]*accountCatalog{}, known: map[string]string{}}

func init() {
	catalog.known = make(map[string]string, len(seedKnownModels)+len(seedBenefitModels))
	for _, id := range append(append([]string{}, seedKnownModels...), seedBenefitModels...) {
		if _, ok := catalog.known[strings.ToLower(id)]; !ok {
			catalog.known[strings.ToLower(id)] = id
		}
	}
}

// SetAccountModels 整体替换某账号的模型目录（不是增补：套餐轮换后旧模型不得残留）。
// 等价于「本轮福利来源也成功」，因此不保留上一轮福利记忆。
func SetAccountModels(accountID string, infos []ModelInfo) {
	setAccountModels(accountID, infos, false)
}

// setAccountModels 写入账号目录。
//
// keepBenefit=true 表示本轮福利来源失败（内置来源成功）：此时保留上一次成功的
// 福利条目，否则非种子的福利模型会瞬间丢掉 maas_type: benefit 标记，直到下一次
// 刷新成功为止——这期间它们在上游按未注册模型 404。
func setAccountModels(accountID string, infos []ModelInfo, keepBenefit bool) {
	if accountID == "" {
		return
	}
	catalog.Lock()
	defer catalog.Unlock()
	if keepBenefit {
		if prev := catalog.accounts[accountID]; prev != nil {
			var retained []ModelInfo
			for _, mi := range prev.infos {
				if mi.Benefit {
					retained = append(retained, mi)
				}
			}
			infos = MergeModels(infos, retained)
		}
	}
	ac := &accountCatalog{infos: MergeModels(infos), fetched: time.Now(), known: true}
	indexCatalog(ac)
	catalog.accounts[accountID] = ac
	rebuildKnownLocked()
}

// AccountModels 返回某账号的模型目录快照（未发现时 ok=false）。
func AccountModels(accountID string) ([]ModelInfo, bool) {
	catalog.RLock()
	defer catalog.RUnlock()
	return accountModelsLocked(accountID, nil, false)
}

// AccountModelsForAuth hides a previous login's retained catalog immediately.
func AccountModelsForAuth(accountID string, owner *auth.Auth) ([]ModelInfo, bool) {
	catalog.RLock()
	defer catalog.RUnlock()
	return accountModelsLocked(accountID, owner, true)
}

func accountModelsLocked(accountID string, owner *auth.Auth, checkOwner bool) ([]ModelInfo, bool) {
	ac, ok := catalog.accounts[accountID]
	if !ok || !ac.known || (checkOwner && ac.owner != nil && ac.owner != owner) {
		return nil, false
	}
	return append([]ModelInfo(nil), ac.infos...), true
}

// AccountModelKnowledgeForAuth reads membership and authoritative completeness
// from one catalog generation. Missing entries in partial or expired discovery
// stay unknown; retained present entries remain useful for account selection.
func AccountModelKnowledgeForAuth(accountID, model string, owner *auth.Auth) (present, complete bool) {
	catalog.RLock()
	defer catalog.RUnlock()
	ac := catalog.accounts[accountID]
	if ac == nil || !ac.known || (ac.owner != nil && ac.owner != owner) {
		return false, false
	}
	present = ac.knows[catalogModelKey(ac, model)]
	// Compatibility snapshots are explicit whole-catalog replacements.
	if len(ac.sources) == 0 {
		return present, true
	}
	if time.Since(ac.fetched) >= CatalogTTL {
		return present, false
	}
	for _, name := range modelSources {
		source, ok := ac.sources[name]
		if !ok || source.LastSuccessAt == nil || !modelSourceSucceeded(source.Status) {
			return present, false
		}
	}
	return present, true
}

// AccountCatalogStale 报告某账号目录是否需重新发现（未发现或超过 CatalogTTL）。
func AccountCatalogStale(accountID string) bool {
	catalog.RLock()
	defer catalog.RUnlock()
	return accountCatalogStaleLocked(accountID, nil, false)
}

func AccountCatalogStaleForAuth(accountID string, owner *auth.Auth) bool {
	catalog.RLock()
	defer catalog.RUnlock()
	return accountCatalogStaleLocked(accountID, owner, true)
}

func accountCatalogStaleLocked(accountID string, owner *auth.Auth, checkOwner bool) bool {
	ac, ok := catalog.accounts[accountID]
	if !ok || (checkOwner && ac.owner != nil && ac.owner != owner) || time.Since(ac.fetched) >= CatalogTTL {
		return true
	}
	for _, src := range ac.sources {
		if !modelSourceSucceeded(src.Status) && (src.RetryAt == nil || !time.Now().Before(*src.RetryAt)) {
			return true
		}
	}
	return false
}

// IsBenefitModel 报告该账号下该模型是否走限时福利路由（大小写不敏感）。
//
// 判定顺序：
//  1. 该账号目录里标了福利 → true（发现结果优先于种子）
//  2. 该账号目录里有但没有标福利 → false：它已经以内置/agent-center 身份注册，
//     套餐轮换后福利模型转正就靠这条摘掉 maas_type 头
//  3. 权威福利快照（包括保留的空快照或关闭状态）不包含该模型 → false
//  4. 尚无该账号的权威福利快照 → 回退冷启动种子
func IsBenefitModel(accountID, model string) bool {
	return isBenefitModel(accountID, model, nil, false)
}

func IsBenefitModelForAuth(accountID, model string, owner *auth.Auth) bool {
	return isBenefitModel(accountID, model, owner, true)
}

// ResolveModelForAuth resolves the exact ID and route from one account/auth
// snapshot. Other accounts' dynamic IDs must never influence an outbound call.
// Established static aliases/seeds remain cold-start fallbacks; unknown inputs
// are preserved, and no metadata fetch occurs on this path.
func ResolveModelForAuth(accountID, model string, owner *auth.Auth) (exact string, benefit bool) {
	catalog.RLock()
	defer catalog.RUnlock()
	ac := catalog.accounts[accountID]
	if ac != nil && ac.owner != nil && ac.owner != owner {
		ac = nil
	}
	exact = model
	if ac != nil {
		if id, ok := ac.exact[catalogModelKey(ac, model)]; ok {
			return id, benefitModelLocked(ac, id)
		}
	}
	exact = canonicalStaticModel(model)
	if ac != nil {
		if id, ok := ac.exact[catalogModelKey(ac, exact)]; ok {
			exact = id
		}
	}
	return exact, benefitModelLocked(ac, exact)
}

func isBenefitModel(accountID, model string, owner *auth.Auth, checkOwner bool) bool {
	key := strings.ToLower(strings.TrimSpace(model))
	if key == "" {
		return false
	}
	catalog.RLock()
	defer catalog.RUnlock()
	ac := catalog.accounts[accountID]
	if ac != nil && checkOwner && ac.owner != nil && ac.owner != owner {
		ac = nil
	}
	return benefitModelLocked(ac, key)
}

// Exact registrations take precedence over display aliases, including aliases
// that collide with an ID from a different source or route.
func catalogModelKey(ac *accountCatalog, model string) string {
	key := strings.ToLower(strings.TrimSpace(model))
	if ac != nil && !ac.knows[key] {
		if target := ac.aliases[key]; target != "" {
			return target
		}
	}
	return key
}

func benefitModelLocked(ac *accountCatalog, model string) bool {
	key := catalogModelKey(ac, model)
	if ac != nil {
		if ac.benefit[key] {
			return true
		}
		if ac.knows[key] {
			return false
		}
		// Once benefit discovery has authoritatively succeeded, its current
		// (possibly retained empty) snapshot also overrides cold-start seeds.
		if source, found := ac.sources["benefit"]; found && source.LastSuccessAt != nil {
			return false
		}
	}
	return seedBenefit[key]
}

// lookupKnownModel 按小写 ID 查精确模型 ID（种子 + 已发现模型）。
func lookupKnownModel(id string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(id))
	if key == "" {
		return "", false
	}
	catalog.RLock()
	defer catalog.RUnlock()
	exact, ok := catalog.known[key]
	return exact, ok
}

// MergeModels 合并多路模型列表：按小写 ID 去重，福利标记取或，参数更全的优先。
// 结果按小写 ID 排序，保证 /v1/models 输出稳定（缓存刷新前后顺序一致）。
func MergeModels(sets ...[]ModelInfo) []ModelInfo {
	merged := map[string]ModelInfo{}
	for _, set := range sets {
		for _, mi := range set {
			if mi.ID == "" {
				continue
			}
			key := strings.ToLower(mi.ID)
			cur, ok := merged[key]
			if !ok {
				merged[key] = mi
				continue
			}
			merged[key] = richerModel(cur, mi)
		}
	}
	out := make([]ModelInfo, 0, len(merged))
	for _, mi := range merged {
		out = append(out, mi)
	}
	sort.Slice(out, func(i, j int) bool {
		li, lj := strings.ToLower(out[i].ID), strings.ToLower(out[j].ID)
		if li != lj {
			return li < lj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// richerModel 合并同一模型的两条记录：福利标记取或（漏标即漏头），其余取更全的一条。
func richerModel(cur, next ModelInfo) ModelInfo {
	benefit := cur.Benefit || next.Benefit
	keep, drop := cur, next
	if betterModel(next, cur) {
		keep, drop = next, cur
	}
	keep.Benefit = benefit
	if keep.Name == "" {
		keep.Name = drop.Name
	}
	if keep.ContextWindow == 0 {
		keep.ContextWindow = drop.ContextWindow
	}
	if keep.MaxTokens == 0 {
		keep.MaxTokens = drop.MaxTokens
	}
	if keep.Desc == "" {
		keep.Desc = drop.Desc
	}
	return keep
}

// betterModel 报告 a 是否比 b 更值得保留：优先带参数，其次保留精确大小写。
func betterModel(a, b ModelInfo) bool {
	if (a.ContextWindow > 0) != (b.ContextWindow > 0) {
		return a.ContextWindow > 0
	}
	return a.ID != strings.ToLower(a.ID) && b.ID == strings.ToLower(b.ID)
}

// FetchModels keeps the CLI compatibility surface while sharing the bounded,
// context-aware source discovery and publication used by the server.
func (c *Client) FetchModels(acct *auth.Auth) ([]ModelInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return c.FetchModelsContext(ctx, acct)
}

func (c *Client) FetchModelsContext(ctx context.Context, acct *auth.Auth) ([]ModelInfo, error) {
	if acct == nil {
		return nil, fmt.Errorf("account required for model fetch")
	}
	result := c.DiscoverModels(ctx, acct, false)
	infos, _ := PublishModelDiscovery(acct.UserID, acct, result)
	if result.Successes() == 0 {
		return infos, fmt.Errorf("all model sources failed")
	}
	return infos, nil
}
