package upstream

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConsumersStopAtProtocolTerminalWithoutEOF(t *testing.T) {
	for _, terminal := range []string{
		"data: [DONE]\n\n",
		"event: done\ndata: {\"error_code\":\"0\"}\n\n",
		"event: end\ndata: {\"error_code\":\"0\"}\n\n",
		"event: finish\ndata: {\"error_code\":\"0\"}\n\n",
		"data: {\"text\":\"[DONE]\",\"error_code\":\"0\"}\n\n",
		"event: onAnswer\ndata: {\"text\":\"[DONE]\",\"error_code\":\"0\"}\n\n",
	} {
		t.Run(terminal, func(t *testing.T) {
			pastEnd := errors.New("read after the protocol terminal event")
			input := func() io.Reader {
				return io.MultiReader(strings.NewReader("data: {\"delta\":{\"content\":\"ok\"}}\n"+terminal), terminalErrorReader{pastEnd})
			}
			result, err := AggregateRaw(input())
			if err != nil || result.Content != "ok" {
				t.Errorf("aggregate waited past completion: result=%+v err=%v", result, err)
			}
			var captured *RawCompletion
			rec := httptest.NewRecorder()
			err = StreamCapture(rec, input(), "model", func(result *RawCompletion) { captured = result })
			if err != nil || captured == nil || captured.Content != "ok" {
				t.Errorf("stream waited past completion: result=%+v err=%v", captured, err)
			}
			if strings.Count(rec.Body.String(), "data: [DONE]") != 1 {
				t.Errorf("expected exactly one downstream terminal event: %s", rec.Body.String())
			}
		})
	}
}

func TestTerminalErrorWinsOverJSONDone(t *testing.T) {
	for _, event := range []string{
		"data: {\"text\":\"[DONE]\",\"error_code\":\"quota.429\",\"error_msg\":\"quota exceeded\"}\n",
		"event: done\ndata: {\"text\":\"[DONE]\",\"error_code\":\"quota.429\",\"error_msg\":\"quota exceeded\"}\n",
		"event: error\ndata: {\"message\":\"quota exceeded\"}\n",
		"data: {\"error\":{\"code\":\"quota.429\",\"message\":\"quota exceeded\"}}\n",
	} {
		t.Run(event, func(t *testing.T) {
			input := func() io.Reader {
				return io.MultiReader(strings.NewReader(event), terminalErrorReader{errors.New("read after upstream error")})
			}
			_, err := AggregateRaw(input())
			var upstreamErr *UpstreamError
			if !errors.As(err, &upstreamErr) || !strings.Contains(err.Error(), "quota exceeded") {
				t.Errorf("aggregate lost terminal error: %T %v", err, err)
			}
			rec := httptest.NewRecorder()
			completed := false
			err = StreamCapture(rec, input(), "model", func(*RawCompletion) { completed = true })
			if !errors.As(err, &upstreamErr) || completed {
				t.Errorf("terminal error reported as success: completed=%v err=%v", completed, err)
			}
			if !strings.Contains(rec.Body.String(), "event: error") || !strings.Contains(rec.Body.String(), "data: [DONE]") {
				t.Errorf("missing downstream terminal error: %s", rec.Body.String())
			}
		})
	}
}

func TestFinishReasonPreservesTrailingUsageAndToolCalls(t *testing.T) {
	data := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking\",\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\"}}]}}]}\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7}}\n" +
		"data: [DONE]\n"
	check := func(result *RawCompletion) {
		t.Helper()
		if result == nil || result.Finish != "tool_calls" || result.Reasoning != "thinking" || len(result.ToolCalls) != 1 ||
			result.ToolCalls[0].Function.Name != "lookup" || result.ToolCalls[0].Function.Arguments != "{}" || result.Usage["completion_tokens"] != float64(7) {
			t.Errorf("lost terminal content/tools/usage: %+v", result)
		}
	}
	result, err := AggregateRaw(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	check(result)
	var captured *RawCompletion
	rec := httptest.NewRecorder()
	err = StreamCaptureWithUsage(rec, strings.NewReader(data), "model", func(result *RawCompletion) { captured = result }, func(result *RawCompletion) map[string]any { return result.Usage })
	if err != nil {
		t.Fatal(err)
	}
	check(captured)
	if !strings.Contains(rec.Body.String(), `"completion_tokens":7`) {
		t.Errorf("trailing usage was not forwarded: %s", rec.Body.String())
	}
}

func TestReadFailureTerminatesWritableSSEWithoutSuccess(t *testing.T) {
	wantErr := errors.New("upstream connection interrupted")
	input := io.MultiReader(strings.NewReader("data: {\"delta\":{\"content\":\"partial\"}}\n"), terminalErrorReader{wantErr})
	rec := httptest.NewRecorder()
	completed := false
	err := StreamCapture(rec, input, "model", func(*RawCompletion) { completed = true })
	if !errors.Is(err, wantErr) || completed {
		t.Fatalf("read failure reported as success: completed=%v err=%v", completed, err)
	}
	if !strings.Contains(rec.Body.String(), "event: error") || !strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Fatalf("missing downstream terminal error after interrupted read: %s", rec.Body.String())
	}
}

