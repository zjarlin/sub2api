package service

import (
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// v0 (Vercel) 应用生成平台。
//
// 与 model3d / systemone 同属「服务型平台」：账号持上游服务凭据，
// 网关按分组调度账号后把 /v1/v0/* 透传到 api.v0.dev/v2/*。
// 上游固定为 v0 官方 API v2，避免凭据被转发到非官方地址。
const (
	// V0DefaultBaseURL 是 v0 公开 API 的默认基地址。
	V0DefaultBaseURL = "https://api.v0.dev/v2"
	// V0CredentialAPIKey 是账号上保存 v0 API key 的凭据字段。
	V0CredentialAPIKey = "api_key"
	// V0CredentialBaseURL 是可选的上游基地址覆盖字段。
	V0CredentialBaseURL = "base_url"
)

// ValidateV0Account 校验 v0 账号只接受 API key 类型，并约束 base_url 形状。
// 与 model3d 一致：不允许把网页会话或 OAuth 凭据塞进这个平台。
func ValidateV0Account(platform, accountType string, credentials map[string]any) error {
	if platform != PlatformV0 {
		return nil
	}
	if accountType != AccountTypeAPIKey && accountType != "" {
		return infraerrors.BadRequest("V0_ACCOUNT_INVALID", "v0 accounts require apikey credentials")
	}
	if v0CredentialString(credentials, V0CredentialAPIKey) == "" {
		return infraerrors.BadRequest("V0_ACCOUNT_INVALID", "v0 API key is required")
	}
	if base := v0CredentialString(credentials, V0CredentialBaseURL); base != "" {
		parsed, err := url.Parse(base)
		if err != nil || parsed.Hostname() == "" ||
			(parsed.Scheme != "https" || parsed.Host != "api.v0.dev" || parsed.Path != "/v2") ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return infraerrors.BadRequest("V0_ACCOUNT_INVALID", "Invalid v0 upstream URL")
		}
	}
	return nil
}

// V0ModelInfo 描述一个 v0 应用生成模型档位。
type V0ModelInfo struct {
	ID          string
	DisplayName string
}

// V0ModelCatalog 返回 v0 官方文档公开的模型档位。
//
// v0 没有 OpenAI 风格的 /models 探测端点，模型只作为 chats 请求里的
// modelConfiguration.modelId 生效，因此目录内置在网关侧而不是向上游探测。
func V0ModelCatalog() []V0ModelInfo {
	return []V0ModelInfo{
		{ID: "v0-mini", DisplayName: "v0 Mini"},
		{ID: "v0-pro", DisplayName: "v0 Pro"},
		{ID: "v0-max", DisplayName: "v0 Max"},
		{ID: "v0-max-fast", DisplayName: "v0 Max Fast"},
	}
}

// V0DefaultModelIDs 返回目录中的模型 ID，供分组默认模型与上游目录同步复用。
func V0DefaultModelIDs() []string {
	catalog := V0ModelCatalog()
	ids := make([]string, 0, len(catalog))
	for _, model := range catalog {
		ids = append(ids, model.ID)
	}
	return ids
}

// V0AccountBaseURL 返回已通过校验的上游基地址，未填写时使用官方地址。
func V0AccountBaseURL(account *Account) string {
	if account == nil {
		return V0DefaultBaseURL
	}
	if base := strings.TrimRight(strings.TrimSpace(account.GetCredential(V0CredentialBaseURL)), "/"); base != "" {
		return base
	}
	return V0DefaultBaseURL
}

// V0AccountAPIKey 返回账号保存的 v0 API key。
func V0AccountAPIKey(account *Account) string {
	if account == nil {
		return ""
	}
	return strings.TrimSpace(account.GetCredential(V0CredentialAPIKey))
}

// v0CredentialString 从凭据 map 读取字符串字段（兼容非 string 的历史值）。
func v0CredentialString(credentials map[string]any, key string) string {
	value, ok := credentials[key]
	if !ok || value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}
