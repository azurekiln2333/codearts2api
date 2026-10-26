package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testSink(t *testing.T, opts Options) *Sink {
	t.Helper()
	if opts.Dir == "" {
		opts.Dir = t.TempDir()
	}
	opts.Enabled = true
	s, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeSink(t, s) })
	return s
}

func closeSink(t *testing.T, s *Sink) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s.Close(ctx)
	select {
	case <-s.done:
	case <-ctx.Done():
		t.Fatal("audit writer did not finish within shutdown budget")
	}
}

func waitAudit(t *testing.T, f func() bool) {
	t.Helper()
	until := time.NewTimer(3 * time.Second)
	defer until.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !f() {
		select {
		case <-until.C:
			t.Fatal("audit condition did not converge")
		case <-tick.C:
		}
	}
}

func TestConcurrentRecordsHaveStableOrderAndOwnMetadata(t *testing.T) {
	const count = 128
	s := testSink(t, Options{MaxQueue: count, Recent: count})
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			account := "account"
			first := int64(12)
			tried := []string{"account"}
			s.Record(Event{ID: fmt.Sprintf("record-%d", i), Outcome: "ok", Account: &account, Tried: tried, FirstDataMS: &first})
			account, tried[0], first = "mutated", "mutated", 999
		}(i)
	}
	wg.Wait()
	page := s.Snapshot(Query{Limit: count})
	if len(page.Events) != count {
		t.Fatalf("recent record count=%d want=%d", len(page.Events), count)
	}
	for i, ev := range page.Events {
		if ev.Seq != uint64(count-i) || ev.Account == nil || *ev.Account != "account" || ev.Tried[0] != "account" || ev.FirstDataMS == nil || *ev.FirstDataMS != 12 {
			t.Fatalf("ordering or event ownership failed at %d: %+v", i, ev)
		}
	}
	*page.Events[0].Account = "page mutation"
	page.Events[0].Tried[0] = "page mutation"
	*page.Events[0].FirstDataMS = 123
	copy := s.Snapshot(Query{Limit: 1}).Events[0]
	if *copy.Account != "account" || copy.Tried[0] != "account" || *copy.FirstDataMS != 12 {
		t.Fatalf("caller mutated stored audit metadata: %+v", copy)
	}
	waitAudit(t, func() bool { return s.Stats().Written == count })
	if s.Stats().Dropped != 0 || s.Stats().WriteFailures != 0 {
		t.Fatalf("unexpected loss: %+v", s.Stats())
	}
}

func TestRestartRestoresRecentWindowSequenceAndCursor(t *testing.T) {
	dir := t.TempDir()
	opts := Options{Enabled: true, Dir: dir, MaxQueue: 1024, Recent: 512, MaxFileBytes: 64 << 10}
	s := testSink(t, opts)
	account := "account-a"
	for i := 0; i < 600; i++ {
		s.Record(Event{ID: fmt.Sprintf("restore-%d", i), Outcome: "ok", Account: &account, Model: "MODEL", Cause: Cause{Class: CauseCapability, Code: "InferHub.4004.200", Text: "benefit not found"}})
	}
	waitAudit(t, func() bool { return s.Stats().Written == 600 })
	closeSink(t, s)
	restored := testSink(t, opts)
	var all []Event
	cursor := uint64(0)
	for {
		page := restored.Snapshot(Query{Limit: 200, Before: cursor, Account: account, Model: "model", Outcome: "ok"})
		all = append(all, page.Events...)
		cursor = page.NextBefore
		if !page.HasMore {
			break
		}
	}
	if len(all) != 512 || all[0].Seq != 600 || all[len(all)-1].Seq != 89 {
		t.Fatalf("restored window/cursor is incomplete: count=%d first=%d last=%d", len(all), all[0].Seq, all[len(all)-1].Seq)
	}
	for i, ev := range all {
		if ev.Seq != uint64(600-i) || ev.Cause.Text != "benefit not claimed or expired" {
			t.Fatalf("replayed order/cause changed: %+v", ev)
		}
	}
	// New records do not shift the next page under a sequence cursor. The
	// bounded window may evict its oldest entry as new traffic arrives.
	restored.Record(Event{ID: "new-between-pages", Outcome: "ok", Model: "other"})
	next := restored.Snapshot(Query{Limit: 200, Before: all[199].Seq}).Events
	if len(next) != 200 || next[0].Seq != 400 || next[199].Seq != 201 {
		t.Fatalf("new traffic shifted a stable cursor: count=%d", len(next))
	}
	newest := restored.Snapshot(Query{Limit: 1}).Events[0]
	if newest.Seq <= 600 {
		t.Fatalf("restart reused an audit sequence: %d", newest.Seq)
	}
}

func TestRotationEnforcesFileCountAndByteCapsBeforeWriting(t *testing.T) {
	s := testSink(t, Options{MaxFileBytes: 1100, MaxTotalBytes: 1700, MaxFiles: 3})
	for i := 0; i < 20; i++ {
		s.Record(Event{ID: fmt.Sprintf("rotate-%d", i), Outcome: "ok", Committed: true, Status: 200})
		want := uint64(i + 1)
		waitAudit(t, func() bool { return s.Stats().Written == want })
		files := s.listFiles()
		var total int64
		for _, file := range files {
			total += file.size
			if file.size > 1100 {
				t.Fatalf("file exceeded its limit before rotation: %d", file.size)
			}
		}
		if len(files) > 3 || total > 1700 {
			t.Fatalf("archive exceeded total limits: files=%d bytes=%d", len(files), total)
		}
	}
	if s.Stats().Rotations == 0 || s.Stats().WriteFailures != 0 {
		t.Fatalf("rotation did not work: %+v", s.Stats())
	}
}

