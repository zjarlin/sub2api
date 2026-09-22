// Package server exposes the OpenAI-compatible HTTP surface and built-in login endpoints.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"glm-zcode-2api/internal/credential"
	"sub2api/builtinlogin"
)

// 授权端点可被测试覆盖，生产默认指向 Z.AI / ZCode 官方入口。
var (
	zcodeAuthorizeURL    = "https://chat.z.ai/api/oauth/authorize"
	zcodeTokenURL        = "https://zcode.z.ai/api/v1/oauth/token"
	zcodeUserInfoURL     = "https://chat.z.ai/api/oauth/userinfo"
	zcodeCustomerInfoURL = "https://api.z.ai/api/biz/customer/getCustomerInfo"
	zcodeBizLoginURL     = "https://api.z.ai/api/auth/z/login"
	zcodeBizBaseURL      = "https://api.z.ai/api/biz"
)

const zcodeOAuthClientID = "client_P8X5CMWmlaRO9gyO-KSqtg"

// beginZcodeLogin 复用 Z.AI 网页授权入口：浏览器登录后把完整回调链接交回后台，
// 由适配器兑换 Coding Plan 凭据并落盘，后续请求不再依赖桌面 config.json。
func (s *Server) beginZcodeLogin(ctx context.Context) (*builtinlogin.Flow, error) {
	state, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/login/callback", s.loginPort)
	authURL := zcodeAuthorizeURL + "?" + url.Values{
		"redirect_uri":  {redirect},
		"response_type": {"code"},
		"client_id":     {zcodeOAuthClientID},
		"state":         {state},
	}.Encode()
	store := s.loginCredStore
	client := s.loginHTTP
	flow := &builtinlogin.Flow{URL: authURL, Mode: "callback"}
	flow.Complete = func(ctx context.Context, callback string) (*builtinlogin.Account, error) {
		code, err := extractZcodeCode(callback)
		if err != nil {
			return nil, err
		}
		data, err := exchangeZcodeToken(ctx, client, code, state, redirect)
		if err != nil {
			return nil, &builtinlogin.PublicError{Status: http.StatusBadGateway, Message: "Built-in authorization failed; retry or start a new login"}
		}
		cred, err := buildZcodeCredential(ctx, client, data)
		if err != nil {
			return nil, &builtinlogin.PublicError{Status: http.StatusBadGateway, Message: "Authorization response is incomplete; retry or start a new login"}
		}
		if err := store.Save(cred); err != nil {
			return nil, &builtinlogin.PublicError{Status: http.StatusBadGateway, Message: "Unable to persist the authorization result"}
		}
		return &builtinlogin.Account{UID: credentialUID(cred), Nickname: cred.Provider}, nil
	}
	return flow, nil
}

// credentialUID 用上游账号身份作为登录结果标识；不含密钥。
func credentialUID(cred credential.Credential) string {
	if cred.ProviderID == "" {
		return "zcode"
	}
	return cred.ProviderID
}

func extractZcodeCode(raw string) (string, error) {
	invalid := &builtinlogin.PublicError{Status: http.StatusBadRequest, Message: "Paste the complete ZCode callback URL or the authorization code"}
	raw = strings.TrimSpace(strings.Trim(raw, "'\""))
	if raw == "" {
		return "", invalid
	}
	if strings.Contains(raw, "code=") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", invalid
		}
		if code := u.Query().Get("code"); code != "" {
			return code, nil
		}
		return "", invalid
	}
	return raw, nil
}

func exchangeZcodeToken(ctx context.Context, client *http.Client, code, state, redirectURI string) (map[string]any, error) {
	payload, _ := json.Marshal(map[string]string{"provider": "zai", "code": code, "redirect_uri": redirectURI, "state": state})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, zcodeTokenURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", upstreamUserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("invalid token response: %w", err)
	}
	if codeVal, ok := v["code"]; ok {
		n, _ := toFloat(codeVal)
		if n != 0 && n != 200 {
			return nil, fmt.Errorf("authorization business code %v", n)
		}
	}
	data, ok := v["data"].(map[string]any)
	if !ok || stringVal(data, "token") == "" {
		return nil, errors.New("authorization response is missing the Coding Plan token")
	}
	return data, nil
}

