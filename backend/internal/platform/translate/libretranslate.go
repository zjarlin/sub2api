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

// LibreTranslateTranslator 调用指定实例的标准 /translate 和 /detect 接口。
type LibreTranslateTranslator struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewLibreTranslateTranslator(cfg *LibreTranslateConfig) *LibreTranslateTranslator {
	t := &LibreTranslateTranslator{client: &http.Client{Timeout: 30 * time.Second}}
	if cfg != nil {
		t.baseURL, t.apiKey = cfg.BaseURL, cfg.APIKey
	}
	return t
}

func (l *LibreTranslateTranslator) Name() string { return "libretranslate" }

func (l *LibreTranslateTranslator) Translate(ctx context.Context, req *TranslateRequest) (*TranslateResponse, error) {
	if req == nil || len(req.Text) == 0 || req.TargetLang == "" {
		return nil, fmt.Errorf("libretranslate: text and target language are required")
	}
	format := req.Format
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "html" {
		return nil, fmt.Errorf("libretranslate: unsupported format")
	}
	source := libreLanguage(req.SourceLang)
	if source == "" {
		source = "auto"
	}
	result := &TranslateResponse{Provider: l.Name(), Translations: make([]TranslationResult, 0, len(req.Text))}
	for index, text := range req.Text {
		if strings.TrimSpace(text) == "" {
			result.Translations = append(result.Translations, TranslationResult{Text: text})
			continue
		}
		var payload struct {
			Text     *string `json:"translatedText"`
			Detected struct {
				Language string `json:"language"`
			} `json:"detectedLanguage"`
		}
		body := map[string]string{"q": text, "source": source, "target": libreLanguage(req.TargetLang), "format": format}
		if err := l.post(ctx, "translate", body, &payload); err != nil {
			return nil, fmt.Errorf("libretranslate: item %d: %w", index, err)
		}
		if payload.Text == nil || *payload.Text == "" {
			return nil, fmt.Errorf("libretranslate: response has no translation")
		}
		translated := TranslationResult{Text: *payload.Text}
		if source == "auto" {
			translated.DetectedLanguage = payload.Detected.Language
		}
		result.Translations = append(result.Translations, translated)
	}
	return result, nil
}

func (l *LibreTranslateTranslator) post(ctx context.Context, path string, payload map[string]string, result any) error {
	base, err := url.Parse(l.baseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.RawQuery != "" || base.Fragment != "" || base.User != nil {
		return fmt.Errorf("libretranslate: invalid instance URL")
	}
	endpoint, err := url.JoinPath(base.String(), path)
	if err != nil {
		return fmt.Errorf("libretranslate: invalid endpoint")
	}
	if l.apiKey != "" {
		payload["api_key"] = l.apiKey
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("libretranslate: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("libretranslate: create request failed")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := l.client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("libretranslate: request canceled: %w", ctx.Err())
		}
		return fmt.Errorf("libretranslate: upstream request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("libretranslate: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(result); err != nil {
		return fmt.Errorf("libretranslate: invalid response")
	}
	return nil
}

func (l *LibreTranslateTranslator) DetectLanguage(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("libretranslate: text is required")
	}
	var result []struct {
		Language   string  `json:"language"`
		Confidence float64 `json:"confidence"`
	}
	if err := l.post(ctx, "detect", map[string]string{"q": text}, &result); err != nil {
		return "", err
	}
	if len(result) == 0 {
		return "", fmt.Errorf("libretranslate: no detected language")
	}
	best := result[0]
	for _, item := range result[1:] {
		if item.Confidence > best.Confidence {
			best = item
		}
	}
	if best.Language == "" {
		return "", fmt.Errorf("libretranslate: no detected language")
	}
	return best.Language, nil
}

func libreLanguage(code string) string {
	switch code {
	case "zh-CN", "zh-Hans":
		return "zh"
	case "zh-TW", "zh-Hant":
		return "zt"
	case "":
		return ""
	default:
		return strings.SplitN(code, "-", 2)[0]
	}
}
