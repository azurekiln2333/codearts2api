// Package audit records bounded, metadata-only summaries of chat requests.
//
// The sink is deliberately dumb and bounded: enqueue never blocks and never
// touches the filesystem, a single goroutine owns all audit I/O, and the recent
// window is a fixed-size ring restored from the retained files on startup.
//
// Nothing here may ever hold request content, prompts, completions, tool
// arguments, headers, keys or credentials. Cause text is chosen from a fixed
// table of safe descriptions; provider codes are reduced to a token alphabet.
package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Event schema version. Bump only for incompatible field changes.
const SchemaVersion = 1

// Cause classes. Fixed vocabulary so operators can filter reliably.
const (
	CauseNone             = "none"
	CauseCapability       = "capability"
	CauseAuth             = "auth"
	CauseParamInvalid     = "param_invalid"
	CauseRateLimit        = "rate_limit"
	CauseConcurrencyLimit = "concurrency_limit"
	CauseUpstream5xx      = "upstream_5xx"
	CauseTimeout          = "timeout"
	CauseCancel           = "cancel"
	CauseDownstream       = "downstream"
	CauseSaturation       = "saturation"
	CauseQueueWait        = "queue_wait"
	CauseUnknown          = "unknown"
)

// Field bounds. Every string that reaches a record passes through these.
const (
	MaxModelLen   = 96
	MaxAccountLen = 128
	MaxTried      = 8
	MaxCodeLen    = 64
	MaxTextLen    = 64
	MaxOutcomeLen = 40
	MaxErrorCode  = 48
)

// Cause is a classified, sanitised provider failure.
type Cause struct {
	Class string `json:"class"`
	Code  string `json:"code,omitempty"`
	Text  string `json:"text,omitempty"`
}

// Event is one chat request summary. All fields are copied values: no pointer
// ever escapes into a record.
type Event struct {
	V   int    `json:"v"`
	Seq uint64 `json:"seq"`
	ID  string `json:"id"`
	TS  string `json:"ts"`

	Model          string `json:"model,omitempty"`
	ModelCanonical string `json:"model_canonical,omitempty"`
	Stream         bool   `json:"stream"`

	// Committed reports whether a response status was actually written to the
	// client. When false, Status is absent rather than a fabricated 200.
	Committed bool   `json:"committed"`
	Status    int    `json:"status,omitempty"`
	Outcome   string `json:"outcome"`
	ErrorCode string `json:"error_code,omitempty"`

	Account  *string  `json:"account"`
	Attempts int      `json:"attempts"`
	Tried    []string `json:"tried,omitempty"`

	WaitMS      int64  `json:"wait_ms"`
	QueueMS     int64  `json:"queue_ms"`
	FirstDataMS *int64 `json:"first_data_ms"`
	TotalMS     int64  `json:"total_ms"`

	RetryAfterS int  `json:"retry_after_s,omitempty"`
	Canceled    bool `json:"canceled"`

	Cause Cause `json:"cause"`
}

// Options configure the sink. Zero values mean "use the default".
type Options struct {
	Enabled       bool
	Dir           string
	MaxQueue      int
	Recent        int
	MaxFileBytes  int64
	MaxFiles      int
	MaxTotalBytes int64
	RetentionDays int
	FlushInterval time.Duration
}

// Defaults are the reviewed operational values.
const (
	DefaultMaxQueue      = 256
	DefaultRecent        = 512
	DefaultMaxFileBytes  = 8 << 20
	DefaultMaxFiles      = 8
	DefaultMaxTotalBytes = 64 << 20
	DefaultRetentionDays = 7
	DefaultFlushInterval = time.Second
)

// Hard clamps so a bad config can never become unbounded.
const (
	clampMaxQueue      = 4096
	clampRecent        = 8192
	clampMaxFileBytes  = 64 << 20
	clampMaxFiles      = 64
	clampMaxTotalBytes = 1 << 30
	clampRetentionDays = 90
	clampFlushInterval = 10 * time.Second
	minFlushInterval   = 100 * time.Millisecond
	maxEventBytes      = 16 << 10
	restoreIOBudget    = 64 << 20
)

