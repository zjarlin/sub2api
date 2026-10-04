package translate

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// BaiduTranslator 百度通用翻译适配器
type BaiduTranslator struct {
	appID  string
	secret string
	client *http.Client
}

// NewBaiduTranslator 创建百度翻译适配器
func NewBaiduTranslator(cfg *BaiduConfig) *BaiduTranslator {
	return &BaiduTranslator{
		appID:  cfg.AppID,
		secret: cfg.Secret,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (b *BaiduTranslator) Name() string { return "baidu" }

func (b *BaiduTranslator) Translate(ctx context.Context, req *TranslateRequest) (*TranslateResponse, error) {
	targetLang := MapLang(req.TargetLang, isoToBaidu)
	sourceLang := MapLang(req.SourceLang, isoToBaidu)
	if sourceLang == "" {
		sourceLang = "auto"
	}

	queryText := strings.Join(req.Text, "\n")
	salt := strconv.FormatInt(time.Now().UnixNano(), 10)
	signStr := b.appID + queryText + salt + b.secret
	sign := fmt.Sprintf("%x", md5.Sum([]byte(signStr)))

	params := url.Values{
		"q":     []string{queryText},
		"from":  []string{sourceLang},
		"to":    []string{targetLang},
		"appid": []string{b.appID},
		"salt":  []string{salt},
		"sign":  []string{sign},
	}

	apiURL := "https://fanyi-api.baidu.com/api/trans/vip/translate?" + params.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("baidu: create request: %w", err)
	}

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("baidu: http do: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		TransResult []struct {
			Src string `json:"src"`
			Dst string `json:"dst"`
		} `json:"trans_result"`
		ErrorCode string `json:"error_code"`
		ErrorMsg  string `json:"error_msg"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("baidu: unmarshal: %w", err)
	}
	if result.ErrorCode != "" && result.ErrorCode != "0" {
		return nil, fmt.Errorf("baidu: api error [%s]: %s", result.ErrorCode, result.ErrorMsg)
	}

	results := make([]TranslationResult, len(result.TransResult))
	for i, r := range result.TransResult {
		results[i] = TranslationResult{Text: r.Dst}
	}
	return &TranslateResponse{Translations: results, Provider: b.Name()}, nil
}

func (b *BaiduTranslator) DetectLanguage(ctx context.Context, text string) (string, error) {
	// 百度翻译 from=auto 时返回结果中包含 src 字段，但标准版不保证返回检测语言
	// 这里简单调用翻译接口并尝试从结果推断
	req := &TranslateRequest{
		Text:       []string{text},
		SourceLang: "",
		TargetLang: "en",
	}
	resp, err := b.Translate(ctx, req)
	if err != nil {
		return "", err
	}
	_ = resp
	return "unknown", nil // 百度基础版不支持独立语言检测
}
