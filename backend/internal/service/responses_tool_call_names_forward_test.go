//go:build unit

package service

import (
	"bytes"
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

func TestForwardResponses_NormalizesDottedToolHistoryBeforeRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"responses", "passthrough", "chat"} {
		t.Run(mode, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.6-sol","stream":false,"tools":[{"type":"custom","name":"exec"}],"input":[
				{"role":"user","content":"Check the current directory"},
				{"type":"custom_tool_call","id":"ctc_old","call_id":"call_old","name":"functions.exec","input":"pwd"},
				{"type":"custom_tool_call_output","call_id":"call_old","output":"/workspace"},
				{"role":"user","content":"Continue"}
			]}`)
			account := rawChatCompletionsTestAccount()
			response := `{"id":"resp_test","object":"response","status":"completed","model":"gpt-5.6-sol","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`
			if mode == "chat" {
				account = forceChatResponsesFallbackAccount()
				response = `{"id":"chat_test","object":"chat.completion","model":"gpt-5.6-sol","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
			}
			if mode == "passthrough" {
				account.Extra = map[string]any{"openai_passthrough": true}
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(response)),
			}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, http.StatusOK, rec.Code)
			if mode == "chat" {
				require.Equal(t, "exec", gjson.GetBytes(upstream.lastBody, "messages.1.tool_calls.0.function.name").String())
				require.Equal(t, "call_old", gjson.GetBytes(upstream.lastBody, "messages.2.tool_call_id").String())
			} else {
				require.Equal(t, "exec", gjson.GetBytes(upstream.lastBody, "input.1.name").String())
				require.Equal(t, "call_old", gjson.GetBytes(upstream.lastBody, "input.2.call_id").String())
			}
			require.Contains(t, string(upstream.lastBody), "/workspace")
		})
	}
}
