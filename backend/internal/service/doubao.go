package service

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 豆包平台连接桌面会话适配器，不把凭据发送到火山方舟或其他默认地址。
const DefaultDoubaoTestModel = "doubao-chat-turbo"

var doubaoSessionProfilePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

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
	if value, exists := credentials["desktop_session"]; exists {
		profile, ok := value.(string)
		if !ok || (profile != "" && !doubaoSessionProfilePattern.MatchString(profile)) {
			return infraerrors.BadRequest("INVALID_DOUBAO_CREDENTIALS", "doubao desktop_session must contain only letters, numbers, hyphens, or underscores")
		}
	}
	if c, ok := credentials["api_protocol"].(string); ok && c != "" && c != APIProtocolChatCompletions && c != APIProtocolResponses && c != APIProtocolAdaptive {
		return infraerrors.BadRequest("INVALID_DOUBAO_CREDENTIALS", "doubao supports responses and chat_completions through the desktop adapter")
	}
	return nil
}

// doubaoChatUnsupportedFields 是桌面适配器 Chat Completions 入口明确拒绝、
// 但 OpenAI 兼容客户端可能自动携带的字段。store 只是服务端持久化开关：
// 适配器不提供 Chat 存储，会话续接由 X-Desktop-Session 与适配器自身状态维护，
// 忽略该字段不改变生成语义；原样透传会让整条请求以 400 失败。
var doubaoChatUnsupportedFields = []string{"store"}

// sanitizeDoubaoChatCompletionsBody 在共用 Chat 出站点移除适配器不接受的字段。
// 原生 Chat 直转、Responses 回退与 Anthropic 回退都经过 sendCCUpstreamRequest，
// 因此在这里统一处理，避免只为某一条路径打补丁。
func sanitizeDoubaoChatCompletionsBody(account *Account, body []byte) ([]byte, error) {
	if account == nil || !account.IsDoubao() || len(body) == 0 {
		return body, nil
	}
	out := body
	for _, field := range doubaoChatUnsupportedFields {
		if !gjson.GetBytes(out, field).Exists() {
			continue
		}
		next, err := sjson.DeleteBytes(out, field)
		if err != nil {
			return body, fmt.Errorf("remove unsupported doubao chat field %s: %w", field, err)
		}
		out = next
	}
	return out, nil
}

// 会话选择只来自账号凭据，不能由调用方请求头覆盖。
func setDoubaoProfileHeader(header http.Header, account *Account) {
	if !account.IsDoubao() {
		return
	}
	profile := strings.TrimSpace(account.GetCredential("desktop_session"))
	if profile == "" {
		profile = "default"
	}
	header.Set("X-Desktop-Profile", profile)
}