func (o Options) normalized() Options {
	if o.MaxQueue <= 0 {
		o.MaxQueue = DefaultMaxQueue
	}
	if o.MaxQueue > clampMaxQueue {
		o.MaxQueue = clampMaxQueue
	}
	if o.Recent <= 0 {
		o.Recent = DefaultRecent
	}
	if o.Recent > clampRecent {
		o.Recent = clampRecent
	}
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = DefaultMaxFileBytes
	}
	if o.MaxFileBytes > clampMaxFileBytes {
		o.MaxFileBytes = clampMaxFileBytes
	}
	if o.MaxFiles <= 0 {
		o.MaxFiles = DefaultMaxFiles
	}
	if o.MaxFiles > clampMaxFiles {
		o.MaxFiles = clampMaxFiles
	}
	if o.MaxTotalBytes <= 0 {
		o.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if o.MaxTotalBytes > clampMaxTotalBytes {
		o.MaxTotalBytes = clampMaxTotalBytes
	}
	// The total cap must never exceed what the file caps can hold, otherwise the
	// documented "64 MiB including the active file" would be a lie.
	if max := o.MaxFileBytes * int64(o.MaxFiles); o.MaxTotalBytes > max {
		o.MaxTotalBytes = max
	}
	if o.MaxFileBytes > o.MaxTotalBytes {
		o.MaxFileBytes = o.MaxTotalBytes
	}
	if o.RetentionDays <= 0 {
		o.RetentionDays = DefaultRetentionDays
	}
	if o.RetentionDays > clampRetentionDays {
		o.RetentionDays = clampRetentionDays
	}
	if o.FlushInterval <= 0 {
		o.FlushInterval = DefaultFlushInterval
	}
	if o.FlushInterval < minFlushInterval {
		o.FlushInterval = minFlushInterval
	}
	if o.FlushInterval > clampFlushInterval {
		o.FlushInterval = clampFlushInterval
	}
	return o
}

// Stats are the visible loss/volume counters.
type Stats struct {
	Enabled       bool   `json:"enabled"`
	Dir           string `json:"dir"`
	Queued        int    `json:"queued"`
	Written       uint64 `json:"written"`
	Dropped       uint64 `json:"dropped"`
	WriteFailures uint64 `json:"write_failures"`
	Rotations     uint64 `json:"rotations"`
	Recent        int    `json:"recent"`
	MaxQueue      int    `json:"max_queue"`
	MaxFiles      int    `json:"max_files"`
	MaxFileBytes  int64  `json:"max_file_bytes"`
	MaxTotalBytes int64  `json:"max_total_bytes"`
	RetentionDays int    `json:"retention_days"`
}

// Query filters the recent window.
type Query struct {
	Limit   int
	Before  uint64 // return events with Seq < Before; 0 means newest
	Outcome string
	Account string
	Model   string
	Status  int
	Since   time.Time
}

// Page is a stable slice of the recent window.
type Page struct {
	Events      []Event `json:"events"`
	NextBefore  uint64  `json:"next_before"`
	HasMore     bool    `json:"has_more"`
	Dropped     uint64  `json:"dropped"`
	WriteFailed uint64  `json:"write_failures"`
	Window      int     `json:"window"`
	Disabled    bool    `json:"disabled"`
}

// Sink is a bounded, non-blocking audit recorder.
type Sink struct {
	opts Options

	ch      chan Event
	closing chan struct{}
	done    chan struct{}

	ringMu sync.RWMutex
	ring   []Event
	// Serialize sequence allocation, recent insertion and enqueue together. No
	// filesystem operation holds this lock; concurrent paging sees that order.
	recordMu sync.Mutex
	closed   bool

	seq atomic.Uint64

	queued        atomic.Int64
	written       atomic.Uint64
	dropped       atomic.Uint64
	writeFailures atomic.Uint64
	rotations     atomic.Uint64

	// Writer-owned state; never touched by Record.
	file        *os.File
	fileSeq     uint64
	fileBytes   int64
	fileStarted time.Time

	closeOnce sync.Once
}

// Open creates the sink, restores the recent window from retained files and
// starts the single writer goroutine.
func Open(opts Options) (*Sink, error) {
	o := opts.normalized()
	s := &Sink{
		opts:    o,
		closing: make(chan struct{}),
		done:    make(chan struct{}),
		ring:    make([]Event, 0, o.Recent),
	}
	if !o.Enabled {
		close(s.done)
		return s, nil
	}
	if o.Dir == "" {
		return nil, errors.New("audit: dir is required when enabled")
	}
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		s.writeFailures.Add(1)
	}
	s.restore()
	if err := s.openActive(); err != nil {
		// A broken directory must not stop the gateway from serving chat.
		s.writeFailures.Add(1)
	}
	s.ch = make(chan Event, o.MaxQueue)
	go s.loop()
	return s, nil
}

