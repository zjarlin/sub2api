package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatResponsesDottedCustomToolName(t *testing.T) {
	for _, name := range []string{"functions.exec", "functions__exec"} {
		t.Run(name, func(t *testing.T) {
			custom := map[string]bool{"exec": true}
			namespaces := map[string]NamespacedToolName{
				"functions__wait": {Namespace: "functions", Name: "wait"},
			}
			call := ChatToolCall{ID: "call_exec", Function: ChatFunctionCall{Name: name, Arguments: `{"input":"pwd"}`}}
			response := &ChatCompletionsResponse{Choices: []ChatChoice{{Message: ChatMessage{ToolCalls: []ChatToolCall{call}}}}}
			got := ChatCompletionsResponseToResponses(response, "test-model", custom, nil, false, namespaces)
			require.Len(t, got.Output, 1)
			require.Equal(t, "custom_tool_call", got.Output[0].Type)
			require.Equal(t, "exec", got.Output[0].Name)
			require.Equal(t, "pwd", got.Output[0].Input)
			require.Equal(t, "call_exec", got.Output[0].CallID)

			state := NewChatCompletionsToResponsesStreamState("test-model")
			state.CustomTools, state.NamespaceTools = custom, namespaces
			index := 0
			call.Index = &index
			chunk := &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{call}}}}}
			events := ChatCompletionsChunkToResponsesEvents(chunk, state)
			events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
			for _, event := range events {
				if event.Item != nil && event.Item.Type == "custom_tool_call" {
					require.Equal(t, "exec", event.Item.Name)
				}
				if event.Type == "response.custom_tool_call_input.done" {
					require.Equal(t, "exec", event.Name)
					require.Equal(t, "pwd", event.Input)
				}
			}
			final := events[len(events)-1].Response
			require.NotNil(t, final)
			require.Len(t, final.Output, 1)
			require.Equal(t, "exec", final.Output[0].Name)
			require.Equal(t, "call_exec", final.Output[0].CallID)
		})
	}
}

func TestDottedCustomToolNameDoesNotOverrideDeclaredFunction(t *testing.T) {
	custom := map[string]bool{"exec": true}
	_, ok := customToolCallName("functions.exec", custom, map[string]bool{"functions.exec": true}, nil)
	require.False(t, ok)
	_, ok = customToolCallName("functions.exec", custom, nil, map[string]NamespacedToolName{
		"functions__exec": {Namespace: "functions", Name: "exec"},
	})
	require.False(t, ok)
	_, ok = customToolCallName("unknown.exec", custom, nil, nil)
	require.False(t, ok)
}
