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
		UID      string `json:"uid"`
		Nickname string `json:"nickname,omitempty"`
	} `json:"account,omitempty"`
}

// BuiltinAdapterLogin 只连接部署配置指定的内部服务，不接受浏览器提供的目标地址或密钥。
func BuiltinAdapterLogin(ctx context.Context, platform, owner, sessionID, action, callback string) (*BuiltinLoginResult, error) {
	switch platform {
	case PlatformTraework, PlatformWorkbuddy, PlatformZcode:
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
	body, err := json.Marshal(map[string]string{"callback_url": callback})
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
			message = "Invalid callback URL; paste the complete TRAE callback URL"
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
