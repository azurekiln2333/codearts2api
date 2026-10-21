package audit

import (
	"encoding/json"
	"sort"
	"sync"
	"time"

	"codearts2api/internal/telemetry"
)

const (
	ConnectionCapacity = 512
	MaxPhasePoints     = 32
)

// PhasePoint uses an offset from the request start; it contains no provider
// payload. Account is the selected account at that point, if one is known.
type PhasePoint struct {
	Phase   telemetry.Phase `json:"phase"`
	AtMS    int64           `json:"at_ms"`
	Attempt int             `json:"attempt"`
	Account string          `json:"account,omitempty"`
}

// Trace is additive to the original completed audit schema. An absent trace on
// a replayed record means that lifecycle detail was not recorded at the time.
type Trace struct {
	Timeline               []PhasePoint `json:"timeline"`
	FirstSSEDataMS         *int64       `json:"first_sse_data_ms"`
	LastUpstreamProgressMS *int64       `json:"last_upstream_progress_ms"`
	Truncated              bool         `json:"truncated"`
}

type ActiveConnection struct {
	ID                        string          `json:"id"`
	TS                        string          `json:"ts"`
	Model                     string          `json:"model"`
	ModelCanonical            string          `json:"model_canonical"`
	Stream                    bool            `json:"stream"`
	Account                   *string         `json:"account"`
	Attempt                   int             `json:"attempt"`
	Phase                     telemetry.Phase `json:"phase"`
	PhaseAgeMS                int64           `json:"phase_age_ms"`
	TotalMS                   int64           `json:"total_ms"`
	LastUpstreamProgressAgeMS *int64          `json:"last_upstream_progress_age_ms"`
	FirstSSEDataMS            *int64          `json:"first_sse_data_ms"`
	Timeline                  []PhasePoint    `json:"timeline"`
	TraceTruncated            bool            `json:"trace_truncated"`
}

type Connections struct {
	Active         []ActiveConnection `json:"active"`
	Recent         []Event            `json:"recent"`
	Disabled       bool               `json:"disabled"`
	Capacity       int                `json:"capacity"`
	Overflow       uint64             `json:"overflow"`
	TraceTruncated uint64             `json:"trace_truncated"`
}

// Tracker is a request-local observer owned by the sink. Its mutation cost is
// bounded, never performs I/O and does not take pool/account/stream locks.
type Tracker struct {
	sink    *Sink
	started time.Time
	id      string
	ts      string
	mu      sync.Mutex

	model     string
	canonical string
	stream    bool
	account   string
	attempt   int
	phase     telemetry.Phase
	phaseAt   time.Time
	timeline  []PhasePoint
	firstSSE  time.Time
	progress  time.Time
	truncated bool
	finished  bool
	closed    bool
}

// Begin allocates only while enabled and below the fixed active cap. An omitted
// live tracker does not prevent the caller from recording its final summary.
func (s *Sink) Begin(id string, started time.Time) *Tracker {
	if !s.Enabled() {
		return nil
	}
	s.recordMu.Lock()
	defer s.recordMu.Unlock()
	if s.closed {
		return nil
	}
	if len(s.active) >= ConnectionCapacity {
		s.connectionOverflow.Add(1)
		return nil
	}
	if s.active == nil {
		s.active = make(map[*Tracker]struct{})
	}
	now := time.Now()
	if started.IsZero() || started.After(now) {
		started = now
	}
	t := &Tracker{
		sink: s, started: started, id: sanitizeToken(clip(id, MaxAccountLen)),
		ts:       started.UTC().Format(time.RFC3339Nano),
		timeline: make([]PhasePoint, 0, MaxPhasePoints),
	}
	t.phaseLocked(telemetry.Admission, now)
	s.active[t] = struct{}{}
	return t
}

func (t *Tracker) SetRequest(model, canonical string, stream bool) {
	if t == nil {
		return
	}
	model = sanitizeText(clip(model, MaxModelLen))
	canonical = sanitizeText(clip(canonical, MaxModelLen))
	t.mu.Lock()
	if !t.finished && !t.closed {
		t.model, t.canonical, t.stream = model, canonical, stream
	}
	t.mu.Unlock()
}

