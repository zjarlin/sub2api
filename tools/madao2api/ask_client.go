// ask_client.go 使用原生 IDE Ask 协议发送纯文本对话；临时 IAM 凭据负责请求签名。
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/signer"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/request"
)

const (
	defaultAskBaseURL = "https://snap-access.cn-north-4.myhuaweicloud.com"
	askChatPath       = "/v1/chat/chat"
	askUserPath       = "/snap-manager/v1/current/user"
	askModelsPath     = "/v1/model/builtin"
	askAgentPath      = "/v1/agent-center/agents/detail?agent_id=9f0e5cb64a104dcfb4aa90dbedab7dc9"
	askPluginVersion  = "26.9.501"
	askMaxFrameBytes  = 4 << 20
)

// askRequest 对实际发送的正文与查询参数签名，不使用网页登录 Cookie。
func (a *adapter) askRequest(ctx context.Context, c credential, method, path string, body []byte, headers map[string]string) (*http.Request, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.IAM == nil || strings.TrimSpace(c.IAM.Credentials.AccessKeyID) == "" ||
		strings.TrimSpace(c.IAM.Credentials.SecretAccessKey) == "" || strings.TrimSpace(c.IAM.Credentials.SecurityToken) == "" {
		return nil, problem(401, "madao_login_required", "CodeArts Ask requires an IDE authorization; sign in again")
	}
	origin := strings.TrimRight(strings.TrimSpace(a.askOrigin), "/")
	if origin == "" {
		origin = defaultAskBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(body))
	if err != nil || req.URL.Host == "" || (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.User != nil || req.URL.Fragment != "" {
		return nil, problem(502, "upstream_unavailable", "Invalid CodeArts Ask endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Language", "zh-cn")
	req.Header.Set("plugin-name", "snap_vscode")
	req.Header.Set("plugin-version", askPluginVersion)
	req.Header.Set("client_version", "Vscode_"+askPluginVersion)
	req.Header.Set("X-Security-Token", c.IAM.Credentials.SecurityToken)
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// SDK 对字符串正文保持原样，避免 JSON 编码器追加换行导致签名不一致。
	builder := request.NewHttpRequestBuilder().
		WithEndpoint(req.URL.Scheme+"://"+req.URL.Host).
		WithPath(req.URL.Path).
		WithMethod(method).
		WithBody("body", string(body)).
		AddHeaderParam("host", req.URL.Host)
	for key, values := range req.Header {
		builder.AddHeaderParam(key, strings.Join(values, ","))
	}
	for key, values := range req.URL.Query() {
		builder.AddQueryParam(key, reflect.ValueOf(values))
	}
	sdkRequest := builder.Build()
	signedHeaders, err := signer.Sign(sdkRequest, c.IAM.Credentials.AccessKeyID, c.IAM.Credentials.SecretAccessKey)
	if err != nil {
		return nil, problem(502, "upstream_unavailable", "CodeArts Ask request signing failed")
	}
	for key, value := range signedHeaders {
		req.Header.Set(key, value)
	}
	return req, nil
}

func (a *adapter) askCall(ctx context.Context, c credential, path, agentType string, out any) error {
	headers := map[string]string{}
	if agentType != "" {
		headers["Agent-Type"] = agentType
	}
	if agentType == "AgentCenter" {
		headers["area"] = "green"
	}
	req, err := a.askRequest(ctx, c, http.MethodGet, path, nil, headers)
	if err != nil {
		return err
	}
	err = a.do(req, out)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (a *adapter) verifyAskSession(ctx context.Context, c credential) (meInfo, error) {
	var raw json.RawMessage
	if err := a.askCall(ctx, c, askUserPath, "", &raw); err != nil {
		return meInfo{}, err
	}
	for depth := 0; depth <= 3; depth++ {
		var user struct {
			UserID   string          `json:"user_id"`
			UserName string          `json:"user_name"`
			Data     json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &user) != nil {
			return meInfo{}, problem(502, "invalid_upstream_response", "Invalid CodeArts Ask user response")
		}
		id, name := strings.TrimSpace(user.UserID), strings.TrimSpace(user.UserName)
		if id != "" && name != "" {
			return meInfo{UserID: id, ID: id, Name: name, NickName: name, UserName: name}, nil
		}
		if len(user.Data) == 0 {
			break
		}
		raw = user.Data
	}
	return meInfo{}, problem(401, "madao_login_required", "CodeArts Ask session is not authenticated")
}

type askModelEntry struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Description    string `json:"description"`
	ModelID        string `json:"model_id"`
	ModelName      string `json:"model_name"`
	ModelAlias     string `json:"model_alias"`
	DisplayEnabled *bool  `json:"display_enabled"`
	Parameters     struct {
		ModelID        string `json:"model_id"`
		Description    string `json:"model_desc"`
		DisplayEnabled *bool  `json:"display_enabled"`
	} `json:"model_parameters"`
}

// listAskModels 优先读取原生内置目录，再读取默认 Ask 专家目录。
func (a *adapter) listAskModels(ctx context.Context, c credential) []builtinModel {
	for _, catalog := range []struct {
		path, agentType string
		builtin         bool
	}{{askModelsPath, "PromptCenter", true}, {askAgentPath, "AgentCenter", false}} {
		var raw json.RawMessage
		if a.askCall(ctx, c, catalog.path, catalog.agentType, &raw) != nil {
			continue
		}
		if models := parseAskModels(raw, catalog.builtin); len(models) > 0 {
			return models
		}
	}
	return kernelModels
}

func parseAskModels(raw json.RawMessage, builtin bool) []builtinModel {
	for depth := 0; depth <= 3; depth++ {
		var catalog struct {
			Data          json.RawMessage `json:"data"`
			BuiltinModels []askModelEntry `json:"builtinModels"`
			Gpts          struct {
				Models []askModelEntry `json:"models"`
			} `json:"gpts"`
		}
		if json.Unmarshal(raw, &catalog) != nil {
			return nil
		}
		entries := catalog.BuiltinModels
		if !builtin {
			entries = catalog.Gpts.Models
		}
		var models []builtinModel
		seen := map[string]bool{}
		for _, entry := range entries {
			id := strings.TrimSpace(firstNonEmpty(entry.Parameters.ModelID, entry.ModelID, entry.ID, entry.ModelAlias, entry.ModelName))
			if id == "" || seen[id] || (entry.DisplayEnabled != nil && !*entry.DisplayEnabled) ||
				(entry.Parameters.DisplayEnabled != nil && !*entry.Parameters.DisplayEnabled) {
				continue
			}
			seen[id] = true
			models = append(models, builtinModel{
				ID: id, Label: firstNonEmpty(entry.Label, entry.ModelName, id),
				Description: firstNonEmpty(entry.Parameters.Description, entry.Description),
			})
		}
		if len(models) > 0 {
			return models
		}
		if len(catalog.Data) == 0 {
			return nil
		}
		raw = catalog.Data
	}
	return nil
}

// askCompletion 只发送文本消息；工具、代码库、附件与专家 ID 均不进入请求。
func (a *adapter) askCompletion(ctx context.Context, c credential, prompt, model string, onToken func(string) error) (streamResult, error) {
	if err := ctx.Err(); err != nil {
		return streamResult{}, err
	}
	if c.IAM == nil || firstNonEmpty(c.IAM.UserName, c.Nickname) == "" {
		return streamResult{}, problem(401, "madao_login_required", "CodeArts Ask requires an authenticated user; sign in again")
	}
	var chatID [16]byte
	if _, err := rand.Read(chatID[:]); err != nil {
		return streamResult{}, problem(502, "upstream_unavailable", "CodeArts Ask request could not be created")
	}
	chatID[6] = chatID[6]&0x0f | 0x40
	chatID[8] = chatID[8]&0x3f | 0x80
	body, err := json.Marshal(map[string]any{
		"chat_id":  hex.EncodeToString(chatID[:]),
		"messages": []map[string]string{{"type": "text", "content": prompt}},
		"client":   "IDE", "task": "chat",
		"task_parameters":       map[string]any{"ide": "Visual Studio Code", "enable_code_interpreter": false},
		"batch_task_parameters": []any{}, "attempt": 1,
		"user_id": firstNonEmpty(c.IAM.UserName, c.Nickname), "user_prompt": askPromptPreview(prompt),
		"model_id": model, "is_delta_response": true,
	})
	if err != nil {
		return streamResult{}, problem(502, "upstream_unavailable", "CodeArts Ask request could not be encoded")
	}
	req, err := a.askRequest(ctx, c, http.MethodPost, askChatPath, body, map[string]string{
		"Agent-Type": "ChatAgent", "Accept": "text/event-stream", "heartbeat-enable": "true", "x-ot-function": "chat",
	})
	if err != nil {
		return streamResult{}, err
	}
	res, err := a.streamClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return streamResult{}, ctx.Err()
		}
		return streamResult{}, problem(502, "upstream_unavailable", "CodeArts Ask stream is unreachable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		data, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		if ctx.Err() != nil {
			return streamResult{}, ctx.Err()
		}
		if err != nil {
			return streamResult{}, problem(502, "invalid_upstream_response", "Invalid CodeArts Ask response")
		}
		if err := classifyStatus(res.StatusCode, data); err != nil {
			return streamResult{}, err
		}
		return streamResult{}, problem(502, "invalid_upstream_response", "CodeArts Ask did not return an event stream")
	}
	contentType, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || contentType != "text/event-stream" {
		return streamResult{}, problem(502, "invalid_upstream_response", "CodeArts Ask did not return an event stream")
	}
	return readAskEventStream(ctx, res.Body, onToken)
}

