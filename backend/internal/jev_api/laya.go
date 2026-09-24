package jev_api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RelayLaya 将已校验的 System One 请求转发到管理员指定的内网 Laya 服务。
func RelayLaya(ctx context.Context, baseURL string, body []byte, client *http.Client) (int, []byte, error) {
	if client == nil {
		return 0, nil, errors.New("laya client unavailable")
	}
	target, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || target.Scheme != "http" || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || target.Path != "" || target.RawPath != "" || target.Hostname() == "" {
		return 0, nil, errors.New("laya endpoint unavailable")
	}
	target.Path = systemOnePath
	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("laya request unavailable")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, errors.New("laya service unavailable")
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(payload) > maxResponseBytes {
		return 0, nil, errors.New("laya response unavailable")
	}
	return response.StatusCode, payload, nil
}
