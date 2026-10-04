package translate

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TencentTranslator 腾讯云翻译适配器
type TencentTranslator struct {
	secretID  string
	secretKey string
	region    string
	client    *http.Client
}

// NewTencentTranslator 创建腾讯云翻译适配器
func NewTencentTranslator(cfg *TencentConfig) *TencentTranslator {
	region := cfg.Region
	if region == "" {
		region = "ap-guangzhou"
	}
	return &TencentTranslator{
		secretID:  cfg.SecretID,
		secretKey: cfg.SecretKey,
		region:    region,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (t *TencentTranslator) Name() string { return "tencent" }

func (t *TencentTranslator) Translate(ctx context.Context, req *TranslateRequest) (*TranslateResponse, error) {
	targetLang := MapLang(req.TargetLang, isoToTencent)
	sourceLang := MapLang(req.SourceLang, isoToTencent)

	payload := map[string]interface{}{
		"SourceText": req.Text,
		"Source":     sourceLang,
		"Target":     targetLang,
		"ProjectId":  0,
	}
	body, _ := json.Marshal(payload)

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	date := time.Now().UTC().Format("2006-01-02")

	host := "tmt.tencentcloudapi.com"
	contentType := "application/json; charset=utf-8"
	hashedPayload := sha256Hex(string(body))

	// 构造待签名字符串
	canonicalRequest := fmt.Sprintf("POST\n/\n\ncontent-type:%s\nhost:%s\n\ncontent-type;host\n%s",
		contentType, host, hashedPayload)

	credentialScope := fmt.Sprintf("%s/tmt/tc3_request", date)
	hashedCanonical := sha256Hex(canonicalRequest)
	stringToSign := fmt.Sprintf("TC3-HMAC-SHA256\n%s\n%s\n%s", timestamp, credentialScope, hashedCanonical)

	// 计算签名
	signingKey := hmacSHA256(hmacSHA256(hmacSHA256([]byte("TC3"+t.secretKey), date), "tmt"), "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	auth := fmt.Sprintf("TC3-HMAC-SHA256 Credential=%s/%s, SignedHeaders=content-type;host, Signature=%s",
		t.secretID, credentialScope, signature)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://"+host, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("tencent: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Host", host)
	httpReq.Header.Set("Authorization", auth)
	httpReq.Header.Set("X-TC-Action", "TextTranslateBatch")
	httpReq.Header.Set("X-TC-Version", "2018-03-21")
	httpReq.Header.Set("X-TC-Timestamp", timestamp)
	httpReq.Header.Set("X-TC-Region", t.region)

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tencent: http do: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Response struct {
			TargetTextList []string `json:"TargetTextList"`
			Source         string   `json:"Source"`
			Error          *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("tencent: unmarshal: %w", err)
	}
	if result.Response.Error != nil {
		return nil, fmt.Errorf("tencent: api error [%s]: %s", result.Response.Error.Code, result.Response.Error.Message)
	}

	results := make([]TranslationResult, len(result.Response.TargetTextList))
	for i, text := range result.Response.TargetTextList {
		results[i] = TranslationResult{Text: text}
	}
	return &TranslateResponse{Translations: results, Provider: t.Name()}, nil
}

func (t *TencentTranslator) DetectLanguage(ctx context.Context, text string) (string, error) {
	// 腾讯云无独立检测接口，使用翻译接口的 Source="auto" 间接实现
	req := &TranslateRequest{
		Text:       []string{text},
		SourceLang: "",
		TargetLang: "en",
	}
	resp, err := t.Translate(ctx, req)
	if err != nil {
		return "", err
	}
	if len(resp.Translations) > 0 && resp.Translations[0].DetectedLanguage != "" {
		return resp.Translations[0].DetectedLanguage, nil
	}
	return "unknown", nil
}

func sha256Hex(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// Ensure strings is used
var _ = strings.Contains
