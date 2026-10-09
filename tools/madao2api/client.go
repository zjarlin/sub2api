// client.go 码道站内 HTTP 客户端：会话校验、模型目录、CloudAgent 会话协议
// 与 SSE 事件流解析，以及错误分类。
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	if strings.HasPrefix(path, "/v1/cloudagent/") {
		req.Header.Set(agentTypeHeaderKey, agentTypeCodeBase)
	}
	if strings.HasPrefix(path, "/PromptCenterService/") {
		req.Header.Set(agentTypeHeaderKey, "AgentCenter")
	}
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
		return fmt.Errorf("CodeArts %s %s returned HTTP %d: %w", req.Method, req.URL.Path, res.StatusCode, err)
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
	var raw json.RawMessage
	query := url.Values{"agent_id": {codebaseAgentID}}
	if err := a.call(ctx, c, http.MethodGet, epAgentsDetail+"?"+query.Encode(), nil, &raw); err != nil {
		return kernelModels
	}
	// 当前网页返回顶层 gpts，部分网关在外层追加 data 包装。
	for depth := 0; depth < 3; depth++ {
		var detail struct {
			Data json.RawMessage `json:"data"`
			Gpts struct {
				Models []struct {
					ModelName  string `json:"model_name"`
					Parameters struct {
						ModelID        string `json:"model_id"`
						Description    string `json:"model_desc"`
						DisplayEnabled *bool  `json:"display_enabled"`
					} `json:"model_parameters"`
				} `json:"models"`
			} `json:"gpts"`
		}
		if json.Unmarshal(raw, &detail) != nil {
			break
		}
		var models []builtinModel
		seen := map[string]bool{}
		for _, model := range detail.Gpts.Models {
			id := firstNonEmpty(model.Parameters.ModelID, model.ModelName)
			if id == "" || seen[id] || (model.Parameters.DisplayEnabled != nil && !*model.Parameters.DisplayEnabled) {
				continue
			}
			seen[id] = true
			models = append(models, builtinModel{ID: id, Label: firstNonEmpty(model.ModelName, id), Description: model.Parameters.Description})
		}
		if len(models) > 0 {
			return models
		}
		if len(detail.Data) == 0 {
			break
		}
		raw = detail.Data
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

// sessionCreate 创建 CloudAgent 临时任务，模型在发送消息时指定。
func (a *adapter) sessionCreate(ctx context.Context, c credential) (string, error) {
	var response struct {
		Result struct {
			SessionID string `json:"session_id"`
		} `json:"result"`
	}
	if err := a.call(ctx, c, http.MethodPost, epSessionCreate, map[string]any{}, &response); err != nil {
		return "", err
	}
	if response.Result.SessionID == "" {
		return "", problem(502, "invalid_upstream_response", "CodeArts did not return a session id")
	}
	return response.Result.SessionID, nil
}

// sessionDelete 只清理本次请求创建的临时任务，避免污染用户工作台。
func (a *adapter) sessionDelete(ctx context.Context, c credential, sessionID string) error {
	path := fmt.Sprintf(epSessionDetail, url.PathEscape(sessionID))
	return a.call(ctx, c, http.MethodDelete, path, nil, nil)
}

// sessionMessages 的 POST 响应直接返回 SSE，不再另外订阅 kernel 事件。
func (a *adapter) sessionMessages(ctx context.Context, c credential, sessionID, content, model string, onToken func(string) error) (streamResult, error) {
	body, err := json.Marshal(map[string]any{"content": content, "model_id": model, "repos": []string{}})
	if err != nil {
		return streamResult{}, err
	}
	path := fmt.Sprintf(epSessionMessages, url.PathEscape(sessionID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL()+path, bytes.NewReader(body))
	if err != nil {
		return streamResult{}, err
	}
	req.Header = a.headers(c)
	req.Header.Set("Accept", "text/event-stream")
	res, err := a.streamClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return streamResult{}, ctx.Err()
		}
		return streamResult{}, problem(502, "upstream_unavailable", "CodeArts stream is unreachable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		data, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		if err != nil {
			return streamResult{}, err
		}
		statusErr := classifyStatus(res.StatusCode, data)
		if statusErr == nil {
			statusErr = problem(502, "invalid_upstream_response", "CodeArts did not return an event stream")
		}
		return streamResult{}, fmt.Errorf("CodeArts POST %s returned HTTP %d: %w", req.URL.Path, res.StatusCode, statusErr)
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		return streamResult{}, problem(502, "invalid_upstream_response", "CodeArts did not return an event stream")
	}
	return readEventStreamFunc(ctx, res.Body, onToken)
}

type streamResult struct {
	Text string
}

func readEventStream(ctx context.Context, body io.Reader) (streamResult, error) {
	return readEventStreamFunc(ctx, body, nil)
}

// 按 SSE 帧合并 event/data 行；只把主代理的 message 转成正文。
func readEventStreamFunc(ctx context.Context, body io.Reader, onToken func(string) error) (streamResult, error) {
	var result streamResult
	var eventName string
	var dataLines []string
	process := func() (bool, error) {
		if len(dataLines) == 0 {
			return false, nil
		}
		payload := strings.Join(dataLines, "\n")
		if payload == "[DONE]" {
			return true, nil
		}
		var event struct {
			Type         string `json:"type"`
			Content      string `json:"content"`
			Message      string `json:"message"`
			ErrorMessage string `json:"error_msg"`
			SubagentID   string `json:"subagent_id"`
			Properties   struct {
				Content string `json:"content"`
				Text    string `json:"text"`
				Delta   string `json:"delta"`
				Message string `json:"message"`
			} `json:"properties"`
		}
		if json.Unmarshal([]byte(payload), &event) != nil {
			return false, problem(502, "invalid_upstream_response", "Invalid CodeArts stream event")
		}
		switch firstNonEmpty(eventName, event.Type) {
		case "message", "text_chunk", "text":
			if event.SubagentID != "" {
				return false, nil
			}
			chunk := event.Content
			for _, candidate := range []string{event.Properties.Content, event.Properties.Text, event.Properties.Delta} {
				if chunk == "" {
					chunk = candidate
				}
			}
			result.Text += chunk
			if chunk != "" && onToken != nil {
				return false, onToken(chunk)
			}
		case "done":
			return true, nil
		case "error":
			message := firstNonEmpty(event.Message, event.ErrorMessage, event.Properties.Message, "CodeArts stream failed")
			return false, problem(502, "upstream_error", message)
		case "tool_authorization", "question":
			return false, problem(502, "interaction_required", "CodeArts requires interactive tool approval")
		}
		return false, nil
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		line := scanner.Text()
		if line == "" {
			done, err := process()
			if done || err != nil {
				return result, err
			}
			eventName = ""
			dataLines = nil
			continue
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
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("CodeArts event stream read failed: %w", err)
	}
	// 末帧可能没有空行，但缺少 done 的流不应作为成功回复返回。
	done, err := process()
	if err != nil || done {
		return result, err
	}
	return result, problem(502, "incomplete_stream", "CodeArts stream ended before completion")
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