func askPromptPreview(prompt string) string {
	count := 0
	for index := range prompt {
		if count == 500 {
			return prompt[:index]
		}
		count++
	}
	return prompt
}

type askStreamEvent struct {
	Type         string            `json:"type"`
	Text         string            `json:"text"`
	ErrorCode    json.RawMessage   `json:"error_code"`
	ErrorMessage string            `json:"error_msg"`
	Error        json.RawMessage   `json:"error"`
	ToolCalls    []json.RawMessage `json:"tool_calls"`
	FunctionCall json.RawMessage   `json:"function_call"`
	Delta        *struct {
		Content      string            `json:"content"`
		ToolCalls    []json.RawMessage `json:"tool_calls"`
		FunctionCall json.RawMessage   `json:"function_call"`
	} `json:"delta"`
	Output []struct {
		Type         string            `json:"type"`
		ToolCalls    []json.RawMessage `json:"tool_calls"`
		FunctionCall json.RawMessage   `json:"function_call"`
	} `json:"output"`
}

func askHasValue(raw json.RawMessage) bool {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == nil {
		return false
	}
	switch value := value.(type) {
	case []any:
		return len(value) > 0
	case map[string]any:
		return len(value) > 0
	case string:
		return value != ""
	case bool:
		return value
	case float64:
		return value != 0
	}
	return false
}

