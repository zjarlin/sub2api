package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  []toolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

func decodeChat(w http.ResponseWriter, r *http.Request) (chatRequest, string, error) {
	var request chatRequest
	var raw map[string]json.RawMessage
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	decoder := json.NewDecoder(r.Body)
	if decoder.Decode(&raw) != nil || decoder.Decode(new(any)) != io.EOF {
		return request, "", problem(400, "invalid_request", "Invalid chat request")
	}
	allowed := map[string]bool{"model": true, "messages": true, "stream": true, "stream_options": true, "tools": true, "tool_choice": true, "parallel_tool_calls": true, "response_format": true}
	ignored := map[string]bool{"max_tokens": true, "max_completion_tokens": true, "reasoning_effort": true}
	for key, value := range raw {
		if !allowed[key] && !ignored[key] && string(bytes.TrimSpace(value)) != "null" {
			return request, "", problem(400, "unsupported_parameter", "VibeX does not support parameter: "+key)
		}
	}
	data, _ := json.Marshal(raw)
	requestDecoder := json.NewDecoder(bytes.NewReader(data))
	requestDecoder.UseNumber()
	if requestDecoder.Decode(&request) != nil || request.Model == "" || len(request.Messages) == 0 || len(request.Messages) > 128 {
		return request, "", problem(400, "invalid_request", "A model and messages are required")
	}
	policy, err := parseToolPolicy(request)
	if err != nil {
		return request, "", err
	}
	request.policy = policy
	request.format, err = parseResponseFormat(request.ResponseFormat)
	if err != nil {
		return request, "", err
	}
	var messages []map[string]json.RawMessage
	_ = json.Unmarshal(raw["messages"], &messages)
	pending, seen := map[string]bool{}, map[string]bool{}
	var prompt strings.Builder
	prompt.WriteString("Answer the following conversation. Do not modify the project. Do not run built-in tools except Read to inspect the uploaded image paths listed below. Never execute client functions or invent their results.\n\n")
	for i, m := range request.Messages {
		for key, value := range messages[i] {
			if key != "role" && key != "content" && key != "tool_calls" && key != "tool_call_id" && key != "name" && string(value) != "null" {
				return request, "", problem(400, "unsupported_message", "Unsupported message field: "+key)
			}
		}
		if m.Role != "system" && m.Role != "developer" && m.Role != "user" && m.Role != "assistant" && m.Role != "tool" {
			return request, "", problem(400, "unsupported_message", "Unsupported conversation role")
		}
		if m.Role == "tool" {
			if !pending[m.ToolCallID] || len(m.ToolCalls) > 0 {
				return request, "", invalidHistory()
			}
			delete(pending, m.ToolCallID)
		} else if len(pending) > 0 || m.ToolCallID != "" {
			return request, "", invalidHistory()
		}
		if len(m.ToolCalls) > 0 && m.Role != "assistant" {
			return request, "", invalidHistory()
		}
		for _, call := range m.ToolCalls {
			if call.ID == "" || seen[call.ID] || call.Type != "function" || call.Function.Name == "" {
				return request, "", invalidHistory()
			}
			var args map[string]any
			if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil || args == nil {
				return request, "", invalidHistory()
			}
			seen[call.ID], pending[call.ID] = true, true
		}
		text, err := request.contentText(m)
		if err != nil {
			return request, "", err
		}
		prompt.WriteString(m.Role + ":\n" + text + "\n")
		if m.ToolCallID != "" {
			prompt.WriteString("tool_call_id: " + m.ToolCallID + "\n")
		}
		if len(m.ToolCalls) > 0 {
			calls, _ := json.Marshal(m.ToolCalls)
			prompt.WriteString("client_tool_calls: " + string(calls) + "\n")
		}
		prompt.WriteString("\n")
	}
	if len(pending) > 0 {
		return request, "", invalidHistory()
	}
	return request, request.format.prompt(policy.prompt(prompt.String(), request.Tools), policy.enabled()), nil
}

func invalidHistory() error {
	return problem(400, "invalid_tool_history", "Tool results must match preceding assistant calls exactly once")
}

func (r *chatRequest) contentText(m chatMessage) (string, error) {
	if len(m.Content) == 0 || string(m.Content) == "null" {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			return "", nil
		}
		return "", problem(400, "unsupported_content", "Message content is required")
	}
	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		return text, nil
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL    string `json:"url"`
			Detail string `json:"detail"`
		} `json:"image_url"`
	}
	if json.Unmarshal(m.Content, &parts) != nil || len(parts) == 0 {
		return "", problem(400, "unsupported_content", "Content must be text or image parts")
	}
	var result strings.Builder
	for _, part := range parts {
		switch part.Type {
		case "text":
			result.WriteString(part.Text + "\n")
		case "image_url":
			if m.Role != "user" || len(r.images) >= 8 {
				return "", problem(400, "invalid_image", "At most 8 user images are supported")
			}
			if part.ImageURL.Detail != "" && part.ImageURL.Detail != "auto" && part.ImageURL.Detail != "high" && part.ImageURL.Detail != "low" {
				return "", problem(400, "invalid_image", "Invalid image detail")
			}
			img, err := parseImage(part.ImageURL.URL)
			if err != nil {
				return "", err
			}
			r.images = append(r.images, img)
			result.WriteString("\nUploaded image: " + img.marker + "\n")
		default:
			return "", problem(400, "unsupported_content", "Only text and image_url parts are supported")
		}
	}
	return result.String(), nil
}
