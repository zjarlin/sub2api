// Package jev_api 承载 Sub2API 对 TypeSafe / JEV System One 的网关能力。
//
// 设计目标：与上游尽量解耦。本目录之外只在上游路由表加一处注册；上游的
// content_moderation 文件保持原样，避免后续合并上游时产生冲突。JEV 的功能
// （模型、问题类型、答案、概率、用量）完全由上游决定，这里只做原样转发。
package jev_api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	// systemOnePath 是 TypeSafe System One 的固定上游路径。
	systemOnePath = "/v1/systemone"
	// commandCodePath 是 CommandCode 兼容入口。
	commandCodePath = "/provider/v1/systemone"
	// ModelID 是本地显式允许的唯一 System One 模型。
	ModelID = "typesafe/jev"
	// LayaModelID 是本地离线决策模型的公开名称。
	LayaModelID = "laya"
	// defaultBaseURL 在上游档案未配置地址时使用。
	defaultBaseURL = "https://api.typesafe.ai"
	// maxRequestBytes 限制中继请求体大小，System One 请求应远小于此上限。
	maxRequestBytes = 1 << 20
	// maxResponseBytes 限制上游响应体大小，避免异常大体积拖垮网关。
	maxResponseBytes = 1 << 20
	// relayTimeout 是调用方未设置更早截止时间时的中继安全上限。
	relayTimeout = 30 * time.Second
)

// ErrRelayUnavailable 表示上游档案缺少可用 API Key，中继无法服务。
var ErrRelayUnavailable = errors.New("typesafe relay unavailable")

// TargetProvider 由 Host 侧适配器实现，提供上游地址、Key 与 HTTP 客户端。
// 返回的 apiKey 只在本函数内使用，不会回传调用方或写入日志。
type TargetProvider interface {
	TypeSafeRelayTarget(ctx context.Context) (baseURL string, apiKey string, client *http.Client, err error)
}

// Relay 把一条 System One 请求原样转发到已配置的 TypeSafe 上游并返回其状态码与响应体。
// 调用方负责鉴权；本函数只做透传，不解析或改写 System One 语义。
func Relay(ctx context.Context, provider TargetProvider, body []byte) (int, []byte, error) {
	if provider == nil {
		return 0, nil, errors.New("typesafe relay provider unavailable")
	}
	baseURL, apiKey, client, err := provider.TypeSafeRelayTarget(ctx)
	if err != nil {
		return 0, nil, err
	}
	if client == nil {
		return 0, nil, errors.New("typesafe relay client unavailable")
	}
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return 0, nil, ErrRelayUnavailable
	}
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = defaultBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return 0, nil, errors.New("typesafe invalid endpoint")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1")) {
		return 0, nil, errors.New("typesafe invalid endpoint")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return 0, nil, errors.New("typesafe invalid endpoint")
	}
	// CommandCode 使用 /provider/v1/systemone；旧 TypeSafe 配置仍沿用 /v1/systemone。
	endpointPath := resolveSystemOnePath(parsed.Hostname(), parsed.Path)
	if strings.HasSuffix(parsed.Path, endpointPath) {
		parsed.Path = path.Clean(parsed.Path)
	} else if endpointPath == commandCodePath && strings.HasSuffix(parsed.Path, "/provider") {
		parsed.Path = path.Join(parsed.Path, systemOnePath)
	} else {
		parsed.Path = path.Join(parsed.Path, endpointPath)
	}
	endpoint := parsed.String()
	reqCtx, cancel := context.WithTimeout(ctx, relayTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("typesafe invalid request")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if reqCtx.Err() != nil {
			return 0, nil, reqCtx.Err()
		}
		// 不回显底层错误，避免泄漏上游地址或凭据。
		return 0, nil, errors.New("typesafe transport unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, errors.New("typesafe invalid response")
	}
	return resp.StatusCode, out, nil
}

func resolveSystemOnePath(host, configuredPath string) string {
	if strings.EqualFold(host, "api.commandcode.ai") || strings.HasSuffix(configuredPath, "/provider") || strings.HasSuffix(configuredPath, commandCodePath) {
		return commandCodePath
	}
	return systemOnePath
}

// MaxRequestBytes 暴露请求体上限，供 handler 层做早期拒绝。
func MaxRequestBytes() int { return maxRequestBytes }
