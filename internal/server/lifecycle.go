package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"codearts2api/internal/audit"
	"codearts2api/internal/pool"
	"codearts2api/internal/telemetry"
	"codearts2api/internal/upstream"
)

var errStartupTimeout = errors.New("chat startup timed out")

// The startup timer can stop without detaching the active HTTP stream from its caller.
// A fixed context.WithTimeout would also kill healthy long-running generations.
type chatLifetime struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	mu     sync.Mutex
	timer  *time.Timer
	ready  bool
}

func newChatLifetime(parent context.Context, timeout time.Duration) *chatLifetime {
	ctx, cancel := context.WithCancelCause(parent)
	l := &chatLifetime{ctx: ctx, cancel: cancel}
	l.timer = time.AfterFunc(timeout, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if !l.ready {
			l.cancel(errStartupTimeout)
		}
	})
	return l
}

func (l *chatLifetime) started() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := context.Cause(l.ctx); err != nil {
		return err
	}
	l.ready = true
	l.timer.Stop()
	return nil
}

func (l *chatLifetime) close() {
	l.mu.Lock()
	l.ready = true
	l.timer.Stop()
	l.mu.Unlock()
	l.cancel(context.Canceled)
}

// A single attempt owns its exact lease and body. Once closed it cannot release
// a slot acquired by another request, including after an account reload.
type chatAttempt struct {
	lease *pool.Lease
	body  io.ReadCloser
	ctx   context.Context
}

func (a *chatAttempt) close() {
	if a == nil {
		return
	}
	telemetry.SetPhase(a.ctx, telemetry.Cleanup)
	if a.body != nil {
		_ = a.body.Close()
		a.body = nil
	}
	if a.lease != nil {
		a.lease.Release()
		a.lease = nil
	}
}

// chatObserver keeps first accepted SSE timing available even when audit is
// disabled. The sink tracker is optional and never receives message content.
// Shared background refresh work does not inherit this request observer.
type chatObserver struct {
	tracker *audit.Tracker
	started time.Time
	mu      sync.Mutex
	first   time.Time
}

func (o *chatObserver) Phase(phase telemetry.Phase) { o.tracker.Phase(phase) }
func (o *chatObserver) UpstreamProgress()           { o.tracker.UpstreamProgress() }

func (o *chatObserver) FirstSSEData() {
	o.mu.Lock()
	if o.first.IsZero() {
		o.first = time.Now()
	}
	o.mu.Unlock()
	o.tracker.FirstSSEData()
}

func (o *chatObserver) firstData() time.Duration {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.first.IsZero() {
		return 0
	}
	return o.first.Sub(o.started)
}

type chatResponseWriter struct {
	http.ResponseWriter
	controller *http.ResponseController
	timeout    time.Duration
	err        error
	// status is the real status committed to the client, 0 before commitment.
	status    int
	committed bool
	errCode   string
}

func newChatResponseWriter(w http.ResponseWriter, timeout time.Duration) *chatResponseWriter {
	return &chatResponseWriter{ResponseWriter: w, controller: http.NewResponseController(w), timeout: timeout}
}

func (w *chatResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// WriteHeader records the actual committed status so a record never has to guess.
func (w *chatResponseWriter) WriteHeader(status int) {
	if w.committed {
		return
	}
	if status < http.StatusOK {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	w.committed = true
	w.ResponseWriter.WriteHeader(status)
}

// Status reports the committed status and whether anything was committed.
func (w *chatResponseWriter) Status() (int, bool) { return w.status, w.committed }

// setErrorCode records the error code actually emitted to the client, so a
// record never has to guess what the caller saw.
func (w *chatResponseWriter) setErrorCode(code string) {
	if w.errCode == "" {
		w.errCode = code
	}
}

// ErrorCode returns the emitted OpenAI error code (empty on success).
func (w *chatResponseWriter) ErrorCode() string { return w.errCode }

func (w *chatResponseWriter) deadline() error {
	err := w.controller.SetWriteDeadline(time.Now().Add(w.timeout))
	if errors.Is(err, http.ErrNotSupported) {
		return nil // In-memory recorders and some middleware do not support deadlines.
	}
	return err
}

func (w *chatResponseWriter) Write(p []byte) (int, error) {
	if w.err == nil {
		w.err = w.deadline()
	}
	if w.err != nil {
		return 0, w.err
	}
	if !w.committed {
		// Implicit 200: net/http commits only when a write is attempted.
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		w.err = err
	}
	return n, err
}

func (w *chatResponseWriter) FlushError() error {
	if w.err == nil {
		w.err = w.deadline()
	}
	if w.err == nil {
		if !w.committed {
			w.WriteHeader(http.StatusOK)
		}
		w.err = w.controller.Flush()
		if errors.Is(w.err, http.ErrNotSupported) {
			w.err = nil
		}
	}
	return w.err
}

func (w *chatResponseWriter) Flush() { _ = w.FlushError() }

func (w *chatResponseWriter) clearDeadline() { _ = w.controller.SetWriteDeadline(time.Time{}) }

func waitChatRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return context.Cause(ctx)
	}
}

func isChatTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errStartupTimeout) ||
		(errors.As(err, &ne) && ne.Timeout())
}

// chatCandidates only reads cached capability. Discovery has its own background/
// administrative lifecycle and must not silently extend a chat's startup budget.
func (h *Handler) chatCandidates(tried map[string]bool, model, sticky string) []pool.AccountCandidate {
	want := strings.ToLower(upstream.CanonicalModel(model))
	var candidates []pool.AccountCandidate
	for _, a := range h.cfg.Pool.Accounts() {
		if a == nil || tried[a.Name] || !h.cfg.Pool.Healthy(a.Name) {
			continue
		}
		state, _ := modelAvailability(a.UID, want)
		if state == availUnusable {
			continue
		}
		present, complete := upstream.AccountModelKnowledgeForAuth(a.UID, want, a.AuthSnapshot())
		if state != availUsable && complete && !present {
			continue
		}
		priority := 1
		if present {
			priority = 0
		}
		candidate := pool.AccountCandidate{Name: a.Name, Priority: priority}
		if a.Name == sticky {
			return []pool.AccountCandidate{candidate}
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}