func askToolEvent(kind string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	return kind == "question" || strings.HasPrefix(kind, "tool") ||
		strings.HasPrefix(kind, "function_call") || strings.HasPrefix(kind, "functioncall") ||
		strings.HasPrefix(kind, "execution") || strings.HasPrefix(kind, "execute") || strings.Contains(kind, "permission")
}

func askReasoningEvent(kind string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	return kind == "analysis" || strings.HasPrefix(kind, "reasoning") || strings.HasPrefix(kind, "thought") || strings.HasPrefix(kind, "thinking")
}

// askEventError 不透传上游错误正文，避免响应回显临时密钥或签名头。
func askEventError(event askStreamEvent, eventName string) error {
	if strings.EqualFold(eventName, "error") || strings.EqualFold(event.Type, "error") ||
		askHasValue(event.Error) {
		return problem(502, "upstream_error", "CodeArts Ask stream failed")
	}
	if len(event.ErrorCode) == 0 || bytes.Equal(bytes.TrimSpace(event.ErrorCode), []byte("null")) {
		if event.ErrorMessage != "" {
			return problem(502, "upstream_error", "CodeArts Ask stream failed")
		}
		return nil
	}
	var code string
	if json.Unmarshal(event.ErrorCode, &code) != nil {
		var number json.Number
		if json.Unmarshal(event.ErrorCode, &number) != nil {
			return problem(502, "invalid_upstream_response", "Invalid CodeArts Ask error code")
		}
		if numericCode, err := number.Float64(); err == nil && numericCode == 0 {
			return nil
		}
		code = number.String()
	}
	code = strings.TrimSpace(code)
	if code == "" || code == "0" {
		return nil
	}
	switch code {
	case "401", "403":
		return problem(401, "madao_login_required", "CodeArts login expired; sign in again")
	case "429":
		return problem(429, "quota_exceeded", "CodeArts is rate limiting; retry later")
	default:
		return problem(502, "upstream_error", "CodeArts Ask stream failed")
	}
}

