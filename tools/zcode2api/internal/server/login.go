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
	"time"

	"glm-zcode-2api/internal/credential"
	"sub2api/builtinlogin"
)

// 授权端点可被测试覆盖，生产默认指向 Z.AI / ZCode 官方入口。
var (
	zcodeOAuthCLIURL        = "https://zcode.z.ai/api/v1/oauth/cli"
	zcodeUserInfoURL        = "https://chat.z.ai/api/oauth/userinfo"
	zcodeCustomerInfoURL    = "https://api.z.ai/api/biz/customer/getCustomerInfo"
	zcodeBizLoginURL        = "https://api.z.ai/api/auth/z/login"
	zcodeBizBaseURL         = "https://api.z.ai/api/biz"
	zcodeBigmodelBizBaseURL = "https://bigmodel.cn/api/biz"
)

const zcodeOAuthRedirectURI = "https://zcode.z.ai/app/oauth/login?redirect=zcode%3A%2F%2Foauth%2Fcallback&app_version=3.14.3"

// 官方网页登录在中转页完成兑换，适配器只轮询当前会话的授权结果。
func (s *Server) beginZcodeLogin(ctx context.Context) (*builtinlogin.Flow, error) {
	options, ok := ctx.Value(loginOptionsKey{}).(loginOptions)
	if !ok {
		options = loginOptions{Plan: s.cfg.Upstream.Plan, Provider: s.cfg.Upstream.OAuthProvider}
	}
	pollToken, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	flow, err := startZcodeOAuthFlow(ctx, s.loginHTTP, pollToken, options.Provider)
	if err != nil {
		return nil, err
	}
	store := s.loginCredStore
	client := s.loginHTTP
	nextPoll := time.Time{}
	return &builtinlogin.Flow{URL: flow.AuthURL, Mode: "poll", Complete: func(ctx context.Context, _ string) (*builtinlogin.Account, error) {
		if time.Now().After(flow.ExpiresAt) {
			return nil, &builtinlogin.PublicError{Status: http.StatusGone, Message: "ZCode login expired; start a new login"}
		}
		if time.Now().Before(nextPoll) {
			return nil, builtinlogin.ErrPending
		}
		nextPoll = time.Now().Add(flow.PollInterval)
		data, err := pollZcodeOAuthFlow(ctx, client, pollToken, flow.ID, options.Provider)
		if err != nil {
			return nil, err
		}
		var cred credential.Credential
		if options.Plan == credential.PlanStart {
			cred, err = s.buildStartPlanCredential(ctx, data, options.Provider)
		} else {
			cred, err = buildZcodeCredential(ctx, client, data, options.Provider)
		}
		if err != nil {
			var public *builtinlogin.PublicError
			if errors.As(err, &public) {
				return nil, public
			}
			return nil, &builtinlogin.PublicError{Status: http.StatusBadGateway, Message: "Authorization response is incomplete; retry or start a new login"}
		}
		if err := store.Save(cred); err != nil {
			return nil, &builtinlogin.PublicError{Status: http.StatusBadGateway, Message: "Unable to persist the authorization result"}
		}
		return &builtinlogin.Account{UID: credentialUID(cred), Nickname: cred.Provider}, nil
	}}, nil
}

// credentialUID 用上游账号身份作为登录结果标识；不含密钥。
func credentialUID(cred credential.Credential) string {
	if cred.ProviderID == "" {
		return "zcode"
	}
	return cred.ProviderID
}

type zcodeOAuthFlow struct {
	ID           string
	AuthURL      string
	ExpiresAt    time.Time
	PollInterval time.Duration
}

