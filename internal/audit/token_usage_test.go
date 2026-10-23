package audit

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func usageCount(value int64) *int64 { return &value }

func assertUsageCount(t *testing.T, got, want *int64) {
	t.Helper()
	if (got == nil) != (want == nil) || (got != nil && *got != *want) {
		t.Fatalf("counter got=%v want=%v", got, want)
	}
}

func TestUsageFromMapKeepsKnownZeroPartialAndDerivedTotal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		input  map[string]any
		prompt *int64
		output *int64
		total  *int64
	}{
		{"native-derived", map[string]any{"prompt_tokens": 10, "completion_tokens": float64(20)}, usageCount(10), usageCount(20), usageCount(30)},
		{"known-zero", map[string]any{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}, usageCount(0), usageCount(0), usageCount(0)},
		{"partial-native", map[string]any{"completion_tokens": 0}, nil, usageCount(0), nil},
		{"invalid-total-not-derived", map[string]any{"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": "30"}, usageCount(10), usageCount(20), nil},
		{"null-total-not-derived", map[string]any{"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": nil}, usageCount(10), usageCount(20), nil},
		{"total-only", map[string]any{"total_tokens": uint64(7)}, nil, nil, usageCount(7)},
		{"exact-json-exponent", map[string]any{"prompt_tokens": json.Number("10e-1"), "completion_tokens": json.Number("2.0")}, usageCount(1), usageCount(2), usageCount(3)},
		{"unsafe-derived-sum", map[string]any{"prompt_tokens": MaxTokenCount, "completion_tokens": int64(1)}, usageCount(MaxTokenCount), usageCount(1), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := UsageFromMap(tc.input, UsageSourceUpstream)
			if usage == nil || usage.Source != UsageSourceUpstream {
				t.Fatalf("valid native evidence discarded: %+v", usage)
			}
			assertUsageCount(t, usage.PromptTokens, tc.prompt)
			assertUsageCount(t, usage.CompletionTokens, tc.output)
			assertUsageCount(t, usage.TotalTokens, tc.total)
		})
	}
	estimated := UsageFromMap(map[string]any{"total_tokens": 3, "arbitrary-provider-field": "fixture-secret"}, UsageSourceEstimated)
	raw, err := json.Marshal(estimated)
	if err != nil || estimated.Source != UsageSourceEstimated || strings.Contains(string(raw), "fixture-secret") {
		t.Fatalf("estimate source or metadata privacy lost: %s error=%v", raw, err)
	}
	if UsageFromMap(map[string]any{"total_tokens": 3}, "fixture-secret") != nil || UsageFromMap(nil, UsageSourceUpstream) != nil {
		t.Fatal("unknown source or absent values manufactured usage")
	}
}

func TestUsageNormalizationRejectsInvalidAndUnsafeCounters(t *testing.T) {
	for _, value := range []any{
		nil, "1", true, map[string]any{"secret": "fixture-secret"}, []int{1},
		-1, float64(-1), math.NaN(), math.Inf(1), math.Inf(-1), 1.5,
		MaxTokenCount + 1, uint64(math.MaxUint64), float64(MaxTokenCount + 1),
		json.Number("9007199254740990.5"), json.Number("1e999999999999"),
		json.Number("1e-99999999999"), json.Number("123/1"), json.Number("1.5"),
	} {
		if got := UsageFromMap(map[string]any{"total_tokens": value}, UsageSourceUpstream); got != nil {
			t.Errorf("invalid %T counter became known: %+v", value, got)
		}
	}
	for _, value := range []any{int8(1), int16(1), int32(1), int64(1), uint(1), uint8(1), uint16(1), uint32(1), uint64(1), float32(1), float64(1), json.Number("1")} {
		got := UsageFromMap(map[string]any{"total_tokens": value}, UsageSourceUpstream)
		if got == nil || got.TotalTokens == nil || *got.TotalTokens != 1 {
			t.Errorf("valid %T counter was lost: %+v", value, got)
		}
	}
}