func (t *Tracker) SetAttempt(attempt int, account string) {
	if t == nil {
		return
	}
	if attempt < 0 {
		attempt = 0
	}
	account = sanitizeText(clip(account, MaxAccountLen))
	t.mu.Lock()
	if !t.finished && !t.closed {
		t.attempt, t.account = attempt, account
	}
	t.mu.Unlock()
}

func (t *Tracker) Phase(phase telemetry.Phase) {
	if t == nil || !phase.Valid() {
		return
	}
	t.mu.Lock()
	if !t.finished && !t.closed {
		t.phaseLocked(phase, time.Now())
	}
	t.mu.Unlock()
}

func (t *Tracker) phaseLocked(phase telemetry.Phase, now time.Time) {
	point := PhasePoint{Phase: phase, AtMS: elapsedMS(t.started, now), Attempt: t.attempt, Account: t.account}
	if n := len(t.timeline); n != 0 {
		previous := t.timeline[n-1]
		if previous.Phase == phase && previous.Attempt == point.Attempt && previous.Account == point.Account {
			return
		}
	}
	t.phase, t.phaseAt = phase, now
	if len(t.timeline) < MaxPhasePoints {
		t.timeline = append(t.timeline, point)
		return
	}
	// Preserve the initial boundary and the current boundary without growing a
	// per-chunk/request log. A visible counter records the lost middle history.
	t.timeline[MaxPhasePoints-1] = point
	if !t.truncated {
		t.truncated = true
		t.sink.traceTruncated.Add(1)
	}
}

func (t *Tracker) UpstreamProgress() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if !t.finished && !t.closed {
		t.progress = time.Now()
	}
	t.mu.Unlock()
}

func (t *Tracker) FirstSSEData() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if !t.finished && !t.closed && t.firstSSE.IsZero() {
		t.firstSSE = time.Now()
	}
	t.mu.Unlock()
}

// Finish must run after upstream body, account lease and admission cleanup. The
// caller supplies the existing final status/outcome/cause classification.
func (t *Tracker) Finish(ev Event) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return
	}
	if !t.closed {
		t.phaseLocked(telemetry.Finished, time.Now())
	}
	t.finished = true
	ev.Trace = &Trace{
		Timeline:               append([]PhasePoint(nil), t.timeline...),
		FirstSSEDataMS:         offsetMS(t.started, t.firstSSE),
		LastUpstreamProgressMS: offsetMS(t.started, t.progress),
		Truncated:              t.truncated,
	}
	t.mu.Unlock()

	wasTruncated := ev.Trace.Truncated
	ev = t.sink.prepare(ev)
	if !wasTruncated && ev.Trace.Truncated {
		t.sink.traceTruncated.Add(1)
	}
	t.sink.recordMu.Lock()
	delete(t.sink.active, t)
	t.sink.recordLocked(ev)
	t.sink.recordMu.Unlock()
}

func (t *Tracker) snapshot(now time.Time) (ActiveConnection, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished || t.closed {
		return ActiveConnection{}, false
	}
	active := ActiveConnection{
		ID: t.id, TS: t.ts, Model: t.model, ModelCanonical: t.canonical,
		Stream: t.stream, Attempt: t.attempt, Phase: t.phase,
		PhaseAgeMS: elapsedMS(t.phaseAt, now), TotalMS: elapsedMS(t.started, now),
		FirstSSEDataMS: offsetMS(t.started, t.firstSSE),
		Timeline:       append([]PhasePoint(nil), t.timeline...), TraceTruncated: t.truncated,
	}
	if t.account != "" {
		account := t.account
		active.Account = &account
	}
	if !t.progress.IsZero() {
		age := elapsedMS(t.progress, now)
		active.LastUpstreamProgressAgeMS = &age
	}
	return active, true
}

