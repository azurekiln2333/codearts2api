package upstream

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamRejectsSnapshotsThatRewriteSentText(t *testing.T) {
	const first = `data: {"text":"ABC_OLD"}` + "\n"
	const nativeFirst = `data: {"choices":[{"delta":{"content":"ABC_OLD"}}]}` + "\n"
	for _, tc := range []struct {
		name, initial, snapshot, replacement string
	}{
		{"same length", first, `data: {"text":"XYZ_NEW"}` + "\n", "XYZ_NEW"},
		{"shorter named event", first, "event: onAnswer\n" + `data: {"text":"XY"}` + "\n", "XY"},
		{"longer output array", first, `data: {"output":[{"type":"output_text","text":"XYZ_NEW_LONG"}]}` + "\n", "XYZ_NEW_LONG"},
		{"snapshot after native delta", nativeFirst, `data: {"content":"XYZ_NEW_LONG"}` + "\n", "XYZ_NEW_LONG"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := tc.initial + tc.snapshot + `data: {"usage":{"completion_tokens":7}}` + "\n" + "data: [DONE]\n"
			aggregate, err := AggregateRaw(strings.NewReader(wire))
			if err != nil || aggregate.Content != tc.replacement || aggregate.Usage["completion_tokens"] != float64(7) {
				t.Fatalf("aggregate=%+v err=%v", aggregate, err)
			}

			var completed, usageCalled bool
			recorder := httptest.NewRecorder()
			err = StreamCaptureWithUsage(recorder, strings.NewReader(wire), "model", func(*RawCompletion) {
				completed = true
			}, func(*RawCompletion) map[string]any {
				usageCalled = true
				return map[string]any{"completion_tokens": 7}
			})
			body := recorder.Body.String()
			if !errors.Is(err, ErrIncompleteStream) || completed || usageCalled ||
				!strings.Contains(body, `"content":"ABC_OLD"`) || !strings.Contains(body, "event: error") ||
				!strings.Contains(body, "data: [DONE]") || strings.Contains(body, `"finish_reason"`) ||
				strings.Contains(body, `"completion_tokens"`) || strings.Contains(body, tc.replacement) {
				t.Fatalf("rewrite not reported as stream failure: completed=%v usage=%v err=%v body=%s", completed, usageCalled, err, body)
			}
			if strings.Contains(err.Error(), "ABC_OLD") || strings.Contains(err.Error(), tc.replacement) {
				t.Fatalf("stream error leaked content: %v", err)
			}
		})
	}
}

func TestStreamForwardsPrefixSnapshotsAndNativeDeltasImmediately(t *testing.T) {
	reader, sender := io.Pipe()
	defer reader.Close()
	defer sender.Close()
	writer := &immediateChunkWriter{header: http.Header{}, chunks: make(chan string, 8)}
	completed := make(chan *RawCompletion, 1)
	result := make(chan error, 1)
	go func() {
		result <- StreamCapture(writer, reader, "model", func(value *RawCompletion) { completed <- value })
	}()
	for _, tc := range []struct{ wire, delta string }{
		{`data: {"text":"\u6c49"}` + "\n", "\"content\":\"\u6c49\""},
		{"event: onAnswer\n" + `data: {"text":"\u6c49\u5b57"}` + "\n", "\"content\":\"\u5b57\""},
		{`data: {"choices":[{"delta":{"content":"!"}}]}` + "\n", `"content":"!"`},
	} {
		if _, err := io.WriteString(sender, tc.wire); err != nil {
			t.Fatal(err)
		}
		select {
		case chunk := <-writer.chunks:
			if !strings.Contains(chunk, tc.delta) || strings.Contains(chunk, `"finish_reason"`) {
				t.Fatalf("unexpected immediate delta: %s", chunk)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("delta was held until the next upstream event")
		}
	}
	if _, err := io.WriteString(sender, `data: {"text":"\u6c49\u5b57!"}`+"\n"+"data: [DONE]\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish")
	}
	finish, done := <-writer.chunks, <-writer.chunks
	if !strings.Contains(finish, `"finish_reason":"stop"`) || !strings.Contains(done, "data: [DONE]") || len(writer.chunks) != 0 {
		t.Fatalf("duplicate snapshot changed the final events: %s %s", finish, done)
	}
	if value := <-completed; value.Content != "\u6c49\u5b57!" {
		t.Fatalf("completion content=%q", value.Content)
	}
}
