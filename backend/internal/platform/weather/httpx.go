package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrLocationUnsupported 表示该数据源不接受本次 location 形式（例如只收行政区划编码）。
// 聚合器据此跳过候选，且不计入健康分，避免形式不匹配被当成上游故障。
var ErrLocationUnsupported = errors.New("weather: location form is not supported by this provider")

// maxBodyBytes 限制单次上游响应体，避免异常大响应放大内存。
const maxBodyBytes = 4 << 20

// browserUserAgent 用于要求常规浏览器头的公开天气页。
const browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/111.0.0.0 Safari/537.36"

func newClient() *http.Client {
	return &http.Client{Timeout: 12 * time.Second}
}

// get 发送 GET 请求；headers 为附加请求头（例如 referer）。
func get(ctx context.Context, client *http.Client, endpoint string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("weather: build request failed")
	}
	req.Header.Set("accept", "application/json, text/plain, */*")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("weather: request canceled: %w", ctx.Err())
		}
		// 不带 URL 与查询串，避免凭据或坐标进入错误正文。
		return nil, fmt.Errorf("weather: upstream request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("weather: read response failed")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weather: upstream HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// getJSON 拉取并按 JSON 解析。
func getJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, target any) error {
	body, err := get(ctx, client, endpoint, headers)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("weather: invalid JSON response")
	}
	return nil
}

// isNumericCode 判断 location 是否为纯数字行政区划编码。
func isNumericCode(location string) bool {
	value := strings.TrimSpace(location)
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func trimmed(value string) string { return strings.TrimSpace(value) }
