package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// collectGuardedStreamEvents 走完整的 chunk → Responses 事件路径并开启退化守卫，
// 返回事件序列与最终状态。
func collectGuardedStreamEvents(t *testing.T, guard ChatDegenerationGuard, payloads []string) ([]ResponsesStreamEvent, *ChatCompletionsToResponsesStreamState) {
	t.Helper()
	state := NewChatCompletionsToResponsesStreamState("deepseek-v4.1-flash")
	state.DegenerationGuard = &guard

	var events []ResponsesStreamEvent
	for _, payload := range payloads {
		var chunk ChatCompletionsChunk
		require.NoError(t, json.Unmarshal([]byte(payload), &chunk))
		events = append(events, ChatCompletionsChunkToResponsesEvents(&chunk, state)...)
		if detected := state.DetectDegeneration(); detected != nil {
			// 与服务层一致：命中即中止，不再下发后续事件。
			events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
			return events, state
		}
	}
	events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
	return events, state
}

func reasoningDeltaChunks(unit string, repeats int) []string {
	payloads := make([]string, 0, repeats)
	for i := 0; i < repeats; i++ {
		payload, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"reasoning_content": unit}}},
		})
		if err != nil {
			panic(err)
		}
		payloads = append(payloads, string(payload))
	}
	return payloads
}

// TestStream_DegenerateReasoningIsNotSynthesizedAsMessage 保证退化复读不会被
// "reasoning-only 兜底" 变成可见正文（否则客户端会收到数十万字符的复读回答）。
func TestStream_DegenerateReasoningIsNotSynthesizedAsMessage(t *testing.T) {
	guard := testDegenerationGuard()
	events, state := collectGuardedStreamEvents(t, guard, reasoningDeltaChunks(degenerateTailRunes, 900))

	require.NotNil(t, state.Degeneration, "必须命中退化检测")
	for _, event := range events {
		require.NotEqual(t, "response.output_text.delta", event.Type, "退化的 reasoning 不得兜底成正文")
		if event.Type == "response.output_item.added" || event.Type == "response.output_item.done" {
			require.NotEqual(t, "message", event.Item.Type, "退化时不得创建 message 项")
		}
	}
}

// TestStream_ShortReasoningOnlyStillSynthesizesMessage 固定既有行为：短小的
// reasoning-only 回复仍要兜底成正文（见 fix(apicompat): surface DeepSeek reasoning-only replies）。
func TestStream_ShortReasoningOnlyStillSynthesizesMessage(t *testing.T) {
	guard := testDegenerationGuard()
	events, state := collectGuardedStreamEvents(t, guard, []string{
		`{"choices":[{"index":0,"delta":{"reasoning_content":"reasoning-only answer"}}]}`,
	})

	require.Nil(t, state.Degeneration)
	saw := false
	for _, event := range events {
		if event.Type == "response.output_text.delta" {
			saw = true
			require.True(t, strings.Contains(event.Delta, "reasoning-only answer"))
		}
	}
	require.True(t, saw, "reasoning-only 回复必须仍然可见")
}
