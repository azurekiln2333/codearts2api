package server

// The Responses adapter uses the chat lifecycle for all inference. Input
// conversion happens when that handler reads the body, after admission.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type responsesOptions struct {
	stream                                                                  bool
	model                                                                   string
	instructions, metadata, tools, toolChoice, maxTokens, temperature, topP any
}

type responsesBody struct {
	source io.ReadCloser
	opts   *responsesOptions
	reader *bytes.Reader
	err    error
	loaded bool
}

func (b *responsesBody) Read(p []byte) (int, error) {
	if !b.loaded {
		b.loaded = true
		raw, err := io.ReadAll(io.LimitReader(b.source, maxBodyBytes+1))
		if err != nil {
			b.err = err
		} else if len(raw) > maxBodyBytes {
			// The chat handler preserves its normal 413 response.
			b.reader = bytes.NewReader(raw)
		} else {
			var translated []byte
			translated, b.err = responsesToChat(raw, b.opts)
			if b.err == nil {
				b.reader = bytes.NewReader(translated)
			}
		}
	}
	if b.err != nil {
		return 0, b.err
	}
	return b.reader.Read(p)
}

func (b *responsesBody) Close() error { return b.source.Close() }

func (h *Handler) responses(w http.ResponseWriter, r *http.Request) {
	opts := &responsesOptions{}
	req := r.Clone(r.Context())
	req.Body = &responsesBody{source: r.Body, opts: opts}
	req.ContentLength = -1
	h.chatCompletions(&responsesWriter{ResponseWriter: w, opts: opts}, req)
}

