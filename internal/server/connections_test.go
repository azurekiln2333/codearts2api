package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codearts2api/internal/audit"
	"codearts2api/internal/pool"
	"codearts2api/internal/telemetry"
	"codearts2api/internal/upstream"
)

func connectionSnapshot(t *testing.T, h *Handler) audit.Connections {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/connections", nil)
	if h.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.cfg.APIKey)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("connections route returned %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("live connection metadata should not be cached")
	}
	var snapshot audit.Connections
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func waitConnectionPhase(t *testing.T, h *Handler, phase telemetry.Phase) audit.ActiveConnection {
	t.Helper()
	var found audit.ActiveConnection
	waitLifecycle(t, func() bool {
		for _, active := range connectionSnapshot(t, h).Active {
			if active.Phase == phase {
				found = active
				return true
			}
		}
		return false
	}, "live request never exposed phase "+string(phase))
	return found
}

func traceHasPhase(event audit.Event, phase telemetry.Phase) bool {
	if event.Trace == nil {
		return false
	}
	for _, point := range event.Trace.Timeline {
		if point.Phase == phase {
			return true
		}
	}
	return false
}

func assertFinishedConnection(t *testing.T, h *Handler, p *pool.Pool, id string) audit.Event {
	t.Helper()
	event := waitForRecord(t, h.cfg.Audit, id)
	if event.Trace == nil || len(event.Trace.Timeline) == 0 || event.Trace.Timeline[len(event.Trace.Timeline)-1].Phase != telemetry.Finished || !traceHasPhase(event, telemetry.Cleanup) {
		t.Fatalf("request did not record cleanup/finished: %+v", event.Trace)
	}
	for _, active := range connectionSnapshot(t, h).Active {
		if active.ID == id {
			t.Fatal("completed request remained active")
		}
	}
	assertLifecycleHealthy(t, p)
	return event
}

func TestConnectionsRouteRequiresAuthAndReportsDisabledTruthfully(t *testing.T) {
	h, _ := lifecycleHandler(t, 0, "http://127.0.0.1:1")
	h.cfg.APIKey = "fixture-connections-key"
	for _, key := range []string{"", "wrong"} {
		req := httptest.NewRequest(http.MethodGet, "/admin/api/connections", nil)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "active") {
			t.Fatalf("unauthorized connection metadata disclosure: %d %s", rec.Code, rec.Body.String())
		}
	}
	if snapshot := connectionSnapshot(t, h); !snapshot.Disabled || snapshot.Active == nil || snapshot.Recent == nil || snapshot.Capacity != audit.ConnectionCapacity {
		t.Fatalf("nil audit sink claimed live tracking: %+v", snapshot)
	}
	disabled, err := audit.Open(audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h.cfg.Audit = disabled
	if snapshot := connectionSnapshot(t, h); !snapshot.Disabled || len(snapshot.Active) != 0 {
		t.Fatalf("disabled audit sink claimed live tracking: %+v", snapshot)
	}
}