func TestUsageRecordAndCompletedConnectionsOwnCounters(t *testing.T) {
	s := testSink(t, Options{Recent: 8})
	prompt, output, total := int64(4), int64(7), int64(11)
	usage := &TokenUsage{PromptTokens: &prompt, CompletionTokens: &output, TotalTokens: &total, Source: UsageSourceUpstream}
	s.Record(Event{ID: "usage-owned", Outcome: "ok", Usage: usage})
	prompt, output, total, usage.Source = 999, 999, 999, "fixture-secret"
	page := s.Snapshot(Query{})
	if len(page.Events) != 1 || page.Events[0].Usage == nil {
		t.Fatal("successful usage was not recorded")
	}
	*page.Events[0].Usage.PromptTokens = 888
	page.Events[0].Usage.Source = "page-mutation"
	connections := s.Connections()
	*connections.Recent[0].Usage.TotalTokens = 888
	fresh := s.Snapshot(Query{})
	if *fresh.Events[0].Usage.PromptTokens != 4 || *fresh.Events[0].Usage.TotalTokens != 11 || fresh.Events[0].Usage.Source != UsageSourceUpstream || fresh.Usage.TotalTokens != 11 {
		t.Fatalf("caller mutated published usage: %+v", fresh)
	}
	waitAudit(t, func() bool { return s.Stats().Written == 1 })
	closeSink(t, s)
	restored := testSink(t, Options{Dir: s.opts.Dir, Recent: 8})
	ev := restored.Snapshot(Query{}).Events[0]
	if ev.Usage == nil || *ev.Usage.CompletionTokens != 7 || *ev.Usage.TotalTokens != 11 || ev.Usage.Source != UsageSourceUpstream {
		t.Fatalf("persisted usage lost its original owned values: %+v", ev)
	}
	for _, file := range restored.listFiles() {
		raw, err := os.ReadFile(file.path)
		if err != nil || strings.Contains(string(raw), "fixture-secret") || strings.Contains(string(raw), "page-mutation") {
			t.Fatalf("mutable caller metadata reached disk: error=%v", err)
		}
	}
}

