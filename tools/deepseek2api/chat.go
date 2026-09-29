package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tiktoken-go/tokenizer"
)

type chatRequest struct {
	Model         string            `json:"model"`
	Messages      []json.RawMessage `json:"messages"`
	Stream        bool              `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

func parseChat(w http.ResponseWriter, r *http.Request) (chatRequest, string, bool, error) {
	var raw map[string]json.RawMessage
	var req chatRequest
	if err := decodeOne(w, r, &raw); err != nil {
		return req, "", false, apiError{400, "invalid_request", "Invalid chat request"}
	}
	allowed := map[string]bool{"model": true, "messages": true, "stream": true, "stream_options": true}
	for key, value := range raw {
		if allowed[key] || string(value) == "null" || (key == "tools" && string(value) == "[]") {
			continue
		}
		return req, "", false, apiError{400, "unsupported_parameter", "DeepSeek web does not support parameter: " + key}
	}
	data, _ := json.Marshal(raw)
	if json.Unmarshal(data, &req) != nil || len(req.Messages) == 0 || len(req.Messages) > 128 {
		return req, "", false, apiError{400, "invalid_request", "A model and text messages are required"}
	}
	thinking := false
	switch req.Model {
	case "deepseek-web-chat":
	case "deepseek-web-reasoner":
		thinking = true
	default:
		return req, "", false, apiError{400, "model_not_found", "Unsupported DeepSeek web model"}
	}
	var prompt strings.Builder
	for _, message := range req.Messages {
		var item struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			ToolCalls  json.RawMessage `json:"tool_calls"`
			ToolCallID string          `json:"tool_call_id"`
		}
		if json.Unmarshal(message, &item) != nil || len(item.ToolCalls) > 0 || item.ToolCallID != "" {
			return req, "", false, apiError{400, "unsupported_message", "DeepSeek web only supports text conversation messages"}
		}
		var text string
		if json.Unmarshal(item.Content, &text) != nil {
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(item.Content, &parts) != nil || len(parts) == 0 {
				return req, "", false, apiError{400, "unsupported_content", "DeepSeek web only supports text content"}
			}
			for _, part := range parts {
				if part.Type != "text" {
					return req, "", false, apiError{400, "unsupported_content", "DeepSeek web only supports text content"}
				}
				text += part.Text
			}
		}
		if text == "" {
			return req, "", false, apiError{400, "invalid_message", "Empty text messages are not supported"}
		}
		switch item.Role {
		case "system", "developer":
			prompt.WriteString("<｜System｜>")
		case "user":
			prompt.WriteString("<｜User｜>")
		case "assistant":
			prompt.WriteString("<｜Assistant｜>")
		default:
			return req, "", false, apiError{400, "unsupported_message", "DeepSeek web only supports text conversation roles"}
		}
		prompt.WriteString(text)
		prompt.WriteString("\n")
	}
	prompt.WriteString("<｜Assistant｜>")
	return req, prompt.String(), thinking, nil
}

type deepSeekFragment struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type deepSeekDelta struct {
	path      string
	operation string
	fragments []deepSeekFragment
	status    string
	usage     int
}

func (d *deepSeekDelta) applyValue(path, operation string, value json.RawMessage) {
	if path == "response" && operation == "SET" {
		var snapshot struct {
			Fragments []deepSeekFragment `json:"fragments"`
			Status    string             `json:"status"`
			Usage     int                `json:"accumulated_token_usage"`
		}
		if json.Unmarshal(value, &snapshot) == nil {
			d.fragments, d.status, d.usage = snapshot.Fragments, snapshot.Status, snapshot.Usage
		}
		return
	}
	if path == "response/fragments" && operation == "APPEND" {
		var fragments []deepSeekFragment
		if json.Unmarshal(value, &fragments) == nil {
			d.fragments = append(d.fragments, fragments...)
			return
		}
		var fragment deepSeekFragment
		if json.Unmarshal(value, &fragment) == nil && fragment.Type != "" {
			d.fragments = append(d.fragments, fragment)
		}
		return
	}
	if path == "response/fragments/-1/content" && len(d.fragments) > 0 {
		var text string
		if json.Unmarshal(value, &text) == nil {
			last := &d.fragments[len(d.fragments)-1]
			if operation == "APPEND" {
				last.Content += text
			} else {
				last.Content = text
			}
		}
		return
	}
	switch path {
	case "response/status", "response/quasi_status":
		_ = json.Unmarshal(value, &d.status)
	case "response/accumulated_token_usage":
		_ = json.Unmarshal(value, &d.usage)
	}
}

func (d *deepSeekDelta) apply(raw json.RawMessage, prefix string) error {
	var event struct {
		Path      *string         `json:"p"`
		Operation *string         `json:"o"`
		Value     json.RawMessage `json:"v"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}
	if len(event.Value) == 0 {
		if event.Path != nil || event.Operation != nil {
			return errors.New("DeepSeek delta is missing a value")
		}
		// 会话、标题和语音元数据不参与增量状态，也不能继承前一条 BATCH 操作。
		return nil
	}
	if event.Path != nil {
		d.path = *event.Path
	}
	if event.Operation != nil {
		d.operation = *event.Operation
	}
	path := strings.Trim(prefix+"/"+d.path, "/ ")
	if d.operation == "BATCH" {
		var batch []json.RawMessage
		if err := json.Unmarshal(event.Value, &batch); err != nil {
			return err
		}
		child := &deepSeekDelta{operation: "SET", fragments: d.fragments, status: d.status, usage: d.usage}
		for _, item := range batch {
			if err := child.apply(item, path); err != nil {
				return err
			}
		}
		d.fragments, d.status, d.usage = child.fragments, child.status, child.usage
		return nil
	}
	if d.path == "" && prefix == "" {
		var snapshot struct {
			Response json.RawMessage `json:"response"`
		}
		if json.Unmarshal(event.Value, &snapshot) == nil && len(snapshot.Response) > 0 {
			d.applyValue("response", "SET", snapshot.Response)
			return nil
		}
	}
	d.applyValue(path, d.operation, event.Value)
	return nil
}

