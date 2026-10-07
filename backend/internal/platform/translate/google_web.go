package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const googleWebEndpoint = "https://translate.googleapis.com/translate_a/single"

// GoogleWebTranslator 使用 Google 网页翻译兼容接口，不依赖 uTools 桌面进程。
type GoogleWebTranslator struct {
	endpoint string
	client   *http.Client
}

func NewGoogleWebTranslator(cfg *GoogleWebConfig) *GoogleWebTranslator {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg != nil && cfg.ProxyURL != "" {
		proxyURL, err := url.Parse(cfg.ProxyURL)
		if err != nil || proxyURL.Host == "" || (proxyURL.Scheme != "http" && proxyURL.Scheme != "https" && proxyURL.Scheme != "socks5" && proxyURL.Scheme != "socks5h") {
			err = fmt.Errorf("google_web: invalid proxy URL")
		}
		transport.Proxy = func(*http.Request) (*url.URL, error) { return proxyURL, err }
	}
	return &GoogleWebTranslator{
		endpoint: googleWebEndpoint,
		client:   &http.Client{Timeout: 10 * time.Second, Transport: transport},
	}
}

func (g *GoogleWebTranslator) Name() string { return "google_web" }

func (g *GoogleWebTranslator) Translate(ctx context.Context, req *TranslateRequest) (*TranslateResponse, error) {
	if req == nil || len(req.Text) == 0 || req.TargetLang == "" {
		return nil, fmt.Errorf("google_web: text and target language are required")
	}
	if req.Format != "" && req.Format != "text" {
		return nil, fmt.Errorf("google_web: unsupported format %q", req.Format)
	}
	source := req.SourceLang
	if source == "" || source == "auto" {
		source = "auto"
	}
	result := &TranslateResponse{Provider: g.Name(), Translations: make([]TranslationResult, 0, len(req.Text))}
	// 分别发送列表项，保持多行文本和结果顺序，不用分隔符拼接批次。
	for index, text := range req.Text {
		if strings.TrimSpace(text) == "" {
			result.Translations = append(result.Translations, TranslationResult{Text: text})
			continue
		}
		translated, err := g.translateText(ctx, text, source, req.TargetLang)
		if err != nil {
			return nil, fmt.Errorf("google_web: item %d: %w", index, err)
		}
		if source != "auto" {
			translated.DetectedLanguage = ""
		}
		result.Translations = append(result.Translations, translated)
	}
	return result, nil
}

func (g *GoogleWebTranslator) translateText(ctx context.Context, text, source, target string) (TranslationResult, error) {
	endpoint, err := url.Parse(g.endpoint)
	if err != nil {
		return TranslationResult{}, fmt.Errorf("google_web: invalid endpoint: %w", err)
	}
	query := endpoint.Query()
	query.Set("client", "gtx")
	query.Set("sl", source)
	query.Set("tl", target)
	query.Set("dt", "t")
	query.Set("dj", "1")
	query.Set("q", text)
	endpoint.RawQuery = query.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return TranslationResult{}, fmt.Errorf("google_web: create request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "Sub2API-Translation/1.0")
	resp, err := g.client.Do(httpReq)
	if err != nil {
		// 避免把含原文的请求 URL 或代理凭据放进网关错误响应。
		if requestErr, ok := err.(*url.Error); ok {
			err = requestErr.Err
		}
		return TranslationResult{}, fmt.Errorf("google_web: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return TranslationResult{}, fmt.Errorf("google_web: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Sentences []struct {
			Text *string `json:"trans"`
		} `json:"sentences"`
		Source string `json:"src"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload); err != nil {
		return TranslationResult{}, fmt.Errorf("google_web: decode response: %w", err)
	}
	var translated strings.Builder
	found := false
	for _, sentence := range payload.Sentences {
		if sentence.Text != nil {
			translated.WriteString(*sentence.Text)
			found = true
		}
	}
	if !found || translated.Len() == 0 {
		return TranslationResult{}, fmt.Errorf("google_web: response has no translation")
	}
	return TranslationResult{Text: translated.String(), DetectedLanguage: payload.Source}, nil
}

func (g *GoogleWebTranslator) DetectLanguage(ctx context.Context, text string) (string, error) {
	resp, err := g.Translate(ctx, &TranslateRequest{Text: []string{text}, TargetLang: "en"})
	if err != nil {
		return "", err
	}
	if resp.Translations[0].DetectedLanguage == "" {
		return "", fmt.Errorf("google_web: response has no detected language")
	}
	return resp.Translations[0].DetectedLanguage, nil
}
