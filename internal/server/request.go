// OpenAI chat.completions 请求解析与会话指纹。
//
// 支持 system/developer/user/assistant/tool 角色、纯文本 content、
// assistant 的 tool_calls 和 tool 结果的 tool_call_id。
package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// openAIToolCall 一次函数调用（assistant 历史或响应里用）。
type openAIToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON 字符串（OpenAI 线格式）
}

// openAIMessage 拍平后的一条消息。
type openAIMessage struct {
	Role             string
	Text             string
	ReasoningContent string
	ToolCalls        []openAIToolCall
	ToolCallID       string // role=tool 时对应的 call id
	Name             string // role=tool 时的函数名（部分客户端会带）
}

// toolChoiceOpenAI 归一化后的 tool_choice。
type toolChoiceOpenAI struct {
	Mode     string // "" / "none" / "auto" / "required" / "function"
	Function string // Mode=="function" 时指定的函数名
}

// chatRequest 完整请求体。
type chatRequest struct {
	Model           string
	Stream          bool
	IncludeUsage    bool
	ReasoningEffort string
	MaxTokens       *int
	Temperature     *float64
	TopP            *float64
	Messages        []openAIMessage
	Tools           []map[string]any
	ToolChoice      toolChoiceOpenAI
	ConversationID  string
}

