package main

import (
	"encoding/json"
	"errors"
	"strings"
)

// chatRequest 同时承载 Chat Completions 与 Responses 的入站字段。
type chatRequest struct {
	Model         string          `json:"model"`
	Messages      []rawMessage    `json:"messages"`
	Input         json.RawMessage `json:"input"`
	Instructions  json.RawMessage `json:"instructions"`
	Stream        bool            `json:"stream"`
	StreamOptions streamOptions   `json:"stream_options"`
	Tools         json.RawMessage `json:"tools"`
	Functions     json.RawMessage `json:"functions"`
	FunctionCall  json.RawMessage `json:"function_call"`
	ToolChoice    json.RawMessage `json:"tool_choice"`
	Text          json.RawMessage `json:"text"`
	ResponseFmt   json.RawMessage `json:"response_format"`
	Audio         json.RawMessage `json:"audio"`
	Modalities    json.RawMessage `json:"modalities"`
	PreviousID    string          `json:"previous_response_id"`
	Conversation  json.RawMessage `json:"conversation"`
	Background    bool            `json:"background"`
	N             *int            `json:"n"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type rawMessage struct {
	Type       string          `json:"type"`
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  json.RawMessage `json:"tool_calls"`
	FunctionCa json.RawMessage `json:"function_call"`
}

type translated struct {
	model    string
	messages []promptMessage
	stream   bool
}

type promptMessage struct {
	role    string
	content string
}

func hasValue(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "false" && trimmed != `""` && trimmed != "[]" && trimmed != "{}" && trimmed != `"none"` && trimmed != `{"type":"text"}`
}

func textContent(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", errors.New("Only text content is supported")
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type != "text" && part.Type != "input_text" && part.Type != "output_text" {
			return "", errors.New("Only text content is supported")
		}
		texts = append(texts, part.Text)
	}
	return strings.Join(texts, "\n"), nil
}

func (b chatRequest) validate(recognized []string) string {
	if hasValue(b.Tools) || hasValue(b.Functions) || hasValue(b.FunctionCall) || hasValue(b.ToolChoice) {
		return "This adapter only supports Q&A; tools are unsupported"
	}
	if b.PreviousID != "" || hasValue(b.Conversation) || b.Background {
		return "Send complete text history; stored conversations are unsupported"
	}
	if hasValue(b.Text) || hasValue(b.ResponseFmt) {
		return "Only a single plain text answer is supported"
	}
	if hasValue(b.Audio) || hasValue(b.Modalities) {
		return "Only text output is supported"
	}
	if b.N != nil && *b.N != 1 {
		return "Only a single plain text answer is supported"
	}
	if !contains(recognized, b.Model) {
		return "Unknown model; use /v1/models"
	}
	return ""
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// toChat 把入站请求规整为内部提示消息列表。
func toChat(request chatRequest, responses bool, recognized []string) (translated, error) {
	if validation := request.validate(recognized); validation != "" {
		return translated{}, apiError{status: 400, code: "invalid_request", message: validation}
	}
	messages := make([]promptMessage, 0, len(request.Messages)+2)
	if responses {
		if hasValue(request.Instructions) {
			text, err := textContent(request.Instructions)
			if err != nil {
				return translated{}, apiError{status: 400, code: "invalid_request", message: err.Error()}
			}
			messages = append(messages, promptMessage{role: "system", content: text})
		}
		var input any
		if err := json.Unmarshal(nonEmpty(request.Input, []byte("[]")), &input); err != nil {
			return translated{}, apiError{status: 400, code: "invalid_request", message: "input must be a string or an array"}
		}
		switch value := input.(type) {
		case string:
			messages = append(messages, promptMessage{role: "user", content: value})
		case []any:
			for _, item := range value {
				object, ok := item.(map[string]any)
				if !ok {
					return translated{}, apiError{status: 400, code: "invalid_request", message: "Only text messages are supported"}
				}
				message, err := decodeMessage(object)
				if err != nil {
					return translated{}, err
				}
				messages = append(messages, message)
			}
		default:
			return translated{}, apiError{status: 400, code: "invalid_request", message: "Only text messages are supported"}
		}
	} else {
		if len(request.Messages) == 0 {
			return translated{}, apiError{status: 400, code: "invalid_request", message: "A nonempty text history is required"}
		}
		for _, message := range request.Messages {
			if message.Type != "" && message.Type != "message" {
				return translated{}, apiError{status: 400, code: "invalid_request", message: "Only text messages are supported"}
			}
			if hasValue(message.ToolCalls) || hasValue(message.FunctionCa) {
				return translated{}, apiError{status: 400, code: "invalid_request", message: "Only text messages are supported"}
			}
			if !allowedRole(message.Role) {
				return translated{}, apiError{status: 400, code: "invalid_request", message: "Only system/developer/user/assistant text messages are supported"}
			}
			text, err := textContent(message.Content)
			if err != nil {
				return translated{}, apiError{status: 400, code: "invalid_request", message: err.Error()}
			}
			messages = append(messages, promptMessage{role: message.Role, content: text})
		}
	}
	if len(messages) == 0 {
		return translated{}, apiError{status: 400, code: "invalid_request", message: "A nonempty text history is required"}
	}
	return translated{model: request.Model, messages: messages, stream: request.Stream}, nil
}

func decodeMessage(object map[string]any) (promptMessage, error) {
	if kind, ok := object["type"].(string); ok && kind != "message" {
		return promptMessage{}, apiError{status: 400, code: "invalid_request", message: "Only text messages are supported"}
	}
	role, _ := object["role"].(string)
	if !allowedRole(role) {
		return promptMessage{}, apiError{status: 400, code: "invalid_request", message: "Only system/developer/user/assistant text messages are supported"}
	}
	if _, ok := object["tool_calls"]; ok {
		return promptMessage{}, apiError{status: 400, code: "invalid_request", message: "Only text messages are supported"}
	}
	encoded, err := json.Marshal(object["content"])
	if err != nil {
		return promptMessage{}, apiError{status: 400, code: "invalid_request", message: "Only text content is supported"}
	}
	text, err := textContent(encoded)
	if err != nil {
		return promptMessage{}, apiError{status: 400, code: "invalid_request", message: err.Error()}
	}
	return promptMessage{role: role, content: text}, nil
}

func allowedRole(role string) bool {
	switch role {
	case "system", "developer", "user", "assistant":
		return true
	default:
		return false
	}
}

func nonEmpty(raw json.RawMessage, fallback []byte) []byte {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return fallback
	}
	return raw
}

// buildPrompt 把文本消息折成一段匿名网页提示：system/developer 作为说明前置。
func buildPrompt(messages []promptMessage) (string, error) {
	instructions := make([]string, 0, 2)
	turns := make([]string, 0, len(messages))
	for _, message := range messages {
		switch message.role {
		case "system", "developer":
			instructions = append(instructions, message.content)
		case "user":
			turns = append(turns, "User: "+message.content)
		case "assistant":
			turns = append(turns, "Assistant: "+message.content)
		}
	}
	if len(turns) == 0 {
		return "", apiError{status: 400, code: "invalid_request", message: "A nonempty user message is required"}
	}
	if len(turns) == 1 && len(instructions) == 0 {
		return turns[0][len("User: "):], nil
	}
	head := ""
	if len(instructions) > 0 {
		head = strings.Join(instructions, "\n\n") + "\n\n"
	}
	return head + strings.Join(turns, "\n"), nil
}
