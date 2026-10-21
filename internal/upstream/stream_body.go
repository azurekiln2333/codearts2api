package upstream

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// IdleTimeoutError identifies a request/header/body wait that made no progress.
// It is also a deadline error so callers can return a gateway timeout.
type IdleTimeoutError struct {
	TimeoutDuration time.Duration
}

func (e *IdleTimeoutError) Error() string {
	return fmt.Sprintf("codearts upstream made no progress for %s", e.TimeoutDuration)
}

func (e *IdleTimeoutError) Timeout() bool   { return true }
func (e *IdleTimeoutError) Temporary() bool { return true }
func (e *IdleTimeoutError) Unwrap() error   { return context.DeadlineExceeded }

// streamWait owns one timer for the HTTP request and all its body reads. The
// timer runs only while waiting for I/O; downstream work has its own write
// deadline. Canceling the request interrupts net/http directly, without a
// goroutine per Read. A mutex/deadline check makes stale timer callbacks safe
// when Stop races with Reset on Go 1.22.
type streamWait struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	limit  time.Duration
	mu     sync.Mutex
	timer  *time.Timer
	until  time.Time
	active bool
	closed bool
}

func newStreamWait(parent context.Context, limit time.Duration) *streamWait {
	if limit <= 0 {
		limit = 60 * time.Second
	}
	ctx, cancel := context.WithCancelCause(parent)
	w := &streamWait{ctx: ctx, cancel: cancel, limit: limit, until: time.Now().Add(limit), active: true}
	w.timer = time.AfterFunc(limit, w.expire)
	return w
}

func (w *streamWait) expire() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || !w.active {
		return
	}
	if remaining := time.Until(w.until); remaining > 0 {
		w.timer.Reset(remaining)
		return
	}
	w.active = false
	w.cancel(&IdleTimeoutError{TimeoutDuration: w.limit})
}

func (w *streamWait) begin() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := context.Cause(w.ctx); err != nil {
		return err
	}
	w.until = time.Now().Add(w.limit)
	w.active = true
	w.timer.Reset(w.limit)
	return nil
}

func (w *streamWait) end() {
	w.mu.Lock()
	w.active = false
	w.timer.Stop()
	w.mu.Unlock()
}

func (w *streamWait) result(err error) error {
	if err != nil {
		if cause := context.Cause(w.ctx); cause != nil {
			return cause
		}
	}
	return err
}

func (w *streamWait) close() {
	w.mu.Lock()
	w.closed = true
	w.active = false
	w.timer.Stop()
	w.cancel(context.Canceled)
	w.mu.Unlock()
}

type timeoutReadCloser struct {
	body      io.ReadCloser
	wait      *streamWait
	closeOnce sync.Once
	closeErr  error
}

func (b *timeoutReadCloser) Read(p []byte) (int, error) {
	if err := b.wait.begin(); err != nil {
		_ = b.Close()
		return 0, err
	}
	n, err := b.body.Read(p)
	b.wait.end()
	err = b.wait.result(err)
	if err != nil {
		_ = b.Close()
	}
	return n, err
}

func (b *timeoutReadCloser) Close() error {
	b.closeOnce.Do(func() {
		b.wait.close()
		b.closeErr = b.body.Close()
	})
	return b.closeErr
}
