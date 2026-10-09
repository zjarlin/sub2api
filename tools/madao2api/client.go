// client.go 码道 Ask 共用的 HTTP、错误分类与模型名称处理。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// resolveModel 把入参模型名规整为码道模型 ID。
func resolveModel(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if mapped, ok := modelAliases[key]; ok {
		return mapped
	}
	return strings.TrimSpace(name)
}

type streamResult struct {
	Text string
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
