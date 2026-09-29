package service

import (
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	accountTestHealthProbeContextKey = "account_test_health_probe"
	accountTestHealthTextContextKey  = "account_test_health_text"
	accountHealthProbePrompt         = "Reply OK."
	// 为默认开启推理的模型保留有限空间；这是上限，不是要求生成这么多 token。
	accountHealthProbeMaxTokens = 256
)

// 周期健康探测只验证最小文本推理，不额外验证工具、图像或其他协议。
func applyAccountHealthProbePayload(c *gin.Context, account *Account, payload map[string]any, protocol string) {
	if !c.GetBool(accountTestHealthProbeContextKey) {
		return
	}
	switch protocol {
	case APIProtocolAnthropic:
		payload["messages"] = []map[string]any{{"role": "user", "content": accountHealthProbePrompt}}
		payload["max_tokens"] = accountHealthProbeMaxTokens
		delete(payload, "temperature")
		// OAuth 仍需客户端身份，其余账号不携带合成身份与缓存写入配置。
		if account.Type != AccountTypeOAuth {
			delete(payload, "system")
			delete(payload, "metadata")
		}
	case APIProtocolChatCompletions:
		payload["messages"] = []map[string]any{{"role": "user", "content": accountHealthProbePrompt}}
		// VibeX 明确拒绝 token 上限参数，保留短提示和请求超时约束。
		if !account.IsVibex() {
			model, _ := payload["model"].(string)
			budgetField := "max_tokens"
			if hasOpenAISeriesPrefix(strings.ToLower(codexProviderQualifiedModelID(model))) || isOpenAICodexReasoningGPTModel(model) {
				budgetField = "max_completion_tokens"
			}
			payload[budgetField] = accountHealthProbeMaxTokens
		}
	case APIProtocolResponses:
		// Codex OAuth 与 Agent Identity 的原生端点要求消息数组。
		payload["input"] = []map[string]any{{
			"role":    "user",
			"content": []map[string]any{{"type": "input_text", "text": accountHealthProbePrompt}},
		}}
		if account.Type != AccountTypeOAuth && !account.IsOpenAIAgentIdentity() {
			delete(payload, "instructions")
			if !account.IsVibex() {
				payload["max_output_tokens"] = accountHealthProbeMaxTokens
			}
		}
	}
}
