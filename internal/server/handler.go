// Package server 暴露 OpenAI 兼容接口：/v1/chat/completions、/v1/models、/status、WebUI。
package server

import (
	"context"
	crand "crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"codearts2api/internal/audit"
	"codearts2api/internal/auth"
	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

// 构建信息由 -ldflags -X 注入（镜像内没有 .git，无法回读 VCS 信息）。
// 未注入时保持空串，对外显式呈现为 unknown，绝不猜一个 commit。
var (
	BuildCommit string
	BuildTime   string
)

// Config handler 依赖。
type Config struct {
	Pool         *pool.Pool
	Upstream     *upstream.Client
	APIKey       string
	MaxRotate    int
	SoftCooldown time.Duration
	ErrThreshold int
	ErrCooldown  time.Duration
	// CodeArts 上游把并发会话/TPM 排队既可能返回 HTTP 400/429，
	// 也可能嵌在 HTTP 200 SSE 内。默认每账号最多 3 次（含首次）、间隔 2s。
	QueueRetryDelay    time.Duration
	QueueMaxAttempts   int
	StartupTimeout     time.Duration
	AccountWaitTimeout time.Duration
	StreamWriteTimeout time.Duration
	DefaultModel       string
	ConvStateFile      string
	WatchInfo          map[string]any
	AuthDir            string
	Listen             string
	// OAuthClient 短超时客户端，用于 WebUI 登录换取 token（可注入测试端点）。
	OAuthClient *upstream.Client
	// LoginConfig WebUI 登录配置（client_id 以官方客户端为准）。
	LoginConfig upstream.LoginConfig
	// OAuthCallbackHost 可选：只读取显式端口覆盖本地回调端口，host/path 不替换
	// 上游固定的 127.0.0.1 回调地址；远程浏览器仍需手动提交回调地址。
	OAuthCallbackHost string
	// Audit 可选的元数据审计接收端。为 nil 时不记录（进程级日志仍保留）。
	Audit *audit.Sink
}

// staticModel 静态兜底模型：动态发现失败时 /v1/models 仍能列出实测可用模型。
type staticModel struct {
	ID            string
	ContextWindow int64
	Benefit       bool
}

// staticModels 静态兜底表：**只放实测确认可用的模型**。
//
// 上游的模型发现（agent-center / builtin）会列出当前账号未注册的模型，静态表
// 若照抄发现结果，就等于把 404 写进了兜底路径。以下清单来自 2026-09-15 真实账号
// 逐个调用验证（见 docs/reverse-engineering.md §7）：
//
//	✅ GLM-5.2、glm-5.2-sft-harmony、Qwen3-VL-235B
//	❌ GLM-5.2-ArkTS-SPARK、OpenPangu-2.0-Pro、OpenPangu-2.0-Flash（002002009.404）
var staticModels = []staticModel{
	{ID: "GLM-5.2", ContextWindow: 202752},
	{ID: "glm-5.2-sft-harmony", ContextWindow: 131072},
	{ID: "Qwen3-VL-235B", ContextWindow: 131072},
	// 限时福利（需领取；领取后实测可用，聊天自动带 maas_type: benefit）
	{ID: "deepseek-v4-flash-0731", ContextWindow: 1048576, Benefit: true},
	{ID: "deepseek-v4-pro-0813", ContextWindow: 1048576, Benefit: true},
	{ID: "glm-5.3-flash", ContextWindow: 1048576, Benefit: true},
}

const maxBodyBytes = 8 << 20

// Handler 主路由。
type Handler struct {
	cfg   Config
	mux   *http.ServeMux
	oauth *oauthStore

	convMu sync.Mutex
	chats  map[string]string // account → 最近 chat_id
	// 黏性路由：conversation_id → account_name（多轮续接锁定同一账号，减少上游并发会话占用）。
	convAcct map[string]string

	loginMu sync.Mutex
	logins  map[string]*pendingLogin

	modelCatalog modelCatalogState
}

// pendingLogin WebUI 登录中间状态。
type pendingLogin struct {
	TicketID string
	Secret   string
	Verifier string
	Port     int
	Done     bool
	Err      string
}

// NewHandler 构建 handler。
func NewHandler(cfg Config) *Handler {
	if cfg.MaxRotate <= 0 {
		cfg.MaxRotate = 3
	}
	if cfg.SoftCooldown <= 0 {
		cfg.SoftCooldown = 60 * time.Second
	}
	if cfg.ErrThreshold <= 0 {
		cfg.ErrThreshold = 3
	}
	if cfg.ErrCooldown <= 0 {
		cfg.ErrCooldown = 10 * time.Minute
	}
	if cfg.QueueRetryDelay <= 0 {
		cfg.QueueRetryDelay = 2 * time.Second
	}
	if cfg.QueueMaxAttempts <= 0 {
		cfg.QueueMaxAttempts = 3
	}
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = 90 * time.Second
	}
	if cfg.AccountWaitTimeout <= 0 {
		cfg.AccountWaitTimeout = 10 * time.Second
	}
	if cfg.StreamWriteTimeout <= 0 {
		cfg.StreamWriteTimeout = 30 * time.Second
	}
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "glm-5.2"
	}
	if cfg.Upstream == nil {
		cfg.Upstream = upstream.New(60 * time.Second)
	}
	if cfg.OAuthClient == nil {
		cfg.OAuthClient = upstream.New(15 * time.Second)
	}
	if cfg.LoginConfig.ClientID == "" {
		cfg.LoginConfig = upstream.DefaultLoginConfig()
	}
	h := &Handler{
		cfg: cfg, mux: http.NewServeMux(), oauth: newOAuthStore(),
		chats: map[string]string{}, logins: map[string]*pendingLogin{},
		convAcct: map[string]string{},
	}
	h.loadChats()
	h.mux.HandleFunc("POST /v1/chat/completions", h.withAuth(h.chatCompletions))
	h.mux.HandleFunc("POST /v1/responses", h.withAuth(h.responses))
	h.mux.HandleFunc("POST /v1/messages", h.anthropicMessages)
	h.mux.HandleFunc("GET /v1/models", h.withAuth(h.models))
	h.mux.HandleFunc("GET /v1/quota", h.withAuth(h.handleQuota))
	h.mux.HandleFunc("GET /status", h.withAuth(h.status))
	h.mux.HandleFunc("GET /healthz", h.healthz)
	// WorkBuddy 风格控制台（页面不鉴权，API 走 Bearer）
	h.mux.HandleFunc("GET /{$}", h.servePanel)
	h.mux.HandleFunc("GET /admin", h.servePanel)
	h.mux.HandleFunc("GET /panel", h.servePanel)
	h.mux.HandleFunc("GET /panel/", h.servePanel)
	h.mux.HandleFunc("GET /admin/api/overview", h.withAuth(h.adminOverview))
	h.mux.HandleFunc("POST /admin/api/quota/refresh", h.withAuth(h.adminQuotaRefresh))
	h.mux.HandleFunc("GET /admin/api/requests", h.withAuth(h.adminRequests))
	h.mux.HandleFunc("GET /admin/api/audit", h.withAuth(h.adminAudit))
	h.mux.HandleFunc("GET /admin/api/models", h.withAuth(h.adminModels))
	h.mux.HandleFunc("POST /admin/api/models/refresh", h.withAuth(h.adminModelsRefresh))
	h.mux.HandleFunc("GET /admin/api/connections", h.withAuth(h.handleConnections))
	h.mux.HandleFunc("POST /admin/api/credits", h.withAuth(h.adminCredits))
	h.mux.HandleFunc("POST /admin/api/checkin", h.withAuth(h.adminCheckin))
	h.mux.HandleFunc("POST /admin/api/keepalive", h.withAuth(h.adminKeepalive))
	h.mux.HandleFunc("POST /admin/api/reload", h.withAuth(h.adminReload))
	h.mux.HandleFunc("POST /admin/api/accounts/enable", h.withAuth(h.adminEnable))
	h.mux.HandleFunc("POST /admin/api/accounts/disable", h.withAuth(h.adminDisable))
	h.mux.HandleFunc("POST /admin/api/accounts/clear-cooldown", h.withAuth(h.adminClearCooldown))
	h.mux.HandleFunc("POST /admin/api/oauth/start", h.withAuth(h.adminOAuthStart))
	h.mux.HandleFunc("POST /admin/api/oauth/poll", h.withAuth(h.adminOAuthPoll))
	h.mux.HandleFunc("POST /admin/api/oauth/import-callback", h.withAuth(h.adminOAuthImportCallback))
	h.mux.HandleFunc("GET /oauth/callback", h.oauthCallback)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.APIKey != "" {
			authz := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if len(authz) < len(prefix) || !strings.EqualFold(authz[:len(prefix)], prefix) {
				writeOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "missing or invalid API key")
				return
			}
			key := authz[len(prefix):]
			if subtle.ConstantTimeCompare([]byte(key), []byte(h.cfg.APIKey)) != 1 {
				writeOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "missing or invalid API key")
				return
			}
		}
		next(w, r)
	}
}

