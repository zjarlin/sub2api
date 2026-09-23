package apicompat

import (
	"encoding/json"
	"fmt"
	"strings"
)

type responsesToolOutputMedia struct {
	callID   string
	imageURL string
}

// LiftResponsesToolOutputMedia 将工具结果编码为字符串，并把图片移到后续用户消息。
// DeepSeek 等原生 Responses 端点无法直接读取数组形式的工具结果；
// 并行结果之间的开发者通知统一保留在结果批次之后，避免上游误报缺少结果。
func LiftResponsesToolOutputMedia(input any) (any, bool) {
	items, ok := input.([]any)
	if !ok {
		return input, false
	}

	rewritten := make([]any, 0, len(items)+1)
	changed := false

	// DeepSeek 要求并行工具结果连续出现，开发者通知在结果和图片之后发送。
	for index := 0; index < len(items); {
		item, ok := items[index].(map[string]any)
		if !ok || !isResponsesToolOutputItem(item) {
			rewritten = append(rewritten, items[index])
			index++
			continue
		}

		batchStart := index
		outputs := make([]any, 0)
		trailing := make([]any, 0)
		pending := make([]responsesToolOutputMedia, 0)
		batchChanged := false

		for index < len(items) {
			rawItem := items[index]
			item, ok := rawItem.(map[string]any)
			if !ok {
				break
			}
			if isResponsesToolOutputItem(item) {
				// 开发者通知可能插在并行结果之间；先发完结果再保留通知。
				if len(trailing) > 0 {
					batchChanged = true
				}
				rewrittenItem, media, didRewrite := liftResponsesToolOutputMediaItem(item)
				if didRewrite {
					batchChanged = true
					pending = append(pending, media...)
				}
				outputs = append(outputs, rewrittenItem)
				index++
				continue
			}
			if isResponsesToolBatchInstruction(item) {
				trailing = append(trailing, rawItem)
				index++
				continue
			}
			break
		}

		if !batchChanged {
			rewritten = append(rewritten, items[batchStart:index]...)
			continue
		}

		rewritten = append(rewritten, outputs...)
		if len(pending) > 0 {
			rewritten = append(rewritten, buildResponsesToolOutputMediaMessage(pending))
		}
		rewritten = append(rewritten, trailing...)
		changed = true
	}

	if !changed {
		return input, false
	}
	return rewritten, true
}

func liftResponsesToolOutputMediaItem(item map[string]any) (any, []responsesToolOutputMedia, bool) {
	output, exists := item["output"]
	if !exists {
		return item, nil, false
	}
	outputRaw, err := json.Marshal(output)
	if err != nil {
		return item, nil, false
	}

	outputText, media, didRewrite := extractToolOutputMedia(outputRaw)
	if !didRewrite {
		// 原生 DeepSeek 的工具结果只接收字符串；文本数组也必须编码，不能丢弃。
		if _, isString := output.(string); isString || output == nil {
			return item, nil, false
		}
		item["output"] = string(outputRaw)
		return item, nil, true
	}

	item["output"] = outputText
	callID := strings.TrimSpace(stringValue(item["call_id"]))
	lifted := make([]responsesToolOutputMedia, 0, len(media))
	for _, part := range media {
		if part.ImageURL == nil {
			continue
		}
		imageURL := strings.TrimSpace(part.ImageURL.URL)
		if imageURL == "" {
			continue
		}
		lifted = append(lifted, responsesToolOutputMedia{
			callID:   callID,
			imageURL: imageURL,
		})
	}
	return item, lifted, true
}

func buildResponsesToolOutputMediaMessage(pending []responsesToolOutputMedia) map[string]any {
	content := make([]map[string]any, 0, len(pending)*2)
	lastCallID := ""
	for _, media := range pending {
		if media.callID != lastCallID {
			text := "Tool output media"
			if media.callID != "" {
				text = fmt.Sprintf(toolOutputMediaAttribution, media.callID)
			}
			content = append(content, map[string]any{
				"type": "input_text",
				"text": text,
			})
			lastCallID = media.callID
		}
		content = append(content, map[string]any{
			"type":      "input_image",
			"image_url": media.imageURL,
		})
	}

	return map[string]any{
		"type":    "message",
		"role":    "user",
		"content": content,
	}
}

func isResponsesToolBatchInstruction(item map[string]any) bool {
	itemType := strings.TrimSpace(stringValue(item["type"]))
	if itemType != "" && itemType != "message" {
		return false
	}
	role := strings.TrimSpace(stringValue(item["role"]))
	return role == "developer" || role == "system"
}

func isResponsesToolOutputItem(item map[string]any) bool {
	switch strings.TrimSpace(stringValue(item["type"])) {
	case "function_call_output", "custom_tool_call_output",
		"tool_search_output", "tool_search_call_output", "mcp_tool_call_output":
		return true
	default:
		return false
	}
}
