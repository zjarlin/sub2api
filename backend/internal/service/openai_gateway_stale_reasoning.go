package service

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// 上游明确以 invalid_encrypted_content 拒绝请求后的响应式兜底（#3138）。
//
// 既有恢复路径只在历史项仍携带 encrypted_content 字段时才认为可恢复：
// trimOpenAIEncryptedReasoningItems 命中 encrypted_content 才会改写。但上游同样会
// 拒绝只剩 summary/元数据骨架的陈旧推理项——密文可能已被客户端或中间层剥掉，
// 或者 compaction 摘要本身失效。此时原判断返回 false，恢复被跳过，上游 400 直接
// 透出给客户端（生产日志表现为
// "Skip non-WSv2 invalid_encrypted_content retry because encrypted reasoning items are missing"）。
//
// 本文件提供"宽剥离"：既然上游已经明确说这批推理/压缩历史不可解，就把
// reasoning / compaction / compaction_summary 这三类历史项整块删除后重试一次，
// 不再要求它们仍带密文。只在收到该错误码后调用，常态请求不受影响。

// isOpenAIStaleReasoningInputItem 报告某项是否为 Responses 的推理/压缩历史项。
// 与 isOpenAIEncryptedReasoningInputItem 的关键差别是不要求 encrypted_content 存在。
func isOpenAIStaleReasoningInputItem(item any) bool {
	inputItem, ok := item.(map[string]any)
	if !ok {
		return false
	}
	itemType, _ := inputItem["type"].(string)
	switch strings.TrimSpace(itemType) {
	case "reasoning", "compaction", "compaction_summary":
		return true
	default:
		return false
	}
}

// dropOpenAIStaleReasoningInputItems 整块删除历史推理/压缩项（含只剩 summary 的
// 骨架），并报告是否有改动。
func dropOpenAIStaleReasoningInputItems(reqBody map[string]any) bool {
	if len(reqBody) == 0 {
		return false
	}
	inputValue, has := reqBody["input"]
	if !has {
		return false
	}
	switch input := inputValue.(type) {
	case []any:
		filtered := input[:0]
		changed := false
		for _, item := range input {
			if isOpenAIStaleReasoningInputItem(item) {
				changed = true
				continue
			}
			filtered = append(filtered, item)
		}
		if !changed {
			return false
		}
		if len(filtered) == 0 {
			delete(reqBody, "input")
			return true
		}
		reqBody["input"] = filtered
		return true
	case []map[string]any:
		filtered := input[:0]
		changed := false
		for _, item := range input {
			if isOpenAIStaleReasoningInputItem(item) {
				changed = true
				continue
			}
			filtered = append(filtered, item)
		}
		if !changed {
			return false
		}
		if len(filtered) == 0 {
			delete(reqBody, "input")
			return true
		}
		reqBody["input"] = filtered
		return true
	case map[string]any:
		if isOpenAIStaleReasoningInputItem(input) {
			delete(reqBody, "input")
			return true
		}
		return false
	default:
		return false
	}
}

// DropOpenAIStaleReasoningInputItems 是字节版入口：上游 invalid_encrypted_content
// 拒绝后，整块剔除陈旧推理/压缩项再重试。无改动时原样返回。
func DropOpenAIStaleReasoningInputItems(body []byte) (sanitized []byte, changed bool, err error) {
	if len(body) == 0 {
		return body, false, nil
	}
	if !gjson.GetBytes(body, "input").Exists() {
		return body, false, nil
	}
	var decoded map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &decoded); err != nil {
		return body, false, fmt.Errorf("decode stale reasoning body: %w", err)
	}
	if !dropOpenAIStaleReasoningInputItems(decoded) {
		return body, false, nil
	}
	out, marshalErr := marshalOpenAIUpstreamJSON(decoded)
	if marshalErr != nil {
		return body, false, fmt.Errorf("serialize stale reasoning body: %w", marshalErr)
	}
	return out, true, nil
}
