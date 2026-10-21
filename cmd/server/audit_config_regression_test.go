package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codearts2api/internal/audit"
)

func isolatedAuditConfig(t *testing.T) {
	t.Helper()
	t.Setenv("CA2A_API_KEY", "fixture-config-key")
	for _, name := range []string{"CA2A_STATE_FILE", "CA2A_AUDIT_ENABLED", "CA2A_AUDIT_DIR", "CA2A_AUDIT_MAX_QUEUE", "CA2A_AUDIT_RECENT", "CA2A_AUDIT_MAX_FILE_BYTES", "CA2A_AUDIT_MAX_FILES", "CA2A_AUDIT_MAX_TOTAL_BYTES", "CA2A_AUDIT_RETENTION_DAYS", "CA2A_AUDIT_FLUSH_INTERVAL"} {
		t.Setenv(name, "")
	}
}

func auditConfigFile(t *testing.T, fields map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.json")
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAuditDefaultsAndDirectoryFollowPersistentState(t *testing.T) {
	isolatedAuditConfig(t)
	state := filepath.Join(t.TempDir(), "persistent", "state.json")
	path := auditConfigFile(t, map[string]any{"state_file": state}) // legacy JSON has no audit section
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	o := c.ToAuditOptions()
	if !o.Enabled || o.Dir != filepath.Join(filepath.Dir(state), "audit") || o.MaxQueue != audit.DefaultMaxQueue || o.Recent != 512 || o.MaxFiles != 8 || o.MaxFileBytes != 8<<20 || o.MaxTotalBytes != 64<<20 || o.RetentionDays != 7 || o.FlushInterval != time.Second {
		t.Fatalf("legacy config did not receive compatible audit defaults: %+v", o)
	}
	customState := filepath.Join(t.TempDir(), "env-state.json")
	t.Setenv("CA2A_STATE_FILE", customState)
	c, err = Load(path)
	if err != nil || c.Audit.Dir != filepath.Join(filepath.Dir(customState), "audit") {
		t.Fatalf("derived audit path ignored the effective state path: cfg=%+v err=%v", c, err)
	}
}

func TestAuditJSONEnvironmentAndOptOutReachSink(t *testing.T) {
	isolatedAuditConfig(t)
	dir := filepath.Join(t.TempDir(), "json-audit")
	path := auditConfigFile(t, map[string]any{"audit": map[string]any{"enabled": false, "dir": dir, "max_queue": 17, "recent": 23, "max_file_bytes": 4096, "max_files": 3, "max_total_bytes": 10000, "retention_days": 2, "flush_interval": "250ms"}})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	o := c.ToAuditOptions()
	if o.Enabled || o.Dir != dir || o.MaxQueue != 17 || o.Recent != 23 || o.MaxFileBytes != 4096 || o.MaxFiles != 3 || o.MaxTotalBytes != 10000 || o.RetentionDays != 2 || o.FlushInterval != 250*time.Millisecond {
		t.Fatalf("JSON audit configuration was lost: %+v", o)
	}
	envDir := filepath.Join(t.TempDir(), "env-audit")
	for name, value := range map[string]string{
		"CA2A_AUDIT_ENABLED": "1", "CA2A_AUDIT_DIR": envDir, "CA2A_AUDIT_MAX_QUEUE": "19", "CA2A_AUDIT_RECENT": "29",
		"CA2A_AUDIT_MAX_FILE_BYTES": "2048", "CA2A_AUDIT_MAX_FILES": "2", "CA2A_AUDIT_MAX_TOTAL_BYTES": "3000",
		"CA2A_AUDIT_RETENTION_DAYS": "3", "CA2A_AUDIT_FLUSH_INTERVAL": "200ms",
	} {
		t.Setenv(name, value)
	}
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	o = c.ToAuditOptions()
	if !o.Enabled || o.Dir != envDir || o.MaxQueue != 19 || o.Recent != 29 || o.MaxFileBytes != 2048 || o.MaxFiles != 2 || o.MaxTotalBytes != 3000 || o.RetentionDays != 3 || o.FlushInterval != 200*time.Millisecond {
		t.Fatalf("environment overrides did not reach audit options: %+v", o)
	}
	sink, err := audit.Open(o)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close(context.Background())
	if st := sink.Stats(); !st.Enabled || st.Dir != envDir || st.MaxQueue != 19 || st.MaxTotalBytes != 3000 {
		t.Fatalf("effective runtime differs from audit config: %+v", st)
	}
	for _, disabled := range []string{"false", "0"} {
		t.Setenv("CA2A_AUDIT_ENABLED", disabled)
		c, err := Load(path)
		if err != nil || c.ToAuditOptions().Enabled {
			t.Fatalf("audit opt-out %q was ignored: err=%v", disabled, err)
		}
	}
}

func TestAuditNumericBoundsAreEffectiveAtStorageBoundary(t *testing.T) {
	isolatedAuditConfig(t)
	path := auditConfigFile(t, map[string]any{"audit": map[string]any{"dir": t.TempDir(), "max_queue": 999999, "recent": 999999, "max_file_bytes": int64(1 << 40), "max_files": 999999, "max_total_bytes": int64(1 << 40), "retention_days": 999999, "flush_interval": "1h"}})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	sink, err := audit.Open(c.ToAuditOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close(context.Background())
	o := sink.Options()
	if o.MaxQueue != 4096 || o.Recent != 8192 || o.MaxFileBytes != 64<<20 || o.MaxFiles != 64 || o.MaxTotalBytes != 1<<30 || o.RetentionDays != 90 || o.FlushInterval != 10*time.Second {
		t.Fatalf("unbounded audit settings reached storage: %+v", o)
	}
	sink.Close(context.Background())
	t.Setenv("CA2A_AUDIT_MAX_QUEUE", "0")
	t.Setenv("CA2A_AUDIT_RECENT", "-1")
	t.Setenv("CA2A_AUDIT_MAX_FILE_BYTES", "0")
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := audit.Open(c.ToAuditOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(context.Background())
	if o := second.Options(); o.MaxQueue != audit.DefaultMaxQueue || o.Recent != audit.DefaultRecent || o.MaxFileBytes != audit.DefaultMaxFileBytes {
		t.Fatalf("nonpositive values did not normalize to defaults: %+v", o)
	}
}