func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"accounts": h.cfg.Pool.List()})
}

// oauthCallback 本地回调：浏览器同机时由 portal 携带 code 跳到这里。
func (h *Handler) oauthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	secret := r.URL.Query().Get("secret")
	redirect := r.URL.Query().Get("redirect")
	log.Printf("oauth callback hit: has_code=%t has_secret=%t has_redirect=%t", code != "", secret != "", redirect != "")
	// portal 第一次回调：带 secret + redirect，要求 307 跳转（登录页链路的一部分）。
	if secret != "" && redirect != "" {
		// 用 redirect 里的 ticket_id 定位 pending login，并把华为云下发的 secret 换进去
		// （ticket 轮询必须用 portal 的 secret，而不是本地生成的）。
		if u, err := url.Parse(redirect); err == nil {
			tid := u.Query().Get("ticket_id")
			if tid != "" {
				h.oauth.enableTicketFallbackByTicket(tid, secret)
				h.loginMu.Lock()
				if p, ok := h.logins[tid]; ok {
					p.Secret = secret
					log.Printf("oauth callback: updated secret for ticket=%s", shortID(tid))
				}
				h.loginMu.Unlock()
			}
		}
		http.Redirect(w, r, redirect, http.StatusTemporaryRedirect)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if code == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("<h3>登录失败：缺少 code</h3><p>请回到 WebUI 重新发起登录。</p>"))
		return
	}
	candidates := h.oauth.codeSessionCandidates(r.URL.Query().Get("ticket_id"), secret)
	if len(candidates) == 0 {
		// 没有匹配（浏览器在远端时 code 通道不可用），提示用 ticket 通道
		_, _ = w.Write([]byte("<h3>登录已提交，请回到 WebUI 等待结果。</h3>"))
		return
	}
	// portal 的回调可能不带任何配对信息（实测只带 code），因此逐个候选尝试。
	// 授权码与 PKCE verifier 一对一校验：用错只会 STS5.1805，不会消耗授权码。
	var (
		tok     *upstream.TokenResponse
		sess    *oauthSession
		lastErr error
	)
	for _, cand := range candidates {
		t, err := h.cfg.OAuthClient.ExchangeCode(r.Context(), h.cfg.LoginConfig, code, cand.Verifier, cand.Port, cand.DPoPPrivateKey)
		if err == nil {
			tok, sess, lastErr = t, cand, nil
			break
		}
		lastErr = err
	}
	if lastErr != nil {
		// STS5.1805 = 授权码与服务端保存的 PKCE verifier 对不上，最常见的成因是
		// 链接生成后服务重启过（会话只在内存里）或重复使用了旧链接。
		hint := ""
		if strings.Contains(lastErr.Error(), "STS5.1805") || strings.Contains(lastErr.Error(), "invalid authorization code") {
			hint = "<p>授权码已失效：请回到 WebUI 重新点「登录」获取新链接，" +
				"并确保从生成到完成授权期间服务没有重启。</p>"
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<h3>换取凭证失败：" + lastErr.Error() + "</h3>" + hint))
		return
	}
	if err := h.saveLoginResult(tok, sess.Verifier, sess.DPoPPrivateKey); err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<h3>保存账号失败：" + err.Error() + "</h3>"))
		return
	}
	h.oauth.complete(sess.ID)
	_, _ = w.Write([]byte("<h3>登录成功，可关闭此页面并回到 WebUI。</h3>"))
}

