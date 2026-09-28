package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatResponsesStreamKeepsItemIDsThroughCompletion(t *testing.T) {
	for _, kind := range []string{"function", "custom", "tool_search"} {
		t.Run(kind, func(t *testing.T) {
			state := NewChatCompletionsToResponsesStreamState("test-model")
			name, args := "lookup", `{"city":"Tianjin"}`
			if kind == "custom" {
				name, args = "exec", `{"input":"printf test"}`
				state.CustomTools = map[string]bool{"exec": true}
			}
			if kind == "tool_search" {
				name, args = "tool_search", `{"query":"test"}`
				state.ToolSearchDeclared = true
			}
			index := 0
			reasoning := "inspect the request"
			chunk := &ChatCompletionsChunk{ID: "chatcmpl-test", Choices: []ChatChunkChoice{{Delta: ChatDelta{ReasoningContent: &reasoning, ToolCalls: []ChatToolCall{{Index: &index, ID: "call_test", Type: "function", Function: ChatFunctionCall{Name: name, Arguments: args}}}}}}}
			events := ChatCompletionsChunkToResponsesEvents(chunk, state)
			events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
			done := map[string]string{}
			var completed *ResponsesResponse
			for _, event := range events {
				if event.Type == "response.output_item.done" {
					done[event.Item.Type] = event.Item.ID
				}
				if event.Type == "response.completed" {
					completed = event.Response
				}
			}
			require.NotNil(t, completed)
			require.Len(t, done, 2)
			require.Len(t, completed.Output, 2)
			for _, item := range completed.Output {
				require.NotEmpty(t, item.ID)
				require.Equal(t, done[item.Type], item.ID)
			}
		})
	}
}
