package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAIStaleReasoningRecoveryKey = "openai_stale_reasoning_recovery"

// 恢复状态只属于本次转发，普通、透传、JSON 与 SSE 共用一次重试预算。
type openAIStaleReasoningRecovery struct {
	body    []byte
	retried bool
}

type openAIStaleReasoningRetrySignal struct {
	body []byte
}

func (e *openAIStaleReasoningRetrySignal) Error() string {
	return "invalid_encrypted_content: retry with stale reasoning removed"
}

// 从原始请求恢复，重新执行账号适配，避免重复改写已经转换过的工具和会话标识。
func (s *OpenAIGatewayService) forwardOnce(ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
	startedAt := time.Now()
	// 恢复副本也必须包含网关可解的摘要，避免错误重试把整段早期历史删掉。
	recoveryBody, restoreErr := s.restoreDeepSeekCompaction(body)
	if restoreErr != nil {
		return nil, restoreErr
	}
	previous, _ := c.Get(openAIStaleReasoningRecoveryKey)
	c.Set(openAIStaleReasoningRecoveryKey, &openAIStaleReasoningRecovery{body: recoveryBody})
	defer c.Set(openAIStaleReasoningRecoveryKey, previous)

	result, err := s.forwardAttempt(ctx, c, account, body)
	var retry *openAIStaleReasoningRetrySignal
	if !errors.As(err, &retry) {
		return result, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	s.markOpenAIWSInvalidEncryptedContentLineageFromPayload(c, body, "invalid_encrypted_content_http_recovery", account.ID, 0)
	logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Retrying request once after invalid_encrypted_content by dropping stale reasoning items (account: %d, passthrough: %v)", account.ID, account.IsOpenAIPassthroughEnabled())
	recoveryDelay := time.Since(startedAt)
	c.Request.Header.Del(openAICodexTurnStateHeader)
	c.Request.Header.Del("x-codex-turn-metadata")
	result, err = s.forwardAttempt(ctx, c, account, retry.body)
	if result != nil {
		result.Duration += recoveryDelay
		if result.FirstTokenMs != nil {
			*result.FirstTokenMs += int(recoveryDelay.Milliseconds())
		}
	}
	return result, err
}

// 仅识别结构化错误字段，避免把正文或工具结果中的同名字符串误判为错误。
func newOpenAIStaleReasoningRetrySignal(c *gin.Context, payload []byte) error {
	if c == nil || openAIStreamClientOutputStarted(c, false) {
		return nil
	}
	code := extractUpstreamErrorCode(payload)
	if code == "" {
		code = strings.TrimSpace(gjson.GetBytes(payload, "code").String())
	}
	if code == "" {
		code = extractUpstreamErrorCode([]byte(gjson.GetBytes(payload, "response").Raw))
	}
	if code != openAIWSFallbackReasonInvalidEncryptedContent {
		return nil
	}
	value, _ := c.Get(openAIStaleReasoningRecoveryKey)
	recovery, ok := value.(*openAIStaleReasoningRecovery)
	if !ok || recovery == nil || recovery.retried {
		return nil
	}
	body, changed, err := recoverOpenAIReadableStaleHistory(recovery.body)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	recovery.retried = true
	return &openAIStaleReasoningRetrySignal{body: body}
}

// 只清理推理状态，压缩历史须先还原为可读摘要；未知密文保留上游原错误。
func recoverOpenAIReadableStaleHistory(body []byte) ([]byte, bool, error) {
	var payload map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &payload); err != nil {
		return nil, false, err
	}
	converted := false
	if input, ok := payload["input"].([]any); ok {
		for _, raw := range input {
			item, _ := raw.(map[string]any)
			if !isOpenAICompactionType(stringValue(item["type"])) {
				continue
			}
			if deepSeekCompactSummaryText(item["summary"]) == "" {
				return body, false, nil
			}
			converted = true
		}
		payload["input"] = convertDeepSeekCompactionItems(input)
	}
	changed := dropOpenAIStaleReasoningInputItems(payload)
	if !changed && !converted {
		return body, false, nil
	}
	next, err := marshalOpenAIUpstreamJSON(payload)
	return next, true, err
}

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
