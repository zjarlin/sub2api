package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// DeepSeek/GLM 压缩通过普通摘要回合完成，再转换为 Codex 所需的压缩项。
const deepSeekCompactSummaryPrompt = `Summarize the conversation so a successor coding assistant can continue it after the earlier turns are removed. Preserve the user's explicit requests, decisions, files, commands, configuration, errors, completed work, unresolved issues, and the next concrete step. Be concise but complete. Output only the summary, with no preamble or analysis.`

const deepSeekCompactContextKey = "deepseek_compact_bridge"
const deepSeekCompactTokenPrefix = "sub2api_compact_v1:"

func isDeepSeekNativeCompaction(c *gin.Context) bool {
	return c != nil && c.GetBool(deepSeekCompactContextKey)
}

// 按最终上游模型识别兼容账号，避免平台标签为 openai 时漏掉 DeepSeek/GLM。
func shouldBridgeDeepSeekCompaction(c *gin.Context, account *Account, body []byte) bool {
	if account == nil || account.Type != AccountTypeAPIKey || account.IsOpenAIPassthroughEnabled() ||
		!isOpenAINativeCompactionV2(c) || account.IsAnthropicProtocol() {
		return false
	}
	rawChat := shouldForwardOpenAIResponsesViaRawChatCompletions(account)
	// 国产 OpenAI 兼容上游（DeepSeek/GLM）无论走原生 Responses 还是 raw Chat
	// Completions，都不会返回 Codex 要求的单个 compaction 输出项，统一走摘要桥接。
	// 原生 Responses 在 handleNonStreamingResponse 转换；raw Chat 在
	// bufferChatCompletionsAsResponses 转换。
	if account.Platform == PlatformDeepseek || account.Platform == PlatformZhipu {
		return true
	}
	if account.Platform != PlatformOpenAI {
		return false
	}
	_, model := resolveOpenAIForwardMappedModels(account, gjson.GetBytes(body, "model").String(), false)
	if isDeepSeekCodexModel(model) {
		return !rawChat
	}
	// platform=openai 但映射到 GLM 的第三方账号同样走 raw Chat Completions，
	// 必须在这里进入桥接；非 raw Chat 路由保持原行为。
	return isGLMCodexModel(model) && rawChat
}

func isGLMCodexModel(modelID string) bool {
	model := strings.ToLower(lastOpenAIModelSegment(modelID))
	return strings.HasPrefix(model, "glm-") || strings.HasPrefix(model, "chatglm")
}

// 复用现有 AES-GCM 实现，以独立密钥域保护摘要；不依赖客户端保留扩展字段或缓存 TTL。
func (s *OpenAIGatewayService) deepSeekCompactionCipher() SecretEncryptor {
	if s == nil || s.cfg == nil || strings.TrimSpace(s.cfg.JWT.Secret) == "" {
		return nil
	}
	return &liveAttestationAES{key: sha256.Sum256([]byte("sub2api/deepseek-compaction/v1\x00" + s.cfg.JWT.Secret))}
}

// 在通用历史清理前还原网关自己的压缩项，保证 Codex 仅回传 encrypted_content 时仍能续聊。
func (s *OpenAIGatewayService) restoreDeepSeekCompaction(body []byte) ([]byte, error) {
	if !bytes.Contains(body, []byte(deepSeekCompactTokenPrefix)) {
		return body, nil
	}
	var payload map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &payload); err != nil {
		return nil, err
	}
	items, ok := payload["input"].([]any)
	if !ok {
		return body, nil
	}
	for index, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok || !isOpenAICompactionType(stringValue(item["type"])) {
			continue
		}
		token := stringValue(item["encrypted_content"])
		if !strings.HasPrefix(token, deepSeekCompactTokenPrefix) {
			continue
		}
		cipher := s.deepSeekCompactionCipher()
		if cipher == nil {
			return nil, fmt.Errorf("DeepSeek compaction requires a configured JWT secret")
		}
		summary, err := cipher.Decrypt(strings.TrimPrefix(token, deepSeekCompactTokenPrefix))
		if err != nil {
			return nil, fmt.Errorf("decrypt DeepSeek compaction: %w", err)
		}
		items[index] = map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "<conversation_summary>\n" + summary + "\n</conversation_summary>"}},
		}
	}
	payload["input"] = items
	return json.Marshal(payload)
}

