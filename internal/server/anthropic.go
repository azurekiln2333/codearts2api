package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Retain chat's admission, deadlines, account selection, cleanup and audit.
func (h *Handler) anthropicMessages(w http.ResponseWriter, r *http.Request) {
	if h.cfg.APIKey != "" {
		key := r.Header.Get("x-api-key")
		if key == "" {
			auth := r.Header.Get("Authorization")
			if len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
				key = auth[7:]
			}
		}
		if subtle.ConstantTimeCompare([]byte(key), []byte(h.cfg.APIKey)) != 1 {
			writeAnthropicError(w, http.StatusUnauthorized, "authentication_error", "missing or invalid API key")
			return
		}
	}
	adapted := &anthropicResponseWriter{ResponseWriter: w}
	clone := r.Clone(r.Context())
	clone.Body = &anthropicRequestBody{source: r.Body}
	clone.ContentLength = -1
	clone.GetBody = nil
	h.chatCompletions(adapted, clone)
	if r.Context().Err() == nil {
		adapted.finish()
	}
}

func writeAnthropicError(w http.ResponseWriter, status int, typ, message string) {
	writeJSON(w, status, map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": message}})
}

// Convert lazily so chatCompletions admits the request and sets read deadlines
// before consuming the original body.
type anthropicRequestBody struct {
	source io.ReadCloser
	once   sync.Once
	reader io.Reader
	err    error
}

func (b *anthropicRequestBody) Read(p []byte) (int, error) {
	b.once.Do(func() {
		raw, err := io.ReadAll(io.LimitReader(b.source, maxBodyBytes+1))
		if err != nil {
			b.err = err
			return
		}
		if len(raw) > maxBodyBytes {
			b.reader = io.LimitReader(anthropicZeroReader{}, maxBodyBytes+1)
			return
		}
		converted, err := anthropicToChat(raw)
		if err != nil {
			b.err = err
			return
		}
		b.reader = bytes.NewReader(converted)
	})
	if b.err != nil {
		return 0, b.err
	}
	return b.reader.Read(p)
}

func (b *anthropicRequestBody) Close() error { return b.source.Close() }

type anthropicZeroReader struct{}

func (anthropicZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type anthropicInput struct {
	Model       string            `json:"model"`
	MaxTokens   *int              `json:"max_tokens"`
	Messages    []json.RawMessage `json:"messages"`
	System      json.RawMessage   `json:"system"`
	Stream      bool              `json:"stream"`
	Temperature *float64          `json:"temperature"`
	TopP        *float64          `json:"top_p"`
	Tools       []json.RawMessage `json:"tools"`
	ToolChoice  json.RawMessage   `json:"tool_choice"`
}

func anthropicToChat(raw []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("invalid Messages request JSON")
	}
	allowed := map[string]bool{"model": true, "max_tokens": true, "messages": true, "system": true, "stream": true, "temperature": true, "top_p": true, "tools": true, "tool_choice": true, "metadata": true}
	for key, value := range fields {
		if !allowed[key] && string(value) != "null" {
			return nil, fmt.Errorf("unsupported Messages parameter: %s", key)
		}
	}
	var in anthropicInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("invalid Messages request fields")
	}
	if strings.TrimSpace(in.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if in.MaxTokens == nil || *in.MaxTokens <= 0 {
		return nil, fmt.Errorf("max_tokens must be greater than 0")
	}
	if len(in.Messages) == 0 {
		return nil, fmt.Errorf("messages is empty")
	}
	messages := make([]map[string]any, 0, len(in.Messages)+1)
	if len(in.System) > 0 && string(in.System) != "null" {
		system, err := anthropicText(in.System)
		if err != nil {
			return nil, fmt.Errorf("system: %w", err)
		}
		if system != "" {
			messages = append(messages, map[string]any{"role": "system", "content": system})
		}
	}
	for i, item := range in.Messages {
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(item, &m); err != nil || (m.Role != "user" && m.Role != "assistant") {
			return nil, fmt.Errorf("messages[%d] must have user or assistant role", i)
		}
		converted, err := anthropicMessage(m.Role, m.Content)
		if err != nil {
			return nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		messages = append(messages, converted...)
	}
	tools := make([]map[string]any, 0, len(in.Tools))
	for i, rawTool := range in.Tools {
		var tool struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		}
		if err := json.Unmarshal(rawTool, &tool); err != nil || tool.Name == "" || (tool.Type != "" && tool.Type != "custom") {
			return nil, fmt.Errorf("tools[%d] must be a custom tool with a name", i)
		}
		var schema map[string]json.RawMessage
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil || schema == nil {
			return nil, fmt.Errorf("tools[%d].input_schema must be an object", i)
		}
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.InputSchema}})
	}
	choice, err := anthropicToolChoice(in.ToolChoice)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"model": in.Model, "messages": messages, "max_tokens": *in.MaxTokens, "stream": in.Stream}
	if in.Stream {
		out["stream_options"] = map[string]any{"include_usage": true}
	}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if len(tools) > 0 {
		out["tools"] = tools
	}
	if choice != nil {
		out["tool_choice"] = choice
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode Messages request: %w", err)
	}
	return encoded, nil
}

