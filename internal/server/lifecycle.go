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

	"codearts2api/internal/pool"
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
}

func (a *chatAttempt) close() {
	if a == nil {
		return
	}
	if a.body != nil {
		_ = a.body.Close()
		a.body = nil
	}
	if a.lease != nil {
		a.lease.Release()
		a.lease = nil
	}
}

type chatResponseWriter struct {
	http.ResponseWriter
	controller *http.ResponseController
	timeout    time.Duration
	err        error
}

func newChatResponseWriter(w http.ResponseWriter, timeout time.Duration) *chatResponseWriter {
	return &chatResponseWriter{ResponseWriter: w, controller: http.NewResponseController(w), timeout: timeout}
}

func (w *chatResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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
		catalog, known := upstream.AccountModels(a.UID)
		if state != availUsable && known && !catalogHas(catalog, want) {
			continue
		}
		priority := 1
		if known {
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
