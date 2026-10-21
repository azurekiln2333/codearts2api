package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCreditsAutoClaimConfigAndEnv(t *testing.T) {
	t.Setenv("CA2A_API_KEY", "test-key")
	t.Setenv("CA2A_CREDITS_AUTO_CLAIM", "")
	if cfg, err := Load(""); err != nil || !cfg.CreditsAutoClaim {
		t.Fatalf("auto claim default disabled: err=%v", err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"credits_auto_claim":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := Load(path); err != nil || cfg.CreditsAutoClaim {
		t.Fatalf("config opt-out ignored: err=%v", err)
	}
	t.Setenv("CA2A_CREDITS_AUTO_CLAIM", "1")
	if cfg, err := Load(path); err != nil || !cfg.CreditsAutoClaim {
		t.Fatalf("env override ignored: err=%v", err)
	}
	t.Setenv("CA2A_CREDITS_AUTO_CLAIM", "0")
	if cfg, err := Load(""); err != nil || cfg.CreditsAutoClaim {
		t.Fatalf("env opt-out ignored: err=%v", err)
	}
}

// 仓库自带的 config.example.json 含 // 注释，README 让人直接 cp 成 config.json；
// 带注释的配置必须能加载，否则快起步就断在第一步。
func TestLoadToleratesExampleConfigComments(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config.example.json"))
	if err != nil {
		t.Fatalf("read config.example.json: %v", err)
	}
	// 没有 api_key 现在会拒绝启动，示例配置的用例显式给一个（env 优先）。
	t.Setenv("CA2A_API_KEY", "test-key")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("带注释的示例配置应能加载: %v", err)
	}
	if cfg.Listen != ":7866" || cfg.AuthDir != "./auths" || cfg.DefaultModel != "snap-chat" {
		t.Fatalf("示例配置字段未生效: %+v", cfg)
	}
	if cfg.Cooldown.SoftRate != "60s" || cfg.MaxConcurrent != 1 || cfg.Watch.PollMinutes != 30 {
		t.Fatalf("示例配置嵌套字段未生效: %+v", cfg)
	}
	if cfg.KeepaliveWindow != "10m" {
		t.Errorf("keepalive_window 未生效: %q", cfg.KeepaliveWindow)
	}
}

func TestChatLimitsConfigDefaultsOverridesAndPoolWiring(t *testing.T) {
	t.Setenv("CA2A_API_KEY", "test-key")
	for _, name := range []string{"CA2A_MAX_CONCURRENT", "CA2A_MAX_PENDING", "CA2A_STARTUP_TIMEOUT_SECONDS", "CA2A_ACCOUNT_WAIT_SECONDS", "CA2A_STREAM_WRITE_TIMEOUT_SECONDS", "CA2A_UPSTREAM_TIMEOUT_SECONDS", "CA2A_QUEUE_RETRY_SECONDS", "CA2A_QUEUE_MAX_ATTEMPTS"} {
		t.Setenv(name, "")
	}
	defaults, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if defaults.MaxConcurrent != 1 || defaults.MaxPending != 64 || defaults.StartupTimeoutSeconds != 90 || defaults.AccountWaitSeconds != 10 || defaults.StreamWriteTimeoutSeconds != 30 || defaults.Upstream.TimeoutSeconds != 60 || defaults.QueueMaxAttempts != 3 || defaults.QueueRetrySeconds != 2 {
		t.Fatalf("chat defaults inconsistent: %+v", defaults)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"max_concurrent":5,"max_pending":7,"startup_timeout_seconds":80,"account_wait_seconds":4,"stream_write_timeout_seconds":12,"upstream":{"timeout_seconds":120},"queue_retry_seconds":6,"queue_max_attempts":9}`), 0o600); err != nil {
		t.Fatal(err)
	}
	explicit, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.MaxConcurrent != 5 || explicit.Upstream.TimeoutSeconds != 120 || explicit.MaxPending != 7 || explicit.QueueMaxAttempts != 9 || explicit.QueueRetrySeconds != 6 {
		t.Fatalf("explicit legacy settings not preserved: %+v", explicit)
	}
	for name, value := range map[string]string{
		"CA2A_MAX_CONCURRENT": "2", "CA2A_MAX_PENDING": "11", "CA2A_STARTUP_TIMEOUT_SECONDS": "70", "CA2A_ACCOUNT_WAIT_SECONDS": "3", "CA2A_STREAM_WRITE_TIMEOUT_SECONDS": "8", "CA2A_UPSTREAM_TIMEOUT_SECONDS": "45", "CA2A_QUEUE_RETRY_SECONDS": "1", "CA2A_QUEUE_MAX_ATTEMPTS": "4",
	} {
		t.Setenv(name, value)
	}
	override, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if override.MaxConcurrent != 2 || override.MaxPending != 11 || override.StartupTimeoutSeconds != 70 || override.AccountWaitSeconds != 3 || override.StreamWriteTimeoutSeconds != 8 || override.Upstream.TimeoutSeconds != 45 || override.QueueRetrySeconds != 1 || override.QueueMaxAttempts != 4 {
		t.Fatalf("environment overrides ignored: %+v", override)
	}
	pc := override.ToPoolConfig()
	if pc.MaxConcurrent != 2 || pc.MaxPending != 11 || pc.UpstreamTimeout != 45*time.Second {
		t.Fatalf("pool did not receive chat settings: %+v", pc)
	}
}

func TestStripJSONCommentsKeepsStrings(t *testing.T) {
	raw := []byte("{\n" +
		"  // 行注释\n" +
		"  \"oauth_callback_host\": \"https://oneapi.example.com/codearts\", /* 块注释 */\n" +
		"  \"note\": \"含 // 与 /* 的字符串\",\n" +
		"  \"n\": 1\n" +
		"}")
	var v map[string]any
	if err := json.Unmarshal(stripJSONComments(raw), &v); err != nil {
		t.Fatalf("剥离注释后应是合法 JSON: %v", err)
	}
	if v["oauth_callback_host"] != "https://oneapi.example.com/codearts" {
		t.Errorf("URL 值被破坏: %v", v["oauth_callback_host"])
	}
	if v["note"] != "含 // 与 /* 的字符串" {
		t.Errorf("字符串内容被破坏: %v", v["note"])
	}
	if v["n"] != float64(1) {
		t.Errorf("n=%v", v["n"])
	}

	// 无注释的配置保持原样。
	plain := []byte(`{"listen":":7866"}`)
	if got := string(stripJSONComments(plain)); got != string(plain) {
		t.Errorf("无注释配置被改动: %q", got)
	}
}

// 剥离器的边界：CRLF、文件末尾无换行的注释、字符串里的转义引号、
// 跨行块注释、以及孤立的 '/'。这些走错一个就会静默吃掉配置。
func TestStripJSONCommentsEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]any
	}{
		{
			name: "CRLF 与行尾注释",
			in:   "{\r\n  \"listen\": \":7866\", // 端口\r\n  \"default_model\": \"glm-5.2\"\r\n}",
			want: map[string]any{"listen": ":7866", "default_model": "glm-5.2"},
		},
		{
			name: "文件末尾注释无换行",
			in:   "{\"listen\":\":7866\"} // 结尾注释",
			want: map[string]any{"listen": ":7866"},
		},
		{
			name: "字符串内转义引号后的 //",
			in:   `{"note":"a\"//b","listen":":7866"}`,
			want: map[string]any{"note": `a"//b`, "listen": ":7866"},
		},
		{
			name: "跨行块注释",
			in:   "{\n/*\n 多行\n 说明\n*/\n\"listen\": \":7866\"\n}",
			want: map[string]any{"listen": ":7866"},
		},
		{
			name: "值后紧跟块注释",
			in:   `{"listen":"/*:7866*/","n":1/* 尾随 */}`,
			want: map[string]any{"listen": "/*:7866*/", "n": float64(1)},
		},
		{
			name: "注释里的引号不改变字符串状态",
			in:   "{\"listen\": \":7866\" // 这里的 \" 不配对\n}",
			want: map[string]any{"listen": ":7866"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]any
			if err := json.Unmarshal(stripJSONComments([]byte(tc.in)), &got); err != nil {
				t.Fatalf("剥离注释后应是合法 JSON: %v\n输入: %s", err, tc.in)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("字段数不符: got=%v want=%v", got, tc.want)
			}
			for k, w := range tc.want {
				if got[k] != w {
					t.Errorf("%s: got=%v want=%v", k, got[k], w)
				}
			}
		})
	}

	// 孤立的 '/' 不能 panic 或死循环（结果是否合法 JSON 无所谓）。
	for _, in := range []string{"{", "/", `{"a":1}/`, `{"a":1}/*`} {
		_ = stripJSONComments([]byte(in))
	}
}
