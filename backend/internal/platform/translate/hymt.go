package translate

import (
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

// HyMTTranslator 使用官方单轮翻译提示，通过私有兼容接口离线推理。
type HyMTTranslator struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewHyMTTranslator(cfg *HyMTConfig) *HyMTTranslator {
	t := &HyMTTranslator{client: &http.Client{
		Timeout:       120 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
	if cfg != nil {
		t.baseURL, t.apiKey = cfg.BaseURL, cfg.APIKey
	}
	return t
}

func (h *HyMTTranslator) Name() string { return "hymt" }

func (h *HyMTTranslator) Translate(ctx context.Context, req *TranslateRequest) (*TranslateResponse, error) {
	if req == nil || len(req.Text) == 0 || len(req.Text) > 16 {
		return nil, fmt.Errorf("hymt: between 1 and 16 text items are required")
	}
	if req.Format != "" && req.Format != "text" {
		return nil, fmt.Errorf("hymt: only text format is supported")
	}
	target, ok := hyMTLanguage(req.TargetLang)
	if !ok {
		return nil, fmt.Errorf("hymt: unsupported target language")
	}
	if req.SourceLang != "" && req.SourceLang != "auto" {
		if _, ok := hyMTLanguage(req.SourceLang); !ok {
			return nil, fmt.Errorf("hymt: unsupported source language")
		}
	}
	// 在发送任何上游请求前检查整批，避免部分推理后才发现超长项。
	for _, text := range req.Text {
		if len(text) > 4096 {
			return nil, fmt.Errorf("hymt: each text item must not exceed 4096 UTF-8 bytes")
		}
	}
	result := &TranslateResponse{Provider: h.Name(), Translations: make([]TranslationResult, 0, len(req.Text))}
	for i, text := range req.Text {
		if strings.TrimSpace(text) == "" {
			result.Translations = append(result.Translations, TranslationResult{Text: text})
			continue
		}
		translated, err := h.translateText(ctx, text, target)
		if err != nil {
			return nil, fmt.Errorf("hymt: item %d: %w", i, err)
		}
		result.Translations = append(result.Translations, TranslationResult{Text: translated})
	}
	return result, nil
}

func (h *HyMTTranslator) translateText(ctx context.Context, text, target string) (string, error) {
	base, err := url.Parse(h.baseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", fmt.Errorf("invalid instance URL")
	}
	endpoint, err := url.JoinPath(base.String(), "v1/chat/completions")
	if err != nil {
		return "", fmt.Errorf("invalid endpoint")
	}
	// 模型没有默认系统提示；同时传递两个引擎各自的重复惩罚字段名。
	payload := map[string]any{
		"model": "hy-mt2", "stream": false,
		"messages":    []map[string]string{{"role": "user", "content": "Translate the following text into " + target + ". Note that you should only output the translated result without any additional explanation:\n\n" + text}},
		"temperature": 0.7, "top_p": 0.6, "top_k": 20, "repeat_penalty": 1.05, "repetition_penalty": 1.05, "max_tokens": 2048,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode request failed")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request failed")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+h.apiKey)
	}
	resp, err := h.client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("request canceled: %w", ctx.Err())
		}
		return "", fmt.Errorf("upstream request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&response); err != nil {
		return "", fmt.Errorf("invalid response")
	}
	if len(response.Choices) != 1 || strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("response has no translation")
	}
	if response.Choices[0].FinishReason != "stop" {
		return "", fmt.Errorf("translation did not finish; output may be truncated")
	}
	return strings.TrimSpace(response.Choices[0].Message.Content), nil
}

func (h *HyMTTranslator) DetectLanguage(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("hymt: standalone language detection is not supported; omit source for automatic translation")
}

var hyMTLanguages = map[string]string{
	"zh": "Chinese", "zh-hant": "Traditional Chinese", "en": "English",
	"fr": "French", "pt": "Portuguese", "es": "Spanish", "ja": "Japanese", "tr": "Turkish",
	"ru": "Russian", "ar": "Arabic", "ko": "Korean", "th": "Thai", "it": "Italian", "de": "German",
	"vi": "Vietnamese", "ms": "Malay", "id": "Indonesian", "tl": "Filipino", "hi": "Hindi",
	"pl": "Polish", "cs": "Czech", "nl": "Dutch", "km": "Khmer", "my": "Burmese", "fa": "Persian",
	"gu": "Gujarati", "ur": "Urdu", "te": "Telugu", "mr": "Marathi", "he": "Hebrew", "bn": "Bengali",
	"ta": "Tamil", "uk": "Ukrainian", "bo": "Tibetan", "kk": "Kazakh", "mn": "Mongolian",
	"ug": "Uyghur", "yue": "Cantonese",
}

func hyMTLanguage(code string) (string, bool) {
	code = strings.ToLower(code)
	if code == "zh-tw" || code == "zh-hk" || code == "zh-hant" || strings.HasPrefix(code, "zh-hant-") {
		return hyMTLanguages["zh-hant"], true
	}
	if code == "fil" {
		code = "tl"
	}
	name, ok := hyMTLanguages[strings.SplitN(code, "-", 2)[0]]
	return name, ok
}
