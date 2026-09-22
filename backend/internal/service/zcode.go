package service

import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ZCode 平台走内置 glm-zcode-2api 适配器，把 z.ai / 智谱 GLM Coding Plan
// 的 Anthropic 通道包装成 OpenAI 兼容接口。
const DefaultZcodeModel = "glm-5.3"

func DefaultZcodeModelIDs() []string { return []string{DefaultZcodeModel, "glm-5.3-flash"} }

func (a *Account) IsZcode() bool { return a != nil && a.Platform == PlatformZcode }

// validateZcodeCredentials 校验内置适配器账号：只支持 apikey，
// 地址与共享密钥由后端注入，协议固定 chat_completions。
func validateZcodeCredentials(platform, accountType string, credentials map[string]any) error {
	if platform != PlatformZcode {
		return nil
	}
	if accountType != AccountTypeAPIKey {
		return infraerrors.BadRequest("INVALID_ZCODE_CREDENTIALS", "zcode requires an API key account connected to the built-in zcode adapter")
	}
	if !BuiltinAdapterEnabled() {
		return infraerrors.BadRequest("INVALID_ZCODE_CREDENTIALS", "zcode requires the built-in zcode adapter; enable it in the server configuration")
	}
	key, _ := credentials["api_key"].(string)
	if strings.TrimSpace(key) == "" {
		return infraerrors.BadRequest("INVALID_ZCODE_CREDENTIALS", "zcode requires the adapter API key")
	}
	protocol, _ := credentials["api_protocol"].(string)
	if protocol != "" && protocol != APIProtocolChatCompletions {
		return infraerrors.BadRequest("INVALID_ZCODE_CREDENTIALS", "zcode only supports the chat_completions upstream protocol")
	}
	return nil
}