func startZcodeOAuthFlow(ctx context.Context, client *http.Client, pollToken, provider string) (zcodeOAuthFlow, error) {
	payload, _ := json.Marshal(map[string]string{"provider": provider})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, zcodeOAuthCLIURL+"/init", bytes.NewReader(payload))
	if err != nil {
		return zcodeOAuthFlow{}, err
	}
	req.Header.Set("Authorization", "Bearer "+pollToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", upstreamUserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return zcodeOAuthFlow{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return zcodeOAuthFlow{}, fmt.Errorf("ZCode login initialization status %d", resp.StatusCode)
	}
	var result struct {
		Code *int `json:"code"`
		Data struct {
			FlowID          string  `json:"flow_id"`
			AuthorizeURL    string  `json:"authorize_url"`
			ExpiresAt       int64   `json:"expires_at"`
			PollIntervalSec float64 `json:"poll_interval_sec"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil || result.Code == nil || *result.Code != 0 {
		return zcodeOAuthFlow{}, errors.New("invalid ZCode login initialization response")
	}
	authURL, err := url.Parse(result.Data.AuthorizeURL)
	if err != nil || authURL.Scheme != "https" || authURL.Hostname() == "" || result.Data.FlowID == "" {
		return zcodeOAuthFlow{}, errors.New("invalid ZCode authorization URL")
	}
	query := authURL.Query()
	if provider == "bigmodel" {
		query.Set("redirect", zcodeOAuthRedirectURI)
	} else {
		query.Set("redirect_uri", zcodeOAuthRedirectURI)
	}
	authURL.RawQuery = query.Encode()
	expiresAt := time.Unix(result.Data.ExpiresAt, 0)
	interval := time.Duration(result.Data.PollIntervalSec * float64(time.Second))
	if !expiresAt.After(time.Now()) || interval < time.Second || interval >= time.Until(expiresAt) {
		return zcodeOAuthFlow{}, errors.New("invalid ZCode login polling interval")
	}
	return zcodeOAuthFlow{ID: result.Data.FlowID, AuthURL: authURL.String(), ExpiresAt: expiresAt, PollInterval: interval}, nil
}

func pollZcodeOAuthFlow(ctx context.Context, client *http.Client, pollToken, flowID, provider string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, zcodeOAuthCLIURL+"/poll/"+url.PathEscape(flowID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+pollToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", upstreamUserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return nil, builtinlogin.ErrPending
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, builtinlogin.ErrPending
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &builtinlogin.PublicError{Status: http.StatusBadGateway, Message: "ZCode login was rejected; start a new login"}
	}
	var result struct {
		Code *int           `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil || result.Code == nil || *result.Code != 0 {
		return nil, &builtinlogin.PublicError{Status: http.StatusBadGateway, Message: "Invalid ZCode login response; start a new login"}
	}
	switch stringVal(result.Data, "status") {
	case "pending":
		return nil, builtinlogin.ErrPending
	case "failed":
		return nil, &builtinlogin.PublicError{Status: http.StatusForbidden, Message: "ZCode authorization failed; start a new login"}
	case "ready":
		oauth, _ := result.Data[provider].(map[string]any)
		user, _ := result.Data["user"].(map[string]any)
		if stringVal(result.Data, "token") == "" || firstNonEmpty(stringVal(oauth, "access_token"), stringVal(oauth, "accessToken")) == "" || stringVal(user, "user_id") == "" {
			return nil, &builtinlogin.PublicError{Status: http.StatusBadGateway, Message: "ZCode login response is incomplete; start a new login"}
		}
		return result.Data, nil
	default:
		return nil, &builtinlogin.PublicError{Status: http.StatusBadGateway, Message: "Invalid ZCode login status; start a new login"}
	}
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
func buildZcodeCredential(ctx context.Context, client *http.Client, data map[string]any, provider string) (credential.Credential, error) {
	oauth, _ := data[provider].(map[string]any)
	accessToken := firstNonEmpty(stringVal(oauth, "access_token"), stringVal(oauth, "accessToken"))
	user, _ := data["user"].(map[string]any)
	if user == nil {
		user = map[string]any{}
	}
	if provider == "zai" {
		if info := fetchZcodeUserInfo(ctx, client, accessToken); info != nil {
			for k, v := range info {
				if _, ok := user[k]; !ok && v != nil {
					user[k] = v
				}
			}
		}
	}
	providerID := firstNonEmpty(stringVal(user, "provider_id"), "account:"+provider+"-individual-coding-plan")
	baseURL := "https://open.bigmodel.cn/api/anthropic"
	if provider == "zai" {
		baseURL = "https://api.z.ai/api/anthropic"
	}
	displayName := firstNonEmpty(stringVal(user, "name"), stringVal(user, "display_name"), stringVal(user, "email"), "ZCode")
	if accessToken == "" {
		return credential.Credential{}, errors.New("unable to derive an upstream API key from the authorization")
	}
	authorization, bizBase, customerURL := accessToken, zcodeBigmodelBizBaseURL, zcodeBigmodelBizBaseURL+"/customer/getCustomerInfo"
	if provider == "zai" {
		bizToken, err := exchangeBizToken(ctx, client, accessToken)
		if err != nil {
			return credential.Credential{}, err
		}
		authorization = "Bearer " + bizToken
		bizBase, customerURL = zcodeBizBaseURL, zcodeCustomerInfoURL
	}
	apiKey, err := zcodeAPIKey(ctx, client, authorization, customerURL, bizBase)
	if err != nil {
		return credential.Credential{}, err
	}
	return credential.Credential{APIKey: apiKey, BaseURL: baseURL, ProviderID: providerID, Provider: displayName, Source: "builtin-login", Plan: credential.PlanCoding}, nil
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

func zcodeAPIKey(ctx context.Context, client *http.Client, authorization, customerURL, bizBaseURL string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, customerURL, nil)
	req.Header.Set("Authorization", authorization)
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
	keysURL := fmt.Sprintf("%s/v1/organization/%s/projects/%s/api_keys", strings.TrimRight(bizBaseURL, "/"), scalarString(org.OrganizationID), scalarString(proj.ProjectID))
	apiKey, err := findOrCreateZcodeAPIKey(ctx, client, authorization, keysURL)
	if err != nil {
		return "", err
	}
	secret, err := copyZcodeAPIKey(ctx, client, authorization, keysURL, apiKey)
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

func findOrCreateZcodeAPIKey(ctx context.Context, client *http.Client, authorization, keysURL string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, keysURL, nil)
	req.Header.Set("Authorization", authorization)
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
	req.Header.Set("Authorization", authorization)
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

func copyZcodeAPIKey(ctx context.Context, client *http.Client, authorization, keysURL, apiKey string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, keysURL+"/copy/"+apiKey, nil)
	req.Header.Set("Authorization", authorization)
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
