package pool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codearts2api/internal/auth"
)

func healthPool(t *testing.T, stateFile string) *Pool {
	t.Helper()
	auths := []*auth.Auth{
		auth.New("acct-a", "a", "domain", "token", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", ""),
	}
	p, err := New(auths, Config{MaxConcurrent: 1, ErrThreshold: 3, ErrCooldown: 10 * time.Minute, SoftCooldown: time.Minute}, stateFile)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func accountState(t *testing.T, p *Pool, name string) map[string]any {
	t.Helper()
	for _, st := range p.List() {
		if st["name"] == name {
			return st
		}
	}
	t.Fatalf("account %q not listed", name)
	return nil
}

// Every counted error must refresh last_error_at, even below the threshold.
func TestNoteErrorAlwaysRecordsFailureTimestamp(t *testing.T) {
	p := healthPool(t, "")
	name := p.Accounts()[0].Name

	before := time.Now().Add(-time.Second)
	p.NoteError(name, 3, time.Minute) // count 1 of 3: no cooldown yet

	st := accountState(t, p, name)
	if st["cooling"] != false {
		t.Fatalf("one error must not cool the account: %#v", st)
	}
	at, _ := st["last_error_at"].(string)
	if at == "" {
		t.Fatal("last_error_at must be set after the first counted error")
	}
	ts, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatalf("parse last_error_at %q: %v", at, err)
	}
	if ts.Before(before) {
		t.Fatalf("last_error_at %s is stale", ts)
	}
	if class, _ := st["last_failure_class"].(string); class == "" {
		t.Fatalf("historical failure class not recorded: %#v", st)
	}
}

// A recovery clears *current* error state but keeps the historical failure.
func TestSuccessAfterErrorsKeepsHistoryAndPersists(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	p := healthPool(t, state)
	name := p.Accounts()[0].Name

	p.NoteError(name, 3, time.Minute)
	p.NoteError(name, 3, time.Minute)
	if got := accountState(t, p, name)["err_count"]; got != 2 {
		t.Fatalf("err_count = %v, want 2", got)
	}

	p.NoteSuccess(name)
	st := accountState(t, p, name)
	if got := st["err_count"]; got != 0 {
		t.Fatalf("err_count must clear on recovery, got %v", got)
	}
	if got, _ := st["last_error"].(string); got != "" {
		t.Fatalf("current last_error must clear on recovery, got %q", got)
	}
	if got, _ := st["last_failure_text"].(string); got == "" {
		t.Fatalf("historical failure description must survive recovery: %#v", st)
	}
	if got, _ := st["last_failure_at"].(string); got == "" {
		t.Fatalf("historical failure timestamp must survive recovery: %#v", st)
	}
	if got, _ := st["last_success_at"].(string); got == "" {
		t.Fatalf("last_success_at must be recorded: %#v", st)
	}

	// The recovery must be persisted, so a restart cannot resurrect err_count.
	raw, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("state not written on recovery: %v", err)
	}
	var persisted []struct {
		Name          string `json:"name"`
		ErrCount      int    `json:"err_count"`
		LastFailureAt string `json:"last_failure_at"`
	}
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || persisted[0].ErrCount != 0 {
		t.Fatalf("persisted err_count not cleared: %s", raw)
	}
	if persisted[0].LastFailureAt == "" {
		t.Fatalf("persisted history missing: %s", raw)
	}

	reloaded := healthPool(t, state)
	if got := accountState(t, reloaded, name)["err_count"]; got != 0 {
		t.Fatalf("restart resurrected err_count = %v", got)
	}
}

// Routine successes must not write state on every request.
func TestSuccessWithoutErrorsDoesNotWriteState(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	p := healthPool(t, state)
	name := p.Accounts()[0].Name

	p.NoteSuccess(name) // no errors yet: nothing to persist
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("routine success created state.json (err=%v)", err)
	}

	for i := 0; i < 5; i++ {
		p.NoteSuccess(name)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("steady-state success wrote state.json (err=%v)", err)
	}
}

// A recovery must not override a manual disable or an active cooldown.
func TestSuccessDoesNotClearManualDisableOrActiveCooldown(t *testing.T) {
	p := healthPool(t, "")
	name := p.Accounts()[0].Name

	p.Disable(name, "manual disable")
	p.NoteSuccess(name)
	st := accountState(t, p, name)
	if st["disabled"] != true || st["schedulable"] != false {
		t.Fatalf("success cleared a manual disable: %#v", st)
	}

	other := healthPool(t, "")
	name2 := other.Accounts()[0].Name
	other.Cooldown(name2, CoolSoft, time.Minute, "upstream HTTP 429: rate limited")
	other.NoteSuccess(name2)
	st2 := accountState(t, other, name2)
	if st2["cooling"] != true {
		t.Fatalf("success cleared an active cooldown: %#v", st2)
	}
}

// Cooldown expiry keeps the counter (policy unchanged) but presentation must not
// claim the account is currently failing.
func TestCooldownExpiryKeepsCounterAndReportsSchedulable(t *testing.T) {
	p := healthPool(t, "")
	name := p.Accounts()[0].Name
	p.Cooldown(name, CoolSoft, 30*time.Millisecond, "upstream HTTP 429: rate limited")
	p.NoteError(name, 1, 30*time.Millisecond)

	time.Sleep(60 * time.Millisecond)
	st := accountState(t, p, name)
	if st["cooling"] != false {
		t.Fatalf("cooldown should have expired: %#v", st)
	}
	if st["schedulable"] != true {
		t.Fatalf("expired cooldown must be schedulable: %#v", st)
	}
	if count, _ := st["err_count"].(int); count == 0 {
		t.Fatal("policy check: expiry must not reset the counter")
	}
	if at, _ := st["last_failure_at"].(string); at == "" {
		t.Fatalf("history must remain after expiry: %#v", st)
	}
}

// A legacy state file without the new fields loads with unknown timestamps.
func TestLegacyStateJSONLoadsWithUnknownTimestamps(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	legacy := `[{"name":"acct-a","err_count":4,"disabled":false,"reason":"","last_error":"context canceled"}]`
	if err := os.WriteFile(state, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	p := healthPool(t, state)
	st := accountState(t, p, "acct-a")
	if got := st["err_count"]; got != 4 {
		t.Fatalf("err_count = %v, want 4", got)
	}
	if got, _ := st["last_error_at"].(string); got != "" {
		t.Fatalf("legacy timestamp must stay unknown, got %q", got)
	}
	if got, _ := st["last_failure_at"].(string); got != "" {
		t.Fatalf("legacy history must stay unknown, got %q", got)
	}
	// The current reason is preserved (policy unchanged), but schedulability is
	// derived from disabled/cooling only.
	if st["schedulable"] != true {
		t.Fatalf("legacy account should be schedulable: %#v", st)
	}
}

// New fields round-trip through state.json.
func TestStateJSONRoundTripKeepsHistory(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	p := healthPool(t, state)
	name := p.Accounts()[0].Name
	p.Cooldown(name, CoolErr, time.Minute, "upstream HTTP 500")
	p.NoteSuccess(name) // recovery: keeps history, clears current

	reloaded := healthPool(t, state)
	st := accountState(t, reloaded, name)
	if got, _ := st["last_failure_class"].(string); got != "upstream_5xx" {
		t.Fatalf("history class not round-tripped: %#v", st)
	}
	if got, _ := st["last_failure_text"].(string); got != "upstream server error" {
		t.Fatalf("history text not round-tripped: %#v", st)
	}
}