func responsesToChat(raw []byte, opts *responsesOptions) ([]byte, error) {
	var req map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil || req == nil {
		return nil, errors.New("invalid Responses JSON object")
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, errors.New("expected one Responses JSON object")
	}
	allowed := map[string]bool{
		"model": true, "input": true, "instructions": true, "metadata": true,
		"tools": true, "tool_choice": true, "max_output_tokens": true,
		"temperature": true, "top_p": true, "stream": true, "stream_options": true,
		"reasoning": true, "text": true, "store": true, "truncation": true,
		"parallel_tool_calls": true, "include": true,
		"previous_response_id": true, "conversation": true, "prompt": true, "background": true,
	}
	for key, value := range req {
		if !allowed[key] && value != nil {
			return nil, fmt.Errorf("%s is not supported", key)
		}
	}
	for _, key := range []string{"previous_response_id", "conversation", "prompt", "background"} {
		if v, ok := req[key]; ok && v != nil && v != false && v != "" {
			return nil, fmt.Errorf("%s is not supported", key)
		}
	}
	if v, ok := req["store"]; ok && v != nil && v != false {
		return nil, errors.New("store is not supported; use store=false")
	}
	if v, ok := req["truncation"]; ok && v != nil && v != "disabled" {
		return nil, errors.New("truncation is not supported")
	}
	if v, ok := req["parallel_tool_calls"]; ok && v != nil && v != true {
		return nil, errors.New("parallel_tool_calls=false is not supported")
	}
	if v, ok := req["include"]; ok && v != nil {
		include, ok := v.([]any)
		if !ok || len(include) != 0 {
			return nil, errors.New("include is not supported")
		}
	}
	if v, ok := req["stream_options"]; ok && v != nil {
		options, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("stream_options must be an object")
		}
		for key, value := range options {
			if key != "include_obfuscation" || (value != nil && value != false) {
				return nil, fmt.Errorf("stream_options.%s is not supported", key)
			}
		}
	}
	if v, ok := req["text"]; ok && v != nil {
		text, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("text must be an object")
		}
		for key, value := range text {
			if key != "format" && value != nil {
				return nil, fmt.Errorf("text.%s is not supported", key)
			}
		}
		if value := text["format"]; value != nil {
			format, ok := value.(map[string]any)
			if !ok || format["type"] != "text" || len(format) != 1 {
				return nil, errors.New("non-text output formats are not supported")
			}
		}
	}
	if v, ok := req["reasoning"]; ok && v != nil {
		reasoning, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("reasoning must be an object")
		}
		for key, value := range reasoning {
			if key != "effort" && value != nil {
				return nil, fmt.Errorf("reasoning.%s is not supported", key)
			}
		}
		if effort := reasoning["effort"]; effort != nil {
			if _, ok := effort.(string); !ok {
				return nil, errors.New("reasoning.effort must be a string")
			}
		}
	}
	if v, ok := req["model"]; ok && v != nil {
		if _, ok := v.(string); !ok {
			return nil, errors.New("model must be a string")
		}
	}
	if v, ok := req["metadata"]; ok && v != nil {
		metadata, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("metadata must be an object")
		}
		for _, value := range metadata {
			if _, ok := value.(string); !ok {
				return nil, errors.New("metadata values must be strings")
			}
		}
	}
	opts.model, _ = req["model"].(string)
	opts.instructions, opts.metadata, opts.tools = req["instructions"], req["metadata"], req["tools"]
	opts.toolChoice, opts.maxTokens, opts.temperature, opts.topP = req["tool_choice"], req["max_output_tokens"], req["temperature"], req["top_p"]
	if v, ok := req["stream"]; ok && v != nil {
		var valid bool
		opts.stream, valid = v.(bool)
		if !valid {
			return nil, errors.New("stream must be a boolean")
		}
	}
	chat := map[string]any{"model": opts.model, "stream": opts.stream}
	if opts.stream {
		chat["stream_options"] = map[string]any{"include_usage": true}
	}
	for from, to := range map[string]string{"max_output_tokens": "max_completion_tokens", "temperature": "temperature", "top_p": "top_p"} {
		if v, ok := req[from]; ok && v != nil {
			chat[to] = v
		}
	}
	if v, ok := req["reasoning"].(map[string]any); ok && v["effort"] != nil {
		chat["reasoning_effort"] = v["effort"]
	}
	var messages []map[string]any
	if v, ok := req["instructions"]; ok && v != nil {
		text, ok := v.(string)
		if !ok {
			return nil, errors.New("instructions must be a string")
		}
		if text != "" {
			messages = append(messages, map[string]any{"role": "system", "content": text})
		}
	}
	callResults := make(map[string]bool)
	pendingCalls, hasToolOutput := 0, false
	switch input := req["input"].(type) {
	case string:
		messages = append(messages, map[string]any{"role": "user", "content": input})
	case []any:
		for i, rawItem := range input {
			item, ok := rawItem.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("input[%d] must be an object", i)
			}
			typ, _ := item["type"].(string)
			switch typ {
			case "", "message":
				if pendingCalls != 0 {
					return nil, fmt.Errorf("input[%d] interrupts pending function call results", i)
				}
				role, ok := item["role"].(string)
				if !ok || (role != "system" && role != "developer" && role != "user" && role != "assistant") {
					return nil, fmt.Errorf("input[%d] has unsupported message role", i)
				}
				if role == "developer" {
					role = "system"
				}
				text, err := responsesInputText(item["content"])
				if err != nil {
					return nil, fmt.Errorf("input[%d]: %w", i, err)
				}
				messages = append(messages, map[string]any{"role": role, "content": text})
			case "function_call":
				if pendingCalls != 0 && hasToolOutput {
					return nil, fmt.Errorf("input[%d] interrupts pending function call results", i)
				}
				id, _ := item["call_id"].(string)
				name, _ := item["name"].(string)
				args, aok := item["arguments"].(string)
				if id == "" || name == "" || !aok {
					return nil, fmt.Errorf("input[%d] function_call requires call_id, name and arguments", i)
				}
				if _, exists := callResults[id]; exists {
					return nil, fmt.Errorf("input[%d] duplicate function call_id", i)
				}
				callResults[id] = false
				pendingCalls++
				call := map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}}
				if n := len(messages); n > 0 && messages[n-1]["role"] == "assistant" && messages[n-1]["tool_calls"] != nil {
					messages[n-1]["tool_calls"] = append(messages[n-1]["tool_calls"].([]any), call)
				} else {
					messages = append(messages, map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{call}})
				}
			case "function_call_output":
				id, _ := item["call_id"].(string)
				if id == "" {
					return nil, fmt.Errorf("input[%d] function_call_output requires call_id", i)
				}
				completed, exists := callResults[id]
				if !exists || completed {
					return nil, fmt.Errorf("input[%d] function_call_output has no matching pending call", i)
				}
				callResults[id] = true
				pendingCalls--
				hasToolOutput = pendingCalls != 0
				text, err := responsesInputText(item["output"])
				if err != nil {
					return nil, fmt.Errorf("input[%d] output: %w", i, err)
				}
				messages = append(messages, map[string]any{"role": "tool", "tool_call_id": id, "content": text})
			default:
				return nil, fmt.Errorf("input[%d] type %q is not supported", i, typ)
			}
		}
	default:
		return nil, errors.New("input must be a string or an array")
	}
	for _, completed := range callResults {
		if !completed {
			return nil, errors.New("function_call requires a matching function_call_output")
		}
	}
	chat["messages"] = messages
	if rawTools, exists := req["tools"]; exists && rawTools != nil {
		tools, ok := rawTools.([]any)
		if !ok {
			return nil, errors.New("tools must be an array")
		}
		converted := make([]any, 0, len(tools))
		for i, rawTool := range tools {
			tool, ok := rawTool.(map[string]any)
			if !ok || tool["type"] != "function" {
				return nil, fmt.Errorf("tools[%d] type is not supported; only functions are supported", i)
			}
			for key, value := range tool {
				if key != "type" && key != "name" && key != "description" && key != "parameters" && key != "strict" && value != nil {
					return nil, fmt.Errorf("tools[%d].%s is not supported", i, key)
				}
			}
			name, ok := tool["name"].(string)
			if !ok || name == "" {
				return nil, fmt.Errorf("tools[%d] requires name", i)
			}
			fn := map[string]any{"name": name}
			for _, key := range []string{"description", "parameters", "strict"} {
				if v, ok := tool[key]; ok {
					fn[key] = v
				}
			}
			converted = append(converted, map[string]any{"type": "function", "function": fn})
		}
		chat["tools"] = converted
	}
	if choice, ok := req["tool_choice"]; ok && choice != nil {
		switch c := choice.(type) {
		case string:
			if c != "auto" && c != "none" && c != "required" {
				return nil, errors.New("unsupported tool_choice")
			}
			chat["tool_choice"] = c
		case map[string]any:
			name, ok := c["name"].(string)
			if c["type"] != "function" || !ok || name == "" {
				return nil, errors.New("unsupported tool_choice")
			}
			chat["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": name}}
		default:
			return nil, errors.New("unsupported tool_choice")
		}
	}
	return json.Marshal(chat)
}

func responsesInputText(content any) (string, error) {
	switch v := content.(type) {
	case string:
		return v, nil
	case []any:
		var out strings.Builder
		for _, raw := range v {
			part, ok := raw.(map[string]any)
			if !ok {
				return "", errors.New("content part must be an object")
			}
			typ, _ := part["type"].(string)
			text, valid := part["text"].(string)
			if !valid || (typ != "input_text" && typ != "output_text" && typ != "text") {
				return "", fmt.Errorf("content type %q is not supported", typ)
			}
			out.WriteString(text)
		}
		return out.String(), nil
	default:
		return "", errors.New("content must be text")
	}
}
