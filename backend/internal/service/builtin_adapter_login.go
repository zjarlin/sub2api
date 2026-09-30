package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// BuiltinLoginResult 刻意排除上游 token 与共享密钥，只向页面返回授权状态。
type BuiltinLoginResult struct {
	SessionID string `json:"session_id"`
	AuthURL   string `json:"auth_url,omitempty"`
	Mode      string `json:"mode"`
	Status    string `json:"status"`
	ExpiresAt int64  `json:"expires_at"`
	Account   *struct {
		UID         string `json:"uid"`
		ModelID     string `json:"model_id,omitempty"`
		Nickname    string `json:"nickname,omitempty"`
		AutoRelogin *bool  `json:"auto_relogin,omitempty"`
	} `json:"account,omitempty"`
}

type BuiltinLoginView struct {
	ContentType string
	Body        []byte
}

type BuiltinLoginOptions struct {
	Email       string `json:"email,omitempty"`
	Password    string `json:"password,omitempty"`
	Plan        string `json:"plan,omitempty"`
	Provider    string `json:"provider,omitempty"`
	AutoRelogin bool   `json:"auto_relogin,omitempty"`
}

// BuiltinAdapterLogin 只连接部署配置指定的内部服务，不接受浏览器提供的目标地址或密钥。
func BuiltinAdapterLogin(ctx context.Context, platform, owner, sessionID, action, callback string, options ...BuiltinLoginOptions) (*BuiltinLoginResult, error) {
	switch platform {
	case PlatformArena, PlatformTraework, PlatformWorkbuddy, PlatformVibex, PlatformZcode, PlatformDeepseekWeb, PlatformQoder, PlatformLaya, PlatformJev:
	default:
		return nil, infraerrors.BadRequest("INVALID_LOGIN_PLATFORM", "Unsupported login platform")
	}
	base, key := builtinAdapterBaseURL(platform), builtinAdapterAPIKey(platform)
	if base == "" || key == "" {
		return nil, infraerrors.BadRequest("BUILTIN_ADAPTER_DISABLED", "Enable the built-in adapter and configure its shared key first")
	}
	if owner == "" {
		return nil, infraerrors.BadRequest("INVALID_LOGIN_OWNER", "Login owner is required")
	}
	path, method := "/internal/login/sessions", http.MethodPost
	if sessionID != "" {
		if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(sessionID) {
			return nil, infraerrors.BadRequest("INVALID_LOGIN_SESSION", "Invalid login session")
		}
		path += "/" + sessionID
		switch action {
		case "poll", "callback":
			path += "/" + action
		case "cancel":
			method = http.MethodDelete
		default:
			return nil, infraerrors.BadRequest("INVALID_LOGIN_ACTION", "Invalid login action")
		}
	} else if action != "start" {
		return nil, infraerrors.BadRequest("INVALID_LOGIN_SESSION", "Login session is required")
	}
	payload := map[string]any{"callback_url": callback}
	if len(options) > 0 && action == "start" && platform == PlatformZcode {
		option := options[0]
		if (option.Plan != "" && option.Plan != "coding-plan" && option.Plan != "start-plan") || (option.Provider != "" && option.Provider != "zai" && option.Provider != "bigmodel") {
			return nil, infraerrors.BadRequest("INVALID_ZCODE_LOGIN_OPTIONS", "Unsupported ZCode plan or account provider")
		}
		payload["plan"], payload["provider"] = option.Plan, option.Provider
	}
	if action == "start" && (platform == PlatformArena || platform == PlatformDeepseekWeb) {
		code := "INVALID_ARENA_LOGIN"
		message := "Arena email and password are required"
		if platform == PlatformDeepseekWeb {
			code = "INVALID_DEEPSEEK_WEB_LOGIN"
			message = "DeepSeek email and password are required"
		}
		if len(options) == 0 || strings.TrimSpace(options[0].Email) == "" || options[0].Password == "" || len(options[0].Email) > 320 || len(options[0].Password) > 4096 {
			return nil, infraerrors.BadRequest(code, message)
		}
		payload["email"], payload["password"] = strings.TrimSpace(options[0].Email), options[0].Password
		if platform == PlatformDeepseekWeb {
			payload["auto_relogin"] = options[0].AutoRelogin
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(base, "/v1")+path, bytes.NewReader(body))
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_ADAPTER_URL", "Invalid adapter configuration")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("X-Login-Owner", owner)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, infraerrors.New(502, "ADAPTER_UNREACHABLE", "Unable to reach the built-in login service")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNoContent {
		return &BuiltinLoginResult{SessionID: sessionID, Status: "cancelled"}, nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if platform == PlatformArena {
			if err := arenaLoginError(res); err != nil {
				return nil, err
			}
		}
		// 错误由后台按状态归一化，不透传可能带凭据的上游响应。
		status := res.StatusCode
		if status < 400 || status > 599 {
			status = 502
		}
		message := "Built-in authorization failed; retry or start a new login"
		if status == 404 {
			message = "Login session expired or unavailable; start a new login"
		}
		if status == 400 {
			message = "Invalid authorization credential or callback URL"
		}
		if platform == PlatformArena {
			message = "Arena login or session preparation failed; retry after checking the service status"
			// 适配器认证失败不能触发管理页面的登录失效处理。
			if status == http.StatusUnauthorized {
				status = http.StatusBadGateway
			}
			if status == http.StatusTooManyRequests {
				message = "Arena is busy; wait for the current login or request to finish"
			}
			if status == http.StatusGone {
				message = "Arena login expired; start a new login"
			}
		}
		if platform == PlatformDeepseekWeb {
			message = "DeepSeek login failed; check the account and password, then try again"
			if status == http.StatusTooManyRequests {
				message = "DeepSeek login is busy; wait for the current login to finish"
			}
			if status == http.StatusGone {
				message = "DeepSeek login expired; start a new login"
			}
		}
		if platform == PlatformZcode && status == http.StatusForbidden {
			message = "ZCode authorization failed or the selected plan has no active entitlement"
		}
		if platform == PlatformZcode && status == http.StatusGone {
			message = "ZCode login expired; start a new login"
		}
		return nil, infraerrors.New(status, "ADAPTER_LOGIN_FAILED", message)
	}
	var result BuiltinLoginResult
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&result); err != nil {
		return nil, infraerrors.New(502, "INVALID_ADAPTER_RESPONSE", "Invalid login service response")
	}
	if result.SessionID == "" || (result.Status != "pending" && result.Status != "completed") {
		return nil, fmt.Errorf("invalid adapter login status")
	}
	return &result, nil
}