// Enabled reports whether records are being persisted.
func (s *Sink) Enabled() bool { return s != nil && s.ch != nil }

// Options returns the effective (normalized) options.
func (s *Sink) Options() Options {
	if s == nil {
		return Options{}.normalized()
	}
	return s.opts
}

// Record enqueues one event. It never blocks and never performs I/O; a full
// queue increments the visible drop counter instead of delaying a chat.
func (s *Sink) Record(ev Event) {
	if s == nil || s.ch == nil {
		return
	}
	ev = s.prepare(ev)
	s.recordMu.Lock()
	defer s.recordMu.Unlock()
	if s.closed {
		s.dropped.Add(1)
		return
	}
	ev.Seq = s.seq.Add(1)
	s.appendRing(ev)
	s.queued.Add(1)
	select {
	case s.ch <- ev:
	default:
		s.queued.Add(-1)
		s.dropped.Add(1)
	}
}

// prepare assigns the sequence/time and enforces every field bound.
func (s *Sink) prepare(ev Event) Event {
	ev.V = SchemaVersion
	ts, err := time.Parse(time.RFC3339Nano, clip(ev.TS, 40))
	if err != nil {
		ts = time.Now()
	}
	ev.TS = ts.UTC().Format(time.RFC3339Nano)
	ev.ID = sanitizeToken(clip(ev.ID, MaxAccountLen))
	ev.Model = sanitizeText(clip(ev.Model, MaxModelLen))
	ev.ModelCanonical = sanitizeText(clip(ev.ModelCanonical, MaxModelLen))
	ev.Outcome = sanitizeToken(clip(ev.Outcome, MaxOutcomeLen))
	ev.ErrorCode = sanitizeToken(clip(ev.ErrorCode, MaxErrorCode))
	ev.Cause.Class = normalizeClass(ev.Cause.Class)
	ev.Cause.Code = sanitizeToken(clip(ev.Cause.Code, MaxCodeLen))
	ev.Cause.Text = SafeCauseText(clip(ev.Cause.Text, MaxTextLen))
	if ev.Account != nil {
		name := sanitizeText(clip(*ev.Account, MaxAccountLen))
		if name == "" {
			ev.Account = nil
		} else {
			ev.Account = &name
		}
	}
	if len(ev.Tried) > MaxTried {
		ev.Tried = ev.Tried[:MaxTried]
	}
	ev.Tried = append([]string(nil), ev.Tried...)
	for i, t := range ev.Tried {
		ev.Tried[i] = sanitizeText(clip(t, MaxAccountLen))
	}
	if !ev.Committed {
		ev.Status = 0
	}
	if ev.WaitMS < 0 {
		ev.WaitMS = 0
	}
	if ev.QueueMS < 0 {
		ev.QueueMS = 0
	}
	if ev.TotalMS < 0 {
		ev.TotalMS = 0
	}
	if ev.FirstDataMS != nil {
		first := *ev.FirstDataMS
		if first < 0 {
			ev.FirstDataMS = nil
		} else {
			ev.FirstDataMS = &first
		}
	}
	if ev.Attempts < 0 {
		ev.Attempts = 0
	}
	return ev
}

func (s *Sink) appendRing(ev Event) {
	s.ringMu.Lock()
	if len(s.ring) >= s.opts.Recent {
		copy(s.ring, s.ring[1:])
		s.ring[len(s.ring)-1] = ev
	} else {
		s.ring = append(s.ring, ev)
	}
	s.ringMu.Unlock()
}

