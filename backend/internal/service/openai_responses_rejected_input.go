package service

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// 部分 Responses 兼容上游只返回 input 整体校验失败，不给出条目索引。
// 仅转换已有等价表示的内容；未知条目、服务端引用和压缩密文始终保留。
func normalizeOpenAIResponsesRejectedInput(body []byte) ([]byte, string, bool, error) {
	var request map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &request); err != nil {
		return nil, "", false, err
	}
	input, ok := request["input"].([]any)
	if !ok {
		return nil, "", false, nil
	}
	if tools, exists := request["tools"]; exists && tools != nil {
		if _, ok := tools.([]any); !ok {
			return nil, "", false, nil
		}
	}
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || item["type"] != "additional_tools" {
			continue
		}
		if _, ok := item["tools"].([]any); !ok {
			return nil, "", false, nil
		}
	}
	changed, err := liftResponsesAdditionalTools(request)
	if err != nil {
		return nil, "", false, err
	}
	input = request["input"].([]any)
	for index, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch item["type"] {
		case "agent_message":
			if message, converted := apicompat.NormalizeResponsesAgentMessage(item); converted {
				input[index] = message
				changed = true
			}
		case nil, "", "message":
			changed = normalizeRejectedResponsesMessageContent(item) || changed
		}
	}
	if !changed {
		return nil, "", false, nil
	}
	retryBody, err := marshalOpenAIUpstreamJSON(request)
	return retryBody, "Responses input compatibility rejection", true, err
}

func normalizeRejectedResponsesMessageContent(item map[string]any) bool {
	role, _ := item["role"].(string)
	switch role {
	case "user", "assistant", "system", "developer":
	default:
		return false
	}
	if content, exists := item["content"]; exists && content == nil {
		item["content"] = ""
		return true
	}
	if role != "assistant" {
		return false
	}
	content, _ := item["content"].([]any)
	changed := false
	for _, raw := range content {
		part, ok := raw.(map[string]any)
		if !ok || part["type"] != "refusal" {
			continue
		}
		text, ok := part["refusal"].(string)
		if !ok {
			continue
		}
		// 保留拒绝答复的完整文本，避免重放历史时丢失这次答复。
		part["type"] = "output_text"
		part["text"] = text
		delete(part, "refusal")
		changed = true
	}
	return changed
}
