package pool

import (
	"sync"
	"testing"
	"time"

	"codearts2api/internal/auth"
)

func TestWithCurrentAuthRejectsReplacedRemovedAndUnhealthyAccounts(t *testing.T) {
	old := auth.New(t.Name(), "original", "domain", "old-token", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", "")
	p, err := New([]*auth.Auth{old}, Config{}, "")
	if err != nil {
		t.Fatal(err)
	}
	acct := p.Accounts()[0]
	called := 0
	if !p.WithCurrentAuth(acct, old, func() { called++ }) || called != 1 {
		t.Fatal("current snapshot rejected")
	}
	replacement := auth.New(old.UserID, "replacement", "domain", "new-token", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", "")
	p.AddAccount(replacement)
	if p.WithCurrentAuth(acct, old, func() { called++ }) || called != 1 {
		t.Fatal("same-object replacement accepted stale auth")
	}
	if !p.WithCurrentAuth(acct, replacement, func() { called++ }) || called != 2 {
		t.Fatal("fresh replacement snapshot rejected")
	}
	p.Disable(acct.Name, "manual disable")
	if p.WithCurrentAuth(acct, replacement, func() { called++ }) {
		t.Fatal("publication ignored manual disable")
	}
	p.Enable(acct.Name)
	p.Cooldown(acct.Name, CoolSoft, time.Minute, "test cooldown")
	if p.WithCurrentAuth(acct, replacement, func() { called++ }) {
		t.Fatal("publication ignored active cooldown")
	}
	p.SyncToDir(nil)
	p.AddAccount(replacement)
	if p.WithCurrentAuth(acct, replacement, func() { called++ }) {
		t.Fatal("publication accepted removed account object")
	}
}

func TestWithCurrentAuthSerializesLoginReplacement(t *testing.T) {
	original := auth.New(t.Name(), "original", "domain", "old-token", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", "")
	p, err := New([]*auth.Auth{original}, Config{}, "")
	if err != nil {
		t.Fatal(err)
	}
	acct := p.Accounts()[0]
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			p.WithCurrentAuth(acct, original, func() {
				if acct.Auth != original {
					t.Error("publication observed replaced credentials while locked")
				}
			})
		}()
		go func() {
			defer wg.Done()
			p.AddAccount(auth.New(original.UserID, "replacement", "domain", "new-token", "ak", "sk", time.Now().Add(time.Hour).Format(time.RFC3339), "", ""))
		}()
	}
	wg.Wait()
	if p.WithCurrentAuth(acct, original, func() { t.Error("stale publication accepted after replacements") }) {
		t.Fatal("old identity still current")
	}
}