func TestConnectionsExposeStalledHeadersAndFirstSSEBeforeCompletion(t *testing.T) {
	for _, phase := range []telemetry.Phase{telemetry.WaitHeaders, telemetry.WaitFirstSSE} {
		t.Run(string(phase), func(t *testing.T) {
			const providerSecret = "fixture-connection-upstream-secret"
			const prompt = "fixture-connection-prompt"
			const key = "fixture-connection-key"
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if phase == telemetry.WaitFirstSSE {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, ": "+providerSecret+"\n\n")
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer backend.Close()
			sink := newAuditSink(t)
			h, p := auditHandler(t, backend.URL, 1, sink, func(c *Config) {
				c.APIKey = key
				c.StartupTimeout = 300 * time.Millisecond
			})
			body := `{"model":"glm-5.2","messages":[{"role":"user","content":"` + prompt + `"}],"stream":false}`
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+key)
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				done <- rec
			}()
			active := waitConnectionPhase(t, h, phase)
			if active.Account == nil || active.Attempt != 1 || active.Model != "glm-5.2" || active.FirstSSEDataMS != nil {
				t.Fatalf("inaccurate running request metadata: %+v", active)
			}
			if phase == telemetry.WaitHeaders && active.LastUpstreamProgressAgeMS != nil {
				t.Fatal("headers wait invented body progress")
			}
			if phase == telemetry.WaitFirstSSE {
				waitLifecycle(t, func() bool {
					for _, next := range connectionSnapshot(t, h).Active {
						if next.ID == active.ID && next.LastUpstreamProgressAgeMS != nil {
							return true
						}
					}
					return false
				}, "upstream comment bytes did not update real-read progress")
			}
			snapshot := connectionSnapshot(t, h)
			if len(snapshot.Recent) != 0 {
				t.Fatal("running request was prematurely recorded as completed")
			}
			raw, _ := json.Marshal(snapshot)
			for _, secret := range []string{providerSecret, prompt, key, "Bearer ", "x-auth-token"} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("live diagnostics leaked content: %q", secret)
				}
			}
			health := httptest.NewRecorder()
			h.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			if health.Code != http.StatusOK {
				t.Fatal("stalled request blocked health")
			}
			select {
			case rec := <-done:
				if rec.Code != http.StatusGatewayTimeout || rec.Header().Get("X-Request-Id") != active.ID {
					t.Fatalf("stall did not return correlated timeout: %d", rec.Code)
				}
			case <-time.After(time.Second):
				t.Fatal("stalled request retained resources after deadline")
			}
			event := assertFinishedConnection(t, h, p, active.ID)
			if event.Outcome != "timeout" || event.Cause.Class != audit.CauseTimeout || !traceHasPhase(event, phase) || event.FirstDataMS != nil || event.Trace.FirstSSEDataMS != nil {
				t.Fatalf("stall completion lost its actual phase/outcome: %+v", event)
			}
		})
	}
}

func TestConnectionsHealthyLongGenerationShowsProgressWithoutPhaseAmplification(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for i := 0; i < 15; i++ {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
					w.(http.Flusher).Flush()
					select {
					case <-time.After(20 * time.Millisecond):
					case <-r.Context().Done():
						return
					}
				}
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer backend.Close()
			sink := newAuditSink(t)
			h, p := auditHandler(t, backend.URL, 1, sink, func(c *Config) { c.StartupTimeout = 80 * time.Millisecond })
			p.Accounts()[0].Client = upstream.NewWithChatEndpoint(120*time.Millisecond, backend.URL)
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, lifecycleRequest(context.Background(), stream))
				done <- rec
			}()
			phase := telemetry.Aggregate
			if stream {
				phase = telemetry.Stream
			}
			active := waitConnectionPhase(t, h, phase)
			waitLifecycle(t, func() bool {
				for _, next := range connectionSnapshot(t, h).Active {
					if next.ID == active.ID && next.TotalMS >= 150 {
						active = next
						return true
					}
				}
				return false
			}, "healthy generation did not remain visible beyond startup budget")
			if active.FirstSSEDataMS == nil || active.LastUpstreamProgressAgeMS == nil || *active.LastUpstreamProgressAgeMS > 100 || active.TraceTruncated || len(active.Timeline) > 12 {
				t.Fatalf("long stream lost actual progress or amplified timeline: %+v", active)
			}
			select {
			case rec := <-done:
				if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "event: error") {
					t.Fatalf("healthy stream was incorrectly terminated: %d %s", rec.Code, rec.Body.String())
				}
			case <-time.After(time.Second):
				t.Fatal("healthy completed stream retained its handler")
			}
			event := assertFinishedConnection(t, h, p, active.ID)
			if event.Outcome != "ok" || event.FirstDataMS == nil || event.Trace.FirstSSEDataMS == nil || event.Trace.LastUpstreamProgressMS == nil || !traceHasPhase(event, phase) || event.Trace.Truncated {
				t.Fatalf("long healthy generation was misclassified: %+v", event)
			}
		})
	}
}

