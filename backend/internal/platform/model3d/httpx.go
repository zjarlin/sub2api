package model3d

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 上游 JSON 响应与错误体的读取上限，避免异常上游把内存打满。
const (
	maxJSONResponseBytes = 4 << 20
	maxErrorBodyBytes    = 8 << 10
)

// newHTTPClient 创建带超时的共享 HTTP 客户端。3D 任务创建本身很快，
// 长耗时发生在服务商侧排队与生成，客户端不需要长连接等待。
func newHTTPClient(timeoutSeconds int) *http.Client {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 60
	}
	return &http.Client{
		Timeout: time.Duration(timeoutSeconds) * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			return nil
		},
	}
}

// doJSON 发送 JSON 请求并按需解析 JSON 响应。
// 非 2xx 统一转成 *UpstreamError，保留上游错误码与消息，便于网关分级返回。
func doJSON(ctx context.Context, client *http.Client, provider, method, url string, headers map[string]string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return &UpstreamError{Provider: provider, StatusCode: 0, Message: err.Error()}
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxJSONResponseBytes)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return &UpstreamError{Provider: provider, StatusCode: resp.StatusCode, Message: "read response: " + err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &UpstreamError{Provider: provider, StatusCode: resp.StatusCode, Message: upstreamMessage(payload, resp.StatusCode)}
	}
	if out == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return &UpstreamError{Provider: provider, StatusCode: resp.StatusCode, Message: "decode response: " + err.Error()}
	}
	return nil
}

// upstreamMessage 从上游错误体里提取可读消息，兼容 OpenAI 风格与国产网关的 code/msg 风格。
func upstreamMessage(payload []byte, status int) string {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return http.StatusText(status)
	}
	var envelope struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Msg     string          `json:"msg"`
		Code    json.RawMessage `json:"code"`
		Detail  string          `json:"detail"`
	}
	if err := json.Unmarshal(trimmed, &envelope); err == nil {
		if len(envelope.Error) > 0 {
			var inner struct {
				Message string          `json:"message"`
				Code    json.RawMessage `json:"code"`
			}
			if json.Unmarshal(envelope.Error, &inner) == nil && strings.TrimSpace(inner.Message) != "" {
				return inner.Message
			}
			var raw string
			if json.Unmarshal(envelope.Error, &raw) == nil && strings.TrimSpace(raw) != "" {
				return raw
			}
		}
		for _, candidate := range []string{envelope.Message, envelope.Msg, envelope.Detail} {
			if strings.TrimSpace(candidate) != "" {
				return candidate
			}
		}
	}
	text := string(trimmed)
	if len(text) > maxErrorBodyBytes {
		text = text[:maxErrorBodyBytes]
	}
	return text
}

// upstreamCode 提取上游业务错误码（兼容 {"code": 30010000} 与 {"error":{"code":"x"}}）。
func upstreamCode(payload []byte) string {
	var envelope struct {
		Code  json.RawMessage `json:"code"`
		Error struct {
			Code json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(payload), &envelope); err != nil {
		return ""
	}
	for _, raw := range []json.RawMessage{envelope.Error.Code, envelope.Code} {
		if len(raw) == 0 {
			continue
		}
		var asString string
		if json.Unmarshal(raw, &asString) == nil && asString != "" {
			return asString
		}
		var asNumber json.Number
		if json.Unmarshal(raw, &asNumber) == nil && asNumber.String() != "" {
			return asNumber.String()
		}
	}
	return ""
}

// maxImageBytes 是图生 3D 输入图片的下载上限（20MB，与服务商限制一致）。
const maxImageBytes = 20 << 20

// fetchImage 下载图生 3D 的输入图片，只允许 http/https 且限制体积。
// 请求体中的 URL 不受信，下载时使用禁止内网访问的独立客户端。
func fetchImage(ctx context.Context, client *http.Client, rawURL string) ([]byte, string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if strings.HasPrefix(rawURL, "data:") {
		return decodeInlineImage(rawURL)
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return nil, "", fmt.Errorf("%w: image_url must be an http(s) URL", ErrInvalidRequest)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("%w: invalid image_url", ErrInvalidRequest)
	}
	resp, err := NewDownloadClient().Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download image: upstream status %d", resp.StatusCode)
	}
	if resp.ContentLength > maxImageBytes {
		return nil, "", fmt.Errorf("%w: image exceeds %d bytes", ErrInvalidRequest, maxImageBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("download image: %w", err)
	}
	if len(data) > maxImageBytes {
		return nil, "", fmt.Errorf("%w: image exceeds %d bytes", ErrInvalidRequest, maxImageBytes)
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("%w: image is empty", ErrInvalidRequest)
	}
	contentType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if contentType == "" {
		contentType = detectImageContentType(data)
	}
	return data, contentType, nil
}

// decodeInlineImage 解析 data URI 或裸 base64 的内联图片。
func decodeInlineImage(raw string) ([]byte, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", fmt.Errorf("%w: image is empty", ErrInvalidRequest)
	}
	contentType := ""
	if strings.HasPrefix(raw, "data:") {
		comma := strings.IndexByte(raw, ',')
		if comma < 0 {
			return nil, "", fmt.Errorf("%w: malformed data URI", ErrInvalidRequest)
		}
		meta := raw[len("data:"):comma]
		raw = raw[comma+1:]
		if semi := strings.IndexByte(meta, ';'); semi >= 0 {
			contentType = meta[:semi]
		}
	}
	data, err := base64Decode(raw)
	if err != nil {
		return nil, "", fmt.Errorf("%w: invalid base64 image", ErrInvalidRequest)
	}
	if len(data) > maxImageBytes {
		return nil, "", fmt.Errorf("%w: image exceeds %d bytes", ErrInvalidRequest, maxImageBytes)
	}
	if contentType == "" {
		contentType = detectImageContentType(data)
	}
	return data, contentType, nil
}

// detectImageContentType 通过魔数判断常见图片格式，供 multipart 上传设置文件类型。
func detectImageContentType(data []byte) string {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png"
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// imageFileExtension 把 content type 映射成服务商 multipart 上传所需的扩展名。
func imageFileExtension(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}
