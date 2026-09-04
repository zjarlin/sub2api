package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DeepSeek Responses implements ordinary response generation but does not
// emit OpenAI's native compaction item for compaction_trigger. Use one normal
// summary turn and translate its response back to the Responses compaction
// shape expected by Codex.
const deepSeekCompactSummaryPrompt = `Summarize the conversation so a successor coding assistant can continue it after the earlier turns are removed. Preserve the user's explicit requests, decisions, files, commands, configuration, errors, completed work, unresolved issues, and the next concrete step. Be concise but complete. Output only the summary, with no preamble or analysis.`

func isDeepSeekNativeCompaction(c *gin.Context, account *Account) bool {
	return account != nil && account.Platform == PlatformDeepseek && !account.IsOpenAIPassthroughEnabled() && isOpenAINativeCompactionV2(c)
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

func convertDeepSeekResponseToOpenAICompact(body []byte) ([]byte, error) {
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode DeepSeek response: %w", err)
	}

	output, ok := response["output"].([]any)
	if !ok {
		return nil, fmt.Errorf("DeepSeek response has no output array")
	}

	var encrypted string
	var summaryParts []string
	for _, raw := range output {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(stringValue(item["type"])) {
		case "reasoning":
			if value := strings.TrimSpace(stringValue(item["encrypted_content"])); value != "" {
				encrypted = value
			}
		case "message":
			if text := deepSeekOutputText(item["content"]); text != "" {
				summaryParts = append(summaryParts, text)
			}
		}
	}
	if encrypted == "" {
		return nil, fmt.Errorf("DeepSeek response has no reasoning.encrypted_content")
	}

	compactItem := map[string]any{
		"id":                "cmp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		"type":              "compaction",
		"status":            "completed",
		"encrypted_content": encrypted,
	}
	if summary := strings.TrimSpace(strings.Join(summaryParts, "\n")); summary != "" {
		// Keep the visible summary as a gateway extension. It is converted back to
		// a user message before the next stateless DeepSeek Responses request.
		compactItem["summary"] = []any{map[string]any{
			"type": "summary_text",
			"text": summary,
		}}
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