func fetchZcodeUserInfo(ctx context.Context, client *http.Client, accessToken string) map[string]any {
	if accessToken == "" {
		return nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, zcodeUserInfoURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", upstreamUserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var v map[string]any
	if json.Unmarshal(body, &v) != nil {
		return nil
	}
	if d, ok := v["data"].(map[string]any); ok {
		return d
	}
	return nil
}

// buildZcodeCredential 走 OAuth access_token → 业务 token → 机构/项目 → API Key 提取链。
func buildZcodeCredential(ctx context.Context, client *http.Client, data map[string]any) (credential.Credential, error) {
	zai, _ := data["zai"].(map[string]any)
	accessToken := stringVal(zai, "access_token")
	user, _ := data["user"].(map[string]any)
	if user == nil {
		user = map[string]any{}
	}
	if info := fetchZcodeUserInfo(ctx, client, accessToken); info != nil {
		for k, v := range info {
			if _, ok := user[k]; !ok && v != nil {
				user[k] = v
			}
		}
	}
	providerID := firstNonEmpty(stringVal(user, "provider_id"), "builtin:bigmodel-coding-plan")
	baseURL := "https://open.bigmodel.cn/api/anthropic"
	if strings.Contains(strings.ToLower(stringVal(user, "plan_name")), "z.ai") {
		baseURL = "https://api.z.ai/api/anthropic"
	}
	displayName := firstNonEmpty(stringVal(user, "name"), stringVal(user, "display_name"), stringVal(user, "email"), "ZCode")
	apiKey := ""
	if accessToken != "" {
		if bizToken, err := exchangeBizToken(ctx, client, accessToken); err == nil {
			if key, err := zcodeAPIKey(ctx, client, bizToken); err == nil {
				apiKey = key
			}
		}
	}
	if apiKey == "" {
		return credential.Credential{}, errors.New("unable to derive an upstream API key from the authorization")
	}
	return credential.Credential{APIKey: apiKey, BaseURL: baseURL, ProviderID: providerID, Provider: displayName, Source: "builtin-login"}, nil
}

func exchangeBizToken(ctx context.Context, client *http.Client, accessToken string) (string, error) {
	body, _ := json.Marshal(map[string]string{"token": accessToken})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, zcodeBizLoginURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", upstreamUserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	return readDataString(resp, "access_token", "accessToken")
}

func zcodeAPIKey(ctx context.Context, client *http.Client, bizToken string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, zcodeCustomerInfoURL, nil)
	req.Header.Set("Authorization", "Bearer "+bizToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", upstreamUserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var envelope struct {
		Data struct {
			Organizations []zcodeOrg `json:"organizations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", err
	}
	org, proj := defaultZcodeProject(envelope.Data.Organizations)
	if org == nil || proj == nil {
		return "", errors.New("authorization response has no usable organization or project")
	}
	keysURL := fmt.Sprintf("%s/v1/organization/%s/projects/%s/api_keys", strings.TrimRight(zcodeBizBaseURL, "/"), scalarString(org.OrganizationID), scalarString(proj.ProjectID))
	apiKey, err := findOrCreateZcodeAPIKey(ctx, client, bizToken, keysURL)
	if err != nil {
		return "", err
	}
	secret, err := copyZcodeAPIKey(ctx, client, bizToken, keysURL, apiKey)
	if err != nil {
		return "", err
	}
	if secret != "" {
		return apiKey + "." + secret, nil
	}
	return apiKey, nil
}

type zcodeOrg struct {
	OrganizationID   any            `json:"organizationId"`
	OrganizationName string         `json:"organizationName"`
	Projects         []zcodeProject `json:"projects"`
}

type zcodeProject struct {
	ProjectID   any    `json:"projectId"`
	ProjectName string `json:"projectName"`
}

func defaultZcodeProject(orgs []zcodeOrg) (*zcodeOrg, *zcodeProject) {
	if len(orgs) == 0 {
		return nil, nil
	}
	org := orgs[0]
	for _, candidate := range orgs {
		if strings.Contains(candidate.OrganizationName, "默认机构") {
			org = candidate
			break
		}
	}
	if len(org.Projects) == 0 {
		return &org, nil
	}
	proj := org.Projects[0]
	for _, candidate := range org.Projects {
		if strings.Contains(candidate.ProjectName, "默认项目") {
			proj = candidate
			break
		}
	}
	return &org, &proj
}

func findOrCreateZcodeAPIKey(ctx context.Context, client *http.Client, bizToken, keysURL string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, keysURL, nil)
	req.Header.Set("Authorization", "Bearer "+bizToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", upstreamUserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	var list struct {
		Data []struct {
			Name   string `json:"name"`
			APIKey string `json:"apiKey"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", err
	}
	for _, item := range list.Data {
		if item.Name == "zcode-api-key" && item.APIKey != "" {
			return item.APIKey, nil
		}
	}
	createBody, _ := json.Marshal(map[string]string{"name": "zcode-api-key"})
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, keysURL, bytes.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer "+bizToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", upstreamUserAgent())
	resp, err = client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var created struct {
		Data struct {
			APIKey string `json:"apiKey"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return "", err
	}
	if created.Data.APIKey == "" {
		return "", errors.New("upstream returned an empty API key")
	}
	return created.Data.APIKey, nil
}

func copyZcodeAPIKey(ctx context.Context, client *http.Client, bizToken, keysURL, apiKey string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, keysURL+"/copy/"+apiKey, nil)
	req.Header.Set("Authorization", "Bearer "+bizToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", upstreamUserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	return readDataString(resp, "secretKey")
}

func readDataString(resp *http.Response, keys ...string) (string, error) {
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("upstream authorization status %d", resp.StatusCode)
	}
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		return "", errors.New("authorization response is not JSON")
	}
	data, ok := v["data"].(map[string]any)
	if !ok {
		return "", errors.New("authorization response is missing data")
	}
	for _, k := range keys {
		if s, ok := data[k].(string); ok && s != "" {
			return s, nil
		}
	}
	return "", errors.New("authorization response data is missing the expected key")
}

func upstreamUserAgent() string { return "glm-zcode-2api/0.1" }