func tokName(t *upstream.TokenResponse) string {
	if t == nil {
		return ""
	}
	return t.UserName
}

func tokToken(t *upstream.TokenResponse) string {
	if t == nil {
		return ""
	}
	return t.Credentials.SecurityToken
}

// saveLoginResult 落盘 auth 并加入账号池。
func (h *Handler) saveLoginResult(tok *upstream.TokenResponse, codeVerifier string, dpopPrivateJWK ...upstream.DPoPPrivateJWK) error {
	if h.cfg.AuthDir == "" {
		return errors.New("auth_dir not configured")
	}
	cred := tok.Credentials
	var privateJWK map[string]string
	if len(dpopPrivateJWK) > 0 {
		privateJWK = map[string]string(dpopPrivateJWK[0])
	}
	a := auth.New(tok.UserID, tok.UserName, tok.DomainID,
		cred.SecurityToken, cred.AccessKeyID, cred.SecretAccessKey,
		cred.Expiration, tok.RefreshToken, codeVerifier)
	a.SetClientID(h.cfg.LoginConfig.ClientID)
	a.SetDPoPPrivateKey(privateJWK)

	// portal 回调只回传 code，不带身份；user_id 为空会让 auth.FileName() 退化成
	// codearts-unknown.json，账号池按 UserID 建索引就会加载不到（或与既有账号重复）。
	// 因此落盘前用 STS 凭证把身份补全，补全失败也要保证 user_id 有值。
	if a.UserID == "" || a.UserName == "" {
		uid, uname, domain := lookupIdentity(a)
		if uid == "" {
			// 兜底：refresh_token 里的 sub 是唯一且稳定的，避免写出 unknown 文件
			uid = identityFromRefreshToken(tok.RefreshToken)
			log.Printf("webui login: identity lookup failed, fallback user_id=%q", uid)
		}
		if uid != "" {
			a.UserID, a.UserName = uid, firstNonEmpty(uname, a.UserName)
			if domain != "" {
				a.DomainID = domain
			}
		}
	}
	if err := auth.SaveNew(h.cfg.AuthDir, a); err != nil {
		return err
	}
	if p := h.cfg.Pool.AddAccount(a); p != nil && p.Auth != nil {
		// 刚登录的账号先不探测：用户很可能马上就要用，探测占用的上游会话
		// 槽位释放很慢，会把第一个真实请求挤到排队。
		noteTraffic(a.UserID)
		log.Printf("webui login success user_id=%s name=%s", a.UserID, a.UserName)
	} else {
		log.Printf("webui login saved user_id=%s name=%s (pool add failed)", a.UserID, a.UserName)
	}
	return nil
}

