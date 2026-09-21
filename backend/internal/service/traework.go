package service

// traework2api 内置反向代理，把 TRAE Work (SOLO CN) 通道包装成 OpenAI 兼容接口。
// 凭证是 TRAE 登录后的 refresh/token，随 Sub2API 部署自动注入。
import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// traework 平台连接内置 traework2api sidecar，不把凭据发送到外部默认地址。
const DefaultTraeworkModel = "glm-5.2"

func DefaultTraeworkModelIDs() []string {
	return []string{DefaultTraeworkModel}
}

func (a *Account) IsTraework() bool {
	return a != nil && a.Platform == PlatformTraework
}

// 创建、复制与更新统一校验，禁止其他平台的默认地址或协议残留。
func validateTraeworkCredentials(platform, accountType string, credentials map[string]any) error {
	if platform != PlatformTraework {
		return nil
	}
	if accountType != AccountTypeAPIKey {
		return infraerrors.BadRequest("INVALID_TRAEWORK_CREDENTIALS", "traework requires an API key account connected to the built-in traework2api sidecar")
	}
	if !BuiltinAdapterEnabled() {
		return infraerrors.BadRequest("INVALID_TRAEWORK_CREDENTIALS", "traework requires the built-in traework2api sidecar; enable it in the server configuration")
	}
	key, _ := credentials["api_key"].(string)
	if strings.TrimSpace(key) == "" {
		return infraerrors.BadRequest("INVALID_TRAEWORK_CREDENTIALS", "traework requires the sidecar API key")
	}
	protocol, _ := credentials["api_protocol"].(string)
	if protocol != "" && protocol != APIProtocolChatCompletions {
		return infraerrors.BadRequest("INVALID_TRAEWORK_CREDENTIALS", "traework only supports the chat_completions upstream protocol")
	}
	return nil
}