func TestExpiredArchiveIsPrunedAndNotRestored(t *testing.T) {
	dir := t.TempDir()
	for i, age := range []time.Duration{8 * 24 * time.Hour, 0} {
		ev := Event{V: SchemaVersion, Seq: uint64(10 + i), TS: time.Now().Add(-age).UTC().Format(time.RFC3339Nano), ID: "aged", Outcome: "ok"}
		raw, _ := json.Marshal(ev)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("audit-%08d.jsonl", i+1)), append(raw, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := testSink(t, Options{Dir: dir, RetentionDays: 7, FlushInterval: 100 * time.Millisecond})
	page := s.Snapshot(Query{})
	if len(page.Events) != 1 || page.Events[0].Seq != 11 {
		t.Fatalf("expired events returned after restart: %+v", page)
	}
	if _, err := os.Stat(filepath.Join(dir, "audit-00000001.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expired archive remains on disk: %v", err)
	}
	// An old archive discovered during normal operation must also be removed
	// while traffic is idle, rather than waiting for the next size rotation.
	oldPath := filepath.Join(dir, "audit-00000000.jsonl")
	if err := os.WriteFile(oldPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(oldPath, old, old); err != nil {
		t.Fatal(err)
	}
	waitAudit(t, func() bool { _, err := os.Stat(oldPath); return os.IsNotExist(err) })
}

func TestOverflowStorageFailureAndClosedSinkRemainNonBlocking(t *testing.T) {
	// A stopped consumer isolates the full-queue contract deterministically.
	// Match Open's normalized defaults: zero retention would expire the ring
	// immediately and make Snapshot depend on the host clock's precision.
	s := &Sink{opts: (Options{Enabled: true, Recent: 2, MaxQueue: 1}).normalized(), ch: make(chan Event, 1)}
	before := time.Now()
	// Keep timestamps distinct from the query time even on coarse clocks.
	ts := before.Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	for i := 0; i < 10000; i++ {
		s.Record(Event{TS: ts, Outcome: "ok"})
	}
	if elapsed := time.Since(before); elapsed > time.Second {
		t.Fatalf("full queue blocked: elapsed=%s", elapsed)
	}
	if stats := s.Stats(); stats.Dropped != 9999 || stats.Queued != 1 {
		t.Fatalf("full queue counters incorrect: %+v", stats)
	}
	if page := s.Snapshot(Query{}); len(page.Events) != 2 {
		t.Fatalf("full queue recent window incorrect: retained=%d want=2 stats=%+v", len(page.Events), s.Stats())
	}
	blocked := filepath.Join(t.TempDir(), "ordinary-file")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := testSink(t, Options{Dir: blocked})
	broken.Record(Event{ID: "storage-failed", Outcome: "ok"})
	waitAudit(t, func() bool { return broken.Stats().Queued == 0 })
	if broken.Stats().WriteFailures == 0 || len(broken.Snapshot(Query{}).Events) != 1 {
		t.Fatalf("storage loss was hidden: %+v", broken.Stats())
	}
	closeSink(t, broken)
	broken.Record(Event{Outcome: "ok"})
	if broken.Stats().Dropped != 1 || broken.Stats().Queued != 0 {
		t.Fatalf("post-close record was silently stranded: %+v", broken.Stats())
	}
}

func TestFieldBoundsAndProviderTextAllowlist(t *testing.T) {
	s := testSink(t, Options{})
	account := strings.Repeat("账", 10000)
	tried := make([]string, 10000)
	for i := range tried {
		tried[i] = account
	}
	s.Record(Event{ID: strings.Repeat("i", 1<<20), TS: strings.Repeat("t", 1<<20), Model: strings.Repeat("m", 1<<20), Account: &account, Tried: tried,
		Outcome: "ok", Cause: Cause{Class: "untrusted", Code: strings.Repeat("c", 10000), Text: "raw error contains fixture-secret and fixture-prompt"}})
	waitAudit(t, func() bool { return s.Stats().Written == 1 })
	ev := s.Snapshot(Query{}).Events[0]
	if len(ev.ID) > MaxAccountLen || len(ev.Model) > MaxModelLen || len(*ev.Account) > MaxAccountLen || len(ev.Tried) > MaxTried || len(ev.Cause.Code) > MaxCodeLen || len(ev.TS) > 40 {
		t.Fatalf("event fields are unbounded: %+v", ev)
	}
	for _, file := range s.listFiles() {
		raw, err := os.ReadFile(file.path)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > maxEventBytes || strings.Contains(string(raw), "fixture-secret") || strings.Contains(string(raw), "fixture-prompt") {
			t.Fatalf("raw provider text or an oversized event reached disk: bytes=%d", len(raw))
		}
	}
	if ev.Cause.Class != CauseUnknown || ev.Cause.Text != "unclassified upstream failure" {
		t.Fatalf("unsafe provider cause was accepted: %+v", ev.Cause)
	}
}

func TestNilSinkAndTinyStorageLimits(t *testing.T) {
	var absent *Sink
	absent.Record(Event{})
	absent.Close(context.Background())
	if page := absent.Snapshot(Query{}); !page.Disabled || len(page.Events) != 0 {
		t.Fatalf("nil sink is not safely disabled: %+v", page)
	}
	s := testSink(t, Options{MaxFileBytes: 1, MaxTotalBytes: 1, MaxFiles: 1})
	s.Record(Event{Outcome: "ok"})
	waitAudit(t, func() bool { return s.Stats().WriteFailures > 0 })
	if s.Stats().WriteFailures == 0 || s.Stats().Written != 0 {
		t.Fatalf("an oversized record bypassed the configured cap: %+v", s.Stats())
	}
}