// Snapshot returns a stable page of the recent window, newest first.
func (s *Sink) Snapshot(q Query) Page {
	page := Page{Disabled: !s.Enabled(), Events: []Event{}}
	if s == nil {
		return page
	}
	page.Window = s.opts.Recent
	page.Dropped = s.dropped.Load()
	page.WriteFailed = s.writeFailures.Load()
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	s.ringMu.RLock()
	events := make([]Event, len(s.ring))
	copy(events, s.ring)
	s.ringMu.RUnlock()

	matched := make([]Event, 0, len(events))
	cutoff := s.cutoff()
	for i := len(events) - 1; i >= 0; i-- { // newest first
		ev := events[i]
		if ts, err := time.Parse(time.RFC3339Nano, ev.TS); err != nil || ts.Before(cutoff) {
			continue
		}
		if q.Before != 0 && ev.Seq >= q.Before {
			continue
		}
		if q.Outcome != "" && ev.Outcome != q.Outcome {
			continue
		}
		if q.Status != 0 && ev.Status != q.Status {
			continue
		}
		if q.Account != "" && (ev.Account == nil || *ev.Account != q.Account) {
			continue
		}
		if q.Model != "" && !strings.EqualFold(ev.Model, q.Model) && !strings.EqualFold(ev.ModelCanonical, q.Model) {
			continue
		}
		if !q.Since.IsZero() {
			if ts, err := time.Parse(time.RFC3339Nano, ev.TS); err == nil && ts.Before(q.Since) {
				continue
			}
		}
		// The returned page owns its slices/pointers, just like queued events.
		ev.Tried = append([]string(nil), ev.Tried...)
		if ev.Account != nil {
			account := *ev.Account
			ev.Account = &account
		}
		if ev.FirstDataMS != nil {
			first := *ev.FirstDataMS
			ev.FirstDataMS = &first
		}
		matched = append(matched, ev)
	}
	if len(matched) > q.Limit {
		matched = matched[:q.Limit]
		page.HasMore = true
	}
	page.Events = matched
	if n := len(matched); n > 0 {
		page.NextBefore = matched[n-1].Seq
	}
	return page
}

// Stats returns the visible counters and effective limits.
func (s *Sink) Stats() Stats {
	if s == nil {
		return Stats{}
	}
	st := Stats{
		Enabled:       s.Enabled(),
		Dir:           s.opts.Dir,
		Queued:        int(s.queued.Load()),
		Written:       s.written.Load(),
		Dropped:       s.dropped.Load(),
		WriteFailures: s.writeFailures.Load(),
		Rotations:     s.rotations.Load(),
		Recent:        s.opts.Recent,
		MaxQueue:      s.opts.MaxQueue,
		MaxFiles:      s.opts.MaxFiles,
		MaxFileBytes:  s.opts.MaxFileBytes,
		MaxTotalBytes: s.opts.MaxTotalBytes,
		RetentionDays: s.opts.RetentionDays,
	}
	return st
}

// Close drains the queue for a bounded time, flushes and stops the writer.
func (s *Sink) Close(ctx context.Context) {
	if s == nil || s.ch == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.recordMu.Lock()
		s.closed = true
		close(s.closing)
		s.recordMu.Unlock()
		select {
		case <-s.done:
		case <-ctx.Done():
		}
	})
}

// ---------------------------------------------------------------- writer side

func (s *Sink) loop() {
	ticker := time.NewTicker(s.opts.FlushInterval)
	defer ticker.Stop()
	defer close(s.done)
	defer func() {
		for {
			select {
			case <-s.ch:
				s.queued.Add(-1)
				s.dropped.Add(1)
			default:
				return
			}
		}
	}()
	for {
		select {
		case ev := <-s.ch:
			s.append(ev)
		case <-ticker.C:
			s.flush()
			s.prune(0, 0)
		case <-s.closing:
			deadline := time.Now().Add(2 * s.opts.FlushInterval)
			for time.Now().Before(deadline) {
				select {
				case ev := <-s.ch:
					s.append(ev)
				default:
					s.flush()
					s.closeFile()
					return
				}
			}
			s.flush()
			s.closeFile()
			return
		}
	}
}

func (s *Sink) append(ev Event) {
	s.queued.Add(-1)
	raw, err := json.Marshal(ev)
	if err != nil {
		s.writeFailures.Add(1)
		return
	}
	raw = append(raw, '\n')
	if len(raw) > maxEventBytes || int64(len(raw)) > s.opts.MaxFileBytes {
		s.writeFailures.Add(1)
		return
	}
	if s.file == nil {
		if err := s.openActive(); err != nil {
			s.writeFailures.Add(1)
			return
		}
	}
	if s.fileBytes+int64(len(raw)) > s.opts.MaxFileBytes || s.fileStarted.Before(s.cutoff()) {
		s.rotate()
		if s.file == nil {
			return
		}
	}
	if !s.prune(int64(len(raw)), 0) {
		return // Do not exceed the cap if removing an archive failed.
	}
	n, err := s.file.Write(raw)
	s.fileBytes += int64(n)
	if err != nil || n != len(raw) {
		s.writeFailures.Add(1)
		// Keep the record in memory only; never retry synchronously.
		return
	}
	s.written.Add(1)
}

