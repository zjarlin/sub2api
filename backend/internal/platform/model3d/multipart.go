package model3d

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
)

// multipartFile 描述一个要上传的文件分片。
type multipartFile struct {
	Field       string
	Filename    string
	ContentType string
	Data        []byte
}

// postMultipart 发送 multipart/form-data 请求并按需解析 JSON 响应。
// 文本字段按 key 排序写入，保证同样的请求产生稳定的字节序（便于测试与复现）。
func postMultipart(
	ctx context.Context,
	client *http.Client,
	provider, url string,
	headers map[string]string,
	fields map[string]string,
	files []multipartFile,
	out any,
) error {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := writer.WriteField(key, fields[key]); err != nil {
			return fmt.Errorf("write multipart field %s: %w", key, err)
		}
	}
	for _, file := range files {
		part, err := writer.CreateFormFile(file.Field, file.Filename)
		if err != nil {
			return fmt.Errorf("create multipart file %s: %w", file.Field, err)
		}
		if _, err := part.Write(file.Data); err != nil {
			return fmt.Errorf("write multipart file %s: %w", file.Field, err)
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return &UpstreamError{Provider: provider, Message: err.Error()}
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONResponseBytes))
	if err != nil {
		return &UpstreamError{Provider: provider, StatusCode: resp.StatusCode, Message: "read response: " + err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &UpstreamError{
			Provider:   provider,
			StatusCode: resp.StatusCode,
			Code:       upstreamCode(payload),
			Message:    upstreamMessage(payload, resp.StatusCode),
		}
	}
	if out == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return &UpstreamError{Provider: provider, StatusCode: resp.StatusCode, Message: "decode response: " + err.Error()}
	}
	return nil
}

// trimExtension 去掉文件名里的查询串与目录，保留安全的扩展名提示。
func trimExtension(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if idx := strings.IndexAny(name, "?#"); idx >= 0 {
		name = name[:idx]
	}
	dot := strings.LastIndexByte(name, '.')
	if dot < 0 || dot == len(name)-1 {
		return ""
	}
	ext := strings.ToLower(name[dot:])
	if len(ext) > 8 {
		return ""
	}
	for _, r := range ext[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return ext
}
