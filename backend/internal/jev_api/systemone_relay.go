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

// RelaySystemOne 把一条 System One 请求转发到指定上游的 /v1/systemone。
//
// Laya 与 JEV 共用同一份 wire protocol，差别只在上游地址与凭据；因此这里只做
// 「按账号注入的 base_url + api_key 原样转发」，不解析或改写请求语义。
// 与 RelayLaya 的区别：凭据来自账号（api_key），而不是全局配置。
func RelaySystemOne(ctx context.Context, baseURL, apiKey string, body []byte, client *http.Client) (int, []byte, error) {
	if client == nil {
		return 0, nil, errors.New("systemone client unavailable")
	}
	target, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || target.Scheme == "" || target.Host == "" || target.User != nil {
		return 0, nil, errors.New("systemone endpoint unavailable")
	}
	if target.Scheme != "https" && target.Scheme != "http" {
		return 0, nil, errors.New("systemone endpoint unavailable")
	}
	// base_url 可能自带 /v1 前缀；统一收敛到 /v1/systemone，避免出现 /v1/v1 或漏段。
	if strings.HasSuffix(target.Path, systemOnePath) {
		target.Path = path.Clean(target.Path)
	} else if strings.HasSuffix(strings.TrimRight(target.Path, "/"), "/v1") {
		target.Path = path.Join(strings.TrimRight(target.Path, "/"), "systemone")
	} else {
		target.Path = path.Join(target.Path, systemOnePath)
	}
	target.RawQuery = ""
	target.Fragment = ""

	requestCtx, cancel := context.WithTimeout(ctx, relayTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("systemone request unavailable")
	}
	request.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(apiKey); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := client.Do(request)
	if err != nil {
		if requestCtx.Err() != nil {
			return 0, nil, requestCtx.Err()
		}
		return 0, nil, errors.New("systemone service unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(payload) > maxResponseBytes {
		return 0, nil, errors.New("systemone response unavailable")
	}
	return response.StatusCode, payload, nil
}

// systemOneTimeout 暴露中继超时，供 handler 侧日志与测试断言使用。
func SystemOneTimeout() time.Duration { return relayTimeout }