func (s *Sink) flush() {
	if s.file == nil {
		return
	}
	if s.fileStarted.Before(s.cutoff()) {
		s.rotate()
		return
	}
	if err := s.file.Sync(); err != nil {
		s.writeFailures.Add(1)
	}
}

func (s *Sink) closeFile() {
	if s.file == nil {
		return
	}
	_ = s.file.Close()
	s.file = nil
}

func (s *Sink) path(seq uint64) string {
	return filepath.Join(s.opts.Dir, fmt.Sprintf("audit-%08d.jsonl", seq))
}

func (s *Sink) openActive() error {
	if err := os.MkdirAll(s.opts.Dir, 0o700); err != nil {
		return err
	}
	files := s.listFiles()
	if len(files) > 0 {
		newest := files[len(files)-1]
		if info, err := os.Stat(newest.path); err == nil && info.Size() < s.opts.MaxFileBytes && !newest.mod.Before(s.cutoff()) {
			f, err := os.OpenFile(newest.path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err == nil {
				s.file, s.fileSeq, s.fileBytes = f, newest.seq, info.Size()
				s.fileStarted = newest.mod
				s.prune(0, 0)
				return nil
			}
		}
		s.fileSeq = newest.seq
	}
	s.fileSeq++
	if !s.prune(0, 1) {
		return errors.New("audit: cannot enforce retention limits")
	}
	f, err := os.OpenFile(s.path(s.fileSeq), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	s.file, s.fileBytes = f, 0
	s.fileStarted = time.Now()
	return nil
}

func (s *Sink) rotate() {
	s.closeFile()
	s.rotations.Add(1)
	s.fileSeq++
	if !s.prune(0, 1) {
		return
	}
	f, err := os.OpenFile(s.path(s.fileSeq), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		s.writeFailures.Add(1)
		return
	}
	s.file, s.fileBytes = f, 0
	s.fileStarted = time.Now()
}

type auditFile struct {
	seq  uint64
	path string
	size int64
	mod  time.Time
}

func (s *Sink) listFiles() []auditFile {
	entries, err := os.ReadDir(s.opts.Dir)
	if err != nil {
		s.writeFailures.Add(1)
		return nil
	}
	out := make([]auditFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "audit-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		digits := strings.TrimSuffix(strings.TrimPrefix(name, "audit-"), ".jsonl")
		seq, err := strconv.ParseUint(digits, 10, 64)
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			s.writeFailures.Add(1)
			continue
		}
		path := filepath.Join(s.opts.Dir, name)
		oldest := info.ModTime()
		if first := firstTimestamp(path); !first.IsZero() && first.Before(oldest) {
			oldest = first
		}
		out = append(out, auditFile{seq: seq, path: path, size: info.Size(), mod: oldest})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// prune removes the oldest files beyond the count, total-byte and age caps. The
// active file always counts toward the total, matching the documented budget.
func (s *Sink) cutoff() time.Time {
	return time.Now().Add(-time.Duration(s.opts.RetentionDays) * 24 * time.Hour)
}

// reserve is the next write; extraFiles reserves a file before creating it.
// Limits hold before a write, including the active file, rather than only once
// a file has already exceeded its cap.
func (s *Sink) prune(reserve int64, extraFiles int) bool {
	files := s.listFiles()
	if len(files) == 0 {
		return reserve <= s.opts.MaxTotalBytes
	}
	cutoff := s.cutoff()
	count := len(files) + extraFiles
	total := int64(0)
	for _, f := range files {
		total += f.size
	}
	for _, f := range files {
		tooMany := count > s.opts.MaxFiles
		tooBig := total+reserve > s.opts.MaxTotalBytes
		tooOld := f.mod.Before(cutoff)
		if !tooMany && !tooBig && !tooOld && f.size <= s.opts.MaxFileBytes {
			continue
		}
		if s.file != nil && f.seq == s.fileSeq {
			continue // never delete the file we are writing to
		}
		if err := os.Remove(f.path); err != nil {
			s.writeFailures.Add(1)
			return false
		}
		total -= f.size
		count--
	}
	return count <= s.opts.MaxFiles && total+reserve <= s.opts.MaxTotalBytes
}

// restore rebuilds the recent window from retained files, newest first, with a
// bounded I/O budget, then continues the sequence so paging stays monotonic.
func (s *Sink) restore() {
	files := s.listFiles()
	if len(files) == 0 {
		return
	}
	want := s.opts.Recent
	if want <= 0 {
		want = DefaultRecent
	}
	var collected []Event
	read := int64(0)
	for i := len(files) - 1; i >= 0 && len(collected) < want && read < restoreIOBudget; i-- {
		evs, n := readTail(files[i].path, want-len(collected), restoreIOBudget-read)
		read += n
		if len(evs) == 0 {
			continue
		}
		collected = append(evs, collected...)
	}
	if len(collected) == 0 {
		return
	}
	var maxSeq uint64
	for _, ev := range collected {
		if ev.Seq > maxSeq {
			maxSeq = ev.Seq
		}
	}
	s.seq.Store(maxSeq)
	sort.SliceStable(collected, func(i, j int) bool { return collected[i].Seq < collected[j].Seq })
	kept := collected[:0]
	for _, ev := range collected {
		if ts, err := time.Parse(time.RFC3339Nano, ev.TS); err == nil && !ts.Before(s.cutoff()) {
			kept = append(kept, s.prepare(ev))
		}
	}
	collected = kept
	start := 0
	if len(collected) > s.opts.Recent {
		start = len(collected) - s.opts.Recent
	}
	s.ring = append(s.ring[:0], collected[start:]...)
}

// readTail parses up to limit events from the end of one file.
func readTail(path string, limit int, budget int64) ([]Event, int64) {
	if limit <= 0 || budget <= 0 {
		return nil, 0
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0
	}
	read := int64(limit+1) * maxEventBytes
	if read > budget {
		read = budget
	}
	if read > info.Size() {
		read = info.Size()
	}
	start := info.Size() - read
	reader := bufio.NewReader(io.NewSectionReader(f, start, read))
	if start > 0 {
		_, _ = reader.ReadString('\n') // A tail seek can start inside a record.
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, maxEventBytes), maxEventBytes)
	out := make([]Event, 0, limit)
	for scanner.Scan() {
		var ev Event
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			continue
		}
		if ev.V != SchemaVersion || ev.Seq == 0 || ev.Outcome == "" {
			continue
		}
		if len(out) == limit {
			copy(out, out[1:])
			out = out[:limit-1]
		}
		out = append(out, ev)
	}
	return out, read
}

func firstTimestamp(path string) time.Time {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	var first struct {
		TS string `json:"ts"`
	}
	if json.NewDecoder(io.LimitReader(f, maxEventBytes)).Decode(&first) != nil {
		return time.Time{}
	}
	ts, _ := time.Parse(time.RFC3339Nano, first.TS)
	return ts
}

// ---------------------------------------------------------------- sanitisers

// sanitizeText keeps printable runes only and collapses whitespace.
func sanitizeText(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			if !space && b.Len() > 0 {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// sanitizeToken keeps a provider-code-like token: letters, digits and . _ - only.
func sanitizeToken(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// clip truncates to at most n bytes without splitting a rune.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// safeCauseTexts is the complete allowlist of provider descriptions that may be
// stored. Anything else is dropped: raw provider errors never reach a record.
var safeCauseTexts = map[string]string{
	"model not registered": "model not registered on account",
	"benefit not found":    "benefit not claimed or expired",
	"rate limited":         "upstream rate limited",
	"concurrency limit":    "upstream concurrent session limit",
	"server error":         "upstream server error",
	"authentication":       "upstream rejected credentials",
	"param invalid":        "upstream rejected request params",
	"invalid request":      "invalid client request",
	"timeout":              "upstream idle or header timeout",
	"canceled":             "client canceled",
	"downstream":           "downstream write failed",
	"no account":           "no eligible account available",
	"capacity":             "account capacity busy",
	"unknown":              "unclassified upstream failure",
}

// SafeCauseText maps a caller-supplied key to the fixed description. Unknown
// input collapses to the unclassified description.
func SafeCauseText(key string) string {
	if key == "" {
		return ""
	}
	if text, ok := safeCauseTexts[key]; ok {
		return text
	}
	for _, text := range safeCauseTexts {
		if key == text {
			return text // Already normalized records remain stable on restart.
		}
	}
	return safeCauseTexts["unknown"]
}

func normalizeClass(class string) string {
	switch class {
	case CauseNone, CauseCapability, CauseAuth, CauseParamInvalid, CauseRateLimit,
		CauseConcurrencyLimit, CauseUpstream5xx, CauseTimeout, CauseCancel,
		CauseDownstream, CauseSaturation, CauseQueueWait, CauseUnknown:
		return class
	case "":
		return CauseNone
	default:
		return CauseUnknown
	}
}
