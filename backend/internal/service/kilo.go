package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Kilo AI 公共网关。免费池（listing 中 isFree=true 的模型）无鉴权、无登录态，
// 只支持 OpenAI Chat Completions。付费 id 需要 Kilo 账号，keyless 请求返回 401。
const (
	kiloPublicCredential = "public"
)

func (a *Account) IsKilo() bool {
	return a != nil && a.Platform == PlatformKilo
}

// Kilo 免费池不需要真实凭证，但账号表要求凭据非空；统一注入占位值，
// 出站时再翻译为网关约定的无鉴权形态。
func normalizeKiloCredentials(credentials map[string]any) {
	if credentials == nil {
		return
	}
	if value, _ := credentials["api_key"].(string); strings.TrimSpace(value) == "" {
		credentials["api_key"] = kiloPublicCredential
	}
	if value, _ := credentials["base_url"].(string); strings.TrimSpace(value) == "" {
		credentials["base_url"] = DefaultKiloBaseURL
	}
	if value, _ := credentials["api_protocol"].(string); strings.TrimSpace(value) == "" {
		credentials["api_protocol"] = APIProtocolChatCompletions
	}
}

func validateKiloCredentials(platform, accountType string, credentials map[string]any) error {
	if platform != PlatformKilo {
		return nil
	}
	if accountType != AccountTypeAPIKey {
		return infraerrors.BadRequest("INVALID_KILO_CREDENTIALS", "kilo requires an API key account")
	}
	normalizeKiloCredentials(credentials)
	protocol, _ := credentials["api_protocol"].(string)
	if protocol != "" && protocol != APIProtocolChatCompletions {
		return infraerrors.BadRequest("INVALID_KILO_CREDENTIALS", "kilo only supports chat_completions")
	}
	return nil
}

// applyKiloHeaders 复刻 dsh 免费车道的请求形态：不带 Authorization，
// 使用稳定 UA；Kilo 网关对无凭证请求直接走免费池。
func applyKiloHeaders(headers http.Header, model string) {
	if headers == nil {
		return
	}
	headers.Del("Authorization")
	headers.Del("x-api-key")
	if strings.TrimSpace(headers.Get("User-Agent")) == "" {
		headers.Set("User-Agent", "sub2api-kilo")
	}
	_ = model
}

// Kilo 免费模型 id 以 org/model 形式出现，保留原样发送；这里仅做会话/请求 id
// 生成，保证同一对话稳定落在同一上游会话。
func kiloSessionForConversation(seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		seed = "global"
	}
	digest := sha256.Sum256([]byte("sub2api-kilo\x00" + seed))
	return "kilo_" + hex.EncodeToString(digest[:16])
}

func kiloRequestID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return "kilo_" + hex.EncodeToString(buf[:])
}
