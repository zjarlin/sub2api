package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

type chatRequest struct {
	Model         string            `json:"model"`
	Messages      []json.RawMessage `json:"messages"`
	Stream        bool              `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	Tools             []functionTool  `json:"tools"`
	ToolChoice        json.RawMessage `json:"tool_choice"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls"`
	search            bool
	policy            toolPolicy
}

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  []toolCall      `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
}

func parseChat(w http.ResponseWriter, r *http.Request) (chatRequest, string, bool, error) {
	var raw map[string]json.RawMessage
	var req chatRequest
	if err := decodeOne(w, r, &raw); err != nil {
		return req, "", false, apiError{400, "invalid_request", "Invalid chat request"}
	}
	allowed := map[string]bool{
		"model": true, "messages": true, "stream": true, "stream_options": true,
		"tools": true, "tool_choice": true, "parallel_tool_calls": true,
		"thinking_enabled": true, "enable_thinking": true, "thinking": true, "reasoning_effort": true,
		"search_enabled": true, "web_search": true,
		"max_tokens": true, "max_completion_tokens": true, "temperature": true, "top_p": true, "n": true, "user": true,
	}
	for key, value := range raw {
		if !allowed[key] && string(value) != "null" {
			return req, "", false, apiError{400, "unsupported_parameter", "DeepSeek web does not support parameter: " + key}
		}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return req, "", false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&req) != nil || len(req.Messages) == 0 || len(req.Messages) > 128 {
		return req, "", false, apiError{400, "invalid_request", "A model and messages are required"}
	}
	thinking := false
	switch req.Model {
	case "deepseek-web-chat", "deepseek-chat":
	case "deepseek-web-reasoner", "deepseek-reasoner", "deepseek-v4.1-flash":
		thinking = true
	default:
		return req, "", false, apiError{400, "model_not_found", "Unsupported DeepSeek web model"}
	}
	thinking, req.search, err = requestFlags(raw, thinking)
	if err != nil {
		return req, "", false, err
	}
	if err := validateCompatibilityOptions(data); err != nil {
		return req, "", false, err
	}
	req.policy, err = parseToolPolicy(req)
	if err != nil {
		return req, "", false, err
	}
	prompt, err := conversationPrompt(req.Messages)
	if err != nil {
		return req, "", false, err
	}
	prompt += req.policy.prompt(req.Tools)
	prompt += "<｜Assistant｜>"
	return req, prompt, thinking, nil
}

func requestFlags(raw map[string]json.RawMessage, thinking bool) (bool, bool, error) {
	invalid := apiError{400, "invalid_request", "Invalid thinking or search option"}
	if value := raw["reasoning_effort"]; len(value) > 0 && string(value) != "null" {
		var effort string
		if json.Unmarshal(value, &effort) != nil {
			return false, false, invalid
		}
		switch effort {
		case "none", "disabled":
			thinking = false
		case "minimal", "low", "medium", "high", "xhigh":
			thinking = true
		default:
			return false, false, invalid
		}
	}
	if value := raw["thinking"]; len(value) > 0 && string(value) != "null" {
		var flag bool
		if json.Unmarshal(value, &flag) == nil {
			thinking = flag
		} else {
			var option struct {
				Type    string `json:"type"`
				Enabled *bool  `json:"enabled"`
			}
			if json.Unmarshal(value, &option) != nil {
				return false, false, invalid
			}
			switch {
			case option.Enabled != nil:
				thinking = *option.Enabled
			case option.Type == "enabled":
				thinking = true
			case option.Type == "disabled":
				thinking = false
			default:
				return false, false, invalid
			}
		}
	}
	search := false
	flags := []struct {
		key    string
		target *bool
	}{
		{"enable_thinking", &thinking}, {"thinking_enabled", &thinking},
		{"web_search", &search}, {"search_enabled", &search},
	}
	for _, flag := range flags {
		value := raw[flag.key]
		if len(value) == 0 || string(value) == "null" {
			continue
		}
		if json.Unmarshal(value, flag.target) != nil {
			return false, false, invalid
		}
	}
	return thinking, search, nil
}

// 兼容客户端常带的采样字段；网页端没有对应控制接口，不能承诺生效。
func validateCompatibilityOptions(data []byte) error {
	var options struct {
		MaxTokens           *int     `json:"max_tokens"`
		MaxCompletionTokens *int     `json:"max_completion_tokens"`
		Temperature         *float64 `json:"temperature"`
		TopP                *float64 `json:"top_p"`
		N                   *int     `json:"n"`
		User                *string  `json:"user"`
	}
	invalid := apiError{400, "invalid_request", "Invalid sampling option; DeepSeek web supports only n=1"}
	if json.Unmarshal(data, &options) != nil {
		return invalid
	}
	if options.N != nil && *options.N != 1 || options.MaxTokens != nil && *options.MaxTokens <= 0 || options.MaxCompletionTokens != nil && *options.MaxCompletionTokens <= 0 {
		return invalid
	}
	if options.Temperature != nil && (*options.Temperature < 0 || *options.Temperature > 2) || options.TopP != nil && (*options.TopP < 0 || *options.TopP > 1) {
		return invalid
	}
	return nil
}

func invalidToolHistory() error {
	return apiError{400, "invalid_tool_history", "Tool results must match preceding assistant calls exactly once"}
}

func conversationPrompt(messages []json.RawMessage) (string, error) {
	var prompt strings.Builder
	pending, seen := map[string]bool{}, map[string]bool{}
	roles := map[string]string{"system": "System", "developer": "System", "user": "User", "assistant": "Assistant", "tool": "Tool"}
	for _, raw := range messages {
		var item chatMessage
		if json.Unmarshal(raw, &item) != nil || roles[item.Role] == "" {
			return "", apiError{400, "unsupported_message", "Unsupported conversation message"}
		}
		if item.Role == "tool" {
			if !pending[item.ToolCallID] || len(item.ToolCalls) > 0 {
				return "", invalidToolHistory()
			}
			delete(pending, item.ToolCallID)
		} else if len(pending) > 0 || item.ToolCallID != "" {
			return "", invalidToolHistory()
		}
		if len(item.ToolCalls) > 0 && item.Role != "assistant" {
			return "", invalidToolHistory()
		}
		for _, call := range item.ToolCalls {
			if call.ID == "" || seen[call.ID] || call.Type != "function" || !functionName.MatchString(call.Function.Name) {
				return "", invalidToolHistory()
			}
			seen[call.ID], pending[call.ID] = true, true
		}
		text, err := messageText(item)
		if err != nil {
			return "", err
		}
		prompt.WriteString("<｜" + roles[item.Role] + "｜>" + text + "\n")
		if item.ToolCallID != "" {
			id, _ := json.Marshal(item.ToolCallID)
			prompt.WriteString("tool_call_id: " + string(id) + "\n")
		}
		if len(item.ToolCalls) > 0 {
			calls, err := renderToolCalls(item.ToolCalls)
			if err != nil {
				return "", invalidToolHistory()
			}
			prompt.WriteString(calls + "\n")
		}
	}
	if len(pending) > 0 {
		return "", invalidToolHistory()
	}
	return prompt.String(), nil
}

func messageText(item chatMessage) (string, error) {
	invalid := apiError{400, "unsupported_content", "DeepSeek web only supports text content"}
	if len(item.Content) == 0 || string(item.Content) == "null" {
		if item.Role == "assistant" && len(item.ToolCalls) > 0 {
			return "", nil
		}
		return "", invalid
	}
	var text string
	if json.Unmarshal(item.Content, &text) != nil {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(item.Content, &parts) != nil || len(parts) == 0 {
			return "", invalid
		}
		for _, part := range parts {
			if part.Type != "text" {
				return "", invalid
			}
			text += part.Text
		}
	}
	if text == "" && item.Role != "tool" && !(item.Role == "assistant" && len(item.ToolCalls) > 0) {
		return "", apiError{400, "invalid_message", "Empty text messages are not supported"}
	}
	return text, nil
}