// lookupIdentity 用刚拿到的 STS 凭证查身份（先 caller-identity，再 current/user）。
func lookupIdentity(a *auth.Auth) (uid, name, domain string) {
	cred := upstream.SignCredential{
		AccessKeyID:     a.AccessKeyID,
		SecretAccessKey: a.SecretAccessKey,
		SecurityToken:   a.CloudDragonTok,
	}
	if uid, name, domain, err := upstream.CallerIdentity(cred); err == nil && uid != "" {
		return uid, name, domain
	} else if err != nil {
		log.Printf("webui login: caller-identity: %v", err)
	}
	if uid, name, domain, err := upstream.CurrentUser(cred); err == nil && uid != "" {
		return uid, name, domain
	} else if err != nil {
		log.Printf("webui login: current/user: %v", err)
	}
	return "", "", ""
}

// identityFromRefreshToken 从 JWT 形式的 refresh_token 里取 sub 作为兜底 user_id。
func identityFromRefreshToken(tok string) string {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	return claims.Sub
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// models
// ---------------------------------------------------------------------------

// models serves the cached list and schedules the existing bounded background
// discovery/probe work only for the OpenAI model-list route.
func (h *Handler) models(w http.ResponseWriter, r *http.Request) {
	entries := h.modelList()
	h.requestModelRefresh(false)
	h.probeUnknownAsync()
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   entries,
	})
}