// parseChatRequest 解析并校验请求体。
func parseChatRequest(body []byte) (*chatRequest, error) {
	var raw struct {
		Model         string `json:"model"`
		Stream        bool   `json:"stream"`
		StreamOptions struct {
			IncludeUsage       bool  `json:"include_usage"`
			IncludeObfuscation *bool `json:"include_obfuscation"`
		} `json:"stream_options"`
		ReasoningEffort     string            `json:"reasoning_effort"`
		MaxTokens           *int              `json:"max_tokens"`
		MaxCompletionTokens *int              `json:"max_completion_tokens"`
		Temperature         *float64          `json:"temperature"`
		TopP                *float64          `json:"top_p"`
		ConversationID      string            `json:"conversation_id"`
		Messages            []json.RawMessage `json:"messages"`
		Tools               []map[string]any  `json:"tools"`
		ToolChoice          json.RawMessage   `json:"tool_choice"`
		N                   *int              `json:"n"`
		ResponseFormat      json.RawMessage   `json:"response_format"`
		Stop                json.RawMessage   `json:"stop"`
		ParallelToolCalls   *bool             `json:"parallel_tool_calls"`
		FrequencyPenalty    *float64          `json:"frequency_penalty"`
		PresencePenalty     *float64          `json:"presence_penalty"`
		LogitBias           map[string]any    `json:"logit_bias"`
		Seed                json.RawMessage   `json:"seed"`
		Logprobs            *bool             `json:"logprobs"`
		TopLogprobs         *int              `json:"top_logprobs"`
		Modalities          []string          `json:"modalities"`
		Audio               json.RawMessage   `json:"audio"`
		Prediction          json.RawMessage   `json:"prediction"`
		Functions           []json.RawMessage `json:"functions"`
		FunctionCall        json.RawMessage   `json:"function_call"`
	}
	if err := decodeJSONNumbers(body, &raw); err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}
	if len(raw.Messages) == 0 {
		return nil, errors.New("messages is empty")
	}
	if raw.N != nil && *raw.N != 1 {
		return nil, errors.New("n must be 1; multiple choices are not supported")
	}
	if nonNullJSON(raw.ResponseFormat) {
		var format map[string]json.RawMessage
		if err := json.Unmarshal(raw.ResponseFormat, &format); err != nil || len(format) != 1 {
			return nil, errors.New("response_format only supports {\"type\":\"text\"}")
		}
		var kind string
		if err := json.Unmarshal(format["type"], &kind); err != nil || kind != "text" {
			return nil, errors.New("response_format only supports {\"type\":\"text\"}")
		}
	}
	if nonNullJSON(raw.Stop) {
		return nil, errors.New("stop is not supported")
	}
	choice, err := parseToolChoice(raw.ToolChoice)
	if err != nil {
		return nil, err
	}
	if choice.Mode == "required" && len(raw.Tools) == 0 {
		return nil, errors.New("tool_choice required needs at least one tool")
	}
	if raw.ParallelToolCalls != nil && !*raw.ParallelToolCalls && len(raw.Tools) > 0 && choice.Mode != "none" {
		return nil, errors.New("parallel_tool_calls=false is not supported")
	}
	if raw.FrequencyPenalty != nil && *raw.FrequencyPenalty != 0 {
		return nil, errors.New("frequency_penalty is not supported except for 0")
	}
	if raw.PresencePenalty != nil && *raw.PresencePenalty != 0 {
		return nil, errors.New("presence_penalty is not supported except for 0")
	}
	if len(raw.LogitBias) != 0 {
		return nil, errors.New("logit_bias is not supported")
	}
	if raw.Logprobs != nil && *raw.Logprobs {
		return nil, errors.New("logprobs is not supported")
	}
	if raw.TopLogprobs != nil && *raw.TopLogprobs != 0 {
		return nil, errors.New("top_logprobs is not supported except for 0")
	}
	if raw.Modalities != nil && (len(raw.Modalities) != 1 || raw.Modalities[0] != "text") {
		return nil, errors.New("modalities only supports text")
	}
	if len(raw.Functions) != 0 {
		return nil, errors.New("functions is not supported; use tools")
	}
	if nonNullJSON(raw.FunctionCall) {
		var choice string
		if err := json.Unmarshal(raw.FunctionCall, &choice); err != nil || choice != "auto" {
			return nil, errors.New("function_call is not supported; use tool_choice")
		}
	}
	if raw.StreamOptions.IncludeObfuscation != nil && *raw.StreamOptions.IncludeObfuscation {
		return nil, errors.New("stream_options.include_obfuscation is not supported")
	}
	for _, field := range []struct {
		name  string
		value json.RawMessage
	}{
		{"seed", raw.Seed},
		{"audio", raw.Audio}, {"prediction", raw.Prediction},
	} {
		if nonNullJSON(field.value) {
			return nil, fmt.Errorf("%s is not supported", field.name)
		}
	}
	req := &chatRequest{
		Model:           raw.Model,
		Stream:          raw.Stream,
		IncludeUsage:    raw.StreamOptions.IncludeUsage,
		ReasoningEffort: strings.ToLower(strings.TrimSpace(raw.ReasoningEffort)),
		MaxTokens:       raw.MaxTokens,
		Temperature:     raw.Temperature,
		TopP:            raw.TopP,
		ConversationID:  raw.ConversationID,
		Tools:           raw.Tools,
		ToolChoice:      choice,
	}
	if raw.MaxCompletionTokens != nil {
		req.MaxTokens = raw.MaxCompletionTokens
	}
	if req.MaxTokens != nil && *req.MaxTokens <= 0 {
		return nil, errors.New("max_tokens must be greater than 0")
	}
	if req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 2) {
		return nil, errors.New("temperature must be between 0 and 2")
	}
	if req.TopP != nil && (*req.TopP < 0 || *req.TopP > 1) {
		return nil, errors.New("top_p must be between 0 and 1")
	}
	for i, rm := range raw.Messages {
		m, err := parseMessage(rm)
		if err != nil {
			return nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		req.Messages = append(req.Messages, m)
	}
	if err := validateToolHistory(req.Messages); err != nil {
		return nil, err
	}
	// 至少要有一条 user 或 tool 消息（否则没有可发送的内容）。
	hasSendable := false
	for _, m := range req.Messages {
		if m.Role == "user" || m.Role == "tool" {
			hasSendable = true
			break
		}
	}
	if !hasSendable {
		return nil, errors.New("no user or tool message found")
	}
	return req, nil
}

func validateToolHistory(messages []openAIMessage) error {
	seen := make(map[string]struct{})
	pending := make(map[string]struct{})
	for i, message := range messages {
		if len(pending) != 0 && message.Role != "tool" {
			return fmt.Errorf("messages[%d]: tool_calls require matching tool results before the next message", i)
		}
		switch message.Role {
		case "assistant":
			for _, call := range message.ToolCalls {
				if _, exists := seen[call.ID]; exists {
					return fmt.Errorf("messages[%d]: duplicate tool_calls id", i)
				}
				seen[call.ID] = struct{}{}
				pending[call.ID] = struct{}{}
			}
		case "tool":
			if _, exists := pending[message.ToolCallID]; !exists || message.ToolCallID == "" {
				return fmt.Errorf("messages[%d]: tool_call_id requires a preceding pending assistant tool call", i)
			}
			delete(pending, message.ToolCallID)
		}
	}
	if len(pending) != 0 {
		return errors.New("tool_calls require matching tool results")
	}
	return nil
}

