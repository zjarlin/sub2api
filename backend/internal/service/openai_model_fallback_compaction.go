package service

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// 降级模型接收普通文本摘要，不携带原模型的推理密文、会话游标或压缩协议状态。
// 摘要回合复用有界分块和辅助用量记账，客户端会话不发生切换。
func (s *OpenAIGatewayService) CompactOpenAIFailoverHistory(ctx context.Context, c *gin.Context, account *Account, body []byte) ([]byte, bool, error) {
	restored, err := s.restoreDeepSeekCompaction(body)
	if err != nil {
		return nil, false, err
	}
	var payload map[string]any
	if err := decodeOpenAIJSONUseNumber(restored, &payload); err != nil {
		return nil, false, err
	}
	// 服务端游标不包含完整历史，不能删除后假装会话已经迁移成功。
	if gjson.GetBytes(body, "previous_response_id").String() != "" || payload["conversation"] != nil {
		return restored, false, nil
	}
	delete(payload, "context_management")
	input, ok := payload["input"].([]any)
	if !ok {
		cleaned, err := marshalOpenAIUpstreamJSON(payload)
		return cleaned, false, err
	}
	input = convertDeepSeekCompactionItems(input)
	for _, raw := range input {
		item, _ := raw.(map[string]any)
		if isOpenAICompactionType(stringValue(item["type"])) {
			return nil, false, errors.New("model fallback cannot summarize opaque compaction without readable history")
		}
	}
	payload["input"] = input
	dropOpenAIStaleReasoningInputItems(payload)
	cleaned, err := marshalOpenAIUpstreamJSON(payload)
	if err != nil {
		return nil, false, err
	}
	// 显式压缩请求随后会执行原生压缩协议，不再重复插入一个隐式摘要回合。
	if IsOpenAIResponsesCompactPath(c) || isOpenAINativeCompactionV2(c) {
		return cleaned, false, nil
	}
	items := gjson.GetBytes(cleaned, "input").Array()
	visibleHistory, hasUser := 0, false
	for _, item := range items {
		switch item.Get("role").String() {
		case "system", "developer":
			continue
		case "user":
			hasUser = true
		}
		if item.Get("type").String() != "additional_tools" {
			visibleHistory++
		}
	}
	if !hasUser || visibleHistory < 2 {
		return cleaned, false, nil
	}
	next, _, err := s.compactAutoContinue(ctx, c, account, cleaned, "", true)
	if errors.Is(err, errAutoCompactionNotReduced) || errors.Is(err, errAutoCompactionNoHistory) {
		// 短历史已经可移植，摘要更长时直接保留可读原文。
		return cleaned, false, nil
	}
	if err != nil {
		// 摘要回合失败不能连累主请求：可移植的明文历史已经算好，直接用它
		// 继续降级，最多损失压缩率，不会把客户端请求变成 502。
		logger.LegacyPrintf("service.openai_gateway_fallback",
			"[OpenAI] Model fallback keeps cleaned history after compaction failure (account: %d, history_bytes: %d): %v",
			account.ID, len(cleaned), err)
		return cleaned, false, nil
	}
	return next, true, nil
}

// 当前工具结果及对应调用必须原样成对保留；图片等非文本输入不能靠摘要猜测内容。
// 早期已经完成的工具回合仍可摘要，避免单次长任务无法缩短上下文。
func openAIFallbackPreservedInput(input []any, latestUser int) map[int]bool {
	preserved := map[int]bool{latestUser: true}
	callIDs := make(map[string]bool)
	for i := len(input) - 1; i >= 0; i-- {
		item, _ := input[i].(map[string]any)
		kind := stringValue(item["type"])
		if kind != "function_call_output" && kind != "custom_tool_call_output" {
			break
		}
		preserved[i] = true
		callIDs[stringValue(item["call_id"])] = true
	}
	for i, raw := range input {
		item, _ := raw.(map[string]any)
		kind := stringValue(item["type"])
		if openAIFallbackHasMedia(raw) {
			preserved[i] = true
			if kind == "function_call_output" || kind == "custom_tool_call_output" {
				callIDs[stringValue(item["call_id"])] = true
			}
		}
	}
	for i, raw := range input {
		item, _ := raw.(map[string]any)
		kind := stringValue(item["type"])
		if (kind == "function_call" || kind == "custom_tool_call") && callIDs[stringValue(item["call_id"])] {
			preserved[i] = true
		}
	}
	return preserved
}

func openAIFallbackHasMedia(value any) bool {
	switch item := value.(type) {
	case []any:
		for _, child := range item {
			if openAIFallbackHasMedia(child) {
				return true
			}
		}
	case map[string]any:
		switch stringValue(item["type"]) {
		case "input_image", "image_url", "image", "input_audio", "audio", "input_file", "file", "input_video", "video", "video_url":
			return true
		}
		return openAIFallbackHasMedia(item["content"]) || openAIFallbackHasMedia(item["output"])
	}
	return false
}