// modelList reads and filters cached metadata only. Admin overview and release
// verification must not trigger metadata requests, benefit claims or chat probes.
func (h *Handler) modelList() []map[string]any {
	entries := modelEntries(h.cachedDynamicModels())
	// 过滤掉「已知不可用」的模型：这些条目在客户端点开就是 404 / benefit not
	// found，列出来只会误导。必须作用在最终条目上——静态兜底表也会补位，
	// 只在动态结果上过滤会让被下架的模型又从静态表冒出来。
	// 未知（还没探测出结论）的保留：宁可让用户试，也别把可用模型藏起来。
	if len(entries) > 0 {
		kept := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			id, _ := e["id"].(string)
			if !h.modelKnownUnusable(id) {
				kept = append(kept, e)
			}
		}
		entries = kept
	}
	return entries
}

// probeUnknownAsync 后台探测「还没有结论」的模型，不阻塞请求。
func (h *Handler) probeUnknownAsync() {
	availability.Lock()
	busy := availability.sweeping
	if !busy {
		availability.sweeping = true
	}
	availability.Unlock()
	if busy {
		return
	}
	go func() {
		defer func() {
			availability.Lock()
			availability.sweeping = false
			availability.Unlock()
		}()
		h.runProbes(h.pendingProbes())
	}()
}

// modelKnownUnusable reports whether every healthy account is excluded by
// authoritative model knowledge. An empty/unhealthy pool is not model evidence.
func (h *Handler) modelKnownUnusable(model string) bool {
	anyHealthy := false
	for _, acct := range h.cfg.Pool.Accounts() {
		if acct == nil || !h.cfg.Pool.Healthy(acct.Name) {
			continue
		}
		anyHealthy = true
		if _, eligible := cachedAccountModelEligibility(acct, model); eligible {
			return false
		}
	}
	return anyHealthy
}

// modelEntries 把账号模型目录 + 静态兜底包装成 OpenAI /v1/models 条目。
//
// 上游模型 ID 区分大小写：既列精确 ID（GLM-5.2），也补一条小写别名（glm-5.2，
// 老客户端习惯用小写），聊天时按选中账号的目录归一回精确 ID。
// 静态表只在动态目录缺失该 ID 时补位，顺序固定（动态已排序，静态按表序）。
func modelEntries(infos []upstream.ModelInfo) []map[string]any {
	out := make([]map[string]any, 0, len(infos)+len(staticModels))
	seen := map[string]bool{}
	add := func(id string, ctx, maxOut int64, benefit bool) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		if ctx == 0 {
			ctx = 131072 // 兜底
		}
		entry := map[string]any{
			"id":             id,
			"object":         "model",
			"created":        1753600000,
			"owned_by":       "codearts",
			"context_length": ctx,
		}
		if maxOut > 0 {
			if maxOut > upstream.MaxOutputTokens {
				maxOut = upstream.MaxOutputTokens
			}
			entry["max_output_tokens"] = maxOut
		}
		if benefit {
			entry["benefit"] = true
		}
		out = append(out, entry)
	}
	// addAlias 精确 ID + 就近补一条小写别名（已是小写则跳过）。
	addAlias := func(id string, ctx, maxOut int64, benefit bool) {
		add(id, ctx, maxOut, benefit)
		if lower := strings.ToLower(id); lower != id {
			add(lower, ctx, maxOut, benefit)
		}
	}
	for _, mi := range infos {
		addAlias(mi.ID, mi.ContextWindow, mi.MaxTokens, mi.Benefit)
	}
	for _, sm := range staticModels {
		addAlias(sm.ID, sm.ContextWindow, 0, sm.Benefit)
	}
	return out
}

// pickAccount 挑一个「目录里登记了这个模型」的健康账号，没有则退回普通轮转。
//
// 混合账号池里各账号的套餐不同：/v1/models 展示的是并集，若把请求先发给目录里
// 没有该模型的账号，上游会按「未注册模型」报 400，既浪费一次轮转（MaxRotate 只有
// 3 次，池子超过 3 个账号时可能永远轮不到有能力的那台），又会给健康账号记错误、
// 累计到阈值就冷却 10 分钟。所以按能力优先挑号。
func (h *Handler) pickAccount(tried map[string]bool, model string) *pool.Account {
	if acct := h.pickByCatalog(tried, model); acct != nil {
		return acct
	}
	// Compatibility helper: selection never performs synchronous discovery.
	// Live chat uses chatCandidates plus atomic pool admission for free slots.
	return h.cfg.Pool.PickExcluding(tried)
}

