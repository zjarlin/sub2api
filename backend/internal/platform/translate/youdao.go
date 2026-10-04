package translate

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// YoudaoTranslator 有道智云翻译适配器
type YoudaoTranslator struct {
	appKey    string
	appSecret string
	client    *http.Client
}

// NewYoudaoTranslator 创建有道翻译适配器
func NewYoudaoTranslator(cfg *YoudaoConfig) *YoudaoTranslator {
	return &YoudaoTranslator{
		appKey:    cfg.AppKey,
		appSecret: cfg.AppSecret,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (y *YoudaoTranslator) Name() string { return "youdao" }

func (y *YoudaoTranslator) Translate(ctx context.Context, req *TranslateRequest) (*TranslateResponse, error) {
	targetLang := MapLang(req.TargetLang, isoToYoudao)
	sourceLang := MapLang(req.SourceLang, isoToYoudao)
	if sourceLang == "" {
		sourceLang = "auto"
	}

	queryText := strings.Join(req.Text, "\n")
	salt := strconv.FormatInt(time.Now().UnixNano(), 10)
	curtime := strconv.FormatInt(time.Now().Unix(), 10)

	// 签名规则: sha256(appKey + input + salt + curtime + appSecret)
	input := queryText
	if len(input) > 20 {
		input = input[:10] + strconv.Itoa(len(input)) + input[len(input)-10:]
	}
	signStr := y.appKey + input + salt + curtime + y.appSecret
	sign := fmt.Sprintf("%x", sha256.Sum256([]byte(signStr)))

	params := url.Values{
		"q":        []string{queryText},
		"from":     []string{sourceLang},
		"to":       []string{targetLang},
		"appKey":   []string{y.appKey},
		"salt":     []string{salt},
		"sign":     []string{sign},
		"signType": []string{"v3"},
		"curtime":  []string{curtime},
	}

	apiURL := "https://openapi.youdao.com/api?" + params.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("youdao: create request: %w", err)
	}

	resp, err := y.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("youdao: http do: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Translation []string `json:"translation"`
		ErrorCode   string   `json:"errorCode"`
		ErrorMsg    string   `json:"errorMsg"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("youdao: unmarshal: %w", err)
	}
	if result.ErrorCode != "" && result.ErrorCode != "0" {
		return nil, fmt.Errorf("youdao: api error [%s]: %s", result.ErrorCode, result.ErrorMsg)
	}

	results := make([]TranslationResult, len(result.Translation))
	for i, text := range result.Translation {
		results[i] = TranslationResult{Text: text}
	}
	return &TranslateResponse{Translations: results, Provider: y.Name()}, nil
}

func (y *YoudaoTranslator) DetectLanguage(ctx context.Context, text string) (string, error) {
	req := &TranslateRequest{
		Text:       []string{text},
		SourceLang: "",
		TargetLang: "en",
	}
	resp, err := y.Translate(ctx, req)
	if err != nil {
		return "", err
	}
	_ = resp
	return "unknown", nil // 有道基础版不返回检测语言
}
