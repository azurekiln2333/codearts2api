package upstream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamFinalFinishMatchesTerminalEvidence(t *testing.T) {
	const tool = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}` + "\n"
	const usage = `data: {"choices":[],"usage":{"completion_tokens":7}}` + "\n"
	for _, tc := range []struct {
		name, wire, want string
	}{
		{
			name: "terminal reason overrides earlier stop",
			wire: tool + `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n" + usage + "event: done\n" + `data: {"finish_reason":"length"}` + "\n",
			want: "length",
		},
		{
			name: "terminal choice reason overrides earlier stop",
			wire: tool + `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n" + usage + "event: done\n" + `data: {"choices":[{"delta":{},"finish_reason":"content_filter"}]}` + "\n",
			want: "content_filter",
		},
		{
			name: "legacy terminal function call",
			wire: tool + usage + "event: done\n" + `data: {"finish_reason":"function_call"}` + "\n",
			want: "tool_calls",
		},
		{
			name: "done text carries a terminal length reason",
			wire: tool + `data: {"text":"[DONE]","finish_reason":"length","usage":{"completion_tokens":7}}` + "\n",
			want: "length",
		},
		{
			name: "named done text carries a terminal filter reason",
			wire: tool + usage + "event: onAnswer\n" + `data: {"text":"[DONE]","finish_reason":"content_filter"}` + "\n",
			want: "content_filter",
		},
		{
			name: "later finish evidence wins",
			wire: `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"length"}]}` + "\n" + `data: {"choices":[{"delta":{},"finish_reason":"content_filter"}]}` + "\n" + usage + "data: [DONE]\n",
			want: "content_filter",
		},
		{
			name: "finish evidence followed by usage and EOF",
			wire: tool + `data: {"choices":[{"delta":{},"finish_reason":"function_call"}]}` + "\n" + usage,
			want: "tool_calls",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aggregated, err := AggregateRaw(strings.NewReader(tc.wire))
			if err != nil {
				t.Fatal(err)
			}
			aggregateFinish := aggregated.Finish
			if len(aggregated.ToolCalls) > 0 && (aggregateFinish == "stop" || aggregateFinish == "function_call") {
				aggregateFinish = "tool_calls"
			}
			if aggregateFinish != tc.want {
				t.Fatalf("aggregate finish=%q, want %q", aggregateFinish, tc.want)
			}
			var captured *RawCompletion
			recorder := httptest.NewRecorder()
			err = StreamCaptureWithUsage(recorder, strings.NewReader(tc.wire), "model", func(result *RawCompletion) {
				captured = result
			}, func(result *RawCompletion) map[string]any { return result.Usage })
			if err != nil || captured == nil || captured.Finish != tc.want || captured.Usage["completion_tokens"] != float64(7) {
				t.Fatalf("captured=%+v err=%v", captured, err)
			}
			body := recorder.Body.String()
			finishAt := strings.Index(body, `"finish_reason":"`+tc.want+`"`)
			usageAt := strings.Index(body, `"completion_tokens":7`)
			doneAt := strings.Index(body, "data: [DONE]")
			if strings.Count(body, `"finish_reason"`) != 1 || finishAt < 0 || usageAt < finishAt || doneAt < usageAt || strings.Count(body, "data: [DONE]") != 1 {
				t.Fatalf("invalid finish/usage/DONE sequence: %s", body)
			}
			if strings.HasPrefix(tc.wire, tool) && !strings.Contains(body, `"name":"lookup"`) {
				t.Fatalf("missing streamed tool delta: %s", body)
			}
		})
	}
}

func TestStreamFinishEvidenceDoesNotHideLaterFailure(t *testing.T) {
	const first = `data: {"choices":[{"delta":{"content":"partial"},"finish_reason":"stop"}]}` + "\n"
	for _, tc := range []struct {
		name  string
		input io.Reader
		check func(error) bool
	}{
		{
			name:  "provider error",
			input: strings.NewReader(first + "event: error\n" + `data: {"message":"failed"}` + "\n"),
			check: func(err error) bool { var upstreamErr *UpstreamError; return errors.As(err, &upstreamErr) },
		},
		{
			name:  "cancellation",
			input: io.MultiReader(strings.NewReader(first), terminalErrorReader{context.Canceled}),
			check: func(err error) bool { return errors.Is(err, context.Canceled) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			completed, usageCalled := false, false
			err := StreamCaptureWithUsage(recorder, tc.input, "model", func(*RawCompletion) { completed = true }, func(*RawCompletion) map[string]any {
				usageCalled = true
				return map[string]any{"completion_tokens": 7}
			})
			body := recorder.Body.String()
			if !tc.check(err) || completed || usageCalled || strings.Contains(body, `"finish_reason"`) || strings.Contains(body, `"completion_tokens"`) || !strings.Contains(body, "event: error") || !strings.Contains(body, "data: [DONE]") {
				t.Fatalf("failure reported as completion: completed=%v usage=%v err=%v body=%s", completed, usageCalled, err, body)
			}
		})
	}
}

type terminalFailureWriter struct {
	header http.Header
	mode   string
	err    error
	writes int
}

func (w *terminalFailureWriter) Header() http.Header { return w.header }
func (w *terminalFailureWriter) WriteHeader(int)     {}
func (w *terminalFailureWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.mode == "write" && w.writes == 2 {
		return 0, w.err
	}
	return len(p), nil
}
func (w *terminalFailureWriter) FlushError() error {
	if w.mode == "flush" && w.writes == 2 {
		return w.err
	}
	return nil
}

func TestStreamTerminalFinishWriteFailureIsNotSuccess(t *testing.T) {
	input := `data: {"choices":[{"delta":{"content":"partial"},"finish_reason":"length"}]}` + "\n" + "data: [DONE]\n"
	for _, mode := range []string{"write", "flush"} {
		t.Run(mode, func(t *testing.T) {
			wantErr := errors.New("downstream disconnected")
			writer := &terminalFailureWriter{header: http.Header{}, mode: mode, err: wantErr}
			completed, usageCalled := false, false
			err := StreamCaptureWithUsage(writer, strings.NewReader(input), "model", func(*RawCompletion) { completed = true }, func(*RawCompletion) map[string]any {
				usageCalled = true
				return map[string]any{"completion_tokens": 7}
			})
			var downstreamErr *DownstreamWriteError
			if !errors.As(err, &downstreamErr) || !errors.Is(err, wantErr) || completed || usageCalled || writer.writes != 2 {
				t.Fatalf("terminal failure misclassified: completed=%v usage=%v writes=%d err=%v", completed, usageCalled, writer.writes, err)
			}
		})
	}
}

type immediateChunkWriter struct {
	header http.Header
	chunks chan string
}

func (w *immediateChunkWriter) Header() http.Header { return w.header }
func (w *immediateChunkWriter) WriteHeader(int)     {}
func (w *immediateChunkWriter) Write(p []byte) (int, error) {
	w.chunks <- string(p)
	return len(p), nil
}
func (w *immediateChunkWriter) Flush() {}

func TestStreamForwardsFinishedDeltaBeforeTerminal(t *testing.T) {
	reader, sender := io.Pipe()
	defer reader.Close()
	defer sender.Close()
	writer := &immediateChunkWriter{header: http.Header{}, chunks: make(chan string, 4)}
	result := make(chan error, 1)
	go func() { result <- StreamCapture(writer, reader, "model", nil) }()
	if _, err := io.WriteString(sender, `data: {"choices":[{"delta":{"content":"early"},"finish_reason":"stop"}]}`+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case chunk := <-writer.chunks:
		if !strings.Contains(chunk, `"content":"early"`) || strings.Contains(chunk, `"finish_reason"`) {
			t.Fatalf("delta not forwarded independently of finish: %s", chunk)
		}
	case <-time.After(time.Second):
		t.Fatal("content was held until the upstream terminal event")
	}
	if _, err := io.WriteString(sender, "event: done\n"+`data: {"finish_reason":"length"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not stop at terminal event")
	}
	finish := <-writer.chunks
	done := <-writer.chunks
	if !strings.Contains(finish, `"finish_reason":"length"`) || !strings.Contains(done, "data: [DONE]") {
		t.Fatalf("unexpected final events: %s %s", finish, done)
	}
}
