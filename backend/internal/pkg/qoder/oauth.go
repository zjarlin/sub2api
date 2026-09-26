package qoder

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Qoder 官方客户端的设备授权（Device Flow）契约。
//
// 与 qodercli 1.0.9 bundle 中一致：
//  1. 生成 PKCE verifier（43..128 字符）与 S256 challenge、随机 nonce、machine_id。
//  2. 打开 {center}/device/selectAccounts?challenge=...&challenge_method=S256
//     &nonce=...&machine_id=...&client_id=<client_id> 让用户选择账号并授权。
//  3. 轮询 {openapi}/api/v1/deviceToken/poll?nonce=...&verifier=...
//     &challenge_method=S256，返回 404 表示仍在等待，200 且带 token 表示完成。
//  4. 轮询到的 token 即模型服务 Bearer token；refresh_token 可用于
//     POST {openapi}/api/v1/deviceToken/refresh 续期。
const (
	// ProdAuthBaseURL 是设备授权页面的生产地址。qodercli 的 loginWithDeviceFlow
	// 传入的是 lN("base")（即 https://qoder.com），授权页会 302 到 qoder.com 登录页。
	ProdAuthBaseURL = "https://qoder.com"
	// ProdOpenAPIBaseURL 是设备令牌接口的生产地址。
	ProdOpenAPIBaseURL = "https://openapi.qoder.sh"

	// DeviceFlowClientID 对应 qodercli 的“全局/试用”客户端身份。
	DeviceFlowClientID = "e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb"
	// DeviceFlowClientIDCN 对应 qodercli 的国内客户端身份。
	DeviceFlowClientIDCN = "e93fe488-5778-4c35-a6fc-0f54ed7b3139"

	// DefaultPollInterval 与官方客户端一致：每秒轮询一次。
	DefaultPollInterval = time.Second
	// DefaultTimeout 与官方客户端一致：授权 5 分钟后超时。
	DefaultTimeout = 5 * time.Minute

	maxAuthBody = 1 << 20

	envAuthBaseURL    = "QODER_AUTH_BASE_URL"
	envCenterBaseURL  = "QODER_CENTER_BASE_URL"
	envOpenAPIBaseURL = "QODER_OPENAPI_BASE_URL"
	envRegion         = "QODER_REGION"
)

// AuthBaseURL 返回设备授权页面地址，可用 QODER_AUTH_BASE_URL（或兼容的
// QODER_CENTER_BASE_URL）覆盖。
func AuthBaseURL() string {
	if value := strings.TrimRight(strings.TrimSpace(os.Getenv(envAuthBaseURL)), "/"); value != "" {
		return value
	}
	return strings.TrimRight(strings.TrimSpace(os.Getenv(envCenterBaseURL)), "/")
}

// OpenAPIBaseURL 返回设备令牌服务地址，可用 QODER_OPENAPI_BASE_URL 覆盖。
func OpenAPIBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv(envOpenAPIBaseURL)), "/")
}

func authBase() string {
	if base := AuthBaseURL(); base != "" {
		return base
	}
	return ProdAuthBaseURL
}

func openAPIBase() string {
	if base := OpenAPIBaseURL(); base != "" {
		return base
	}
	return ProdOpenAPIBaseURL
}

// ClientID 依据 QODER_REGION=cn 切换国内客户端身份。
func ClientID() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv(envRegion)), "cn") {
		return DeviceFlowClientIDCN
	}
	return DeviceFlowClientID
}

func authHostAllowed(host string) bool {
	switch strings.ToLower(host) {
	case "qoder.com", "www.qoder.com",
		"openapi.qoder.sh", "openapi.qoder.com.cn",
		"test-openapi.qoder.sh", "test-openapi.qoder.com.cn":
		return true
	default:
		return false
	}
}

func trustedURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return false
	}
	if os.Getenv(envAuthBaseURL) != "" || os.Getenv(envCenterBaseURL) != "" || os.Getenv(envOpenAPIBaseURL) != "" {
		// 允许部署方通过环境变量指向自建/代理端点（可为 http，供内网与测试使用）。
		return parsed.Scheme == "https" || parsed.Scheme == "http"
	}
	if parsed.Scheme != "https" {
		return false
	}
	return authHostAllowed(parsed.Hostname())
}

// PKCEPair 是 PKCE 的 verifier 与 S256 challenge。
type PKCEPair struct {
	Verifier  string
	Challenge string
}

// GeneratePKCE 生成 43..128 字符的 verifier 及其 S256 challenge，语义与官方一致。
func GeneratePKCE() (PKCEPair, error) {
	length := 43 + randInt(86)
	raw := make([]byte, length)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return PKCEPair{}, err
	}
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	verifier := make([]byte, length)
	for i, b := range raw {
		verifier[i] = charset[int(b)%len(charset)]
	}
	sum := sha256.Sum256(verifier)
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return PKCEPair{Verifier: string(verifier), Challenge: challenge}, nil
}

func randInt(max int) int {
	if max <= 0 {
		return 0
	}
	var buf [4]byte
	if _, err := io.ReadFull(rand.Reader, buf[:]); err != nil {
		return 0
	}
	v := int(buf[0])<<24 | int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
	if v < 0 {
		v = -v
	}
	return v % max
}

// GenerateNonce 生成设备流使用的随机 nonce（UUID v4）。
func GenerateNonce() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst), nil
}

// MachineID 生成设备流使用的随机 machine_id，语义同官方（每台机器稳定即可）。
func MachineID() (string, error) {
	return GenerateNonce()
}

