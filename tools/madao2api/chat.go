// chat.go 把 OpenAI 文本对话翻译成官方 Ask 请求，并转发正文增量。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
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
}

// 允许透传但忽略的采样参数（码道不暴露等价控制）。
var ignorableParameters = map[string]bool{
	"temperature": true, "top_p": true, "max_tokens": true, "max_completion_tokens": true,
	"presence_penalty": true, "frequency_penalty": true, "seed": true, "stop": true,
	"n": true, "user": true, "logprobs": true, "top_logprobs": true, "response_format": true,
	"parallel_tool_calls": true, "reasoning_effort": true, "metadata": true, "service_tier": true,
	"store": true, "modalities": true,
}

// parseChat 校验请求并返回拍平后的提示文本与目标模型。
func parseChat(w http.ResponseWriter, r *http.Request) (chatRequest, string, string, error) {
	var raw map[string]json.RawMessage
	var req chatRequest
	if err := decodeOne(w, r, &raw); err != nil {
		return req, "", "", problem(400, "invalid_request", "Invalid chat request")
	}
	for key, value := range raw {
		if ignorableParameters[key] || string(value) == "null" {
			continue
		}
		switch key {
		case "model", "messages", "stream", "stream_options":
		case "tools", "tool_choice", "functions", "function_call":
			// Ask 仅供对话，不接受外部工具协议。
			if string(value) != "[]" && string(value) != "null" && string(value) != `"none"` && string(value) != `"auto"` {
				return req, "", "", problem(400, "unsupported_parameter", "CodeArts adapter does not accept client tools: "+key)
			}
		default:
			return req, "", "", problem(400, "unsupported_parameter", "CodeArts adapter does not support parameter: "+key)
		}
	}
	data, _ := json.Marshal(raw)
	if json.Unmarshal(data, &req) != nil || len(req.Messages) == 0 || len(req.Messages) > 256 {
		return req, "", "", problem(400, "invalid_request", "A model and text messages are required")
	}
	model := resolveModel(req.Model)
	if model == "" {
		model = kernelModels[0].ID
	}
	prompt, err := flattenMessages(req.Messages)
	if err != nil {
		return req, "", "", err
	}
	return req, prompt, model, nil
}

// flattenMessages 把 OpenAI 消息列表拼成单条提示。码道没有独立 system 通道，
// system/developer 内容以标题形式前置。
func flattenMessages(messages []json.RawMessage) (string, error) {
	var builder strings.Builder
	for _, message := range messages {
		var item struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			ToolCalls  json.RawMessage `json:"tool_calls"`
			ToolCallID string          `json:"tool_call_id"`
			Name       string          `json:"name"`
		}
		if json.Unmarshal(message, &item) != nil || len(item.ToolCalls) > 0 || item.ToolCallID != "" {
			return "", problem(400, "unsupported_message", "CodeArts adapter only supports text conversation messages")
		}
		text, err := messageText(item.Content)
		if err != nil {
			return "", err
		}
		if text == "" {
			return "", problem(400, "invalid_message", "Empty text messages are not supported")
		}
		switch item.Role {
		case "system", "developer":
			builder.WriteString("[系统指令] ")
		case "user":
			builder.WriteString("[用户] ")
		case "assistant":
			builder.WriteString("[助手] ")
		default:
			return "", problem(400, "unsupported_message", "CodeArts adapter only supports text conversation roles")
		}
		builder.WriteString(text)
		builder.WriteString("\n\n")
	}
	return strings.TrimSpace(builder.String()), nil
}

func messageText(content json.RawMessage) (string, error) {
	if len(content) == 0 {
		return "", nil
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return "", problem(400, "unsupported_content", "CodeArts adapter only supports text content")
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type != "text" {
			return "", problem(400, "unsupported_content", "CodeArts adapter only supports text content")
		}
		builder.WriteString(part.Text)
	}
	return builder.String(), nil
}

func decodeOne(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected extra JSON")
	}
	return nil
}

// generate 只调用官方 Ask，不创建 CloudAgent 任务。
func (a *adapter) generate(ctx context.Context, c credential, prompt, model string, onToken func(string) error) (string, error) {
	if err := a.acquire(ctx); err != nil {
		return "", err
	}
	defer a.release()
	streamCtx, cancel := context.WithTimeout(ctx, a.streamTimeout)
	defer cancel()
	result, err := a.askCompletion(streamCtx, c, prompt, model, onToken)
	if err != nil {
		return result.Text, err
	}
	if strings.TrimSpace(result.Text) == "" {
		return "", problem(502, "upstream_error", "CodeArts returned an empty response")
	}
	return result.Text, nil
}

func newCompletionID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "chatcmpl-madao"
	}
	return "chatcmpl-" + hex.EncodeToString(raw[:])
}

func completionBody(id, model, text string, created int64) map[string]any {
	return map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": text},
			"finish_reason": "stop",
		}},
	}
}
