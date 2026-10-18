package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func responsesCallIDs(t *testing.T, body []byte) (calls, outputs []string) {
	t.Helper()
	var payload struct {
		Input []map[string]any `json:"input"`
	}
	require.NoError(t, json.Unmarshal(body, &payload))
	for _, item := range payload.Input {
		id, _ := item["call_id"].(string)
		if id == "" {
			continue
		}
		if isResponsesCallOutputType(stringValue(item["type"])) {
			outputs = append(outputs, id)
			continue
		}
		calls = append(calls, id)
	}
	return calls, outputs
}

func TestDedupeOpenAIResponsesCallIDs_KeepsLastCompletePair(t *testing.T) {
	body := []byte(`{"model":"m","input":[
		{"role":"user","content":"q"},
		{"type":"function_call","call_id":"c0","name":"exec","arguments":"{\"cmd\":\"first\"}"},
		{"type":"function_call_output","call_id":"c0","output":"first"},
		{"type":"function_call","call_id":"c0","name":"exec","arguments":"{\"cmd\":\"second\"}"},
		{"type":"function_call_output","call_id":"c0","output":"second"}
	]}`)
	got, changed, err := dedupeOpenAIResponsesCallIDs(body)
	require.NoError(t, err)
	require.True(t, changed)

	calls, outputs := responsesCallIDs(t, got)
	require.Equal(t, []string{"c0"}, calls)
	require.Equal(t, []string{"c0"}, outputs)
	require.Contains(t, string(got), `"second"`)
	require.NotContains(t, string(got), "first")
}

func TestDedupeOpenAIResponsesCallIDs_KeepsCompletePairWhenReplayUnanswered(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"function_call","call_id":"c0","name":"exec","arguments":"{\"cmd\":\"first\"}"},
		{"type":"function_call_output","call_id":"c0","output":"first"},
		{"type":"function_call","call_id":"c0","name":"exec","arguments":"{\"cmd\":\"second\"}"}
	]}`)
	got, changed, err := dedupeOpenAIResponsesCallIDs(body)
	require.NoError(t, err)
	require.True(t, changed)

	calls, outputs := responsesCallIDs(t, got)
	require.Equal(t, []string{"c0"}, calls)
	require.Equal(t, []string{"c0"}, outputs)
	require.Contains(t, string(got), "first")
	require.NotContains(t, string(got), "second")
}

func TestDedupeOpenAIResponsesCallIDs_LeavesUniqueHistoryUntouched(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"function_call","call_id":"c0","name":"exec","arguments":"{}"},
		{"type":"function_call_output","call_id":"c0","output":"ok"},
		{"type":"function_call","call_id":"c1","name":"exec","arguments":"{}"},
		{"type":"function_call_output","call_id":"c1","output":"ok"}
	]}`)
	got, changed, err := dedupeOpenAIResponsesCallIDs(body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)
}

func TestDedupeOpenAIResponsesCallIDs_IgnoresChatBodies(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","tool_calls":[{"id":"c0","type":"function","function":{"name":"exec","arguments":"{}"}}]}]}`)
	got, changed, err := dedupeOpenAIResponsesCallIDs(body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)
}
