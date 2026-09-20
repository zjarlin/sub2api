package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const rejectedNoneReasoningResponse = `{"error":{"code":"unsupported_value","message":"Unsupported value: 'none' is not supported with the 'gpt-6-astra' model. Supported values are: 'low', 'medium', 'high', 'xhigh', and 'max'.","param":"reasoning.effort","type":"invalid_request_error"}}`

func TestOpenAIGatewayServiceRetriesExplicitlyRejectedNoneEffort(t *testing.T) {
	body := []byte(`{"model":"gpt-6","stream":false,"reasoning":{"effort":"none","summary":"auto"},"input":"synthetic check"}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, rejectedNoneReasoningResponse),
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"output":[],"usage":{"input_tokens":1,"output_tokens":1}}`),
	}}
	account := newOpenAIRejectedFieldTestAccount()
	account.Extra["openai_passthrough"] = true
	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(), newOpenAIRejectedFieldTestContext(body), account, body,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "none", gjson.GetBytes(upstream.bodies[0], "reasoning.effort").String())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "reasoning.effort").Exists())
	require.Equal(t, "auto", gjson.GetBytes(upstream.bodies[1], "reasoning.summary").String())
	require.Equal(t, gjson.GetBytes(upstream.bodies[0], "model"), gjson.GetBytes(upstream.bodies[1], "model"))
	require.Equal(t, "synthetic check", gjson.GetBytes(upstream.bodies[1], "input").String())
}

func TestRejectedNoneEffortRetryPreservesUnrelatedRequests(t *testing.T) {
	for _, tc := range []struct {
		name, body, response string
		status               int
	}{
		{"valid effort", `{"reasoning":{"effort":"high"}}`, rejectedNoneReasoningResponse, 400},
		{"unrelated param", `{"reasoning":{"effort":"none"}}`, `{"error":{"code":"unsupported_value","param":"tools","message":"Unsupported value: 'none' is not supported"}}`, 400},
		{"generic error", `{"reasoning":{"effort":"none"}}`, `{"error":{"code":"upstream_error","message":"request failed"}}`, 400},
		{"server error", `{"reasoning":{"effort":"none"}}`, rejectedNoneReasoningResponse, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(tc.status, []byte(tc.body), []byte(tc.response))
			require.NoError(t, err)
			require.False(t, changed)
		})
	}
}

func TestNormalizeDeepSeekLegacyOpenAIResponsesToolOutputs(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.deepseek.com"}}
	body := []byte(`{"store":true,"previous_response_id":"old","input":[
		{"type":"function_call","call_id":"call_A","name":"probe","arguments":"{}"},
		{"type":"function_call","call_id":"call_B","name":"probe","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_A","output":[{"type":"input_text","text":"A"}]},
		{"role":"developer","content":"preserve this notice"},
		{"type":"function_call_output","call_id":"call_B","output":[{"type":"input_text","text":"B"}]}
	]}`)
	normalized := normalizeDeepSeekResponsesRequestBody(account, body)
	require.False(t, gjson.GetBytes(normalized, "store").Bool())
	require.False(t, gjson.GetBytes(normalized, "previous_response_id").Exists())
	for i, callID := range []string{"call_A", "call_B"} {
		item := gjson.GetBytes(normalized, "input").Array()[i+2]
		require.Equal(t, callID, item.Get("call_id").String())
		require.Equal(t, gjson.String, item.Get("output").Type)
		require.JSONEq(t, gjson.GetBytes(body, "input").Array()[2+i*2].Get("output").Raw, item.Get("output").String())
	}
	require.Equal(t, "preserve this notice", gjson.GetBytes(normalized, "input.4.content").String())
	require.Equal(t, string(normalized), string(normalizeDeepSeekResponsesRequestBody(account, normalized)))
	account.Credentials["base_url"] = "https://compat.example"
	require.Equal(t, string(body), string(normalizeDeepSeekResponsesRequestBody(account, body)))
}
