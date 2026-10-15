package service

import (
	"context"
	"net/mail"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const PlatformMail = "mail"

// ValidateMailAccount 复用敏感 api_key 字段保存授权码，避免列表接口泄露凭据。
func ValidateMailAccount(platform, kind string, credentials map[string]any) error {
	if platform != PlatformMail {
		return nil
	}
	value := func(key string) string { v, _ := credentials[key].(string); return strings.TrimSpace(v) }
	address, err := mail.ParseAddress(value("email"))
	if err != nil || address.Address != value("email") || kind != AccountTypeAPIKey || value("api_key") == "" {
		return infraerrors.BadRequest("MAIL_ACCOUNT_INVALID", "Email and application password or authorization code are required")
	}
	switch value("mail_provider") {
	case "google", "outlook", "qq", "163":
	default:
		return infraerrors.BadRequest("MAIL_ACCOUNT_INVALID", "Unsupported mail provider")
	}
	return nil
}

// MailAccountBody 用保存的身份覆盖请求中的凭据和代理，选定代理不可静默降级直连。
func MailAccountBody(account *Account, body map[string]any) error {
	if account == nil || account.Platform != PlatformMail || account.Status != StatusActive || (account.ExpiresAt != nil && !account.ExpiresAt.After(time.Now())) {
		return infraerrors.BadRequest("MAIL_ACCOUNT_UNAVAILABLE", "Mail account unavailable")
	}
	if err := ValidateMailAccount(account.Platform, account.Type, account.Credentials); err != nil {
		return err
	}
	for _, key := range []string{"account_id", "credential", "proxy", "imap_host", "imap_port", "smtp_host", "smtp_port", "from_name"} {
		delete(body, key)
	}
	body["email"] = account.GetCredential("email")
	body["provider"] = account.GetCredential("mail_provider")
	body["credential"] = account.GetCredential("api_key")
	if account.ProxyID != nil {
		if account.Proxy == nil || !account.Proxy.IsActive() || account.Proxy.IsExpired(time.Now()) {
			return infraerrors.BadRequest("MAIL_PROXY_UNAVAILABLE", "Mail account proxy unavailable")
		}
		switch account.Proxy.Protocol {
		case "http", "socks5", "socks5h":
		default:
			return infraerrors.BadRequest("MAIL_PROXY_UNSUPPORTED", "Mail requires HTTP CONNECT or SOCKS5 proxy")
		}
		body["proxy"] = account.Proxy.URL()
	}
	return nil
}

// ResolveMailAccountBody 限制 API Key 只能使用其分组内的邮件账号。
func (s *GatewayService) ResolveMailAccountBody(ctx context.Context, id, groupID int64, body map[string]any) (*Account, error) {
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, infraerrors.NotFound("MAIL_ACCOUNT_UNAVAILABLE", "Mail account unavailable")
	}
	allowed := false
	for _, group := range account.GroupIDs {
		if group == groupID {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, infraerrors.NotFound("MAIL_ACCOUNT_UNAVAILABLE", "Mail account unavailable")
	}
	if err := MailAccountBody(account, body); err != nil {
		return nil, err
	}
	return account, nil
}
