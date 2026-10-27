package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type anthropicResponseWriter struct {
	http.ResponseWriter
	status       int
	buffer       bytes.Buffer
	pending      []byte
	stream       bool
	started      bool
	ended        bool
	failed       bool
	sent         bool
	actualStatus int
	committed    bool
	model        string
	id           string
	finishReason string
	inputTokens  int
	outputTokens int
	nextBlock    int
	activeBlock  int
	activeKind   string
	activeTool   int
	primaryTool  int
	hasTool      bool
	tools        map[int]*anthropicStreamTool
	toolBytes    int
	deferred     []*anthropicDeferredBlock
	deferredSize int
	writeErr     error
}

type anthropicStreamTool struct {
	id        string
	name      string
	pending   strings.Builder
	arguments strings.Builder
	block     int
	started   bool
}

type anthropicDeferredBlock struct {
	tool      bool
	toolIndex int
	text      strings.Builder
}

var errAnthropicStreamTranslation = errors.New("anthropic stream translation failed")
var errAnthropicResponseTranslation = errors.New("anthropic response translation failed")

func (w *anthropicResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// ResponseStatus reports what the translated response committed to the client.
func (w *anthropicResponseWriter) ResponseStatus() (int, bool) {
	return w.actualStatus, w.committed
}

func (w *anthropicResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *anthropicResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if !w.stream && w.status < 400 && strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		w.stream = true
	}
	if !w.stream {
		if w.buffer.Len()+len(p) > maxBodyBytes {
			if err := w.writeTranslatedJSON(http.StatusBadGateway, map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": "Messages response exceeds 8MB limit"}}); err != nil {
				return 0, err
			}
			return 0, errAnthropicResponseTranslation
		}
		_, _ = w.buffer.Write(p)
		if json.Valid(w.buffer.Bytes()) {
			if err := w.writeJSONResponse(); err != nil {
				w.writeErr = err
				return 0, err
			}
		}
		return len(p), nil
	}
	w.pending = append(w.pending, p...)
	for {
		end := bytes.Index(w.pending, []byte("\n\n"))
		if end < 0 {
			break
		}
		event := w.pending[:end]
		if err := w.processEvent(event); err != nil {
			w.writeErr = err
			return 0, err
		}
		w.pending = w.pending[end+2:]
	}
	if len(w.pending) > maxBodyBytes {
		return 0, fmt.Errorf("Messages stream event exceeds 8MB limit")
	}
	return len(p), nil
}

func (w *anthropicResponseWriter) FlushError() error {
	if w.writeErr != nil {
		return w.writeErr
	}
	if !w.stream || !w.started && !w.failed {
		return nil
	}
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err == http.ErrNotSupported {
		return nil
	}
	return err
}

func (w *anthropicResponseWriter) finish() {
	if w.stream {
		if !w.ended && !w.failed && w.writeErr == nil {
			_ = w.streamError("upstream stream ended without a completion event")
		}
		return
	}
	if w.sent || w.writeErr != nil {
		return
	}
	_ = w.writeJSONResponse()
}