func askEventUsesTools(event askStreamEvent, eventName string) bool {
	if askToolEvent(eventName) || askToolEvent(event.Type) || len(event.ToolCalls) > 0 || askHasValue(event.FunctionCall) {
		return true
	}
	if event.Delta != nil && (len(event.Delta.ToolCalls) > 0 || askHasValue(event.Delta.FunctionCall)) {
		return true
	}
	for _, output := range event.Output {
		if askToolEvent(output.Type) || len(output.ToolCalls) > 0 || askHasValue(output.FunctionCall) {
			return true
		}
	}
	return false
}

// readAskEventStream 按 SSE 帧解析原生文本增量；只有 JSON 完成标记能结束回复。
func readAskEventStream(ctx context.Context, body io.Reader, onToken func(string) error) (streamResult, error) {
	var result streamResult
	var visible strings.Builder
	var eventName string
	var dataLines []string
	frameBytes := 0
	emit := func(chunk string) error {
		if chunk == "" {
			return nil
		}
		visible.WriteString(chunk)
		result.Text = visible.String()
		if onToken != nil {
			return onToken(chunk)
		}
		return nil
	}
	process := func() (bool, error) {
		if len(dataLines) == 0 {
			if strings.EqualFold(eventName, "error") {
				return false, problem(502, "upstream_error", "CodeArts Ask stream failed")
			}
			if askToolEvent(eventName) {
				return false, problem(502, "ask_only_violation", "CodeArts Ask returned a tool request; only text answers are supported")
			}
			return false, nil
		}
		payload := bytes.TrimSpace([]byte(strings.Join(dataLines, "\n")))
		var event askStreamEvent
		if len(payload) == 0 || payload[0] != '{' || json.Unmarshal(payload, &event) != nil {
			return false, problem(502, "invalid_upstream_response", "Invalid CodeArts Ask stream event")
		}
		if err := askEventError(event, eventName); err != nil {
			return false, err
		}
		if askEventUsesTools(event, eventName) {
			return false, problem(502, "ask_only_violation", "CodeArts Ask returned a tool request; only text answers are supported")
		}
		if event.Text == "[DONE]" {
			return true, nil
		}
		if askReasoningEvent(eventName) || askReasoningEvent(event.Type) {
			return false, nil
		}
		switch strings.ToLower(firstNonEmpty(event.Type, eventName)) {
		case "answer", "stage", "codebase", "status", "usage", "token_usage":
			return false, nil
		}
		if event.Delta != nil {
			return false, emit(event.Delta.Content)
		}
		if event.Text == "" || strings.HasPrefix(result.Text, event.Text) {
			return false, nil
		}
		if !strings.HasPrefix(event.Text, result.Text) {
			return false, problem(502, "invalid_upstream_response", "CodeArts Ask changed an already streamed answer")
		}
		return false, emit(strings.TrimPrefix(event.Text, result.Text))
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), askMaxFrameBytes)
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !scanner.Scan() {
			break
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		line := scanner.Text()
		if line == "" {
			done, err := process()
			if done || err != nil {
				return result, err
			}
			eventName, dataLines, frameBytes = "", nil, 0
			continue
		}
		frameBytes += len(line) + 1
		if frameBytes > askMaxFrameBytes {
			return result, problem(502, "invalid_upstream_response", "CodeArts Ask stream event is too large")
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if errors.Is(scanner.Err(), bufio.ErrTooLong) {
		return result, problem(502, "invalid_upstream_response", "CodeArts Ask stream event is too large")
	}
	if scanner.Err() != nil {
		return result, problem(502, "incomplete_stream", "CodeArts Ask stream could not be read to completion")
	}
	done, err := process()
	if done || err != nil {
		return result, err
	}
	return result, problem(502, "incomplete_stream", "CodeArts Ask stream ended before completion")
}