// BuildAuthURL 构造设备授权页面地址。
func BuildAuthURL(challenge, nonce, machineID, clientID string) (string, error) {
	if strings.TrimSpace(challenge) == "" || strings.TrimSpace(nonce) == "" {
		return "", errors.New("qoder auth url requires challenge and nonce")
	}
	if strings.TrimSpace(clientID) == "" {
		clientID = ClientID()
	}
	values := url.Values{
		"challenge":        {challenge},
		"challenge_method": {"S256"},
		"nonce":            {nonce},
		"machine_id":       {machineID},
		"client_id":        {clientID},
	}
	endpoint := authBase() + "/device/selectAccounts?" + values.Encode()
	if !trustedURL(endpoint) {
		return "", fmt.Errorf("qoder auth url is not trusted: %s", endpoint)
	}
	return endpoint, nil
}

// DeviceToken 是设备流返回的令牌。
type DeviceToken struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	ExpiresIn    int64  `json:"expires_in"`
}

// TokenResult 是暴露给上层的令牌信息（access_token 即 device token）。
type TokenResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
	ExpiresIn    int64
}

func (t TokenResult) toResult() TokenResult { return t }

// PollResult 描述一次轮询结果，Done=false 表示仍在等待用户授权。
type PollResult struct {
	Done  bool
	Token *TokenResult
}

// Client 执行设备流轮询与令牌刷新。
type Client struct {
	HTTPClient *http.Client
	UserAgent  string
}

func (c *Client) httpClient() *http.Client {
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) userAgent() string {
	if c != nil && strings.TrimSpace(c.UserAgent) != "" {
		return c.UserAgent
	}
	return "qoder-cli"
}

// PollOnce 轮询一次设备令牌。404 表示尚未授权，返回 Done=false。
func (c *Client) PollOnce(ctx context.Context, nonce, verifier, challengeMethod string) (PollResult, error) {
	if strings.TrimSpace(nonce) == "" || strings.TrimSpace(verifier) == "" {
		return PollResult{}, errors.New("qoder poll requires nonce and verifier")
	}
	if challengeMethod == "" {
		challengeMethod = "S256"
	}
	values := url.Values{
		"nonce":            {nonce},
		"verifier":         {verifier},
		"challenge_method": {challengeMethod},
	}
	endpoint := openAPIBase() + "/api/v1/deviceToken/poll?" + values.Encode()
	if !trustedURL(endpoint) {
		return PollResult{}, fmt.Errorf("qoder poll url is not trusted: %s", endpoint)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return PollResult{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent())

	response, err := c.httpClient().Do(request)
	if err != nil {
		return PollResult{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAuthBody+1))
	if err != nil {
		return PollResult{}, err
	}
	if len(body) > maxAuthBody {
		return PollResult{}, errors.New("qoder poll response exceeds 1 MiB")
	}
	if response.StatusCode == http.StatusNotFound {
		return PollResult{Done: false}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return PollResult{}, fmt.Errorf("qoder poll failed (%d): %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload DeviceToken
	if err := json.Unmarshal(body, &payload); err != nil {
		return PollResult{}, fmt.Errorf("parse qoder poll response: %w", err)
	}
	if strings.TrimSpace(payload.Token) == "" {
		return PollResult{Done: false}, nil
	}
	return PollResult{Done: true, Token: &TokenResult{
		AccessToken:  payload.Token,
		RefreshToken: payload.RefreshToken,
		ExpiresAt:    payload.ExpiresAt,
		ExpiresIn:    payload.ExpiresIn,
	}}, nil
}

// WaitForToken 以固定间隔轮询，直到授权完成或超时。
func (c *Client) WaitForToken(ctx context.Context, nonce, verifier, challengeMethod string) (*TokenResult, error) {
	deadline := time.Now().Add(DefaultTimeout)
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		result, err := c.PollOnce(ctx, nonce, verifier, challengeMethod)
		if err != nil {
			return nil, err
		}
		if result.Done {
			return result.Token, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("qoder device flow timed out after 5 minutes")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(DefaultPollInterval):
		}
	}
}

// Refresh 使用 refresh_token 换取新的设备令牌。
func (c *Client) Refresh(ctx context.Context, refreshToken string) (*TokenResult, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, errors.New("qoder refresh requires refresh_token")
	}
	endpoint := openAPIBase() + "/api/v1/deviceToken/refresh"
	if !trustedURL(endpoint) {
		return nil, fmt.Errorf("qoder refresh url is not trusted: %s", endpoint)
	}
	payload, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent())

	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAuthBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxAuthBody {
		return nil, errors.New("qoder refresh response exceeds 1 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("qoder token refresh failed (%d): %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var refreshPayload struct {
		DeviceToken  string `json:"device_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    string `json:"expires_at"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &refreshPayload); err != nil {
		return nil, fmt.Errorf("parse qoder refresh response: %w", err)
	}
	if strings.TrimSpace(refreshPayload.DeviceToken) == "" {
		return nil, errors.New("qoder token refresh returned an empty device_token")
	}
	result := &TokenResult{
		AccessToken:  refreshPayload.DeviceToken,
		RefreshToken: refreshPayload.RefreshToken,
		ExpiresIn:    refreshPayload.ExpiresIn,
	}
	if strings.TrimSpace(refreshPayload.ExpiresAt) != "" {
		if parsed, err := time.Parse(time.RFC3339, refreshPayload.ExpiresAt); err == nil {
			result.ExpiresAt = parsed.Unix()
		}
	}
	if result.RefreshToken == "" {
		result.RefreshToken = refreshToken
	}
	return result, nil
}
