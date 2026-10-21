package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"codearts2api/internal/telemetry"
)

func TestLiveConnectionsCapReuseCloseAndLateObservers(t *testing.T) {
	s := testSink(t, Options{})
	trackers := make([]*Tracker, ConnectionCapacity)
	for i := range trackers {
		trackers[i] = s.Begin(fmt.Sprintf("active-%d", i), time.Now())
		if trackers[i] == nil {
			t.Fatalf("tracker rejected below cap at %d", i)
		}
	}
	if s.Begin("overflow", time.Now()) != nil {
		t.Fatal("live metadata exceeded its fixed cap")
	}
	snapshot := s.Connections()
	if snapshot.Disabled || snapshot.Capacity != ConnectionCapacity || len(snapshot.Active) != ConnectionCapacity || snapshot.Overflow != 1 {
		t.Fatalf("dishonest live capacity/loss: active=%d capacity=%d overflow=%d disabled=%t", len(snapshot.Active), snapshot.Capacity, snapshot.Overflow, snapshot.Disabled)
	}
	for _, active := range snapshot.Active {
		if active.Phase != telemetry.Admission || active.FirstSSEDataMS != nil || active.LastUpstreamProgressAgeMS != nil {
			t.Fatalf("invented pre-admission progress: %+v", active)
		}
	}
	trackers[0].Finish(Event{ID: "active-0", Outcome: "admission_rejected"})
	before := s.Snapshot(Query{Limit: 1}).Events[0]
	trackers[0].Phase(telemetry.Stream)
	trackers[0].FirstSSEData()
	trackers[0].UpstreamProgress()
	trackers[0].Finish(Event{ID: "duplicate", Outcome: "ok"})
	if after := s.Snapshot(Query{Limit: 1}).Events[0]; !reflect.DeepEqual(before, after) {
		t.Fatal("late observers or duplicate Finish changed a completed record")
	}
	if s.Begin("reused", time.Now()) == nil {
		t.Fatal("finished metadata did not free capacity")
	}
	closeSink(t, s)
	if snapshot := s.Connections(); !snapshot.Disabled || len(snapshot.Active) != 0 || s.Begin("closed", time.Now()) != nil {
		t.Fatalf("closed sink retained visible trackers: %+v", snapshot)
	}
	dropped := s.Stats().Dropped
	trackers[1].Phase(telemetry.Stream)
	trackers[1].Finish(Event{ID: "after-close", Outcome: "canceled"})
	if s.Stats().Dropped != dropped+1 {
		t.Fatal("final record after shutdown was not a counted drop")
	}
	var nilSink *Sink
	if nilSink.Begin("nil", time.Now()) != nil || !nilSink.Connections().Disabled {
		t.Fatal("nil sink allocated metadata or claimed enabled")
	}
	disabled, err := Open(Options{})
	if err != nil || disabled.Begin("disabled", time.Now()) != nil || !disabled.Connections().Disabled {
		t.Fatalf("disabled sink allocated metadata: %v", err)
	}
}