func TestChatTimeoutCoversActualHTTPWaits(t *testing.T) {
	for _, phase := range []string{"headers", "first_data", "error_body", "later_idle"} {
		t.Run(phase, func(t *testing.T) {
			closed := make(chan struct{})
			stop := make(chan struct{})
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)
				_, _ = io.Copy(io.Discard, r.Body)
				if phase != "headers" {
					w.Header().Set("Content-Type", "text/event-stream")
					if phase == "error_body" {
						w.WriteHeader(http.StatusBadGateway)
					}
					if phase == "later_idle" {
						_, _ = io.WriteString(w, "data: {\"delta\":{\"content\":\"started\"}}\n\n")
					}
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-stop:
				}
			}))
			defer backend.Close()
			defer close(stop)
			ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
			defer cancel()
			client := NewWithChatEndpoint(60*time.Millisecond, backend.URL)
			start := time.Now()
			body, err := client.SendChatV2(ctx, map[string]any{"model": "test"}, "", SignCredential{}, "", false)
			if body != nil {
				_, err = AggregateRaw(body)
				_ = body.Close()
			}
			var timeoutErr net.Error
			if !errors.As(err, &timeoutErr) || !timeoutErr.Timeout() || time.Since(start) > 400*time.Millisecond {
				t.Errorf("configured timeout did not bound %s: duration=%v err=%T %v", phase, time.Since(start), err, err)
			}
			select {
			case <-closed:
			case <-time.After(300 * time.Millisecond):
				t.Error("timed-out upstream request remained open")
			}
		})
	}
}

func TestActiveChatOutlivesIdleTimeoutAndStopsWithoutEOF(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "aggregate", true: "stream"}[streaming], func(t *testing.T) {
			closed, stop := make(chan struct{}), make(chan struct{})
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				for i := 0; i < 12; i++ {
					_, _ = io.WriteString(w, "data: {\"delta\":{\"content\":\"x\"}}\n\n")
					w.(http.Flusher).Flush()
					select {
					case <-time.After(20 * time.Millisecond):
					case <-r.Context().Done():
						return
					}
				}
				_, _ = io.WriteString(w, "data: {\"text\":\"[DONE]\",\"error_code\":\"0\"}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
				case <-stop:
				}
			}))
			defer backend.Close()
			defer close(stop)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			start := time.Now()
			body, err := NewWithChatEndpoint(100*time.Millisecond, backend.URL).SendChatV2(ctx, map[string]any{}, "", SignCredential{}, "", false)
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			var result *RawCompletion
			if streaming {
				err = StreamCapture(httptest.NewRecorder(), body, "model", func(completion *RawCompletion) { result = completion })
			} else {
				result, err = AggregateRaw(body)
			}
			if err != nil || result == nil || result.Content != strings.Repeat("x", 12) || time.Since(start) < 100*time.Millisecond || time.Since(start) > time.Second {
				t.Fatalf("active stream failed or waited for EOF: duration=%v result=%+v err=%v", time.Since(start), result, err)
			}
			if err := body.Close(); err != nil {
				t.Fatal(err)
			}
			if err := body.Close(); err != nil {
				t.Fatalf("second close was not idempotent: %v", err)
			}
			select {
			case <-closed:
			case <-time.After(300 * time.Millisecond):
				t.Error("completed request did not close the upstream connection")
			}
		})
	}
}

