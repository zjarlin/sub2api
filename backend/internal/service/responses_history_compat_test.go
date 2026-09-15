package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeResponsesAssistantHistory(t *testing.T) {
	body := []byte(`{"input":[
		{"role":"assistant","content":[{"type":"output_text","text":"answer"},{"type":"output_text","text":"cited","annotations":[{"type":"url_citation","url":"https://example.com"}],"logprobs":[{"token":"cited"}]}]},
		{"role":"assistant","content":[{"type":"output_text","text":"","annotations":null,"logprobs":null}]},
		{"role":"user","content":[{"type":"input_text","text":"next"}]},
		{"type":"function_call","namespace":"probe","name":"ping","arguments":"{}"},
		{"role":"assistant","content":[{"type":"output_text","text":"image"},{"type":"image","url":"https://example.com/image"}]},
		{"type":"reasoning","content":[{"type":"reasoning_text","text":"exact reasoning"}]}
	]}`)
	got, err := normalizeResponsesAssistantHistory(body)
	require.NoError(t, err)
	require.Equal(t, "answercited", gjson.GetBytes(got, "input.0.content").String())
	require.Equal(t, gjson.String, gjson.GetBytes(got, "input.1.content").Type)
	require.Empty(t, gjson.GetBytes(got, "input.1.content").String())
	for _, path := range []string{"input.2", "input.3", "input.4", "input.5"} {
		require.Equal(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(got, path).Raw)
	}
	again, err := normalizeResponsesAssistantHistory(got)
	require.NoError(t, err)
	require.Equal(t, got, again)
}