func TestConnectionTraceBoundedOwnedAndPersistedWithinEventCeiling(t *testing.T) {
	s := testSink(t, Options{MaxFileBytes: 1 << 20})
	started := time.Now().Add(-time.Second)
	tracker := s.Begin("bounded", started)
	account := strings.Repeat("<", MaxAccountLen)
	tracker.SetRequest(strings.Repeat("m", 1<<20), "canonical", true)
	tracker.SetAttempt(1, account)
	tracker.Phase(telemetry.WaitHeaders)
	for i := 0; i < MaxPhasePoints*3; i++ {
		tracker.Phase(telemetry.RetryDelay)
		tracker.Phase(telemetry.WaitHeaders)
	}
	for i := 0; i < 1000; i++ {
		tracker.UpstreamProgress()
	}
	tracker.FirstSSEData()
	tracker.Phase(telemetry.Stream)
	snapshot := s.Connections()
	active := snapshot.Active[0]
	if len(active.Timeline) != MaxPhasePoints || !active.TraceTruncated || snapshot.TraceTruncated != 1 || active.LastUpstreamProgressAgeMS == nil || active.FirstSSEDataMS == nil {
		t.Fatalf("missing bounded trace/progress diagnostics: %+v", active)
	}
	active.Timeline[0].Account = "caller-mutation"
	*active.Account = "caller-account"
	*active.FirstSSEDataMS = -1
	if fresh := s.Connections().Active[0]; fresh.Timeline[0].Account == "caller-mutation" || *fresh.Account == "caller-account" || *fresh.FirstSSEDataMS < 0 {
		t.Fatal("live snapshot aliases tracker storage")
	}
	tried := make([]string, MaxTried)
	for i := range tried {
		tried[i] = account
	}
	tracker.Finish(Event{ID: "bounded", TS: started.UTC().Format(time.RFC3339Nano), Account: &account,
		Model: strings.Repeat("<", MaxModelLen), ModelCanonical: strings.Repeat("<", MaxModelLen),
		Outcome: "ok", Tried: tried, TotalMS: time.Since(started).Milliseconds()})
	event := s.Snapshot(Query{Limit: 1}).Events[0]
	if event.Trace == nil || len(event.Trace.Timeline) > MaxPhasePoints || !event.Trace.Truncated || event.Trace.FirstSSEDataMS == nil || event.Trace.LastUpstreamProgressMS == nil {
		t.Fatalf("trace was not preserved: %+v", event.Trace)
	}
	if len(event.Trace.Timeline) >= MaxPhasePoints {
		t.Fatal("worst-case escaped event was not reduced to fit the existing ceiling")
	}
	raw, err := json.Marshal(event)
	if err != nil || len(raw)+1 > maxEventBytes {
		t.Fatalf("encoded event exceeds 16KiB: size=%d err=%v", len(raw)+1, err)
	}
	if event.Trace.Timeline[len(event.Trace.Timeline)-1].Phase != telemetry.Finished {
		t.Fatal("trimming lost the actual final boundary")
	}
	event.Trace.Timeline[0].Account = "page-mutation"
	*event.Trace.FirstSSEDataMS = 999999
	*event.Trace.LastUpstreamProgressMS = 999999
	fresh := s.Snapshot(Query{Limit: 1}).Events[0]
	if fresh.Trace.Timeline[0].Account == "page-mutation" || *fresh.Trace.FirstSSEDataMS == 999999 || *fresh.Trace.LastUpstreamProgressMS == 999999 {
		t.Fatal("completed trace snapshots alias recent storage")
	}
	waitAudit(t, func() bool { return s.Stats().Written == 1 })
	if s.Stats().WriteFailures != 0 || s.Stats().Dropped != 0 {
		t.Fatalf("bounded traced event failed persistence: %+v", s.Stats())
	}
}

func TestConnectionTraceReplayKeepsOldRecordsWithoutInventedHistory(t *testing.T) {
	opts := Options{Enabled: true, Dir: t.TempDir()}
	s := testSink(t, opts)
	s.Record(Event{ID: "legacy", Outcome: "ok"})
	start := time.Now().Add(-time.Second)
	tracker := s.Begin("traced", start)
	tracker.Phase(telemetry.WaitFirstSSE)
	tracker.UpstreamProgress()
	tracker.FirstSSEData()
	tracker.Phase(telemetry.Aggregate)
	tracker.Finish(Event{ID: "traced", Outcome: "ok", TotalMS: time.Since(start).Milliseconds()})
	waitAudit(t, func() bool { return s.Stats().Written == 2 })
	closeSink(t, s)
	restored := testSink(t, opts)
	page := restored.Connections()
	if len(page.Active) != 0 || len(page.Recent) != 2 || page.Recent[0].Trace == nil || page.Recent[1].Trace != nil {
		t.Fatalf("replay fabricated or lost lifecycle history: %+v", page)
	}
	if page.Recent[0].Trace.FirstSSEDataMS == nil || page.Recent[0].Trace.LastUpstreamProgressMS == nil {
		t.Fatal("replay lost actual first-data/progress metadata")
	}
}

func TestConnectionObserversAndSnapshotRaceWithFinish(t *testing.T) {
	s := testSink(t, Options{MaxQueue: 128, Recent: 128})
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		tracker := s.Begin(fmt.Sprintf("race-%d", i), time.Now())
		group.Add(3)
		go func() {
			defer group.Done()
			for j := 0; j < 30; j++ {
				tracker.SetAttempt(j, "account")
				tracker.SetRequest("model", "canonical", true)
				tracker.Phase(telemetry.Stream)
				tracker.UpstreamProgress()
				tracker.FirstSSEData()
			}
		}()
		go func() {
			defer group.Done()
			for j := 0; j < 10; j++ {
				_ = s.Connections()
			}
		}()
		go func() {
			defer group.Done()
			tracker.Finish(Event{ID: fmt.Sprintf("race-%d", i), Outcome: "ok", TotalMS: 1000})
		}()
	}
	group.Wait()
	if live := s.Connections(); len(live.Active) != 0 || len(live.Recent) != 32 {
		t.Fatalf("concurrent Finish lost or stranded metadata: active=%d recent=%d", len(live.Active), len(live.Recent))
	}
	s.Close(context.Background())
}