func (w *anthropicResponseWriter) writeJSONResponse() error {
	status := w.status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	var body map[string]any
	if json.Unmarshal(w.buffer.Bytes(), &body) != nil {
		return w.writeTranslatedJSON(http.StatusBadGateway, map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": "invalid upstream response"}})
	}
	if status >= 400 {
		message := "upstream request failed"
		if e, ok := body["error"].(map[string]any); ok {
			if value, ok := e["message"].(string); ok {
				message = value
			}
		}
		return w.writeTranslatedJSON(status, map[string]any{"type": "error", "error": map[string]any{"type": anthropicErrorType(status), "message": message}})
	}
	response, err := anthropicCompletion(body)
	if err != nil {
		if writeErr := w.writeTranslatedJSON(http.StatusBadGateway, map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": err.Error()}}); writeErr != nil {
			return writeErr
		}
		return errAnthropicResponseTranslation
	}
	return w.writeTranslatedJSON(status, response)
}

func (w *anthropicResponseWriter) writeTranslatedJSON(status int, body map[string]any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.ResponseWriter.WriteHeader(status)
	w.actualStatus, w.committed = status, true
	w.sent = true
	_, err = w.ResponseWriter.Write(raw)
	return err
}

func anthropicErrorType(status int) string {
	switch status {
	case 400, 413, 422:
		return "invalid_request_error"
	case 401:
		return "authentication_error"
	case 403:
		return "permission_error"
	case 404:
		return "not_found_error"
	case 429:
		return "rate_limit_error"
	case 503:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

func anthropicStopReason(reason string) string {
	switch reason {
	case "tool_calls", "function_call":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "refusal"
	default:
		return "end_turn"
	}
}

func anthropicCount(usage map[string]any, key string) int {
	if value, ok := usage[key].(float64); ok && value >= 0 {
		return int(value)
	}
	return 0
}

func anthropicCompletion(body map[string]any) (map[string]any, error) {
	choices, ok := body["choices"].([]any)
	if !ok || len(choices) == 0 {
		return nil, fmt.Errorf("invalid chat completion choices")
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid chat completion choice")
	}
	message, ok := choice["message"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid chat completion message")
	}
	content := []any{}
	if text, ok := message["content"].(string); ok && text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	if calls, ok := message["tool_calls"].([]any); ok {
		for _, raw := range calls {
			call, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid tool call")
			}
			fn, ok := call["function"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid tool function")
			}
			id, _ := call["id"].(string)
			name, _ := fn["name"].(string)
			arguments, _ := fn["arguments"].(string)
			if arguments == "" {
				arguments = "{}"
			}
			var input map[string]json.RawMessage
			if id == "" || name == "" || json.Unmarshal([]byte(arguments), &input) != nil || input == nil {
				return nil, fmt.Errorf("invalid tool call input")
			}
			content = append(content, map[string]any{"type": "tool_use", "id": id, "name": name, "input": json.RawMessage(arguments)})
		}
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}
	usage, _ := body["usage"].(map[string]any)
	reason, _ := choice["finish_reason"].(string)
	model, _ := body["model"].(string)
	return map[string]any{"id": "msg_" + randHex(24), "type": "message", "role": "assistant", "model": model, "content": content, "stop_reason": anthropicStopReason(reason), "stop_sequence": nil, "usage": map[string]any{"input_tokens": anthropicCount(usage, "prompt_tokens"), "output_tokens": anthropicCount(usage, "completion_tokens")}}, nil
}

func (w *anthropicResponseWriter) emit(event string, payload map[string]any) error {
	payload["type"] = event
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if !w.started && !w.failed {
		w.ResponseWriter.WriteHeader(w.status)
		w.actualStatus, w.committed = w.status, true
	}
	_, err = io.WriteString(w.ResponseWriter, "event: "+event+"\ndata: "+string(raw)+"\n\n")
	return err
}

func (w *anthropicResponseWriter) begin() error {
	if w.started {
		return nil
	}
	if w.id == "" {
		w.id = "msg_" + randHex(24)
	}
	message := map[string]any{"id": w.id, "type": "message", "role": "assistant", "model": w.model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}
	if err := w.emit("message_start", map[string]any{"message": message}); err != nil {
		return err
	}
	w.started = true
	return nil
}

func (w *anthropicResponseWriter) closeBlock() error {
	if w.activeKind == "" {
		return nil
	}
	if err := w.emit("content_block_stop", map[string]any{"index": w.activeBlock}); err != nil {
		return err
	}
	w.activeKind = ""
	return nil
}

func (w *anthropicResponseWriter) textDelta(text string) error {
	if text == "" {
		return nil
	}
	// Later blocks must wait because the first tool may still receive fragments.
	if w.hasTool {
		if len(text) > maxBodyBytes-w.toolBytes-w.deferredSize {
			return w.protocolError("buffered stream content exceeds 8MB limit")
		}
		w.deferredSize += len(text)
		if len(w.deferred) == 0 || w.deferred[len(w.deferred)-1].tool {
			w.deferred = append(w.deferred, &anthropicDeferredBlock{})
		}
		w.deferred[len(w.deferred)-1].text.WriteString(text)
		return nil
	}
	return w.emitTextDelta(text)
}

func (w *anthropicResponseWriter) emitTextDelta(text string) error {
	if w.activeKind != "text" {
		if err := w.closeBlock(); err != nil {
			return err
		}
		w.activeBlock = w.nextBlock
		w.nextBlock++
		w.activeKind = "text"
		if err := w.emit("content_block_start", map[string]any{"index": w.activeBlock, "content_block": map[string]any{"type": "text", "text": ""}}); err != nil {
			return err
		}
	}
	return w.emit("content_block_delta", map[string]any{"index": w.activeBlock, "delta": map[string]any{"type": "text_delta", "text": text}})
}

func (w *anthropicResponseWriter) toolDelta(index int, call map[string]any) error {
	if w.tools == nil {
		w.tools = make(map[int]*anthropicStreamTool)
	}
	t := w.tools[index]
	if t == nil {
		if len(w.tools) >= 256 {
			return w.protocolError("too many tool calls")
		}
		if !w.hasTool {
			if err := w.closeBlock(); err != nil {
				return err
			}
			w.hasTool = true
			w.primaryTool = index
		}
		t = &anthropicStreamTool{}
		w.tools[index] = t
		w.deferred = append(w.deferred, &anthropicDeferredBlock{tool: true, toolIndex: index})
	}
	if id, ok := call["id"].(string); ok && id != "" {
		if t.started && id != t.id {
			return w.protocolError("tool call id changed after start")
		}
		w.toolBytes += len(id) - len(t.id)
		t.id = id
	}
	if fn, ok := call["function"].(map[string]any); ok {
		if name, ok := fn["name"].(string); ok && name != "" {
			if t.started {
				return w.protocolError("tool name changed after start")
			}
			w.toolBytes += len(name)
			if w.toolBytes > maxBodyBytes-w.deferredSize {
				return w.protocolError("tool input exceeds 8MB limit")
			}
			t.name += name
		}
		if args, ok := fn["arguments"].(string); ok {
			w.toolBytes += len(args)
			if w.toolBytes > maxBodyBytes-w.deferredSize {
				return w.protocolError("tool input exceeds 8MB limit")
			}
			t.pending.WriteString(args)
			t.arguments.WriteString(args)
		}
	}
	if w.toolBytes > maxBodyBytes-w.deferredSize {
		return w.protocolError("tool input exceeds 8MB limit")
	}
	if index != w.primaryTool || !t.started && (t.id == "" || t.name == "" || t.pending.Len() == 0) {
		return nil
	}
	return w.flushTool(index)
}

func (w *anthropicResponseWriter) flushTool(index int) error {
	t := w.tools[index]
	if t == nil {
		return w.protocolError("invalid tool call")
	}
	if !t.started {
		if t.id == "" || t.name == "" {
			return w.protocolError("incomplete tool call")
		}
		if err := w.closeBlock(); err != nil {
			return err
		}
		t.block = w.nextBlock
		w.nextBlock++
		w.activeBlock = t.block
		w.activeKind = "tool"
		w.activeTool = index
		if err := w.emit("content_block_start", map[string]any{"index": t.block, "content_block": map[string]any{"type": "tool_use", "id": t.id, "name": t.name, "input": map[string]any{}}}); err != nil {
			return err
		}
		t.started = true
	}
	if w.activeKind != "tool" || w.activeTool != index {
		return w.protocolError("tool call emitted outside its content block")
	}
	if t.pending.Len() != 0 {
		if err := w.emit("content_block_delta", map[string]any{"index": t.block, "delta": map[string]any{"type": "input_json_delta", "partial_json": t.pending.String()}}); err != nil {
			return err
		}
		t.pending.Reset()
	}
	return nil
}

func (w *anthropicResponseWriter) streamError(message string) error {
	if w.failed {
		return nil
	}
	if err := w.emit("error", map[string]any{"error": map[string]any{"type": "api_error", "message": message}}); err != nil {
		return err
	}
	w.failed = true
	return nil
}

func (w *anthropicResponseWriter) protocolError(message string) error {
	if err := w.streamError(message); err != nil {
		return err
	}
	return errAnthropicStreamTranslation
}

func (w *anthropicResponseWriter) processEvent(event []byte) error {
	if w.failed || w.ended {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(string(event), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	data := strings.Join(lines, "\n")
	if data == "[DONE]" {
		return w.completeStream()
	}
	var chunk map[string]any
	if json.Unmarshal([]byte(data), &chunk) != nil {
		return w.protocolError("invalid upstream stream event")
	}
	if sourceError, ok := chunk["error"].(map[string]any); ok {
		message, _ := sourceError["message"].(string)
		if message == "" {
			message = "upstream stream error"
		}
		return w.streamError(message)
	}
	if model, ok := chunk["model"].(string); ok && w.model == "" {
		w.model = model
	}
	if usage, ok := chunk["usage"].(map[string]any); ok {
		w.inputTokens = anthropicCount(usage, "prompt_tokens")
		w.outputTokens = anthropicCount(usage, "completion_tokens")
	}
	if err := w.begin(); err != nil {
		return err
	}
	choices, _ := chunk["choices"].([]any)
	for _, item := range choices {
		choice, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if reason, ok := choice["finish_reason"].(string); ok && reason != "" {
			w.finishReason = reason
		}
		delta, _ := choice["delta"].(map[string]any)
		if text, ok := delta["content"].(string); ok {
			if err := w.textDelta(text); err != nil {
				return err
			}
		}
		if calls, ok := delta["tool_calls"].([]any); ok {
			for _, item := range calls {
				call, ok := item.(map[string]any)
				if !ok {
					return w.protocolError("invalid tool call delta")
				}
				index, ok := call["index"].(float64)
				if !ok || index < 0 || index != float64(int(index)) {
					return w.protocolError("invalid tool call index")
				}
				if err := w.toolDelta(int(index), call); err != nil {
					return err
				}
				if w.failed {
					return nil
				}
			}
		}
	}
	return nil
}

func (w *anthropicResponseWriter) completeStream() error {
	if err := w.begin(); err != nil {
		return err
	}
	for _, block := range w.deferred {
		if !block.tool {
			continue
		}
		t := w.tools[block.toolIndex]
		var input map[string]json.RawMessage
		args := t.arguments.String()
		if args == "" {
			args = "{}"
		}
		if json.Unmarshal([]byte(args), &input) != nil || input == nil {
			return w.protocolError("invalid tool call input")
		}
		if t.id == "" {
			t.id = "toolu_" + randHex(24)
		}
	}
	for _, block := range w.deferred {
		if block.tool {
			if err := w.flushTool(block.toolIndex); err != nil {
				return err
			}
		} else if err := w.emitTextDelta(block.text.String()); err != nil {
			return err
		}
		if err := w.closeBlock(); err != nil {
			return err
		}
	}
	if err := w.closeBlock(); err != nil {
		return err
	}
	if err := w.emit("message_delta", map[string]any{"delta": map[string]any{"stop_reason": anthropicStopReason(w.finishReason), "stop_sequence": nil}, "usage": map[string]any{"input_tokens": w.inputTokens, "output_tokens": w.outputTokens}}); err != nil {
		return err
	}
	if err := w.emit("message_stop", map[string]any{}); err != nil {
		return err
	}
	w.ended = true
	return nil
}