func readDeepSeekStream(body io.Reader) (string, string, int, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	delta := &deepSeekDelta{operation: "SET"}
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		if err := delta.apply(json.RawMessage(data), ""); err != nil {
			return "", "", 0, fmt.Errorf("decode DeepSeek event: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", 0, err
	}
	if delta.status != "FINISHED" {
		return "", "", 0, errors.New("DeepSeek response ended without completion")
	}
	var content, reasoning strings.Builder
	for _, fragment := range delta.fragments {
		switch fragment.Type {
		case "RESPONSE":
			content.WriteString(fragment.Content)
		case "THINK":
			reasoning.WriteString(fragment.Content)
		}
	}
	return content.String(), reasoning.String(), delta.usage, nil
}

func newCompletionID() string {
	var random [12]byte
	_, _ = rand.Read(random[:])
	return "chatcmpl-" + hex.EncodeToString(random[:])
}

func (a *adapter) chat(w http.ResponseWriter, r *http.Request) {
	req, prompt, thinking, err := parseChat(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := a.acquire(r.Context()); err != nil {
		writeError(w, apiError{503, "adapter_busy", "DeepSeek adapter is busy"})
		return
	}
	defer a.release()
	credential, err := a.account()
	if err != nil {
		writeError(w, err)
		return
	}
	sessionID, err := a.upstream.createSession(r.Context(), credential)
	if err != nil {
		writeError(w, err)
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		a.upstream.deleteSession(ctx, credential, sessionID)
	}()
	challenge, err := a.upstream.challenge(r.Context(), credential)
	if err != nil {
		writeError(w, err)
		return
	}
	pow, err := a.pow.solveChallenge(r.Context(), challenge)
	if err != nil {
		writeError(w, err)
		return
	}
	resp, err := a.upstream.completion(r.Context(), credential, sessionID, prompt, "default", thinking, pow)
	if err != nil {
		writeError(w, err)
		return
	}
	defer resp.Body.Close()
	content, reasoning, totalTokens, err := readDeepSeekStream(resp.Body)
	if err != nil {
		writeError(w, err)
		return
	}
	id, created := newCompletionID(), time.Now().Unix()
	codec, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		writeError(w, err)
		return
	}
	promptTokens, err := codec.Count(prompt)
	if err != nil {
		writeError(w, err)
		return
	}
	usage := map[string]int{"prompt_tokens": promptTokens, "completion_tokens": totalTokens, "total_tokens": promptTokens + totalTokens}
	message := map[string]any{"role": "assistant", "content": content}
	delta := map[string]any{"role": "assistant", "content": content}
	if reasoning != "" {
		message["reasoning_content"] = reasoning
		delta["reasoning_content"] = reasoning
	}
	if !req.Stream {
		writeJSON(w, 200, map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": req.Model,
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}},
			"usage":   usage,
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	send := func(value any) {
		data, _ := json.Marshal(value)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	}
	send(map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": req.Model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}},
	})
	send(map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": req.Model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	})
	if req.StreamOptions.IncludeUsage {
		send(map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": req.Model,
			"choices": []any{}, "usage": usage,
		})
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}
