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

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardResponses_OpenCodeVendorRoutesToOpenCodeServer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"opencode/big-pickle","instructions":"Be precise","input":[{"role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"data:image/png;base64,abc"}]}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"sess_1","title":"hello"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"info":{"id":"msg_2","role":"assistant","tokens":{"input":3,"output":2,"reasoning":1,"cache":{"read":1,"write":0}},"modelID":"big-pickle","providerID":"opencode","finish":"stop"},"parts":[{"type":"reasoning","text":"why"},{"type":"text","text":"ok"}]}`,
			)),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := openCodeServerTestAccount()

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "http://opencode.local/session?directory=%2Ftmp%2Fproject", upstream.requests[0].URL.String())
	require.Equal(t, "http://opencode.local/session/sess_1/message?directory=%2Ftmp%2Fproject", upstream.requests[1].URL.String())
	require.Equal(t, "Bearer pw-opencode", upstream.requests[1].Header.Get("Authorization"))
	require.Equal(t, "opencode", gjson.GetBytes(upstream.bodies[1], "model.providerID").String())
	require.Equal(t, "big-pickle", gjson.GetBytes(upstream.bodies[1], "model.modelID").String())
	require.Equal(t, "Be precise", gjson.GetBytes(upstream.bodies[1], "system").String())
	require.Equal(t, "user: hello\n[image omitted]", gjson.GetBytes(upstream.bodies[1], "parts.0.text").String())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "parts.0.image_url").Exists())

	require.Equal(t, "response", gjson.Get(rec.Body.String(), "object").String())
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "output.1.content.0.text").String())
	require.Equal(t, "why", gjson.Get(rec.Body.String(), "output.0.summary.0.text").String())
	require.Equal(t, int64(3), gjson.Get(rec.Body.String(), "usage.input_tokens").Int())
	require.Equal(t, int64(2), gjson.Get(rec.Body.String(), "usage.output_tokens").Int())
	require.Equal(t, int64(1), gjson.Get(rec.Body.String(), "usage.input_tokens_details.cached_tokens").Int())
	require.Equal(t, int64(1), gjson.Get(rec.Body.String(), "usage.output_tokens_details.reasoning_tokens").Int())
	require.Equal(t, "opencode/big-pickle", result.Model)
	require.Equal(t, "opencode/big-pickle", result.UpstreamModel)
}

func TestForwardAsChatCompletions_OpenCodeVendorRoutesToOpenCodeServer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"opencode/big-pickle","messages":[{"role":"system","content":"Be terse"},{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"sess_1"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"info":{"id":"msg_2","tokens":{"input":4,"output":2,"reasoning":0,"cache":{"read":0,"write":0}},"modelID":"big-pickle","providerID":"opencode","finish":"stop"},"parts":[{"type":"text","text":"ok"}]}`,
			)),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := openCodeServerTestAccount()

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "http://opencode.local/session/sess_1/message?directory=%2Ftmp%2Fproject", upstream.requests[1].URL.String())
	require.Equal(t, "opencode", gjson.GetBytes(upstream.bodies[1], "model.providerID").String())
	require.Equal(t, "big-pickle", gjson.GetBytes(upstream.bodies[1], "model.modelID").String())
	require.Equal(t, "system: Be terse\n\nuser: hello", gjson.GetBytes(upstream.bodies[1], "parts.0.text").String())

	require.Equal(t, "chat.completion", gjson.Get(rec.Body.String(), "object").String())
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
	require.Equal(t, "stop", gjson.Get(rec.Body.String(), "choices.0.finish_reason").String())
	require.Equal(t, int64(4), gjson.Get(rec.Body.String(), "usage.prompt_tokens").Int())
	require.Equal(t, int64(2), gjson.Get(rec.Body.String(), "usage.completion_tokens").Int())
	require.Equal(t, "opencode/big-pickle", result.Model)
}

func TestForwardResponses_OpenCodeVendorStreamsResponsesEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"opencode/big-pickle","input":"hello","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"sess_1"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"info":{"id":"msg_2","tokens":{"input":1,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}},"parts":[{"type":"text","text":"ok"}]}`,
			)),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := openCodeServerTestAccount()

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Contains(t, rec.Body.String(), "event: response.output_text.delta")
	require.Contains(t, rec.Body.String(), `"delta":"ok"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func openCodeServerTestAccount() *Account {
	return &Account{
		ID:          103,
		Name:        "opencode-openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"vendor":                   "opencode",
			"base_url":                 "http://opencode.local",
			"opencode_server_password": "pw-opencode",
			"opencode_directory":       "/tmp/project",
		},
		Extra: map[string]any{
			openai_compat.ExtraKeyResponsesMode:      string(openai_compat.ResponsesSupportModeAuto),
			openai_compat.ExtraKeyResponsesSupported: true,
		},
	}
}