func arenaLoginError(res *http.Response) error {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<10)).Decode(&body); err != nil {
		return nil
	}
	// 仅识别内部定义的错误码，消息和状态由后台决定，不透传上游原文。
	failures := map[string]struct {
		status  int
		reason  string
		message string
	}{
		"arena_access_blocked":         {http.StatusServiceUnavailable, "ARENA_ACCESS_BLOCKED", "The server's access to Arena was blocked by Cloudflare before credentials could be verified; configure an accessible Arena proxy"},
		"arena_verification_required": {http.StatusServiceUnavailable, "ARENA_VERIFICATION_REQUIRED", "Arena accepted sign-in but rejected security verification when sending a chat message"},
		"login_invalid_credentials":    {http.StatusBadRequest, "ARENA_INVALID_CREDENTIALS", "Arena rejected the supplied sign-in credentials"},
		"session_not_usable":           {http.StatusForbidden, "ARENA_SESSION_UNUSABLE", "Arena accepted the sign-in but the account cannot access Agent sessions"},
		"session_not_ready":            {http.StatusBadGateway, "ARENA_SESSION_NOT_READY", "Arena signed in but the new Agent session did not return a completed text response"},
		"browser_unavailable":          {http.StatusServiceUnavailable, "ARENA_BROWSER_UNAVAILABLE", "The Arena login browser is unavailable; check the adapter service"},
		"arena_network_error":          {http.StatusBadGateway, "ARENA_NETWORK_ERROR", "The server could not reach Arena; check the adapter network or proxy"},
		"arena_login_timeout":          {http.StatusGatewayTimeout, "ARENA_LOGIN_TIMEOUT", "Arena login timed out; check the adapter network and try again"},
		"arena_session_prepare_failed": {http.StatusBadGateway, "ARENA_SESSION_PREPARE_FAILED", "Arena signed in but preparing the new Agent session failed"},
	}
	failure, ok := failures[body.Error.Code]
	if !ok {
		return nil
	}
	return infraerrors.New(failure.status, failure.reason, failure.message)
}

func BuiltinAdapterLoginView(ctx context.Context, platform, owner, sessionID string) (*BuiltinLoginView, error) {
	base, key := builtinAdapterBaseURL(platform), builtinAdapterAPIKey(platform)
	if base == "" || key == "" {
		return nil, infraerrors.BadRequest("BUILTIN_ADAPTER_DISABLED", "Enable the built-in adapter and configure its shared key first")
	}
	if owner == "" || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(sessionID) {
		return nil, infraerrors.BadRequest("INVALID_LOGIN_SESSION", "Invalid login session")
	}
	url := strings.TrimSuffix(base, "/v1") + "/internal/login/sessions/" + sessionID + "/view"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_ADAPTER_URL", "Invalid adapter configuration")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("X-Login-Owner", owner)
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, infraerrors.New(502, "ADAPTER_UNREACHABLE", "Unable to reach the built-in login service")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		status := res.StatusCode
		if status < 400 || status > 599 {
			status = 502
		}
		return nil, infraerrors.New(status, "ADAPTER_LOGIN_VIEW_FAILED", "Unable to load the login view")
	}
	contentType := strings.TrimSpace(strings.Split(res.Header.Get("Content-Type"), ";")[0])
	if contentType != "image/png" && contentType != "image/jpeg" {
		return nil, infraerrors.New(502, "INVALID_ADAPTER_RESPONSE", "Invalid login view response")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 5<<20))
	if err != nil || len(body) == 0 {
		return nil, infraerrors.New(502, "INVALID_ADAPTER_RESPONSE", "Invalid login view response")
	}
	return &BuiltinLoginView{ContentType: contentType, Body: body}, nil
}
