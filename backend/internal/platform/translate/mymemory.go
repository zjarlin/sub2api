package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MyMemoryTranslator 使用正式公开接口，遵守单次 500 字节限制。
type MyMemoryTranslator struct {
	endpoint string
	email    string
	apiKey   string
	client   *http.Client
}

func NewMyMemoryTranslator(cfg *MyMemoryConfig) *MyMemoryTranslator {
	t := &MyMemoryTranslator{endpoint: "https://api.mymemory.translated.net/get", client: &http.Client{Timeout: 10 * time.Second}}
	if cfg != nil {
		t.email, t.apiKey = cfg.Email, cfg.APIKey
	}
	return t
}

func (m *MyMemoryTranslator) Name() string { return "mymemory" }

func (m *MyMemoryTranslator) Translate(ctx context.Context, req *TranslateRequest) (*TranslateResponse, error) {
	if req == nil || len(req.Text) == 0 || req.TargetLang == "" {
		return nil, fmt.Errorf("mymemory: text and target language are required")
	}
	if req.SourceLang == "" || req.SourceLang == "auto" {
		return nil, fmt.Errorf("mymemory: explicit source language is required")
	}
	if req.Format != "" && req.Format != "text" {
		return nil, fmt.Errorf("mymemory: only text format is supported")
	}
	for _, text := range req.Text {
		if len(text) > 500 {
			return nil, fmt.Errorf("mymemory: each text must not exceed 500 UTF-8 bytes")
		}
	}
	result := &TranslateResponse{Provider: m.Name(), Translations: make([]TranslationResult, 0, len(req.Text))}
	for index, text := range req.Text {
		if strings.TrimSpace(text) == "" {
			result.Translations = append(result.Translations, TranslationResult{Text: text})
			continue
		}
		translated, err := m.translateText(ctx, text, req.SourceLang, req.TargetLang)
		if err != nil {
			return nil, fmt.Errorf("mymemory: item %d: %w", index, err)
		}
		result.Translations = append(result.Translations, TranslationResult{Text: translated})
	}
	return result, nil
}

func (m *MyMemoryTranslator) translateText(ctx context.Context, text, source, target string) (string, error) {
	endpoint, err := url.Parse(m.endpoint)
	if err != nil {
		return "", fmt.Errorf("mymemory: invalid endpoint")
	}
	query := endpoint.Query()
	query.Set("q", text)
	query.Set("langpair", source+"|"+target)
	if m.email != "" {
		query.Set("de", m.email)
	}
	if m.apiKey != "" {
		query.Set("key", m.apiKey)
	}
	endpoint.RawQuery = query.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", fmt.Errorf("mymemory: create request failed")
	}
	resp, err := m.client.Do(httpReq)
	if err != nil {
		// URL 中有原文、邮箱及密钥，传输错误只保留类别，不回传完整 URL。
		if ctx.Err() != nil {
			return "", fmt.Errorf("mymemory: request canceled: %w", ctx.Err())
		}
		return "", fmt.Errorf("mymemory: upstream request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mymemory: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Data struct {
			Text string `json:"translatedText"`
		} `json:"responseData"`
		Status        json.Number `json:"responseStatus"`
		QuotaFinished bool        `json:"quotaFinished"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload); err != nil {
		return "", fmt.Errorf("mymemory: invalid response")
	}
	status, err := payload.Status.Int64()
	if err != nil || status != 200 || payload.QuotaFinished {
		return "", fmt.Errorf("mymemory: API status %s, quota exhausted=%t", payload.Status, payload.QuotaFinished)
	}
	if payload.Data.Text == "" {
		return "", fmt.Errorf("mymemory: response has no translation")
	}
	return html.UnescapeString(payload.Data.Text), nil
}

func (m *MyMemoryTranslator) DetectLanguage(context.Context, string) (string, error) {
	return "", fmt.Errorf("mymemory: automatic language detection is not supported")
}
