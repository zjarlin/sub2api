package main

import (
	"time"
	"unicode/utf8"
)

// estimatedUsage 依据文本长度估算 token：网页不返回用量，但网关要求非空 usage。
// 该值为近似估计（约 4 字符/token），仅用于满足网关计费与解析，不代表上游真实用量。
func estimatedUsage(prompt, text string) map[string]int {
	input := utf8.RuneCountInString(prompt) / 4
	output := utf8.RuneCountInString(text) / 4
	if input < 1 {
		input = 1
	}
	if output < 1 {
		output = 1
	}
	return map[string]int{"input_tokens": input, "output_tokens": output, "total_tokens": input + output}
}

// buildResponse 构造非流式 Responses 对象；网页无 token 统计，usage 为 null。
func buildResponse(text, model, prompt string) map[string]any {
	return map[string]any{
		"id":                 "resp_" + randomID(),
		"object":             "response",
		"created_at":         time.Now().Unix(),
		"model":              model,
		"status":             "completed",
		"error":              nil,
		"incomplete_details": nil,
		"output": []map[string]any{{
			"id": "msg_" + randomID(), "type": "message", "role": "assistant", "status": "completed",
			"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}},
		}},
		"usage": estimatedUsage(prompt, text),
	}
}

type responseEvent struct {
	Name    string
	Payload map[string]any
}

// responseEvents 生成与 Responses 流式协议一致的 SSE 事件序列。
func responseEvents(response map[string]any) []responseEvent {
	output := response["output"].([]map[string]any)
	item := output[0]
	part := item["content"].([]map[string]any)[0]
	text := part["text"].(string)
	itemID := item["id"].(string)
	status := response["status"].(string)

	pending := mergeMaps(response, map[string]any{"status": "in_progress", "output": []any{}, "usage": nil, "incomplete_details": nil})
	started := mergeMaps(item, map[string]any{"status": "in_progress", "content": []any{}})
	coords := map[string]any{"item_id": itemID, "output_index": 0, "content_index": 0}
	return []responseEvent{
		{"response.created", map[string]any{"response": pending}},
		{"response.in_progress", map[string]any{"response": pending}},
		{"response.output_item.added", map[string]any{"output_index": 0, "item": started}},
		{"response.content_part.added", mergeMaps(coords, map[string]any{"part": mergeMaps(part, map[string]any{"text": ""})})},
		{"response.output_text.delta", mergeMaps(coords, map[string]any{"delta": text})},
		{"response.output_text.done", mergeMaps(coords, map[string]any{"text": text})},
		{"response.content_part.done", mergeMaps(coords, map[string]any{"part": part})},
		{"response.output_item.done", map[string]any{"output_index": 0, "item": item}},
		{"response." + status, map[string]any{"response": response}},
	}
}