func anthropicText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil || blocks == nil {
		return "", fmt.Errorf("content must be text or text blocks")
	}
	var out strings.Builder
	for _, block := range blocks {
		if block.Type != "text" {
			return "", fmt.Errorf("unsupported content block type: %s", block.Type)
		}
		out.WriteString(block.Text)
	}
	return out.String(), nil
}

func anthropicMessage(role string, raw json.RawMessage) ([]map[string]any, error) {
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return []map[string]any{{"role": role, "content": plain}}, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil || len(blocks) == 0 {
		return nil, fmt.Errorf("content must be text or non-empty content blocks")
	}
	var result []map[string]any
	var text strings.Builder
	var calls []map[string]any
	flush := func() {
		if text.Len() == 0 && len(calls) == 0 {
			return
		}
		m := map[string]any{"role": role, "content": text.String()}
		if len(calls) > 0 {
			m["tool_calls"] = calls
		}
		result = append(result, m)
		text.Reset()
		calls = nil
	}
	for _, rawBlock := range blocks {
		var block struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		}
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			return nil, fmt.Errorf("invalid content block")
		}
		switch block.Type {
		case "text":
			if len(calls) > 0 {
				flush()
			}
			text.WriteString(block.Text)
		case "tool_use":
			if role != "assistant" || block.ID == "" || block.Name == "" {
				return nil, fmt.Errorf("tool_use requires assistant role, id and name")
			}
			var input map[string]json.RawMessage
			if json.Unmarshal(block.Input, &input) != nil || input == nil {
				return nil, fmt.Errorf("tool_use input must be an object")
			}
			calls = append(calls, map[string]any{"id": block.ID, "type": "function", "function": map[string]any{"name": block.Name, "arguments": string(block.Input)}})
		case "tool_result":
			if role != "user" || block.ToolUseID == "" {
				return nil, fmt.Errorf("tool_result requires user role and tool_use_id")
			}
			content, err := anthropicText(block.Content)
			if err != nil {
				return nil, fmt.Errorf("tool_result: %w", err)
			}
			flush()
			if block.IsError {
				content = "[tool error] " + content
			}
			result = append(result, map[string]any{"role": "tool", "tool_call_id": block.ToolUseID, "content": content})
		default:
			return nil, fmt.Errorf("unsupported content block type: %s", block.Type)
		}
	}
	flush()
	if len(result) == 0 {
		return nil, fmt.Errorf("content is empty")
	}
	return result, nil
}

func anthropicToolChoice(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var choice struct {
		Type               string `json:"type"`
		Name               string `json:"name"`
		DisableParallelUse bool   `json:"disable_parallel_tool_use"`
	}
	if json.Unmarshal(raw, &choice) != nil || choice.DisableParallelUse {
		return nil, fmt.Errorf("unsupported tool_choice")
	}
	switch choice.Type {
	case "auto":
		return "auto", nil
	case "any":
		return "required", nil
	case "none":
		return "none", nil
	case "tool":
		if choice.Name != "" {
			return map[string]any{"type": "function", "function": map[string]any{"name": choice.Name}}, nil
		}
	}
	return nil, fmt.Errorf("unsupported tool_choice")
}