func nonNullJSON(raw json.RawMessage) bool {
	return len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// decodeJSONNumbers keeps exact numeric values in tool schemas and arguments.
func decodeJSONNumbers(body []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

// parseMessage 解析单条消息：content 支持 string 与 [{type:text,text}] 分片。
func parseMessage(rm json.RawMessage) (openAIMessage, error) {
	var probe struct {
		Role             string          `json:"role"`
		Content          json.RawMessage `json:"content"`
		ReasoningContent string          `json:"reasoning_content"`
		ToolCalls        json.RawMessage `json:"tool_calls"`
		ToolCallID       string          `json:"tool_call_id"`
		Name             string          `json:"name"`
		FunctionCall     json.RawMessage `json:"function_call"`
		Audio            json.RawMessage `json:"audio"`
		Refusal          json.RawMessage `json:"refusal"`
	}
	if err := json.Unmarshal(rm, &probe); err != nil {
		return openAIMessage{}, err
	}
	role := strings.ToLower(strings.TrimSpace(probe.Role))
	switch role {
	case "system", "developer", "user", "assistant", "tool":
	default:
		return openAIMessage{}, errors.New("unsupported or missing message role")
	}
	if probe.ToolCallID != "" && role != "tool" {
		return openAIMessage{}, errors.New("tool_call_id is only supported on tool messages")
	}
	for _, field := range []struct {
		name  string
		value json.RawMessage
	}{
		{"function_call", probe.FunctionCall}, {"audio", probe.Audio}, {"refusal", probe.Refusal},
	} {
		if nonNullJSON(field.value) {
			return openAIMessage{}, fmt.Errorf("message %s is not supported", field.name)
		}
	}
	text, err := flattenContent(probe.Content)
	if err != nil {
		return openAIMessage{}, err
	}
	m := openAIMessage{
		Role:             role,
		Text:             text,
		ReasoningContent: probe.ReasoningContent,
		ToolCallID:       probe.ToolCallID,
		Name:             probe.Name,
	}
	if nonNullJSON(probe.ToolCalls) {
		var calls []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"function"`
		}
		if err := json.Unmarshal(probe.ToolCalls, &calls); err != nil {
			return openAIMessage{}, errors.New("tool_calls must be an array of function calls")
		}
		if len(calls) > 0 && role != "assistant" {
			return openAIMessage{}, errors.New("tool_calls are only supported on assistant messages")
		}
		for i, c := range calls {
			if (c.Type != "" && c.Type != "function") || c.ID == "" || c.Function.Name == "" {
				return openAIMessage{}, fmt.Errorf("tool_calls[%d] requires a function type, id and name", i)
			}
			m.ToolCalls = append(m.ToolCalls, openAIToolCall{
				ID:        c.ID,
				Name:      c.Function.Name,
				Arguments: rawJSONString(c.Function.Arguments),
			})
		}
	}
	return m, nil
}

// flattenContent rejects non-text parts instead of sending incomplete messages.
func flattenContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", errors.New("content must be a string, null, or an array of text parts")
	}
	var sb strings.Builder
	for i, rawPart := range parts {
		var part struct {
			Type string  `json:"type"`
			Text *string `json:"text"`
		}
		if err := json.Unmarshal(rawPart, &part); err != nil {
			return "", fmt.Errorf("content[%d] must be a text part", i)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(rawPart, &fields); err != nil {
			return "", fmt.Errorf("content[%d] must be a text part", i)
		}
		for name, value := range fields {
			switch strings.ToLower(name) {
			case "image_url", "imageurl", "image", "input_image", "inputimage",
				"input_audio", "inputaudio", "audio", "audio_url", "audiourl",
				"file", "input_file", "inputfile", "video", "video_url", "videourl", "input_video", "inputvideo":
				if nonNullJSON(value) {
					return "", fmt.Errorf("content[%d]: unsupported media field %q; only text is supported", i, name)
				}
			}
		}
		switch part.Type {
		case "", "text", "input_text", "output_text":
		default:
			return "", fmt.Errorf("content[%d]: unsupported content type %q; only text is supported", i, part.Type)
		}
		if part.Text == nil {
			return "", fmt.Errorf("content[%d]: text must be a string", i)
		}
		sb.WriteString(*part.Text)
	}
	return sb.String(), nil
}

// rawJSONString 把 arguments 统一成 JSON 字符串（兼容字符串与对象两种线格式）。
func rawJSONString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return "{}"
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var v any
	if err := decodeJSONNumbers(raw, &v); err != nil {
		return "{}"
	}
	out, err := json.Marshal(v) // Go map marshal 按 key 排序，天然归一化
	if err != nil {
		return "{}"
	}
	return string(out)
}

// parseToolChoice 归一化 tool_choice：字符串或 {"type":"function","function":{"name":...}}。
func parseToolChoice(raw json.RawMessage) (toolChoiceOpenAI, error) {
	if !nonNullJSON(raw) {
		return toolChoiceOpenAI{Mode: "auto"}, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch strings.ToLower(s) {
		case "auto", "none", "required":
			return toolChoiceOpenAI{Mode: strings.ToLower(s)}, nil
		}
		return toolChoiceOpenAI{}, errors.New("unsupported tool_choice")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) != 2 {
		return toolChoiceOpenAI{}, errors.New("unsupported tool_choice")
	}
	var kind string
	var function map[string]json.RawMessage
	if json.Unmarshal(obj["type"], &kind) != nil || kind != "function" ||
		json.Unmarshal(obj["function"], &function) != nil || len(function) != 1 {
		return toolChoiceOpenAI{}, errors.New("unsupported tool_choice")
	}
	var name string
	if json.Unmarshal(function["name"], &name) != nil || strings.TrimSpace(name) == "" {
		return toolChoiceOpenAI{}, errors.New("tool_choice function requires a name")
	}
	return toolChoiceOpenAI{Mode: "function", Function: name}, nil
}

// ---------------------------------------------------------------------------
// 会话指纹（无状态客户端的历史对齐）
// ---------------------------------------------------------------------------

// canonical 消息归一化串：指纹链的输入。client 回发的 assistant 消息
// 由我们生成（tool_calls id 也是我们发的），因此可逐字节复算。
func canonical(m openAIMessage) string {
	var sb strings.Builder
	if m.Name != "" {
		fmt.Fprintf(&sb, "\x00name:%d:", len(m.Name))
		sb.WriteString(m.Name)
	}
	sb.WriteString(m.Role)
	sb.WriteString("\x00")
	sb.WriteString(m.Text)
	for _, c := range m.ToolCalls {
		sb.WriteString("\x00")
		sb.WriteString(c.ID)
		sb.WriteString("\x00")
		sb.WriteString(c.Name)
		sb.WriteString("\x00")
		sb.WriteString(normalizeArgs(c.Arguments))
	}
	if m.ToolCallID != "" {
		sb.WriteString("\x00tool:")
		sb.WriteString(m.ToolCallID)
	}
	return sb.String()
}

// normalizeArgs 归一化 arguments（key 排序、去空白）。
func normalizeArgs(args string) string {
	if args == "" {
		return "{}"
	}
	var v any
	if err := decodeJSONNumbers([]byte(args), &v); err != nil {
		return args
	}
	out, err := json.Marshal(v)
	if err != nil {
		return args
	}
	return string(out)
}

// chainFingerprint 链式指纹：fp_i = sha256(fp_{i-1} + canonical(m_i))。
func chainFingerprint(prev string, m openAIMessage) string {
	sum := sha256.Sum256([]byte(prev + "\x01" + canonical(m)))
	return hex.EncodeToString(sum[:16])
}

// fingerprintOf 计算整段消息列表的链式指纹。
func fingerprintOf(msgs []openAIMessage) string {
	fp := ""
	for _, m := range msgs {
		fp = chainFingerprint(fp, m)
	}
	return fp
}

// conversationIDFor 为不带会话 ID 的 OpenAI 客户端提供稳定的 CodeArts chat_id。
// Agent 工具链的历史会持续增长，但首条 user 消息不变，因此可作为会话锚点。
func conversationIDFor(req *chatRequest, headerValue string) string {
	for _, explicit := range []string{req.ConversationID, headerValue} {
		explicit = strings.TrimSpace(explicit)
		if explicit == "" {
			continue
		}
		if validChatID(explicit) {
			return strings.ToLower(explicit)
		}
		sum := sha256.Sum256([]byte(explicit))
		return hex.EncodeToString(sum[:16])
	}
	for _, message := range req.Messages {
		if message.Role == "user" {
			anchor := canonical(message)
			if len(req.Tools) > 0 {
				// OpenCode 会用同一用户消息先请求无工具的会话标题，再启动工具 Agent。
				// 两者必须分开，否则标题生成会污染内置 Agent 会话。
				anchor += "\x00tools"
			}
			sum := sha256.Sum256([]byte(anchor))
			return hex.EncodeToString(sum[:16])
		}
	}
	return ""
}