func buildDeepSeekCompactRequestBody(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode DeepSeek compact request: %w", err)
	}

	input, err := normalizeDeepSeekCompactInput(payload["input"])
	if err != nil {
		return nil, err
	}
	input = removeDeepSeekCompactionTriggers(input)
	input = append(input, map[string]any{
		"type": "message",
		"role": "user",
		"content": []any{map[string]any{
			"type": "input_text",
			"text": deepSeekCompactSummaryPrompt,
		}},
	})
	payload["input"] = input
	payload["stream"] = false
	payload["store"] = false
	delete(payload, "tools")
	delete(payload, "tool_choice")
	delete(payload, "parallel_tool_calls")
	delete(payload, "previous_response_id")

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode DeepSeek compact request: %w", err)
	}
	return encoded, nil
}

func normalizeDeepSeekCompactInput(value any) ([]any, error) {
	switch input := value.(type) {
	case nil:
		return []any{}, nil
	case []any:
		return convertDeepSeekCompactionItems(input), nil
	case string:
		return []any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": input,
			}},
		}}, nil
	case map[string]any:
		return []any{input}, nil
	default:
		return nil, fmt.Errorf("DeepSeek compact input must be a string, object, or array")
	}
}

func normalizeDeepSeekCompactionReplayBody(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, err
	}
	input, ok := payload["input"].([]any)
	if !ok {
		return body, nil
	}
	payload["input"] = convertDeepSeekCompactionItems(input)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body, err
	}
	return encoded, nil
}

func (s *OpenAIGatewayService) convertDeepSeekResponseToOpenAICompact(body []byte) ([]byte, error) {
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode DeepSeek response: %w", err)
	}

	output, ok := response["output"].([]any)
	if !ok {
		return nil, fmt.Errorf("DeepSeek response has no output array")
	}

	if status := stringValue(response["status"]); status != "completed" {
		return nil, fmt.Errorf("DeepSeek compact response is not completed: %s", status)
	}
	var summaryParts []string
	for _, raw := range output {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(stringValue(item["type"])) {
		case "message":
			if text := deepSeekOutputText(item["content"]); text != "" {
				summaryParts = append(summaryParts, text)
			}
		}
	}
	summary := strings.TrimSpace(strings.Join(summaryParts, "\n"))
	if summary == "" {
		return nil, fmt.Errorf("DeepSeek compact response has no summary text")
	}
	cipher := s.deepSeekCompactionCipher()
	if cipher == nil {
		return nil, fmt.Errorf("DeepSeek compaction requires a configured JWT secret")
	}
	encrypted, err := cipher.Encrypt(summary)
	if err != nil {
		return nil, fmt.Errorf("encrypt DeepSeek compaction: %w", err)
	}
	compactItem := map[string]any{
		"id":                "cmp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		"type":              "compaction",
		"encrypted_content": deepSeekCompactTokenPrefix + encrypted,
	}
	response["output"] = []any{compactItem}
	response["status"] = "completed"
	delete(response, "output_text")

	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode DeepSeek compact response: %w", err)
	}
	return encoded, nil
}

// writeCompatibleCompactionJSON 把普通摘要回合产生的 Responses 结果转换为
// Codex remote compaction v2 要求的单个 compaction item，并按客户端原始 stream
// 意图写回（合成 SSE 或 JSON）。DeepSeek 原生 Responses 路径在
// handleNonStreamingResponse 中直接转换；GLM 等只支持 Chat Completions 的上游
// 经 raw Chat Completions 回退后调用本函数。
func (s *OpenAIGatewayService) writeCompatibleCompactionJSON(c *gin.Context, statusCode int, responseBody []byte) error {
	converted, err := s.convertDeepSeekResponseToOpenAICompact(responseBody)
	if err != nil {
		return err
	}
	if writeOpenAICompactSSEBridge(c, statusCode, converted) {
		return nil
	}
	c.Data(statusCode, "application/json", converted)
	return nil
}

func convertDeepSeekCompactionItems(items []any) []any {
	if len(items) == 0 {
		return items
	}
	converted := make([]any, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok || !isOpenAICompactionType(stringValue(item["type"])) {
			converted = append(converted, raw)
			continue
		}
		summary := deepSeekCompactSummaryText(item["summary"])
		if summary == "" {
			converted = append(converted, raw)
			continue
		}
		converted = append(converted, map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": "<conversation_summary>\n" + summary + "\n</conversation_summary>",
			}},
		})
	}
	return converted
}

func removeDeepSeekCompactionTriggers(items []any) []any {
	filtered := make([]any, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if ok && strings.TrimSpace(stringValue(item["type"])) == "compaction_trigger" {
			continue
		}
		filtered = append(filtered, raw)
	}
	return filtered
}

func deepSeekCompactSummaryText(value any) string {
	parts, ok := value.([]any)
	if !ok {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if text := strings.TrimSpace(stringValue(part["text"])); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

func deepSeekOutputText(value any) string {
	parts, ok := value.([]any)
	if !ok {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if text := strings.TrimSpace(stringValue(part["text"])); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}
