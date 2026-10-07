package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// caiyunDefaultToken 是彩云小译网页端使用的公开令牌，无需注册即可调用。
// 该令牌来自公开前端，可能随时变更或被限流，仅适合作为免费兜底。
const caiyunDefaultToken = "token:3975l6lr5pcbvidl6jl2"

// caiyunEndpoint 是彩云小译翻译接口。
const caiyunEndpoint = "https://api.interpreter.caiyunai.com/v1/translator"

// CaiyunTranslator 彩云小译适配器，免费且无需自备密钥。
type CaiyunTranslator struct {
	token    string
	endpoint string
	client   *http.Client
}

// NewCaiyunTranslator 创建彩云小译适配器，token 为空时使用内置公开令牌。
func NewCaiyunTranslator(cfg *CaiyunConfig) *CaiyunTranslator {
	token := caiyunDefaultToken
	if cfg != nil && cfg.Token != "" {
		token = cfg.Token
	}
	return &CaiyunTranslator{
		token:    token,
		endpoint: caiyunEndpoint,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *CaiyunTranslator) Name() string { return "caiyun" }

// Translate 调用彩云小译接口。该接口按 trans_type 指定语向，仅支持中/英/日。
func (c *CaiyunTranslator) Translate(ctx context.Context, req *TranslateRequest) (*TranslateResponse, error) {
	transType, err := caiyunTransType(req.SourceLang, req.TargetLang)
	if err != nil {
		return nil, err
	}

	payload := map[string]any{
		"source":     req.Text,
		"trans_type": transType,
		"request_id": "sub2api",
		"detect":     req.SourceLang == "",
	}
	body, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("caiyun: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-authorization", c.token)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("caiyun: http do: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Target    []string `json:"target"`
		TransType string   `json:"trans_type"`
		Message   string   `json:"message"`
		Rc        int      `json:"rc"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("caiyun: unmarshal: %w", err)
	}
	if result.Rc != 0 {
		return nil, fmt.Errorf("caiyun: api error [%d]: %s", result.Rc, result.Message)
	}

	detected := caiyunDetectedSource(result.TransType)
	results := make([]TranslationResult, len(result.Target))
	for i, text := range result.Target {
		results[i] = TranslationResult{Text: text}
		if req.SourceLang == "" {
			results[i].DetectedLanguage = detected
		}
	}
	return &TranslateResponse{Translations: results, Provider: c.Name()}, nil
}

// DetectLanguage 借由 auto 语向推断源语言，仅在中/英/日范围内可靠。
func (c *CaiyunTranslator) DetectLanguage(ctx context.Context, text string) (string, error) {
	resp, err := c.Translate(ctx, &TranslateRequest{
		Text:       []string{text},
		SourceLang: "",
		TargetLang: "en",
	})
	if err != nil {
		return "", err
	}
	if len(resp.Translations) > 0 && resp.Translations[0].DetectedLanguage != "" {
		return resp.Translations[0].DetectedLanguage, nil
	}
	return "unknown", nil
}

// caiyunTransType 把 ISO 639-1 语向转成彩云的 "<src>2<tgt>" 形式。
// 彩云只支持中/英/日，其余语言直接报错而不是透传成非法语向。
func caiyunTransType(source, target string) (string, error) {
	src := "auto"
	if source != "" {
		mapped, ok := isoToCaiyun[source]
		if !ok {
			return "", fmt.Errorf("caiyun: unsupported source language %q", source)
		}
		src = mapped
	}
	tgt, ok := isoToCaiyun[target]
	if !ok {
		return "", fmt.Errorf("caiyun: unsupported target language %q", target)
	}
	return src + "2" + tgt, nil
}

// caiyunDetectedSource 从返回的 trans_type（如 "zh2en"）解析实际源语言。
func caiyunDetectedSource(transType string) string {
	for i := 0; i < len(transType); i++ {
		if transType[i] == '2' {
			return transType[:i]
		}
	}
	return ""
}