func TestChatCancellationUnblocksHTTPWaits(t *testing.T) {
	for _, phase := range []string{"headers", "first_data", "error_body", "later_idle"} {
		t.Run(phase, func(t *testing.T) {
			ready, closed, stop := make(chan struct{}), make(chan struct{}), make(chan struct{})
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)
				_, _ = io.Copy(io.Discard, r.Body)
				if phase != "headers" {
					w.Header().Set("Content-Type", "text/event-stream")
					if phase == "error_body" {
						w.WriteHeader(http.StatusServiceUnavailable)
					}
					if phase == "later_idle" {
						_, _ = io.WriteString(w, "data: {\"delta\":{\"content\":\"started\"}}\n\n")
					}
					w.(http.Flusher).Flush()
				}
				close(ready)
				select {
				case <-r.Context().Done():
				case <-stop:
				}
			}))
			defer backend.Close()
			defer close(stop)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				body, err := NewWithChatEndpoint(2*time.Second, backend.URL).SendChatV2(ctx, map[string]any{}, "", SignCredential{}, "", false)
				if body != nil {
					_, err = AggregateRaw(body)
					_ = body.Close()
				}
				result <- err
			}()
			select {
			case <-ready:
			case <-time.After(time.Second):
				t.Fatal("upstream did not receive request")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost caller cancellation: %T %v", err, err)
				}
			case <-time.After(300 * time.Millisecond):
				t.Fatalf("canceled %s wait did not return", phase)
			}
			select {
			case <-closed:
			case <-time.After(300 * time.Millisecond):
				t.Error("canceled upstream request remained open")
			}
		})
	}
}

type failingResponseWriter struct {
	header   http.Header
	writeErr error
	flushErr error
	writes   int
}

func (w *failingResponseWriter) Header() http.Header { return w.header }
func (w *failingResponseWriter) WriteHeader(int)     {}
func (w *failingResponseWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return len(p), nil
}
func (w *failingResponseWriter) FlushError() error { return w.flushErr }

func TestStreamDistinguishesDownstreamWriteAndFlushFailures(t *testing.T) {
	for _, phase := range []string{"write", "flush"} {
		t.Run(phase, func(t *testing.T) {
			want := errors.New("downstream disconnected")
			writer := &failingResponseWriter{header: http.Header{}}
			if phase == "write" {
				writer.writeErr = want
			} else {
				writer.flushErr = want
			}
			completed := false
			err := StreamCapture(writer, strings.NewReader("data: {\"delta\":{\"content\":\"first\"}}\n"), "model", func(*RawCompletion) { completed = true })
			var downstreamErr *DownstreamWriteError
			if !errors.As(err, &downstreamErr) || !errors.Is(err, want) || completed || writer.writes != 1 {
				t.Fatalf("downstream failure was misclassified or retried: completed=%v writes=%d err=%T %v", completed, writer.writes, err, err)
			}
		})
	}
}

func TestTerminalFramePreservesLastDeltaAndUsage(t *testing.T) {
	input := "event: done\ndata: {\"choices\":[{\"delta\":{\"content\":\"last\",\"reasoning_content\":\"thought\"},\"finish_reason\":\"length\"}],\"usage\":{\"completion_tokens\":3}}\n"
	result, err := AggregateRaw(io.MultiReader(strings.NewReader(input), terminalErrorReader{errors.New("read after terminal")}))
	if err != nil || result.Content != "last" || result.Reasoning != "thought" || result.Finish != "length" || result.Usage["completion_tokens"] != float64(3) {
		t.Fatalf("terminal payload was lost: result=%+v err=%v", result, err)
	}
}

func TestPreflightUsesSharedErrorClassification(t *testing.T) {
	for _, event := range []string{
		"event: error\ndata: {\"message\":\"upstream overloaded 429\"}\n",
		"data: {\"error\":{\"code\":\"429\",\"message\":\"upstream overloaded\"}}\n",
		"data: {\"text\":\"[DONE]\",\"error_code\":429,\"error_msg\":\"upstream overloaded\"}\n",
	} {
		t.Run(event, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, event)
			}))
			defer backend.Close()
			body, err := NewWithChatEndpoint(time.Second, backend.URL).SendChatV2(context.Background(), map[string]any{}, "", SignCredential{}, "", false)
			if body != nil {
				_ = body.Close()
			}
			var apiErr *ApiError
			if !errors.As(err, &apiErr) || apiErr.Status != http.StatusTooManyRequests || !strings.Contains(apiErr.Message, "overloaded") {
				t.Fatalf("preflight lost the terminal provider error: %T %v", err, err)
			}
		})
	}
}
