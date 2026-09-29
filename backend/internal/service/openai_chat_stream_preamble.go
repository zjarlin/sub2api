package service

import (
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
)

const (
	ccStreamPreambleMaxBytes = openAIFirstOutputStageMemoryLimit
	ccStreamPreambleMaxItems = 1024
)

// 空前导过量时保留未提交状态，让现有上游错误策略换号，不能强制冲刷前导。
func ccStreamPreambleLimitError() *ccStreamError {
	return parseCCStreamError(`{"error":{"code":"upstream_stream_preamble_too_large","type":"server_error","message":"Upstream Chat Completions stream preamble exceeded buffer limit; please retry"}}`, "error")
}

// 直转路径也识别兼容扩展；未知非空输出按已输出处理，避免重放工具或多模态操作。
func chatPayloadStartsClientOutput(payload string) bool {
	if strings.TrimSpace(payload) == "" {
		return false
	}
	var chunk apicompat.ChatCompletionsChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return true
	}
	if chatChunkStartsResponsesOutput(&chunk) {
		return true
	}
	for _, choice := range gjson.Get(payload, "choices").Array() {
		delta := choice.Get("delta")
		if chatObjectHasUnknownOutput(delta, "role", "content", "reasoning_content", "reasoning", "tool_calls") {
			return true
		}
		for _, tool := range delta.Get("tool_calls").Array() {
			if chatObjectHasUnknownOutput(tool, "index", "id", "type", "function") ||
				chatObjectHasUnknownOutput(tool.Get("function"), "name", "arguments") {
				return true
			}
		}
	}
	return false
}

// 只忽略已由标准协议分类的字段，保留自定义工具等扩展的不可重放边界。
func chatObjectHasUnknownOutput(object gjson.Result, knownFields ...string) bool {
	unknown := false
	object.ForEach(func(key, value gjson.Result) bool {
		for _, field := range knownFields {
			if key.String() == field {
				return true
			}
		}
		unknown = value.Type != gjson.Null && (value.Type != gjson.String || value.String() != "")
		return !unknown
	})
	return unknown
}