// pickByCatalog 在已发现目录的健康账号里挑第一个能服务该模型的。
func (h *Handler) pickByCatalog(tried map[string]bool, model string) *pool.Account {
	for _, acct := range h.cfg.Pool.Accounts() {
		if acct == nil || tried[acct.Name] || !h.cfg.Pool.Healthy(acct.Name) {
			continue
		}
		authz := acct.AuthSnapshot()
		resolved, _ := upstream.ResolveModelForAuth(acct.UID, model, authz)
		wantLower := strings.ToLower(resolved)
		if models, known := upstream.AccountModelsForAuth(acct.UID, authz); known && catalogHas(models, wantLower) {
			if st, _ := modelAvailabilityForAuth(acct.UID, resolved, authz); st == availUnusable {
				continue // 该账号实测调不通这个模型，别选它
			}
			return acct
		}
	}
	return nil
}

// accountCanServe 报告账号目录里是否登记了该模型；账号不存在或目录未知时乐观放行
// （交给上游判定，不凭缺失的目录拒请求）。
func (h *Handler) accountCanServe(acct *pool.Account, model string) bool {
	if acct == nil {
		return true
	}
	authz := acct.AuthSnapshot()
	resolved, _ := upstream.ResolveModelForAuth(acct.UID, model, authz)
	present, complete := upstream.AccountModelKnowledgeForAuth(acct.UID, resolved, authz)
	return present || !complete
}

// catalogHas 按小写 ID 在模型目录里查模型。
func catalogHas(models []upstream.ModelInfo, wantLower string) bool {
	for _, mi := range models {
		if strings.ToLower(mi.ID) == wantLower {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// chat
// ---------------------------------------------------------------------------

// buildUpstreamMessages 把 OpenAI 消息原生映射为 CodeArts V2 chat-completions。
// Chat-Id/Session-Id 只做亲和与缓存；多轮语义仍由完整 messages 承载。
func buildUpstreamMessages(req *chatRequest, toolsOn bool) []upstream.ChatMessage {
	_ = toolsOn // 保留参数以稳定内部调用面；工具 schema 由 ChatOptions 原生传递。
	keepCalls, keepResults := pairedToolMessages(req.Messages)
	out := make([]upstream.ChatMessage, 0, len(req.Messages))
	for i, m := range req.Messages {
		switch m.Role {
		case "system", "user":
			out = append(out, upstream.ChatMessage{Role: m.Role, Content: m.Text})
		case "assistant":
			reasoning := m.ReasoningContent
			wire := upstream.ChatMessage{Role: "assistant", Content: m.Text, ReasoningContent: &reasoning}
			if keepCalls[i] {
				for _, c := range m.ToolCalls {
					wire.ToolCalls = append(wire.ToolCalls, upstream.ChatToolCall{
						ID: c.ID, Type: "function",
						Function: upstream.ChatToolFunction{Name: c.Name, Arguments: c.Arguments},
					})
				}
				if m.Text == "" {
					wire.Content = nil
				}
			}
			out = append(out, wire)
		case "tool":
			if keepResults[i] {
				content := m.Text
				if content == "" {
					content = "(no output)"
				}
				out = append(out, upstream.ChatMessage{
					Role: "tool", Content: content, ToolCallID: m.ToolCallID, Name: m.Name,
				})
			}
		}
	}
	return out
}

// pairedToolMessages 只保留完整成对的 assistant.tool_calls + tool results。
// CodeArts 会对孤儿或部分成对的工具历史返回 400，并使后续每轮持续失败。
func pairedToolMessages(messages []openAIMessage) (map[int]bool, map[int]bool) {
	resultIndex := make(map[string]int)
	for i, m := range messages {
		if m.Role == "tool" && m.ToolCallID != "" {
			resultIndex[m.ToolCallID] = i
		}
	}
	keepCalls := make(map[int]bool)
	keepResults := make(map[int]bool)
	for i, m := range messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		complete := true
		for _, c := range m.ToolCalls {
			if c.ID == "" {
				complete = false
				break
			}
			if _, ok := resultIndex[c.ID]; !ok {
				complete = false
				break
			}
		}
		if !complete {
			continue
		}
		keepCalls[i] = true
		for _, c := range m.ToolCalls {
			keepResults[resultIndex[c.ID]] = true
		}
	}
	return keepCalls, keepResults
}

// nativeTools preserves a named function choice by exposing only that function
// to the upstream, whose tool_choice accepts strings but not a named object.
func nativeTools(tools []map[string]any, choice toolChoiceOpenAI) ([]map[string]any, error) {
	if choice.Mode != "function" {
		return tools, nil
	}
	var selected map[string]any
	for _, tool := range tools {
		fn, ok := tool["function"].(map[string]any)
		if !ok || fn["name"] != choice.Function {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("tool_choice function %q is defined more than once", choice.Function)
		}
		selected = tool
	}
	if selected == nil {
		return nil, fmt.Errorf("tool_choice function %q is not defined in tools", choice.Function)
	}
	return []map[string]any{selected}, nil
}

func nativeToolChoice(choice toolChoiceOpenAI) any {
	switch choice.Mode {
	case "none", "required":
		return choice.Mode
	case "function":
		// CodeArts InferHub only accepts string-form tool_choice; the object form
		// is rejected with InferHub.001001005.400 param invalid (measured 2026-10-06).
		// nativeTools limits the tools to the named function, so "required"
		// preserves the client's forced-function constraint.
		return "required"
	default:
		return "auto"
	}
}

func fromUpstreamToolCalls(calls []upstream.ChatToolCall) []openAIToolCall {
	out := make([]openAIToolCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, openAIToolCall{ID: c.ID, Name: c.Function.Name, Arguments: c.Function.Arguments})
	}
	assignCallIDs(out)
	return out
}

func usageEstimate(msgs []upstream.ChatMessage, comp *upstream.RawCompletion) map[string]any {
	if comp.Usage != nil {
		return comp.Usage
	}
	var pt int
	for _, m := range msgs {
		switch content := m.Content.(type) {
		case string:
			pt += len([]rune(content))/4 + 1
		default:
			raw, _ := json.Marshal(content)
			pt += len([]rune(string(raw)))/4 + 1
		}
	}
	ct := (len([]rune(comp.Content))+len([]rune(comp.Reasoning)))/4 + 1
	return map[string]any{"prompt_tokens": pt, "completion_tokens": ct, "total_tokens": pt + ct}
}

// buildCompletion 组装非流式响应。
func buildCompletion(model, reasoning, content string, calls []openAIToolCall, finish, chatID string, usage map[string]any) map[string]any {
	message := map[string]any{"role": "assistant"}
	if len(calls) > 0 {
		message["tool_calls"] = toOpenAIToolCalls(calls)
		if content == "" {
			message["content"] = nil
		} else {
			message["content"] = content
		}
	} else {
		message["content"] = content
	}
	if reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	resp := map[string]any{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}},
		"chat_id": chatID,
	}
	if usage != nil {
		resp["usage"] = usage
	}
	return resp
}

