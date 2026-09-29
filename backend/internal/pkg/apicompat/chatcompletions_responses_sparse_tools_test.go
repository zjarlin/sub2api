package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 上游工具索引可能从 1 开始或不连续，所有已宣告的调用都必须完成并保留到最终输出。
func TestChatCompletionsResponsesStreamSparseToolIndices(t *testing.T) {
	tests := []struct {
		name    string
		indices []int
		names   []string
		types   []string
	}{
		{"function", []int{1}, []string{"echo"}, []string{"function_call"}},
		{"namespace", []int{1}, []string{"functions__echo"}, []string{"function_call"}},
		{"custom", []int{1}, []string{"exec"}, []string{"custom_tool_call"}},
		{"tool_search", []int{1}, []string{toolSearchProxyName}, []string{"tool_search_call"}},
		{"gaps", []int{0, 3}, []string{"echo", "functions__echo"}, []string{"function_call", "function_call"}},
		{"arrival_order", []int{7, 2, 5}, []string{"exec", toolSearchProxyName, "functions__echo"}, []string{"custom_tool_call", "tool_search_call", "function_call"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewChatCompletionsToResponsesStreamState("glm")
			state.FunctionTools = map[string]bool{"echo": true}
			state.CustomTools = map[string]bool{"exec": true}
			state.ToolSearchDeclared = true
			state.NamespaceTools = map[string]NamespacedToolName{
				"functions__echo": {Namespace: "functions", Name: "echo"},
			}
			reasoning, content := "准备调用工具", "我现在执行。"
			events := ChatCompletionsChunkToResponsesEvents(&ChatCompletionsChunk{
				Choices: []ChatChunkChoice{{Delta: ChatDelta{ReasoningContent: &reasoning, Content: &content}}},
			}, state)
			for i, index := range tt.indices {
				chunk := &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{
					ToolCalls: []ChatToolCall{{Index: &index, ID: "call_" + tt.names[i], Function: ChatFunctionCall{
						Name: tt.names[i], Arguments: `{"input":`,
					}}},
				}}}}
				events = append(events, ChatCompletionsChunkToResponsesEvents(chunk, state)...)
				chunk.Choices[0].Delta.ToolCalls[0].Function = ChatFunctionCall{Arguments: `"GLM_TOOL_PROBE"}`}
				events = append(events, ChatCompletionsChunkToResponsesEvents(chunk, state)...)
			}
			require.NoError(t, state.ValidateToolCallArguments())
			events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
			completed := events[len(events)-1]
			require.Equal(t, "response.completed", completed.Type)
			require.Len(t, completed.Response.Output, 2+len(tt.indices))
			added := make(map[string]ResponsesStreamEvent)
			done := make(map[string]ResponsesStreamEvent)
			argumentsDone := make(map[string]ResponsesStreamEvent)
			for _, event := range events {
				switch event.Type {
				case "response.output_item.added":
					added[event.Item.ID] = event
				case "response.output_item.done":
					done[event.Item.ID] = event
				case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
					argumentsDone[event.ItemID] = event
				}
			}
			for i, output := range completed.Response.Output {
				require.Contains(t, added, output.ID)
				require.Contains(t, done, output.ID)
				require.Equal(t, i, added[output.ID].OutputIndex)
				require.Equal(t, i, done[output.ID].OutputIndex)
				if i < 2 {
					continue
				}
				require.Equal(t, tt.types[i-2], output.Type)
				require.Equal(t, "call_"+tt.names[i-2], output.CallID)
				require.Equal(t, output, *done[output.ID].Item)
				switch output.Type {
				case "custom_tool_call":
					require.Equal(t, "GLM_TOOL_PROBE", output.Input)
					require.Equal(t, output.Input, argumentsDone[output.ID].Input)
				case "function_call":
					require.Equal(t, `{"input":"GLM_TOOL_PROBE"}`, output.Arguments)
					require.Equal(t, output.Arguments, argumentsDone[output.ID].Arguments)
				case "tool_search_call":
					require.Equal(t, `{"input":"GLM_TOOL_PROBE"}`, output.Arguments)
				}
			}
			require.Empty(t, FinalizeChatCompletionsResponsesStream(state))
		})
	}
}