// Connections reads bounded in-memory snapshots only. Completed records reuse
// the same recent window, sanitizer, retention and final classifier as audit.
func (s *Sink) Connections() Connections {
	out := Connections{Active: []ActiveConnection{}, Recent: []Event{}, Disabled: true, Capacity: ConnectionCapacity}
	if s == nil {
		return out
	}
	s.recordMu.Lock()
	out.Disabled = !s.Enabled() || s.closed
	trackers := make([]*Tracker, 0, len(s.active))
	if !out.Disabled {
		for t := range s.active {
			trackers = append(trackers, t)
		}
	}
	s.recordMu.Unlock()
	if !out.Disabled {
		now := time.Now()
		// Start times/IDs are immutable; sort those rather than RFC3339 strings
		// whose variable-width fractional seconds do not sort chronologically.
		sort.Slice(trackers, func(i, j int) bool {
			if trackers[i].started.Equal(trackers[j].started) {
				return trackers[i].id < trackers[j].id
			}
			return trackers[i].started.Before(trackers[j].started)
		})
		for _, t := range trackers {
			if active, ok := t.snapshot(now); ok {
				out.Active = append(out.Active, active)
			}
		}
		out.Recent = s.Snapshot(Query{Limit: 50}).Events
	}
	out.Overflow = s.connectionOverflow.Load()
	out.TraceTruncated = s.traceTruncated.Load()
	return out
}

// closeTrackersLocked detaches active metadata at shutdown without inventing
// request outcomes. Each real final Record after shutdown remains a counted drop.
func (s *Sink) closeTrackersLocked() {
	for t := range s.active {
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		delete(s.active, t)
	}
}

func elapsedMS(from, to time.Time) int64 {
	ms := to.Sub(from).Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}

func offsetMS(start, at time.Time) *int64 {
	if at.IsZero() {
		return nil
	}
	ms := elapsedMS(start, at)
	return &ms
}

func cloneTrace(trace *Trace) *Trace {
	if trace == nil {
		return nil
	}
	copy := *trace
	copy.Timeline = append([]PhasePoint(nil), trace.Timeline...)
	if trace.FirstSSEDataMS != nil {
		ms := *trace.FirstSSEDataMS
		copy.FirstSSEDataMS = &ms
	}
	if trace.LastUpstreamProgressMS != nil {
		ms := *trace.LastUpstreamProgressMS
		copy.LastUpstreamProgressMS = &ms
	}
	return &copy
}

func sanitizeTrace(trace *Trace, totalMS int64) *Trace {
	if trace == nil {
		return nil
	}
	out := &Trace{Timeline: []PhasePoint{}, Truncated: trace.Truncated}
	points := trace.Timeline
	if len(points) > MaxPhasePoints {
		points = points[:MaxPhasePoints]
		out.Truncated = true
	}
	var last int64
	for _, point := range points {
		if !point.Phase.Valid() {
			out.Truncated = true
			continue
		}
		if point.AtMS < last {
			point.AtMS = last
		}
		if point.AtMS > totalMS {
			point.AtMS = totalMS
		}
		if point.Attempt < 0 {
			point.Attempt = 0
		}
		point.Account = sanitizeText(clip(point.Account, MaxAccountLen))
		out.Timeline = append(out.Timeline, point)
		last = point.AtMS
	}
	copyOffset := func(p *int64) *int64 {
		if p == nil || *p < 0 || *p > totalMS {
			return nil
		}
		n := *p
		return &n
	}
	out.FirstSSEDataMS = copyOffset(trace.FirstSSEDataMS)
	out.LastUpstreamProgressMS = copyOffset(trace.LastUpstreamProgressMS)
	return out
}

// Bound the encoded event before recent insertion/enqueue as well as on disk.
// HTML-escaped account names can expand sixfold, so field counts alone cannot
// guarantee the existing 16-KiB limit. All inputs here were already bounded.
func fitTrace(ev Event) Event {
	if ev.Trace == nil {
		return ev
	}
	for len(ev.Trace.Timeline) > 1 {
		// Sequence is assigned after prepare. Reserve its largest encoding so
		// an event exactly at the ceiling cannot exceed it on publication.
		sized := ev
		sized.Seq = ^uint64(0)
		raw, err := json.Marshal(sized)
		if err == nil && len(raw)+1 <= maxEventBytes {
			break
		}
		points := ev.Trace.Timeline
		keep := (len(points) - 1) / 2
		trimmed := make([]PhasePoint, 0, keep+1)
		trimmed = append(trimmed, points[:keep]...)
		trimmed = append(trimmed, points[len(points)-1])
		ev.Trace.Timeline = trimmed
		ev.Trace.Truncated = true
	}
	return ev
}
