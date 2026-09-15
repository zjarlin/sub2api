package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeResponsesOutputTextMetadata(t *testing.T) {
	body := []byte(`{"input":[
		{"role":"assistant","content":[{"type":"output_text","text":"answer"},{"type":"output_text","text":"cited","annotations":[{"type":"url_citation","url":"https://example.com"}],"logprobs":[{"token":"cited"}]}]},
		{"role":"assistant","content":[{"type":"output_text","text":"","annotations":null,"logprobs":null}]},
		{"role":"user","content":[{"type":"input_text","text":"next"}]},
		{"type":"function_call","namespace":"probe","name":"ping","arguments":"{}"}
	]}`)
	got, err := normalizeResponsesOutputTextMetadata(body)
	require.NoError(t, err)
	for _, path := range []string{"input.0.content.0.annotations", "input.0.content.0.logprobs", "input.1.content.0.annotations", "input.1.content.0.logprobs"} {
		require.Equal(t, "[]", gjson.GetBytes(got, path).Raw)
	}
	require.Equal(t, "answer", gjson.GetBytes(got, "input.0.content.0.text").String())
	require.Equal(t, gjson.GetBytes(body, "input.0.content.1").Raw, gjson.GetBytes(got, "input.0.content.1").Raw)
	require.Equal(t, gjson.GetBytes(body, "input.2").Raw, gjson.GetBytes(got, "input.2").Raw)
	require.Equal(t, gjson.GetBytes(body, "input.3").Raw, gjson.GetBytes(got, "input.3").Raw)
	again, err := normalizeResponsesOutputTextMetadata(got)
	require.NoError(t, err)
	require.Equal(t, got, again)
}
