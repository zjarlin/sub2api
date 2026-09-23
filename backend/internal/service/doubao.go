package service

import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 豆包平台连接桌面会话适配器，不把凭据发送到火山方舟或其他默认地址。
const DefaultDoubaoTestModel = "doubao-chat-turbo"

func DefaultDoubaoModelIDs() []string {
	return []string{DefaultDoubaoTestModel, "doubao-auto", "doubao-pro"}
}

func (a *Account) IsDoubao() bool {
	return a != nil && a.Platform == PlatformDoubao
}

// 创建、复制与更新统一校验，禁止其他平台的默认地址或协议残留。
func validateDoubaoCredentials(platform, accountType string, credentials map[string]any) error {
	if platform != PlatformDoubao {
		return nil
	}
	if accountType != AccountTypeAPIKey {
		return infraerrors.BadRequest("INVALID_DOUBAO_CREDENTIALS", "doubao requires an API key account connected to the desktop adapter")
	}
	if !BuiltinAdapterEnabled() {
		return infraerrors.BadRequest("INVALID_DOUBAO_CREDENTIALS", "doubao requires the built-in desktop adapter; enable it in the server configuration")
	}
	key, _ := credentials["api_key"].(string)
	if strings.TrimSpace(key) == "" {
		return infraerrors.BadRequest("INVALID_DOUBAO_CREDENTIALS", "doubao requires the built-in desktop adapter API key")
	}
	if c, ok := credentials["api_protocol"].(string); ok && c != "" && c != APIProtocolChatCompletions {
		return infraerrors.BadRequest("INVALID_DOUBAO_CREDENTIALS", "doubao only supports the chat_completions upstream protocol")
	}
	return nil
}