func TestUsageReplayKeepsLegacyAndMalformedOptionalMetadata(t *testing.T) {
	dir := t.TempDir()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	usageJSON := []string{
		``,
		`,"usage":{"prompt_tokens":"bad","completion_tokens":2.5,"total_tokens":-1,"source":"upstream"}`,
		`,"usage":{"prompt_tokens":9,"completion_tokens":"bad","source":"upstream"}`,
		`,"usage":["fixture-secret"]`,
		`,"usage":{"total_tokens":10,"source":"fixture-secret"}`,
		`,"usage":{"prompt_tokens":2,"completion_tokens":4,"source":"upstream"}`,
		`,"usage":{"total_tokens":9007199254740992,"source":"upstream"}`,
	}
	var data strings.Builder
	for i, usage := range usageJSON {
		fmt.Fprintf(&data, `{"v":1,"seq":%d,"ts":%q,"id":"replay-%d","outcome":"ok"%s}`+"\n", i+1, ts, i+1, usage)
	}
	if err := os.WriteFile(filepath.Join(dir, "audit-00000001.jsonl"), []byte(data.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	s := testSink(t, Options{Dir: dir})
	page := s.Snapshot(Query{})
	if len(page.Events) != len(usageJSON) || page.Usage.Requests != 7 || page.Usage.WithUsage != 2 || page.Usage.UnknownRequests != 5 {
		t.Fatalf("invalid optional usage discarded valid records or fabricated counters: %+v", page)
	}
	byID := make(map[string]Event)
	for _, event := range page.Events {
		byID[event.ID] = event
	}
	partial := byID["replay-3"].Usage
	if partial == nil || partial.PromptTokens == nil || *partial.PromptTokens != 9 || partial.CompletionTokens != nil || partial.TotalTokens != nil {
		t.Fatalf("partial native evidence did not survive replay: %+v", partial)
	}
	if got := byID["replay-6"].Usage; got == nil || got.TotalTokens == nil || *got.TotalTokens != 6 {
		t.Fatalf("missing total was not derived safely during replay: %+v", got)
	}
	for _, id := range []string{"replay-1", "replay-2", "replay-4", "replay-5", "replay-7"} {
		if byID[id].Usage != nil {
			t.Errorf("unknown usage was made known for %s: %+v", id, byID[id].Usage)
		}
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "fixture-secret") {
		t.Fatal("provider-controlled optional source/usage escaped replay sanitization")
	}
}

func TestUsageSummarySharesFiltersRetentionAndIgnoresPageCursor(t *testing.T) {
	s := testSink(t, Options{Recent: 6})
	now := time.Now().UTC()
	for i := 0; i < 8; i++ {
		account := "account-a"
		ev := Event{ID: fmt.Sprintf("summary-%d", i), TS: now.Add(time.Duration(i-8) * time.Minute).Format(time.RFC3339Nano),
			Outcome: "ok", Committed: true, Status: 200, Model: "Alias-A", ModelCanonical: "wire-a", Account: &account,
			Usage: UsageFromMap(map[string]any{"total_tokens": 1000}, UsageSourceUpstream)}
		switch i {
		case 2:
			ev.TS = now.Add(-8 * 24 * time.Hour).Format(time.RFC3339Nano)
		case 3:
			ev.Usage = UsageFromMap(map[string]any{"total_tokens": 10}, UsageSourceUpstream)
		case 4:
			ev.Usage = UsageFromMap(map[string]any{"total_tokens": 20}, UsageSourceEstimated)
		case 5:
			account, ev.Model, ev.ModelCanonical = "account-b", "wire-b", "wire-b"
			ev.Usage = UsageFromMap(map[string]any{"total_tokens": 3}, UsageSourceUpstream)
		case 6:
			ev.Outcome = "upstream_stream_error" // Even committed 200 is not success.
		case 7:
			ev.Usage = nil // A legacy record is unknown, not zero.
		}
		s.Record(ev)
	}
	all := s.Snapshot(Query{Limit: 1})
	if all.Usage.Requests != 5 || all.Usage.TotalTokens != 33 || all.Usage.WithUsage != 3 || all.Usage.UnknownRequests != 2 || all.Usage.UpstreamRequests != 2 || all.Usage.EstimatedRequests != 1 || all.Usage.TotalKnown != 3 || all.Usage.PromptKnown != 0 || all.Usage.Window != 5 {
		t.Fatalf("summary included expired/evicted data or fabricated usage: %+v", all.Usage)
	}
	query := Query{Limit: 1, Model: "WIRE-A", Account: "account-a", Outcome: "ok", Status: 200, Since: now.Add(-5 * time.Minute)}
	first := s.Snapshot(query)
	if len(first.Events) != 1 || first.Events[0].ID != "summary-7" || first.Usage.Requests != 3 || first.Usage.TotalTokens != 30 || first.Usage.UnknownRequests != 1 || !first.HasMore {
		t.Fatalf("filters or exclusive page metadata differ from usage scope: %+v", first)
	}
	query.Before = first.NextBefore
	second := s.Snapshot(query)
	if len(second.Events) != 1 || second.Events[0].ID != "summary-4" || !reflect.DeepEqual(first.Usage, second.Usage) {
		t.Fatalf("pagination changed/doubled filtered totals: first=%+v second=%+v", first, second)
	}
	query.Before = 1
	older := s.Snapshot(query)
	if len(older.Events) != 0 || !reflect.DeepEqual(first.Usage, older.Usage) {
		t.Fatalf("empty older page erased full filtered totals: %+v", older)
	}
	query.Before, query.Since = 0, now.Add(-3*time.Minute)
	if since := s.Snapshot(query); since.Usage.Requests != 1 || since.Usage.WithUsage != 0 || since.Usage.UnknownRequests != 1 {
		t.Fatalf("since filter differs between row and summary: %+v", since)
	}
	failed := s.Snapshot(Query{Outcome: "upstream_stream_error"})
	if len(failed.Events) != 1 || failed.Events[0].Usage != nil || failed.Usage.UnknownRequests != 1 || failed.Usage.WithUsage != 0 {
		t.Fatalf("failed generation retained success-like usage: %+v", failed)
	}
	if len(all.Usage.ByAccount) != 2 || all.Usage.ByAccount[0].Key != "account-a" || all.Usage.ByAccount[0].TotalTokens != 30 || len(all.Usage.ByModel) != 2 || all.Usage.ByModel[0].Key != "wire-a" {
		t.Fatalf("account/canonical model grouping changed: %+v", all.Usage)
	}
	if first.Usage.From != now.Add(-5*time.Minute).Format(time.RFC3339Nano) || first.Usage.To != now.Add(-time.Minute).Format(time.RFC3339Nano) {
		t.Fatalf("summary range must describe actual matched request starts: %+v", first.Usage)
	}
}

func TestUsageSummaryWindowCountsRetainedEventsBeforeFilters(t *testing.T) {
	s := testSink(t, Options{Recent: 512})
	now := time.Now().UTC()
	for i := 0; i < 4; i++ {
		account := "account-a"
		ts := now.Add(time.Duration(i-4) * time.Minute)
		if i == 0 {
			ts = now.Add(-8 * 24 * time.Hour)
		}
		if i == 3 {
			account = "account-b"
		}
		s.Record(Event{ID: fmt.Sprintf("window-%d", i), TS: ts.Format(time.RFC3339Nano),
			Outcome: "ok", Account: &account, Model: "wire-model"})
	}
	page := s.Snapshot(Query{Limit: 1, Account: "account-b", Model: "WIRE-MODEL", Since: now.Add(-2 * time.Minute)})
	if page.Window != 512 || page.Usage.Window != 3 || page.Usage.Requests != 1 || len(page.Events) != 1 {
		t.Fatalf("capacity, retained count and filtered count were conflated: %+v", page)
	}
	before := s.Snapshot(Query{Limit: 1, Before: 1, Account: "account-b"})
	if len(before.Events) != 0 || before.Usage.Window != 3 || before.Usage.Requests != 1 {
		t.Fatalf("pagination altered retained window or filtered totals: %+v", before)
	}
	empty := s.Snapshot(Query{Account: "missing-account"})
	if empty.Usage.Window != 3 || empty.Usage.Requests != 0 {
		t.Fatalf("empty filter erased independently retained window: %+v", empty.Usage)
	}
}

func TestUsageSummaryBoundsGroupingAndSafeSums(t *testing.T) {
	const records = 106
	s := testSink(t, Options{Recent: records})
	for i := records - 1; i >= 0; i-- {
		account := fmt.Sprintf("account-%03d", i)
		count := int64(1)
		if i < 2 {
			count = MaxTokenCount
		}
		s.Record(Event{Outcome: "ok", Account: &account, Model: fmt.Sprintf("model-%03d", i),
			Usage: UsageFromMap(map[string]any{"prompt_tokens": count, "completion_tokens": 0, "total_tokens": count}, UsageSourceUpstream)})
	}
	summary := s.Snapshot(Query{Limit: 1}).Usage
	if summary.Requests != records || summary.TotalKnown != records || summary.TotalTokens != MaxTokenCount || summary.PromptTokens != MaxTokenCount || !summary.Overflowed || summary.CompletionTokens != 0 || summary.CompletionKnown != records {
		t.Fatalf("unsafe numeric totals or incomplete overall match count: %+v", summary)
	}
	if len(summary.ByAccount) != MaxUsageGroups || len(summary.ByModel) != MaxUsageGroups || summary.AccountsTruncated != 6 || summary.ModelsTruncated != 6 || summary.ByAccount[0].Key != "account-000" || summary.ByAccount[99].Key != "account-099" {
		t.Fatalf("group cap/order/truncation is not deterministic: %+v", summary)
	}
	if summary.ByAccount[0].Overflowed || summary.ByAccount[0].TotalTokens != MaxTokenCount || summary.ByAccount[0].CompletionKnown != 1 {
		t.Fatal("a safe boundary value or known zero was incorrectly called overflow")
	}
	raw, err := json.Marshal(summary)
	if err != nil || len(raw) > 100000 {
		t.Fatalf("bounded summary could not be serialized safely: bytes=%d error=%v", len(raw), err)
	}
	if got := s.Snapshot(Query{Limit: 200}).Usage; !reflect.DeepEqual(summary, got) {
		t.Fatal("changing page size altered bounded grouped totals")
	}
}

func TestUsageNilDisabledAndConcurrentSnapshotsStayHonest(t *testing.T) {
	var nilSink *Sink
	disabled, err := Open(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, sink := range []*Sink{nilSink, disabled} {
		page := sink.Snapshot(Query{})
		if !page.Disabled || !page.Usage.Disabled || page.Usage.Scope != "recent_window" || page.Usage.ByAccount == nil || page.Usage.ByModel == nil || page.Usage.Requests != 0 {
			t.Fatalf("nil/disabled summary is dishonest: %+v", page)
		}
	}
	s := testSink(t, Options{Recent: 128, MaxQueue: 256})
	var wg sync.WaitGroup
	for writer := 0; writer < 8; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for i := 0; i < 16; i++ {
				count := int64(3)
				s.Record(Event{ID: fmt.Sprintf("usage-concurrent-%d-%d", writer, i), Outcome: "ok",
					Usage: &TokenUsage{TotalTokens: &count, Source: UsageSourceUpstream}})
				count = 999 // Record owns its copy before returning.
			}
		}(writer)
	}
	for reader := 0; reader < 3; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				page := s.Snapshot(Query{Limit: 1})
				if page.Usage.TotalTokens != int64(page.Usage.Requests)*3 || page.Usage.Requests > 128 || page.Usage.UnknownRequests != 0 {
					t.Errorf("concurrent page and summary do not use one immutable window: %+v", page.Usage)
				}
				if len(page.Events) != 0 {
					*page.Events[0].Usage.TotalTokens = 888
				}
				connections := s.Connections()
				if len(connections.Recent) != 0 {
					*connections.Recent[0].Usage.TotalTokens = 777
				}
			}
		}()
	}
	wg.Wait()
	final := s.Snapshot(Query{})
	if final.Usage.Requests != 128 || final.Usage.TotalTokens != 384 {
		t.Fatalf("concurrent writers/readers corrupted usage counts: %+v", final.Usage)
	}
	if len(final.Usage.ByAccount) != 1 || final.Usage.ByAccount[0].Key != "" || len(final.Usage.ByModel) != 1 || final.Usage.ByModel[0].Key != "" {
		t.Fatal("missing identity acquired an invented grouping label")
	}
}
