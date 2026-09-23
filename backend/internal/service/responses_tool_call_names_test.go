package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeResponsesToolCallNames(t *testing.T) {
	body := []byte(`{"large":9007199254740993,"tools":[
		{"type":"custom","name":"exec"},
		{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"spawn_agent"}]}
	],"input":[
		{"type":"custom_tool_call","id":"ctc_1","call_id":"call_1","name":"functions.exec","input":"console.log(9007199254740993)","extra":9007199254740993},
		{"type":"custom_tool_call_output","call_id":"call_1","output":"functions.exec failed"},
		{"type":"function_call","call_id":"call_2","name":"functions.collaboration.spawn_agent","arguments":"{}"},
		{"type":"function_call","call_id":"call_3","namespace":"collaboration","name":"collaboration.spawn_agent","arguments":"{}"},
		{"type":"message","role":"user","name":"functions.exec","content":"functions.exec"}
	]}`)
	got, err := normalizeResponsesToolCallNames(body)
	require.NoError(t, err)
	require.Equal(t, "exec", gjson.GetBytes(got, "input.0.name").String())
	require.False(t, gjson.GetBytes(got, "input.0.namespace").Exists())
	for _, field := range []string{"type", "id", "call_id", "input", "extra"} {
		require.Equal(t, gjson.GetBytes(body, "input.0."+field).Raw, gjson.GetBytes(got, "input.0."+field).Raw)
	}
	for _, path := range []string{"large", "tools", "input.1", "input.4"} {
		require.Equal(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(got, path).Raw)
	}
	for _, path := range []string{"input.2", "input.3"} {
		require.Equal(t, "spawn_agent", gjson.GetBytes(got, path+".name").String())
		require.Equal(t, "collaboration", gjson.GetBytes(got, path+".namespace").String())
	}
	again, err := normalizeResponsesToolCallNames(got)
	require.NoError(t, err)
	require.Equal(t, got, again)
}

func TestNormalizeResponsesToolCallNames_AdditionalTools(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"additional_tools","tools":[{"type":"custom","name":"exec"},{"type":"namespace","name":"probe","children":[{"type":"function","name":"ping"}]}]},
		{"type":"custom_tool_call","name":"functions.exec","input":"pwd"},
		{"type":"function_call","name":"probe.ping","arguments":"{}"}
	]}`)
	got, err := normalizeResponsesToolCallNames(body)
	require.NoError(t, err)
	require.Equal(t, "exec", gjson.GetBytes(got, "input.1.name").String())
	require.Equal(t, "ping", gjson.GetBytes(got, "input.2.name").String())
	require.Equal(t, "probe", gjson.GetBytes(got, "input.2.namespace").String())
}

func TestNormalizeResponsesToolCallNames_LeavesUnknownOrConflictingCallsUnchanged(t *testing.T) {
	for _, body := range []string{
		`{"tools":[{"type":"custom","name":"exec"}],"input":"functions.exec"}`,
		`{"input":[{"type":"custom_tool_call","name":"functions.exec","input":"pwd"}]}`,
		`{"tools":[{"type":"custom","name":"exec"}],"input":[{"type":"custom_tool_call","name":"other.exec","input":"pwd"}]}`,
		`{"tools":[{"type":"custom","name":"exec"}],"input":[{"type":"function_call","name":"functions.exec","arguments":"{}"}]}`,
		`{"tools":[{"type":"custom","name":"exec"}],"input":[{"type":"custom_tool_call","namespace":"other","name":"functions.exec","input":"pwd"}]}`,
		`{"tools":[{"type":"function","name":"exec"},{"type":"namespace","name":"functions","tools":[{"type":"function","name":"exec"}]}],"input":[{"type":"function_call","name":"functions.exec","arguments":"{}"}]}`,
		`{"tools":[{"type":"custom","name":"exec"}],"input":[{"type":"custom_tool_call","name":"exec","input":"pwd"}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			got, err := normalizeResponsesToolCallNames([]byte(body))
			require.NoError(t, err)
			require.Equal(t, body, string(got))
		})
	}
}