func TestConnectionsCancellationWhileWaitingAccountHasNoInventedSelection(t *testing.T) {
	sink := newAuditSink(t)
	backend := okBackend(t)
	h, p := auditHandler(t, backend.URL, 1, sink, nil)
	name := p.Accounts()[0].Name
	if !p.AcquireLock(name) {
		t.Fatal("failed to occupy fixture account")
	}
	defer p.ReleaseLock(name)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), lifecycleRequest(ctx, false))
	}()
	active := waitConnectionPhase(t, h, telemetry.WaitAccount)
	if active.Account != nil || active.Attempt != 0 || active.FirstSSEDataMS != nil || active.LastUpstreamProgressAgeMS != nil {
		t.Fatalf("slot waiter invented selected account/upstream activity: %+v", active)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled account waiter was not released")
	}
	p.ReleaseLock(name)
	event := assertFinishedConnection(t, h, p, active.ID)
	if event.Outcome != "canceled" || event.Committed || event.Account != nil || event.FirstDataMS != nil || !traceHasPhase(event, telemetry.WaitAccount) {
		t.Fatalf("canceled waiter invented response/selection: %+v", event)
	}
}

func TestConnectionsAdmissionRejectionLeavesOnlyTruthfulBoundaries(t *testing.T) {
	h, p := lifecycleHandlerConfig(t, 1, "http://127.0.0.1:1", pool.Config{MaxConcurrent: 1, MaxPending: 1})
	h.cfg.Audit = newAuditSink(t)
	for i := 0; i < 2; i++ {
		release, err := p.AdmitRequest(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, lifecycleRequest(context.Background(), false))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("saturated request was not promptly rejected: %d", rec.Code)
	}
	event := assertFinishedConnection(t, h, p, rec.Header().Get("X-Request-Id"))
	if event.Outcome != "admission_rejected" || event.Account != nil || event.Attempts != 0 || len(event.Trace.Timeline) != 3 || !traceHasPhase(event, telemetry.Admission) || traceHasPhase(event, telemetry.ReadBody) {
		t.Fatalf("admission rejection invented later work: %+v", event)
	}
}

func TestConnectionsEmptyPreflightDoesNotInventFirstSSEData(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": heartbeat only\n\n")
	}))
	defer backend.Close()
	sink := newAuditSink(t)
	h, p := auditHandler(t, backend.URL, 1, sink, nil)
	_, id := doChat(h, "glm-5.2")
	event := assertFinishedConnection(t, h, p, id)
	if event.FirstDataMS != nil || event.Trace.FirstSSEDataMS != nil || event.Trace.LastUpstreamProgressMS == nil {
		t.Fatalf("empty preflight was labeled as accepted SSE data: %+v", event)
	}
}

type connectionBlockedWriter struct {
	deadlineLifecycleWriter
	entered chan struct{}
}

func (w *connectionBlockedWriter) Write(p []byte) (int, error) {
	select {
	case <-w.entered:
	default:
		close(w.entered)
	}
	return w.deadlineLifecycleWriter.Write(p)
}

func TestConnectionsBlockedDownstreamRemainsVisibleUntilDeadlineCleanup(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			sink := newAuditSink(t)
			backend := okBackend(t)
			h, p := auditHandler(t, backend.URL, 1, sink, func(c *Config) { c.StreamWriteTimeout = 150 * time.Millisecond })
			writer := &connectionBlockedWriter{deadlineLifecycleWriter: deadlineLifecycleWriter{header: make(http.Header)}, entered: make(chan struct{})}
			done := make(chan struct{})
			go func() {
				defer close(done)
				h.ServeHTTP(writer, lifecycleRequest(context.Background(), stream))
			}()
			select {
			case <-writer.entered:
			case <-time.After(time.Second):
				t.Fatal("fixture never entered blocked downstream write")
			}
			phase := telemetry.WriteResponse
			if stream {
				phase = telemetry.Stream
			}
			active := waitConnectionPhase(t, h, phase)
			if active.FirstSSEDataMS == nil || active.LastUpstreamProgressAgeMS == nil || len(connectionSnapshot(t, h).Recent) != 0 {
				t.Fatalf("blocked downstream disappeared or lost real upstream progress: %+v", active)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("downstream write deadline failed to clean up request")
			}
			event := assertFinishedConnection(t, h, p, active.ID)
			if event.Outcome != "downstream_canceled" || event.Cause.Class != audit.CauseDownstream || !traceHasPhase(event, phase) {
				t.Fatalf("downstream timeout was labeled as an upstream stall: %+v", event)
			}
		})
	}
}