// handleUpstreamError 记录错误并决定是否处罚账号。
// 返回 true 表示模型只对当前账号不可用；首次数据前可继续尝试其他候选账号。
func (h *Handler) handleUpstreamError(acct *pool.Account, model string, err error, capturedOwner ...*auth.Auth) bool {
	var downstream *upstream.DownstreamWriteError
	if isParamInvalidError(err) || isChatTimeout(err) || errors.Is(err, context.Canceled) || errors.Is(err, upstream.ErrIncompleteStream) || errors.As(err, &downstream) {
		return false
	}
	if reason := upstreamUnavailableReason(err); reason != "" {
		owner := acct.AuthSnapshot()
		if len(capturedOwner) != 0 {
			owner = capturedOwner[0]
		}
		h.markModelUnusable(acct, owner, model, reason)
		log.Printf("model unusable account=%s model=%s: %s", acct.Name, model, reason)
		return true
	}
	var ae *upstream.ApiError
	if errors.As(err, &ae) {
		// 「模型对这个账号不可用」是账号能力问题，不是账号故障，必须在状态码分支
		// **之前**判断：not registered / benefit not found 会被嵌入错误包装成
		// 5xx，落到下面的 `ae.Status >= 500` 就把健康账号冷却 10 分钟——实测
		// 调用一个未注册模型，整个账号被冷却，后续正常请求全部失败。
		switch {
		case ae.Status == 401 || ae.Code == 401:
			h.cfg.Pool.Disable(acct.Name, "upstream HTTP 401: authentication rejected")
		case isConcurrentUpstreamError(err):
			log.Printf("upstream concurrent limit (transient) account=%s", acct.Name)
		case ae.Status == 429:
			h.cfg.Pool.Cooldown(acct.Name, pool.CoolSoft, h.cfg.SoftCooldown, "upstream HTTP 429: rate limited")
		case ae.Status >= 500:
			h.cfg.Pool.Cooldown(acct.Name, pool.CoolErr, h.cfg.ErrCooldown, fmt.Sprintf("upstream HTTP %d", ae.Status))
		default:
			h.cfg.Pool.NoteError(acct.Name, h.cfg.ErrThreshold, h.cfg.ErrCooldown)
		}
		return false
	}
	// Late SSE errors use UpstreamError rather than ApiError. They are still
	// transient concurrency refusals, without enabling a retry after first data.
	if isConcurrentUpstreamError(err) {
		return false
	}
	h.cfg.Pool.NoteError(acct.Name, h.cfg.ErrThreshold, h.cfg.ErrCooldown)
	return false
}

