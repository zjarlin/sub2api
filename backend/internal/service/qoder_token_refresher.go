package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// qoderTokenRefreshSkew 是设备令牌提前刷新窗口。Qoder 设备令牌有效期通常较短，
// 提前 30 分钟刷新可避免请求路径上的过期失败。
const qoderTokenRefreshSkew = 30 * time.Minute

// QoderTokenRefresher 处理 Qoder 设备流 OAuth 账号的 refresh_token 续期。
type QoderTokenRefresher struct {
	oauthService *QoderOAuthService
}

func NewQoderTokenRefresher(oauthService *QoderOAuthService) *QoderTokenRefresher {
	return &QoderTokenRefresher{oauthService: oauthService}
}

// QoderTokenCacheKey 是 Qoder 令牌分布式锁使用的稳定缓存键。
func QoderTokenCacheKey(account *Account) string {
	if account == nil {
		return "qoder:account:0"
	}
	return "qoder:account:" + strconv.FormatInt(account.ID, 10)
}

func (r *QoderTokenRefresher) CacheKey(account *Account) string {
	return QoderTokenCacheKey(account)
}

func (r *QoderTokenRefresher) CanRefresh(account *Account) bool {
	return account != nil && account.Platform == PlatformQoder && account.Type == AccountTypeOAuth &&
		qoderAccountRefreshToken(account) != ""
}

func (r *QoderTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	if account == nil || qoderAccountRefreshToken(account) == "" {
		return false
	}
	if qoderAccountToken(account) == "" {
		return true
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		return true
	}
	if refreshWindow < qoderTokenRefreshSkew {
		refreshWindow = qoderTokenRefreshSkew
	}
	return time.Until(*expiresAt) < refreshWindow
}

func (r *QoderTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	if r == nil || r.oauthService == nil {
		return nil, errors.New("qoder oauth service is not configured")
	}
	refreshToken := qoderAccountRefreshToken(account)
	if refreshToken == "" {
		return nil, errors.New("qoder account has no refresh token")
	}
	token, err := r.oauthService.RefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	newCredentials := map[string]any{
		"access_token":  token.AccessToken,
		"refresh_token": token.RefreshToken,
	}
	if token.ExpiresAt > 0 {
		newCredentials["expires_at"] = time.Unix(token.ExpiresAt, 0).UTC().Format(time.RFC3339)
	}
	newCredentials = MergeCredentials(account.Credentials, newCredentials)
	if baseURL := strings.TrimSpace(account.GetCredential("base_url")); baseURL != "" {
		newCredentials["base_url"] = baseURL
	}
	return newCredentials, nil
}

// qoderCredentialToken 从刷新后的 credentials 中读取新的设备令牌。
func qoderCredentialToken(credentials map[string]any) string {
	if credentials == nil {
		return ""
	}
	if token, _ := credentials["access_token"].(string); strings.TrimSpace(token) != "" {
		return strings.TrimSpace(token)
	}
	token, _ := credentials["api_key"].(string)
	return strings.TrimSpace(token)
}
