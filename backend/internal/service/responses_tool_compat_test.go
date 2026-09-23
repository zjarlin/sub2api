package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeOpenAIResponsesWebSearchPreview(t *testing.T) {
	body := []byte(`{
		"tools":[
			{"type":"function","name":"web_search"},
			{"type":"web_search","search_context_size":"medium"},
			{"type":"web_search_preview"}
		],
		"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"type":"web_search"},{"type":"function","name":"lookup"}]}
	}`)

	normalized, changed := normalizeOpenAIResponsesWebSearchPreview(body)

	require.True(t, changed)
	require.Equal(t, "function", gjson.GetBytes(normalized, "tools.0.type").String())
	require.Equal(t, "web_search", gjson.GetBytes(normalized, "tools.0.name").String())
	require.Equal(t, "web_search_preview", gjson.GetBytes(normalized, "tools.1.type").String())
	require.Equal(t, "medium", gjson.GetBytes(normalized, "tools.1.search_context_size").String())
	require.Equal(t, "web_search_preview", gjson.GetBytes(normalized, "tools.2.type").String())
	require.Equal(t, "web_search_preview", gjson.GetBytes(normalized, "tool_choice.tools.0.type").String())
	require.Equal(t, "function", gjson.GetBytes(normalized, "tool_choice.tools.1.type").String())
}

func TestNormalizeOpenAIResponsesWebSearchPreviewLeavesOtherToolsUntouched(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"web_search"}],"tool_choice":"auto"}`)

	normalized, changed := normalizeOpenAIResponsesWebSearchPreview(body)

	require.False(t, changed)
	require.Equal(t, body, normalized)
}