// isParamInvalidError 判断是否为上游「请求参数无效」类错误（InferHub.001001005）。
// 这类错误是客户端请求形状问题：换账号结果相同，绝不能冷却账号（曾因此打停整池）。
func isParamInvalidError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *upstream.ApiError
	var streamErr *upstream.UpstreamError
	var message string
	switch {
	case errors.As(err, &apiErr):
		message = apiErr.Message
	case errors.As(err, &streamErr):
		message = streamErr.Code + " " + streamErr.Msg
	default:
		return false
	}
	low := strings.ToLower(message)
	return strings.Contains(low, "inferhub.001001005") || strings.Contains(low, "request param is invalid")
}

// isConcurrentLimitError 判断是否为上游并发会话上限错误（瞬时、可重试）。
func isConcurrentLimitError(msg string) bool {
	low := strings.ToLower(msg)
	return strings.Contains(low, "tm.00001041") || strings.Contains(low, "并发会话")
}

func isConcurrentUpstreamError(err error) bool {
	var apiErr *upstream.ApiError
	if errors.As(err, &apiErr) {
		return apiErr.Status != http.StatusUnauthorized && apiErr.Code != http.StatusUnauthorized && isConcurrentLimitError(apiErr.Message)
	}
	var streamErr *upstream.UpstreamError
	return errors.As(err, &streamErr) && isConcurrentLimitError(streamErr.Code+" "+streamErr.Msg)
}

func isQueueLimitError(err *upstream.ApiError) bool {
	if err == nil {
		return false
	}
	if err.Status == http.StatusTooManyRequests {
		return true
	}
	low := strings.ToLower(err.Message)
	return (err.Status == http.StatusBadRequest && isConcurrentLimitError(low)) ||
		strings.Contains(low, "inferhub.modelarts.81111.429") ||
		strings.Contains(low, "tpm limit")
}

func truncateMsg(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	raw, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func writeOpenAIError(w http.ResponseWriter, status int, code, msg string) {
	if cw, ok := w.(*chatResponseWriter); ok {
		cw.setErrorCode(code)
	}
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"message": msg, "type": "api_error", "code": code},
	})
}

func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := crand.Read(b); err != nil {
		seed := fmt.Sprintf("%x", time.Now().UnixNano())
		for len(seed) < n {
			seed += seed
		}
		return seed[:n]
	}
	return hex.EncodeToString(b)[:n]
}

func validChatID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func (h *Handler) loadChats() {
	if h.cfg.ConvStateFile == "" {
		return
	}
	raw, err := os.ReadFile(h.cfg.ConvStateFile)
	if err != nil {
		return
	}
	_ = json.Unmarshal(raw, &h.chats)
}

func (h *Handler) saveChats() {
	if h.cfg.ConvStateFile == "" {
		return
	}
	h.convMu.Lock()
	defer h.convMu.Unlock()
	raw, _ := json.MarshalIndent(h.chats, "", "  ")
	if err := os.WriteFile(h.cfg.ConvStateFile+".tmp", raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(h.cfg.ConvStateFile+".tmp", h.cfg.ConvStateFile)
}
