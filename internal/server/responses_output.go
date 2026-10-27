package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

const maxResponsesToolCalls = 256

var errResponsesStreamTranslation = errors.New("responses stream translation failed")
var errResponsesResponseTranslation = errors.New("responses response translation failed")

// responsesWriter streams text immediately and buffers function calls until
// their potentially fragmented names have been received in full.
type responsesWriter struct {
	http.ResponseWriter
	opts                  *responsesOptions
	status                int
	actualStatus          int
	committed             bool
	started, done, failed bool
	buffer                []byte
	id                    string
	created               int64
	model                 string
	sequence              int
	items                 []*responsesOutputItem
	message               *responsesOutputItem
	toolCalls             map[int]*responsesOutputItem
	usage                 map[string]any
	finish                string
	outputBytes           int
	writeErr              error
}

type responsesOutputItem struct {
	kind, id, callID string
	index, emitted   int
	name, text       strings.Builder
	added            bool
}

func (w *responsesWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responsesWriter) ResponseStatus() (int, bool) {
	return w.actualStatus, w.committed
}

func (w *responsesWriter) commit(status int) {
	if !w.committed {
		w.actualStatus, w.committed = status, true
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *responsesWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status < http.StatusOK {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	if status != http.StatusOK {
		w.commit(status)
	}
}

func (w *responsesWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	n, err := w.write(p)
	if err != nil {
		w.writeErr = err
	}
	return n, err
}

func (w *responsesWriter) write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.status != http.StatusOK {
		return w.ResponseWriter.Write(p)
	}
	if !w.opts.stream {
		if len(p) > maxBodyBytes {
			return 0, w.responseFailure("upstream response exceeds the size limit")
		}
		var chat map[string]any
		if err := json.Unmarshal(p, &chat); err != nil || chat == nil {
			return 0, w.responseFailure("invalid upstream response")
		}
		response, err := w.nonstream(chat)
		if err != nil {
			return 0, w.responseFailure(err.Error())
		}
		data, err := json.Marshal(response)
		if err != nil {
			return 0, w.responseFailure("invalid upstream response")
		}
		w.commit(http.StatusOK)
		n, err := w.ResponseWriter.Write(data)
		if err != nil {
			return 0, err
		}
		if n != len(data) {
			return 0, io.ErrShortWrite
		}
		return len(p), nil
	}
	if err := w.writeStream(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *responsesWriter) FlushError() error {
	if w.writeErr != nil {
		return w.writeErr
	}
	if !w.committed {
		return nil
	}
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

func (w *responsesWriter) responseFailure(message string) error {
	w.Header().Set("Content-Type", "application/json")
	raw, _ := json.Marshal(map[string]any{"error": map[string]any{
		"message": message, "type": "api_error", "code": "upstream_error",
	}})
	w.commit(http.StatusBadGateway)
	n, err := w.ResponseWriter.Write(raw)
	if err != nil {
		return err
	}
	if n != len(raw) {
		return io.ErrShortWrite
	}
	return fmt.Errorf("%w: %s", errResponsesResponseTranslation, message)
}

func (w *responsesWriter) streamFailure(message string) error {
	if err := w.start(""); err != nil {
		return err
	}
	w.usage = nil
	response := w.response("failed")
	response["error"] = map[string]any{"code": "server_error", "message": message}
	if err := w.emit("response.failed", map[string]any{"response": response}); err != nil {
		return err
	}
	w.failed, w.done = true, true
	if err := w.FlushError(); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s", errResponsesStreamTranslation, message)
}

// Process one bounded frame at a time, including separators split across writes.
func (w *responsesWriter) writeStream(p []byte) error {
	for len(p) > 0 {
		if len(w.buffer) > 0 && w.buffer[len(w.buffer)-1] == '\n' && p[0] == '\n' {
			frame := string(w.buffer[:len(w.buffer)-1])
			w.buffer = nil
			p = p[1:]
			if err := w.streamFrame(frame); err != nil {
				return err
			}
			if w.done {
				return nil
			}
			continue
		}
		end := bytes.Index(p, []byte("\n\n"))
		length := len(p)
		if end >= 0 {
			length = end
		}
		if length > maxBodyBytes-len(w.buffer) {
			return w.streamFailure("upstream stream event exceeds the size limit")
		}
		w.buffer = append(w.buffer, p[:length]...)
		if end < 0 {
			break
		}
		frame := string(w.buffer)
		w.buffer = nil
		p = p[end+2:]
		if err := w.streamFrame(frame); err != nil {
			return err
		}
		if w.done {
			return nil
		}
	}
	return nil
}

func (w *responsesWriter) init(model string) {
	if w.id != "" {
		return
	}
	w.id, w.created, w.model = "resp_"+randHex(24), time.Now().Unix(), model
	if w.model == "" {
		w.model = w.opts.model
	}
	w.toolCalls = make(map[int]*responsesOutputItem)
}

func (w *responsesWriter) response(status string) map[string]any {
	tools, toolChoice := w.opts.tools, w.opts.toolChoice
	if tools == nil {
		tools = []any{}
	}
	if toolChoice == nil {
		toolChoice = "auto"
	}
	output := make([]any, 0, len(w.items))
	for _, item := range w.items {
		itemStatus := "completed"
		if status == "in_progress" {
			itemStatus = "in_progress"
		} else if status == "incomplete" || status == "failed" {
			itemStatus = "incomplete"
		}
		output = append(output, w.item(item, itemStatus))
	}
	result := map[string]any{
		"id": w.id, "object": "response", "created_at": w.created, "status": status,
		"model": w.model, "output": output, "error": nil, "incomplete_details": nil,
		"instructions": w.opts.instructions, "metadata": w.opts.metadata, "store": false,
		"parallel_tool_calls": true, "tools": tools, "tool_choice": toolChoice,
		"max_output_tokens": w.opts.maxTokens, "temperature": w.opts.temperature,
		"top_p": w.opts.topP, "usage": w.usage,
	}
	if w.finish == "length" {
		result["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	if w.finish == "content_filter" {
		result["incomplete_details"] = map[string]any{"reason": "content_filter"}
	}
	return result
}

func (w *responsesWriter) item(item *responsesOutputItem, status string) map[string]any {
	pending := status == "in_progress"
	if item.kind == "function_call" {
		arguments := item.text.String()
		if pending {
			arguments = ""
		}
		return map[string]any{"id": item.id, "type": "function_call", "call_id": item.callID, "name": item.name.String(), "arguments": arguments, "status": status}
	}
	content := []any{}
	if !pending || item.text.Len() > 0 {
		content = append(content, map[string]any{"type": "output_text", "text": item.text.String(), "annotations": []any{}})
	}
	return map[string]any{"id": item.id, "type": "message", "role": "assistant", "status": status, "content": content}
}

func responsesUsage(raw any) map[string]any {
	u, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	result := map[string]any{}
	for a, b := range map[string]string{"prompt_tokens": "input_tokens", "completion_tokens": "output_tokens", "total_tokens": "total_tokens"} {
		if v, ok := u[a]; ok {
			result[b] = v
		}
	}
	if d, ok := u["prompt_tokens_details"].(map[string]any); ok {
		result["input_tokens_details"] = d
	}
	if d, ok := u["completion_tokens_details"].(map[string]any); ok {
		result["output_tokens_details"] = d
	}
	return result
}

func (w *responsesWriter) nonstream(chat map[string]any) (map[string]any, error) {
	model, _ := chat["model"].(string)
	w.init(model)
	w.usage = responsesUsage(chat["usage"])
	choices, _ := chat["choices"].([]any)
	if len(choices) > 0 {
		choice, _ := choices[0].(map[string]any)
		w.finish, _ = choice["finish_reason"].(string)
		message, _ := choice["message"].(map[string]any)
		text, _ := message["content"].(string)
		calls, _ := message["tool_calls"].([]any)
		if len(calls) > maxResponsesToolCalls {
			return nil, errors.New("upstream returned too many tool calls")
		}
		if text != "" || len(calls) == 0 {
			item := &responsesOutputItem{kind: "message", id: "msg_" + randHex(24), index: len(w.items)}
			item.text.WriteString(text)
			w.items = append(w.items, item)
		}
		for _, raw := range calls {
			call, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("invalid upstream tool call")
			}
			fn, _ := call["function"].(map[string]any)
			id, _ := call["id"].(string)
			name, _ := fn["name"].(string)
			args, _ := fn["arguments"].(string)
			if strings.TrimSpace(name) == "" {
				return nil, errors.New("upstream tool call is missing a function name")
			}
			if id == "" {
				id = "call_" + randHex(24)
			}
			item := &responsesOutputItem{kind: "function_call", id: "fc_" + randHex(24), callID: id, index: len(w.items)}
			item.name.WriteString(name)
			item.text.WriteString(args)
			w.items = append(w.items, item)
		}
	}
	status := "completed"
	if w.finish == "length" || w.finish == "content_filter" {
		status = "incomplete"
	}
	return w.response(status), nil
}

func (w *responsesWriter) emit(typ string, values map[string]any) error {
	if values == nil {
		values = make(map[string]any)
	}
	values["type"], values["sequence_number"] = typ, w.sequence
	w.sequence++
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	w.commit(http.StatusOK)
	data := fmt.Sprintf("event: %s\ndata: %s\n\n", typ, raw)
	n, err := io.WriteString(w.ResponseWriter, data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}

func (w *responsesWriter) start(model string) error {
	if w.started {
		return nil
	}
	w.init(model)
	w.started = true
	if err := w.emit("response.created", map[string]any{"response": w.response("in_progress")}); err != nil {
		return err
	}
	return w.emit("response.in_progress", map[string]any{"response": w.response("in_progress")})
}

func (w *responsesWriter) add(item *responsesOutputItem) error {
	if item.added {
		return nil
	}
	item.added, item.index = true, len(w.items)
	w.items = append(w.items, item)
	if err := w.emit("response.output_item.added", map[string]any{"output_index": item.index, "item": w.item(item, "in_progress")}); err != nil {
		return err
	}
	if item.kind == "message" {
		return w.emit("response.content_part.added", map[string]any{"item_id": item.id, "output_index": item.index, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
	}
	return nil
}

func (w *responsesWriter) streamFrame(frame string) error {
	if w.done {
		return nil
	}
	var data string
	for _, line := range strings.Split(frame, "\n") {
		if strings.HasPrefix(line, "data:") {
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			break
		}
	}
	if data == "" {
		return nil
	}
	if data == "[DONE]" {
		if w.failed {
			w.done = true
			return nil
		}
		if err := w.start(""); err != nil {
			return err
		}
		return w.complete()
	}
	var chunk map[string]any
	if err := json.Unmarshal([]byte(data), &chunk); err != nil || chunk == nil {
		return w.streamFailure("invalid upstream stream event")
	}
	model, _ := chunk["model"].(string)
	if err := w.start(model); err != nil {
		return err
	}
	if v, ok := chunk["error"].(map[string]any); ok {
		w.failed = true
		w.usage = nil
		response := w.response("failed")
		response["error"] = v
		return w.emit("response.failed", map[string]any{"response": response})
	}
	if u := responsesUsage(chunk["usage"]); u != nil {
		w.usage = u
	}
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return nil
	}
	choice, _ := choices[0].(map[string]any)
	if finish, ok := choice["finish_reason"].(string); ok && finish != "" {
		w.finish = finish
	}
	delta, _ := choice["delta"].(map[string]any)
	if text, ok := delta["content"].(string); ok && text != "" {
		if err := w.reserveOutput(len(text)); err != nil {
			return err
		}
		if w.message == nil {
			w.message = &responsesOutputItem{kind: "message", id: "msg_" + randHex(24)}
		}
		if err := w.add(w.message); err != nil {
			return err
		}
		w.message.text.WriteString(text)
		if err := w.emit("response.output_text.delta", map[string]any{"item_id": w.message.id, "output_index": w.message.index, "content_index": 0, "delta": text, "logprobs": []any{}}); err != nil {
			return err
		}
	}
	if calls, ok := delta["tool_calls"].([]any); ok {
		for pos, raw := range calls {
			call, ok := raw.(map[string]any)
			if !ok {
				return w.streamFailure("invalid upstream tool call")
			}
			idx := pos
			if rawIndex, exists := call["index"]; exists {
				n, ok := rawIndex.(float64)
				if !ok || n < 0 || n > (1<<31)-1 || math.Trunc(n) != n {
					return w.streamFailure("invalid upstream tool call index")
				}
				idx = int(n)
			}
			item := w.toolCalls[idx]
			if item == nil {
				if len(w.toolCalls) >= maxResponsesToolCalls {
					return w.streamFailure("upstream returned too many tool calls")
				}
				item = &responsesOutputItem{kind: "function_call", id: "fc_" + randHex(24)}
				w.toolCalls[idx] = item
			}
			if id, ok := call["id"].(string); ok && id != "" {
				if err := w.reserveOutput(len(id) - len(item.callID)); err != nil {
					return err
				}
				item.callID = id
			}
			if fn, ok := call["function"].(map[string]any); ok {
				if name, ok := fn["name"].(string); ok {
					if err := w.reserveOutput(len(name)); err != nil {
						return err
					}
					item.name.WriteString(name)
				}
				if args, ok := fn["arguments"].(string); ok {
					if err := w.reserveOutput(len(args)); err != nil {
						return err
					}
					item.text.WriteString(args)
				}
			}
		}
	}
	return nil
}

func (w *responsesWriter) reserveOutput(n int) error {
	if n > maxBodyBytes-w.outputBytes {
		return w.streamFailure("upstream stream output exceeds the size limit")
	}
	w.outputBytes += n
	return nil
}

func (w *responsesWriter) toolDelta(item *responsesOutputItem) error {
	args := item.text.String()
	if item.emitted == len(args) {
		return nil
	}
	delta := args[item.emitted:]
	item.emitted = len(args)
	return w.emit("response.function_call_arguments.delta", map[string]any{"item_id": item.id, "output_index": item.index, "delta": delta})
}

func (w *responsesWriter) complete() error {
	keys := make([]int, 0, len(w.toolCalls))
	for k := range w.toolCalls {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		item := w.toolCalls[k]
		if strings.TrimSpace(item.name.String()) == "" {
			return w.streamFailure("upstream tool call is missing a function name")
		}
	}
	for _, k := range keys {
		item := w.toolCalls[k]
		if item.callID == "" {
			item.callID = "call_" + randHex(24)
		}
		if err := w.add(item); err != nil {
			return err
		}
		if err := w.toolDelta(item); err != nil {
			return err
		}
	}
	if len(w.items) == 0 {
		w.message = &responsesOutputItem{kind: "message", id: "msg_" + randHex(24)}
		if err := w.add(w.message); err != nil {
			return err
		}
	}
	for _, item := range w.items {
		if item.kind == "message" {
			if err := w.emit("response.output_text.done", map[string]any{"item_id": item.id, "output_index": item.index, "content_index": 0, "text": item.text.String(), "logprobs": []any{}}); err != nil {
				return err
			}
			if err := w.emit("response.content_part.done", map[string]any{"item_id": item.id, "output_index": item.index, "content_index": 0, "part": map[string]any{"type": "output_text", "text": item.text.String(), "annotations": []any{}}}); err != nil {
				return err
			}
		} else if err := w.emit("response.function_call_arguments.done", map[string]any{"item_id": item.id, "output_index": item.index, "name": item.name.String(), "arguments": item.text.String()}); err != nil {
			return err
		}
		itemStatus := "completed"
		if w.finish == "length" || w.finish == "content_filter" {
			itemStatus = "incomplete"
		}
		if err := w.emit("response.output_item.done", map[string]any{"output_index": item.index, "item": w.item(item, itemStatus)}); err != nil {
			return err
		}
	}
	status, event := "completed", "response.completed"
	if w.finish == "length" || w.finish == "content_filter" {
		status, event = "incomplete", "response.incomplete"
	}
	w.done = true
	return w.emit(event, map[string]any{"response": w.response(status)})
}
