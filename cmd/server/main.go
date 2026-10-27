package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"codearts2api/internal/audit"
	"codearts2api/internal/auth"
	"codearts2api/internal/pool"
	"codearts2api/internal/scheduler"
	"codearts2api/internal/server"
	"codearts2api/internal/upstream"
)

func main() {
	cfgPath := flag.String("config", "config.json", "path to config json")
	flag.Parse()

	cfg, err := Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	auths, err := auth.LoadDir(cfg.AuthDir)
	if err != nil {
		log.Fatalf("load auths: %v", err)
	}
	log.Printf("loaded %d account(s) from %s", len(auths), cfg.AuthDir)
	if len(auths) == 0 {
		log.Printf("WARNING: no accounts found. Run `codearts2api login` first.")
	}

	if cfg.StateFile != "" {
		_ = os.MkdirAll(filepath.Dir(cfg.StateFile), 0o700)
	}
	p, err := pool.New(auths, cfg.ToPoolConfig(), cfg.StateFile)
	if err != nil {
		log.Fatalf("build pool: %v", err)
	}

	c := upstream.New(time.Duration(cfg.Upstream.TimeoutSeconds) * time.Second)
	c.SetBenefitAutoClaim(cfg.BenefitAutoClaim)

	sink, err := audit.Open(cfg.ToAuditOptions())
	if err != nil {
		// Audit is diagnostic only: never fail startup because of it.
		log.Printf("WARNING: audit disabled: %v", err)
		sink = nil
	}

	sch := scheduler.New(scheduler.Config{
		Pool:              p,
		Enabled:           cfg.Watch.Enabled,
		CreditsAutoClaim:  cfg.CreditsAutoClaim,
		PollInterval:      time.Duration(cfg.Watch.PollMinutes) * time.Minute,
		RefreshSkew:       time.Duration(cfg.Watch.RefreshSkewM) * time.Minute,
		KeepaliveInterval: time.Duration(cfg.Watch.KeepaliveInterval) * time.Minute,
	})

	h := server.NewHandler(server.Config{
		Pool:               p,
		Upstream:           c,
		APIKey:             cfg.APIKey,
		MaxRotate:          3,
		SoftCooldown:       cfg.SoftRateDur,
		ErrThreshold:       cfg.Cooldown.ErrThresh,
		ErrCooldown:        cfg.ErrCooldownDur,
		DefaultModel:       cfg.DefaultModel,
		QueueRetryDelay:    time.Duration(cfg.QueueRetrySeconds) * time.Second,
		QueueMaxAttempts:   cfg.QueueMaxAttempts,
		StartupTimeout:     time.Duration(cfg.StartupTimeoutSeconds) * time.Second,
		AccountWaitTimeout: time.Duration(cfg.AccountWaitSeconds) * time.Second,
		StreamWriteTimeout: time.Duration(cfg.StreamWriteTimeoutSeconds) * time.Second,
		ConvStateFile:      cfg.StateFile + ".chats.json",
		WatchInfo: map[string]any{
			"enabled":              cfg.Watch.Enabled,
			"credits_auto_claim":   cfg.CreditsAutoClaim,
			"poll_minutes":         cfg.Watch.PollMinutes,
			"refresh_skew_minutes": cfg.Watch.RefreshSkewM,
			"keepalive_interval":   cfg.Watch.KeepaliveInterval,
			"max_concurrent":       cfg.MaxConcurrent,
			"max_pending":          cfg.MaxPending,
			"keepalive_window":     "10m",
		},
		AuthDir:           cfg.AuthDir,
		Listen:            cfg.Listen,
		OAuthClient:       upstream.New(15 * time.Second),
		LoginConfig:       loginConfig(cfg),
		OAuthCallbackHost: cfg.OAuthCallbackHost,
		Audit:             sink,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go sch.Run(ctx)
	h.StartQuotaCollection(ctx)
	h.StartModelDiscovery(ctx)
	h.StartAvailabilityProber(ctx)
	if sink != nil {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			sink.Close(closeCtx)
		}()
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           h,
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("codearts2api listening on %s (api_key=%v)", cfg.Listen, cfg.APIKey != "")
	if sink != nil && sink.Enabled() {
		st := sink.Stats()
		log.Printf("audit enabled dir=%s recent=%d queue=%d files=%dx%dMiB retention=%dd",
			st.Dir, st.Recent, st.MaxQueue, st.MaxFiles, st.MaxFileBytes>>20, st.RetentionDays)
	} else {
		log.Printf("audit disabled")
	}
	log.Printf("chat limits per_account=%d pending=%d startup=%ds account_wait=%ds upstream_idle=%ds write=%ds queue_attempts=%d queue_delay=%ds",
		cfg.MaxConcurrent, cfg.MaxPending, cfg.StartupTimeoutSeconds, cfg.AccountWaitSeconds,
		cfg.Upstream.TimeoutSeconds, cfg.StreamWriteTimeoutSeconds, cfg.QueueMaxAttempts, cfg.QueueRetrySeconds)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("http: %v", err)
	}
	log.Printf("bye")
}

// loginConfig 决定 WebUI 登录使用的 OAuth 配置。
//
// client_id 优先取已有账号记录的取值（refresh_token 与 client_id 绑定，混用会让
// 老账号刷新失败），其次取配置项，最后才是内置默认值。其余字段必须来自
// DefaultLoginConfig（否则 authorize 链接会缺 host / plugin 信息）。
func loginConfig(cfg *Config) upstream.LoginConfig {
	lc := upstream.DefaultLoginConfig()
	if cfg.LoginClientID != "" {
		lc.ClientID = cfg.LoginClientID
	}
	if auths, err := auth.LoadDir(cfg.AuthDir); err == nil {
		for _, a := range auths {
			if id := a.ClientIDOr(""); id != "" {
				lc.ClientID = id
				break
			}
		}
	}
	return lc
}
