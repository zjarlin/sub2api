// client.go 码道站内 HTTP 客户端：会话校验、模型目录、Agent kernel 会话协议
// 与 SSE 事件流解析，以及错误分类。
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type upstreamError struct {
	status  int
	code    string
	message string
}

func (e *upstreamError) Error() string { return e.message }

func problem(status int, code, message string) error {
	return &upstreamError{status: status, code: code, message: message}
}

type builtinModel struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type meInfo struct {
	UserID   string `json:"userId"`
	ID       string `json:"id"`
	DomainID string `json:"domainId"`
	Name     string `json:"name"`
	NickName string `json:"nickName"`
	UserName string `json:"userName"`
}

// headers 构造码道站内请求头。cftk 同时出现在 Cookie 与独立头里，
// 与前端 axios 拦截器的行为一致（handleHeader 追加 cftk）。
func (a *adapter) headers(c credential) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json, text/plain, */*")
	h.Set("x-requested-with", "XMLHttpRequest")
	h.Set(scenarioHeaderKey, scenarioHeaderValue)
	h.Set(langHeaderKey, langHeaderValue)
	h.Set("Origin", loginOrigin)
	h.Set("Referer", a.baseURL()+"/")
	h.Set("User-Agent", madaoUserAgent)
	if cookie := c.cookieHeader(); cookie != "" {
		h.Set("Cookie", cookie)
	}
	if c.Cftk != "" {
		h.Set("cftk", c.Cftk)
	}
	return h
}

const madaoUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

func (a *adapter) baseURL() string { return strings.TrimRight(a.origin, "/") }

// call 发送普通 JSON 请求并解析结果。非 2xx 或会话失效会返回分类后的错误。
func (a *adapter) call(ctx context.Context, c credential, method, path string, body, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL()+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header = a.headers(c)
	return a.do(req, out)
}

func (a *adapter) do(req *http.Request, out any) error {
	res, err := a.client.Do(req)
	if err != nil {
		return problem(502, "upstream_unavailable", "CodeArts is unreachable")
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return problem(502, "invalid_upstream_response", "Invalid CodeArts response")
	}
	if err := classifyStatus(res.StatusCode, data); err != nil {
		return err
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if json.Unmarshal(data, out) != nil {
			return problem(502, "invalid_upstream_response", "Invalid CodeArts response")
		}
	}
	return nil
}

// classifyStatus 把 HTTP 状态与响应体归类为适配器错误。
func classifyStatus(status int, data []byte) error {
	lower := strings.ToLower(string(data))
	isLogin := strings.Contains(lower, "not login") || strings.Contains(lower, "未登录") ||
		strings.Contains(lower, "login required") || strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "hw-ajax-redirect")
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return problem(401, "madao_login_required", "CodeArts login expired; sign in again")
	case status == http.StatusTooManyRequests:
		return problem(429, "quota_exceeded", "CodeArts is rate limiting; retry later")
	case status >= 500:
		return problem(502, "upstream_error", "CodeArts is unavailable")
	case status >= 400:
		if isLogin {
			return problem(401, "madao_login_required", "CodeArts login expired; sign in again")
		}
		return problem(502, "upstream_error", "CodeArts rejected the request")
	}
	if isLogin {
		return problem(401, "madao_login_required", "CodeArts login expired; sign in again")
	}
	return nil
}

func (a *adapter) verifySession(ctx context.Context, c credential) (meInfo, error) {
	var me meInfo
	if err := a.call(ctx, c, http.MethodGet, epMe, nil, &me); err != nil {
		return meInfo{}, err
	}
	if strings.TrimSpace(me.UserID) == "" {
		return meInfo{}, problem(401, "madao_login_required", "CodeArts session is not authenticated")
	}
	return me, nil
}

// listModels 优先读取站内模型目录，失败时回退到内置目录。
func (a *adapter) listModels(ctx context.Context, c credential) []builtinModel {
	var detail struct {
		Data struct {
			Data struct {
				Gpts struct {
					Models []struct {
						ModelName      string `json:"model_name"`
						ModelNameAlt   string `json:"modelName"`
						ModelID        string `json:"model_id"`
						DisplayName    string `json:"display_name"`
						DisplayEnabled *bool  `json:"display_enabled"`
						Enable         *bool  `json:"enable"`
					} `json:"models"`
				} `json:"gpts"`
			} `json:"data"`
		} `json:"data"`
	}
	query := url.Values{"agent_id": {codebaseAgentID}}
	if err := a.call(ctx, c, http.MethodGet, epAgentsDetail+"?"+query.Encode(), nil, &detail); err == nil {
		var models []builtinModel
		seen := map[string]bool{}
		for _, m := range detail.Data.Data.Gpts.Models {
			id := firstNonEmpty(m.ModelName, m.ModelNameAlt, m.ModelID)
			if id == "" || seen[id] {
				continue
			}
			if m.Enable != nil && !*m.Enable {
				continue
			}
			seen[id] = true
			label := firstNonEmpty(m.DisplayName, id)
			models = append(models, builtinModel{ID: id, Label: label})
		}
		if len(models) > 0 {
			return models
		}
	}
	return kernelModels
}

// resolveModel 把入参模型名规整为码道模型 ID。
func resolveModel(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if mapped, ok := modelAliases[key]; ok {
		return mapped
	}
	return strings.TrimSpace(name)
}

// sessionCreate 创建 Agent kernel 会话并返回 sessionId。
func (a *adapter) sessionCreate(ctx context.Context, c credential, agent, model string) (string, error) {
	if agent == "" {
		agent = defaultAgent
	}
	body := map[string]any{"agent": agent}
	if model != "" {
		body["model_name"] = model
	}
	var raw map[string]json.RawMessage
	if err := a.call(ctx, c, http.MethodPost, epSessionCreate, body, &raw); err != nil {
		return "", err
	}
	// 兼容 result / data 包装与顶层 session_id / sessionId / id。
	candidates := []map[string]json.RawMessage{raw}
	for _, key := range []string{"result", "data"} {
		if inner, ok := raw[key]; ok {
			var nested map[string]json.RawMessage
			if json.Unmarshal(inner, &nested) == nil {
				candidates = append(candidates, nested)
			}
		}
	}
	for _, candidate := range candidates {
		for _, key := range []string{"session_id", "sessionId", "id", "sid"} {
			if value, ok := candidate[key]; ok {
				var id string
				if json.Unmarshal(value, &id) == nil && strings.TrimSpace(id) != "" {
					return id, nil
				}
			}
		}
	}
	return "", problem(502, "invalid_upstream_response", "CodeArts did not return a session id")
}

// sessionPrompt 向会话提交一段文本提示。
func (a *adapter) sessionPrompt(ctx context.Context, c credential, sessionID, content, agent string) error {
	body := map[string]any{
		"content": content,
		"parts":   []map[string]string{{"type": "text", "text": content}},
	}
	if agent != "" {
		body["agent"] = agent
	}
	return a.call(ctx, c, http.MethodPost, fmt.Sprintf(epSessionPrompt, url.PathEscape(sessionID)), body, nil)
}

func (a *adapter) sessionClose(ctx context.Context, c credential, sessionID string) {
	_ = a.call(ctx, c, http.MethodPost, fmt.Sprintf(epSessionClose, url.PathEscape(sessionID)), map[string]any{}, nil)
}

// streamResult 是一次 SSE 事件流的汇总结果。
type streamResult struct {
	Text string
}

// sessionStream 订阅会话事件流，阻塞直到收到 done / error / 流结束或上下文取消。
func (a *adapter) sessionStream(ctx context.Context, c credential, sessionID string) (streamResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL()+fmt.Sprintf(epSessionEvents, url.PathEscape(sessionID)), nil)
	if err != nil {
		return streamResult{}, err
	}
	headers := a.headers(c)
	headers.Set("Accept", "text/event-stream")
	headers.Set(agentTypeHeaderKey, agentTypeCodeBase)
	req.Header = headers

	res, err := a.streamClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return streamResult{}, ctx.Err()
		}
		return streamResult{}, problem(502, "upstream_unavailable", "CodeArts stream is unreachable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		return streamResult{}, classifyStatus(res.StatusCode, data)
	}
	return readEventStream(ctx, res.Body)
}

// sessionStreamFunc 订阅会话事件流，并对每个文本增量调用 onToken（流式转发用）。
func (a *adapter) sessionStreamFunc(ctx context.Context, c credential, sessionID string, onToken func(string) error) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL()+fmt.Sprintf(epSessionEvents, url.PathEscape(sessionID)), nil)
	if err != nil {
		return "", err
	}
	headers := a.headers(c)
	headers.Set("Accept", "text/event-stream")
	headers.Set(agentTypeHeaderKey, agentTypeCodeBase)
	req.Header = headers
	res, err := a.streamClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", problem(502, "upstream_unavailable", "CodeArts stream is unreachable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		return "", classifyStatus(res.StatusCode, data)
	}
	result, err := readEventStreamFunc(ctx, res.Body, onToken)
	return result.Text, err
}

// readEventStream 解析码道 SSE：每行 `data: {json}`，其 json 形如
// {type, properties:{...}}（或部分事件的 {type, content}）。
func readEventStream(ctx context.Context, body io.Reader) (streamResult, error) {
	return readEventStreamFunc(ctx, body, nil)
}

func readEventStreamFunc(ctx context.Context, body io.Reader, onToken func(string) error) (streamResult, error) {
	var result streamResult
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") ||
			strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			if payload == "[DONE]" {
				return result, nil
			}
			continue
		}
		var event struct {
			Type       string          `json:"type"`
			Message    string          `json:"message"`
			Content    string          `json:"content"`
			Properties json.RawMessage `json:"properties"`
		}
		if json.Unmarshal([]byte(payload), &event) != nil {
			continue
		}
		props := event.Properties
		var prop struct {
			Content   string `json:"content"`
			Text      string `json:"text"`
			Delta     string `json:"delta"`
			PartType  string `json:"part_type"`
			Message   string `json:"message"`
		}
		if len(props) > 0 {
			_ = json.Unmarshal(props, &prop)
		}
		switch event.Type {
		case "message", "text_chunk", "text":
			chunk := firstNonEmpty(prop.Content, prop.Text, prop.Delta, event.Content)
			if chunk != "" {
				result.Text += chunk
				if onToken != nil {
					if err := onToken(chunk); err != nil {
						return result, err
					}
				}
			}
		case "done", "idle", "step_finish":
			if event.Type == "done" {
				return result, nil
			}
		case "error":
			message := firstNonEmpty(prop.Message, event.Message, "CodeArts stream failed")
			return result, problem(502, "upstream_error", message)
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if errors.Is(err, context.Canceled) {
			return result, ctx.Err()
		}
	}
	return result, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// newHTTPClients 构造短请求与流式请求两套客户端。流式客户端不设总超时。
func newHTTPClients() (*http.Client, *http.Client) {
	short := &http.Client{
		Timeout:       45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	stream := &http.Client{
		Transport:     http.DefaultTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return short, stream
}
