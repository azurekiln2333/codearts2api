package upstream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// chatSession owns one remote root session for one inference attempt. Conversation
// and prompt-cache IDs are separate: a late idle must never release a parallel
// attempt using the same conversation. There is at most one renewal worker.
type chatSession struct {
	client   *Client
	id       string
	endpoint string
	cred     SignCredential
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	interval time.Duration
	timeout  time.Duration

	mu        sync.Mutex
	transport io.Closer
	closed    bool
}

func (c *Client) beginChatSession(ctx context.Context, inference *url.URL, cred SignCredential) (*chatSession, error) {
	if inference.Scheme == "" || inference.Host == "" {
		return nil, fmt.Errorf("invalid inference origin for session lifecycle")
	}
	id, err := RandomHex(16)
	if err != nil {
		return nil, fmt.Errorf("create upstream session identity: %w", err)
	}
	interval, timeout := c.sessionInterval, c.sessionTimeout
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if timeout <= 0 || timeout > 3*time.Second {
		timeout = 3 * time.Second
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	s := &chatSession{
		client: c, id: id,
		endpoint: (&url.URL{Scheme: inference.Scheme, Host: inference.Host, Path: EpSessionHeartbeat}).String(),
		cred:     cred, ctx: sessionCtx, cancel: cancel, done: make(chan struct{}),
		interval: interval, timeout: timeout,
	}
	// Match the official busy -> inference -> idle lifecycle deterministically.
	// Notification failure is visible, but does not replace an inference result.
	s.notify(sessionCtx, "busy")
	go s.run()
	return s, nil
}

// setTransport only publishes ownership under the lock. Closing network resources
// happens outside it, including a response that arrives after cancellation.
func (s *chatSession) setTransport(transport io.Closer) bool {
	s.mu.Lock()
	closed := s.closed
	if !closed {
		s.transport = transport
	}
	s.mu.Unlock()
	if closed {
		_ = transport.Close()
	}
	return !closed
}

func (s *chatSession) run() {
	defer close(s.done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			s.mu.Lock()
			s.closed = true
			transport := s.transport
			s.transport = nil
			s.mu.Unlock()
			if transport != nil {
				_ = transport.Close()
			}
			// Caller cancellation must not cancel remote release. The notification
			// has its own strict deadline and still uses this attempt's credentials.
			s.notify(context.WithoutCancel(s.ctx), "idle")
			return
		case <-ticker.C:
			if s.ctx.Err() == nil {
				s.notify(s.ctx, "busy")
			}
		}
	}
}

// finish cancels an in-flight renewal, joins the only worker, and waits for its
// bounded idle cleanup before the caller can release the local account lease.
func (s *chatSession) finish() {
	s.cancel()
	<-s.done
}

func (s *chatSession) notify(parent context.Context, status string) {
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()
	endpoint, _ := url.Parse(s.endpoint)
	query := endpoint.Query()
	query.Set("status", status)
	endpoint.RawQuery = query.Encode()
	raw := []byte("{}")
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), bytes.NewReader(raw))
	if err != nil {
		Log("session heartbeat status=%s outcome=request_error", status)
		return
	}
	traceID, err := RandomHex(16)
	if err != nil {
		Log("session heartbeat status=%s outcome=identity_error", status)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-client-type", "kernel")
	req.Header.Set("X-Language", "zh-cn")
	req.Header.Set("x-snap-traceid", traceID)
	req.Header.Set("user-session-id", s.id)
	signRequest(req, raw, s.cred)
	resp, err := s.client.http.Do(req)
	if err != nil {
		outcome := "transport_error"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			outcome = "timeout"
		} else if errors.Is(ctx.Err(), context.Canceled) {
			outcome = "canceled"
		}
		Log("session heartbeat status=%s outcome=%s", status, outcome)
		return
	}
	defer resp.Body.Close()
	// Bound even an unexpected response body. Never retain or log its contents.
	_, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		Log("session heartbeat status=%s outcome=http_error http=%d", status, resp.StatusCode)
	} else if readErr != nil {
		Log("session heartbeat status=%s outcome=read_error", status)
	}
}

type sessionCloseFunc func() error

func (f sessionCloseFunc) Close() error { return f() }

type sessionReadCloser struct {
	body    io.ReadCloser
	session *chatSession
}

func (b *sessionReadCloser) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if err != nil {
		b.session.finish()
	}
	return n, err
}

func (b *sessionReadCloser) Close() error {
	b.session.finish()
	return nil
}
