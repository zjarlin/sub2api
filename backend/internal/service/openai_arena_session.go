package service

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"maps"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const arenaSessionAdapterContextKey = "arena_session_adapter"

func isArenaSessionAdapter(account *Account) bool {
	return account != nil && account.Type == AccountTypeAPIKey && (account.IsArena() || (account.Platform == PlatformOpenAI && account.GetExtraString("openai_session_adapter") == "arena"))
}

func validateArenaCredentials(accountType string, credentials map[string]any) error {
	baseURL, _ := credentials["base_url"].(string)
	apiKey, _ := credentials["api_key"].(string)
	protocol, _ := credentials["api_protocol"].(string)
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if accountType != AccountTypeAPIKey || strings.TrimSpace(apiKey) == "" || err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return infraerrors.BadRequest("INVALID_ARENA_CREDENTIALS", "arena requires an API key account with an explicit adapter URL and adapter key")
	}
	if protocol != "" && protocol != APIProtocolChatCompletions {
		return infraerrors.BadRequest("INVALID_ARENA_CREDENTIALS", "arena only supports chat_completions")
	}
	return nil
}

// 专属会话不允许自动生成探测，平台固定经 Chat Completions 传递会话身份。
func normalizeArenaAccountExtra(platform string, extra map[string]any) map[string]any {
	if platform != PlatformArena {
		return extra
	}
	normalized := maps.Clone(extra)
	if normalized == nil {
		normalized = make(map[string]any)
	}
	normalized["openai_session_adapter"] = "arena"
	normalized["openai_responses_mode"] = "force_chat_completions"
	normalized["model_health_probe_enabled"] = false
	return normalized
}

// Arena 会话是有状态资源，仅为明确启用的账号传递经过租户隔离的身份。
func applyArenaSessionHeaders(c *gin.Context, account *Account, headers http.Header, body []byte) {
	if !isArenaSessionAdapter(account) {
		return
	}
	for name := range headers {
		if strings.EqualFold(name, "X-Arena-Session-Id") || strings.EqualFold(name, "X-Arena-Idempotency-Key") || strings.EqualFold(name, "Idempotency-Key") || strings.EqualFold(name, "X-Codex-Session-Id") || strings.EqualFold(name, "X-Session-Id") || strings.EqualFold(name, "X-Arena-Workspace") {
			delete(headers, name)
		}
	}
	apiKeyID := getAPIKeyIDFromContext(c)
	if c == nil || c.Request == nil || apiKeyID <= 0 {
		return
	}
	sessionID := resolveArenaSessionID(c, body)
	if sessionID == "" {
		return
	}
	headers.Set("X-Arena-Session-Id", arenaNamespacedIdentity(apiKeyID, sessionID))
	for _, name := range []string{"X-Arena-Idempotency-Key", "Idempotency-Key"} {
		if identity := sanitizeSessionID(c.GetHeader(name)); identity != "" {
			headers.Set("X-Arena-Idempotency-Key", arenaNamespacedIdentity(apiKeyID, sessionID+"\x00"+identity))
			break
		}
	}
}

func resolveArenaSessionID(c *gin.Context, body []byte) string {
	for _, name := range []string{"X-Arena-Session-Id", "X-Codex-Session-Id", "X-Session-Id", "session_id", "conversation_id"} {
		if sessionID := sanitizeSessionID(c.GetHeader(name)); sessionID != "" {
			return sessionID
		}
	}
	for _, payload := range append(openCodeInboundBodies(c), body) {
		for _, field := range []string{"session_id", "conversation_id", "prompt_cache_key", "metadata.session_id"} {
			if sessionID := sanitizeSessionID(gjson.GetBytes(payload, field).String()); sessionID != "" {
				return sessionID
			}
		}
		if sessionID := sanitizeSessionID(openCodeSessionIDFromPayload(payload)); sessionID != "" {
			return sessionID
		}
	}
	return ""
}

func arenaNamespacedIdentity(apiKeyID int64, identity string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("arena:%d:%s", apiKeyID, identity)))
	return fmt.Sprintf("sub2api-%x", digest)
}
