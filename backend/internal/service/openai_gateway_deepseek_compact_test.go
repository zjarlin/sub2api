package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildDeepSeekCompactRequestBody(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-flash","input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}],"tools":[{"type":"function","name":"shell"}],"tool_choice":"auto","stream":true}`)

	patched, err := buildDeepSeekCompactRequestBody(body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "stream").Bool())
	require.False(t, gjson.GetBytes(patched, "store").Bool())
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())
	require.Equal(t, 2, len(gjson.GetBytes(patched, "input").Array()))
	require.Equal(t, "message", gjson.GetBytes(patched, "input.1.type").String())
	require.Contains(t, gjson.GetBytes(patched, "input.1.content.0.text").String(), "Summarize the conversation")
}

func TestConvertDeepSeekResponseToOpenAICompact(t *testing.T) {
	body := []byte(`{
		"id":"resp_deepseek_1",
		"object":"response",
		"status":"completed",
		"model":"deepseek-v4-flash",
		"output":[
			{"id":"reasoning_1","type":"reasoning","encrypted_content":"deepseek-state"},
			{"id":"message_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"summary text"}]}
		],
		"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}
	}`)

	converted, err := convertDeepSeekResponseToOpenAICompact(body)
	require.NoError(t, err)
	require.Equal(t, "resp_deepseek_1", gjson.GetBytes(converted, "id").String())
	require.Len(t, gjson.GetBytes(converted, "output").Array(), 1)
	require.Equal(t, "compaction", gjson.GetBytes(converted, "output.0.type").String())
	require.Equal(t, "deepseek-state", gjson.GetBytes(converted, "output.0.encrypted_content").String())
	require.Equal(t, "summary text", gjson.GetBytes(converted, "output.0.summary.0.text").String())
	require.Equal(t, int64(14), gjson.GetBytes(converted, "usage.total_tokens").Int())
}

func TestNormalizeDeepSeekResponsesRequestBodyRestoresCompactSummary(t *testing.T) {
	account := &Account{
		Platform: PlatformDeepseek,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_protocol": APIProtocolResponses,
		},
	}
	body := []byte(`{"model":"deepseek-v4-flash","input":[{"type":"compaction","encrypted_content":"deepseek-state","summary":[{"type":"summary_text","text":"summary text"}]},{"type":"message","role":"user","content":"continue"}]}`)

	normalized := normalizeDeepSeekResponsesRequestBody(account, body)
	require.Equal(t, "message", gjson.GetBytes(normalized, "input.0.type").String())
	require.Contains(t, gjson.GetBytes(normalized, "input.0.content.0.text").String(), "summary text")
	require.Equal(t, "continue", gjson.GetBytes(normalized, "input.1.content").String())
	require.False(t, gjson.GetBytes(normalized, "store").Bool())
}

func TestHandleNonStreamingResponseDeepSeekNativeCompactionBridgesToSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	MarkOpenAINativeCompactionV2(c)
	MarkOpenAICompactClientStream(c)

	service := newCompactBridgeTestService()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_deepseek_2",
			"object":"response",
			"status":"completed",
			"output":[
				{"type":"reasoning","encrypted_content":"deepseek-state"},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary text"}]}
			],
			"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}
		}`)),
	}

	result, err := service.handleNonStreamingResponse(context.Background(), resp, c, &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey}, "deepseek-v4-flash", "deepseek-v4-flash")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	events := parseCompactBridgeSSE(t, recorder.Body.String())
	require.Len(t, events, 2)
	require.Equal(t, "response.output_item.done", events[0][0])
	require.Equal(t, "compaction", gjson.Get(events[0][1], "item.type").String())
	require.Equal(t, "deepseek-state", gjson.Get(events[0][1], "item.encrypted_content").String())
	require.Equal(t, "response.completed", events[1][0])
}
