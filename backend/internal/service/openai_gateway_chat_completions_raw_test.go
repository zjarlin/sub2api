//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildOpenAIChatCompletionsURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base string
		want string
	}{
		// 已是 /chat/completions：原样返回
		{"already chat/completions", "https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1/chat/completions"},
		// 以 /v1 结尾：追加 /chat/completions
		{"bare /v1", "https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		// 其他情况：追加 /v1/chat/completions
		{"bare domain", "https://api.openai.com", "https://api.openai.com/v1/chat/completions"},
		{"domain with trailing slash", "https://api.openai.com/", "https://api.openai.com/v1/chat/completions"},
		// 第三方上游常见形式
		{"third-party bare domain", "https://api.deepseek.com", "https://api.deepseek.com/v1/chat/completions"},
		{"third-party with path prefix", "https://api.gptgod.online/api", "https://api.gptgod.online/api/v1/chat/completions"},
		{"gemini openai compatibility root", "https://generativelanguage.googleapis.com/v1beta/openai/", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
		{"third-party versioned path", "https://open.bigmodel.cn/api/paas/v4", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		// 带空白字符
		{"whitespace trimmed", "  https://api.openai.com/v1  ", "https://api.openai.com/v1/chat/completions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildOpenAIChatCompletionsURL(tt.base)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestBuildOpenAIResponsesURL_ProbeURL 锁定 probe/测试端点使用的 URL 构建逻辑，
// 确保 buildOpenAIResponsesURL 对标准 OpenAI base_url 格式均拼出 `/v1/responses`。
func TestBuildOpenAIResponsesURL_ProbeURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base string
		want string
	}{
		{"bare domain", "https://api.openai.com", "https://api.openai.com/v1/responses"},
		{"domain trailing slash", "https://api.openai.com/", "https://api.openai.com/v1/responses"},
		{"bare /v1", "https://api.openai.com/v1", "https://api.openai.com/v1/responses"},
		{"already /responses", "https://api.openai.com/v1/responses", "https://api.openai.com/v1/responses"},
		{"chat endpoint root", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions", "https://generativelanguage.googleapis.com/v1beta/openai/responses"},
		{"third-party bare domain", "https://api.deepseek.com", "https://api.deepseek.com/v1/responses"},
		{"gemini openai compatibility root", "https://generativelanguage.googleapis.com/v1beta/openai/", "https://generativelanguage.googleapis.com/v1beta/openai/responses"},
		{"third-party versioned path", "https://open.bigmodel.cn/api/paas/v4", "https://open.bigmodel.cn/api/paas/v4/responses"},
		{"only domain, no scheme", "api.gptgod.online", "api.gptgod.online/v1/responses"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildOpenAIResponsesURL(tt.base)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestForwardAsRawChatCompletions_ForcesStreamUsageUpstreamAndPassesUsageDownstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		"",
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":3}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_raw_usage"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)
	require.NotNil(t, upstream.lastReq)
	require.NoError(t, upstream.lastReq.Context().Err())
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())
	require.Contains(t, rec.Body.String(), `"usage"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestForwardAsRawChatCompletions_ChatGPTWeb2APIAllowsEmptyAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_chatgpt_web2api_raw"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_web2api_raw","object":"chat.completion","model":"auto","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
		)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          109,
		Name:        "chatgpt-web2api-raw",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"vendor": "chatgpt-web2api",
		},
	}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://127.0.0.1:8080/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "auto", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
}

func TestForwardAsRawChatCompletions_ChatGPTWeb2APINormalizesModelSlug(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_chatgpt_web2api_model"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_web2api_model","object":"chat.completion","model":"gpt-5-5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
		)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          110,
		Name:        "chatgpt-web2api-raw",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"vendor": "chatgpt-web2api",
		},
	}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5-5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "gpt-5-5", result.UpstreamModel)
}

func TestForwardAsRawChatCompletions_PreservesDeepSeekReasoningContentNonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-reasoner","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamJSON := `{"id":"chatcmpl_reasoning","object":"chat.completion","model":"deepseek-reasoner","choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"think first","content":"final answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_deepseek_reasoning_json"}},
		Body:       io.NopCloser(strings.NewReader(upstreamJSON)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, "think first", gjson.Get(rec.Body.String(), "choices.0.message.reasoning_content").String())
	require.Equal(t, "final answer", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
}

func TestForwardAsRawChatCompletions_PreservesDeepSeekReasoningContentStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-reasoner","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"reasoning_content":"think first"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"content":"final answer"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_deepseek_reasoning_stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Contains(t, rec.Body.String(), `"reasoning_content":"think first"`)
	require.Contains(t, rec.Body.String(), `"content":"final answer"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestForwardAsRawChatCompletions_PreservesDeepSeekReasoningContentInRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"weather"},{"role":"assistant","reasoning_content":"need tool","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"cloudy"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_deepseek_reasoning_request"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_request","object":"chat.completion","model":"deepseek-v4-pro","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "need tool", gjson.GetBytes(upstream.lastBody, "messages.1.reasoning_content").String())
	require.Equal(t, "get_weather", gjson.GetBytes(upstream.lastBody, "messages.1.tool_calls.0.function.name").String())
}

func TestForwardAsRawChatCompletions_DeepSeekImageInputStripsImageAndForwardsText(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}]}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_deepseek_raw_image_stripped"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_image_stripped","object":"chat.completion","model":"deepseek-v4-pro","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          102,
		Name:        "deepseek-openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-deepseek",
			"vendor":  "deepseek",
		},
	}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "describe", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "messages.0.content.0.image_url").Exists())
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
}

func TestStripDeepSeekImageInputFromChatBody_ImageOnlyUsesNotice(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}]}],"stream":false}`)

	got, changed, err := stripDeepSeekImageInputFromChatBody(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, openAIDeepSeekImageOmittedNotice, gjson.GetBytes(got, "messages.0.content").String())
	require.False(t, gjson.GetBytes(got, "messages.0.content.0.image_url").Exists())
}

func TestForwardAsRawChatCompletions_SilentRefusalTriggersFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := largeRawChatCompletionsBody()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_silent","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		"",
		`data: {"id":"chatcmpl_silent","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_silent"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "")
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, IsOpenAISilentRefusalErrorBody(failoverErr.ResponseBody))
	require.False(t, c.Writer.Written(), "silent refusal must not commit a 200 response before failover")
	require.Empty(t, rec.Body.String())
}

func TestForwardAsRawChatCompletions_SilentRefusalToolCallsExempt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := largeRawChatCompletionsBody()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		"",
		`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":""}}]}}]}`,
		"",
		`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_tool"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), `"tool_calls"`)
	require.Contains(t, rec.Body.String(), `"finish_reason":"tool_calls"`)
}

func TestHandleChatStreamingResponse_SilentRefusalReasoningSummaryExempt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_reasoning","model":"gpt-5.5"}}`,
		"",
		`data: {"type":"response.reasoning_summary_text.delta","delta":"thinking only"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_reasoning","model":"gpt-5.5","status":"completed"}}`,
		"",
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_reasoning"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}

	result, err := svc.handleChatStreamingResponse(
		resp,
		c,
		rawChatCompletionsTestAccount(),
		"gpt-5.5",
		"gpt-5.5",
		"gpt-5.5",
		time.Now(),
		openAISilentRefusalMinRequestBodyBytes,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), `"reasoning_content":"thinking only"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestForwardAsRawChatCompletions_SilentRefusalNormalContentExempt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := largeRawChatCompletionsBody()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_ok","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		"",
		`data: {"id":"chatcmpl_ok","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		"",
		`data: {"id":"chatcmpl_ok","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_ok"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), `"content":"ok"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestForwardAsRawChatCompletions_ClientDisconnectDrainsUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Writer = &openAIChatFailingWriter{ResponseWriter: c.Writer, failAfter: 0}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		"",
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[],"usage":{"prompt_tokens":17,"completion_tokens":8,"total_tokens":25,"prompt_tokens_details":{"cached_tokens":6}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_raw_disconnect"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 17, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Equal(t, 6, result.Usage.CacheReadInputTokens)
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())
}

func TestForwardAsRawChatCompletions_UpstreamRequestIgnoresClientCancel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reqCtx, cancel := context.WithCancel(context.Background())
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(reqCtx)
	c.Request.Header.Set("Content-Type", "application/json")
	cancel()

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_raw_ctx"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()

	result, err := svc.forwardAsRawChatCompletions(reqCtx, c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.NoError(t, upstream.lastReq.Context().Err())
}

func TestForwardAsChatCompletions_UnknownResponsesSupportFallbackUsesVersionedChatURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"glm-4.5-air","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"not found"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_raw_fallback"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"chatcmpl_1","object":"chat.completion","model":"glm-4.5-air","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
			)),
		},
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()
	account.Credentials["base_url"] = "https://open.bigmodel.cn/api/paas/v4"

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "https://open.bigmodel.cn/api/paas/v4/responses", upstream.requests[0].URL.String())
	require.Equal(t, "https://open.bigmodel.cn/api/paas/v4/chat/completions", upstream.requests[1].URL.String())
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"content":"ok"`)
}

func TestIsOpenAIChatUsageOnlyStreamChunk(t *testing.T) {
	t.Parallel()

	require.True(t, isOpenAIChatUsageOnlyStreamChunk(`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	require.False(t, isOpenAIChatUsageOnlyStreamChunk(`{"choices":[{"index":0}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	require.False(t, isOpenAIChatUsageOnlyStreamChunk(`{"choices":[]}`))
	require.False(t, isOpenAIChatUsageOnlyStreamChunk(``))
}

func TestEnsureOpenAIChatStreamUsage(t *testing.T) {
	t.Parallel()

	body, err := ensureOpenAIChatStreamUsage([]byte(`{"model":"gpt-5.4"}`))
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(body, "stream_options.include_usage").Bool())

	body, err = ensureOpenAIChatStreamUsage([]byte(`{"model":"gpt-5.4","stream_options":{"include_usage":false}}`))
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(body, "stream_options.include_usage").Bool())
}

func TestBufferRawChatCompletions_RejectsOversizedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader("toolong")),
	}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	svc.cfg.Gateway.UpstreamResponseReadMaxBytes = 3

	result, err := svc.bufferRawChatCompletions(c, resp, "gpt-5.4", "gpt-5.4", "gpt-5.4", nil, nil, time.Now())
	require.ErrorIs(t, err, ErrUpstreamResponseBodyTooLarge)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestForwardAsOpenCodeLocalChatCompletions_NonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"opencode-go/minimax-m3","messages":[{"role":"system","content":"short"},{"role":"user","content":"只回复 OK"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"ses_test","model":{"id":"minimax-m3","providerID":"opencode-go"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"info":{"id":"msg_test","modelID":"minimax-m3","providerID":"opencode-go","finish":"stop","tokens":{"input":11,"output":2,"reasoning":0,"cache":{"read":3,"write":0}},"time":{"created":1781610521583,"completed":1781610526394}},"parts":[{"type":"reasoning","text":"ignore"},{"type":"text","text":"OK"}]}`,
			)),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := openCodeLocalTestAccount()

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "opencode-go/minimax-m3", result.Model)
	require.Equal(t, "minimax-m3", result.BillingModel)
	require.Equal(t, "minimax-m3", result.UpstreamModel)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)
	require.False(t, result.Stream)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "http://host.docker.internal:4096/session", upstream.requests[0].URL.String())
	require.Equal(t, "http://host.docker.internal:4096/session/ses_test/message", upstream.requests[1].URL.String())
	require.Equal(t, "allow", openCodeLocalPermissionActionForTest(upstream.bodies[0], "read"))
	require.Equal(t, "allow", openCodeLocalPermissionActionForTest(upstream.bodies[0], "glob"))
	require.Equal(t, "allow", openCodeLocalPermissionActionForTest(upstream.bodies[0], "grep"))
	require.Equal(t, "deny", openCodeLocalPermissionActionForTest(upstream.bodies[0], "bash"))
	require.Equal(t, "deny", openCodeLocalPermissionActionForTest(upstream.bodies[0], "write"))
	require.Equal(t, "opencode-go", gjson.GetBytes(upstream.bodies[1], "model.providerID").String())
	require.Equal(t, "minimax-m3", gjson.GetBytes(upstream.bodies[1], "model.modelID").String())
	require.True(t, gjson.GetBytes(upstream.bodies[1], "tools.read").Bool())
	require.True(t, gjson.GetBytes(upstream.bodies[1], "tools.glob").Bool())
	require.True(t, gjson.GetBytes(upstream.bodies[1], "tools.grep").Bool())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "tools.bash").Bool())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "tools.write").Bool())
	require.Contains(t, gjson.GetBytes(upstream.bodies[1], "parts.0.text").String(), "只回复 OK")
	require.Equal(t, "chat.completion", gjson.Get(rec.Body.String(), "object").String())
	require.Equal(t, "OK", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
	require.Equal(t, int64(11), gjson.Get(rec.Body.String(), "usage.prompt_tokens").Int())
}

func TestOpenCodeLocalTools_DefaultReadonlyAndCredentialOverrides(t *testing.T) {
	t.Parallel()

	account := openCodeLocalTestAccount()
	defaultTools := openCodeLocalTools(account)
	require.True(t, defaultTools["read"])
	require.True(t, defaultTools["glob"])
	require.True(t, defaultTools["grep"])
	require.False(t, defaultTools["bash"])
	require.False(t, defaultTools["write"])
	require.False(t, defaultTools["apply_patch"])

	account.Credentials["opencode_tools"] = map[string]any{
		"bash":        true,
		"write":       "true",
		"apply_patch": "1",
		"grep":        false,
	}
	overridden := openCodeLocalTools(account)
	require.True(t, overridden["bash"])
	require.True(t, overridden["write"])
	require.True(t, overridden["apply_patch"])
	require.False(t, overridden["grep"])
	require.True(t, overridden["read"])
}

func TestOpenCodeLocalTools_DisabledCredentialClosesAllTools(t *testing.T) {
	t.Parallel()

	account := openCodeLocalTestAccount()
	account.Credentials["opencode_tools_enabled"] = false
	account.Credentials["opencode_tools"] = map[string]any{"read": true, "bash": true}

	tools := openCodeLocalTools(account)
	for name, enabled := range tools {
		require.False(t, enabled, "tool %s should be disabled", name)
	}
}

func TestOpenCodeLocalTools_PresetAllEnablesKnownTools(t *testing.T) {
	t.Parallel()

	account := openCodeLocalTestAccount()
	account.Credentials["opencode_tool_preset"] = "all"

	tools := openCodeLocalTools(account)
	require.True(t, tools["bash"])
	require.True(t, tools["read"])
	require.True(t, tools["edit"])
	require.True(t, tools["write"])
	require.True(t, tools["apply_patch"])
}

func TestOpenCodeLocalPermissions_MirrorTools(t *testing.T) {
	t.Parallel()

	account := openCodeLocalTestAccount()
	account.Credentials["opencode_tools_enabled"] = false
	account.Credentials["opencode_tools"] = map[string]any{"read": true}

	permissions := openCodeLocalPermissions(account)
	require.NotEmpty(t, permissions)
	for _, permission := range permissions {
		require.Equal(t, "deny", permission["action"], "permission %s", permission["permission"])
		require.Equal(t, "*", permission["pattern"])
	}
}

func openCodeLocalPermissionActionForTest(body []byte, permission string) string {
	items := gjson.GetBytes(body, "permission").Array()
	for _, item := range items {
		if item.Get("permission").String() == permission {
			return item.Get("action").String()
		}
	}
	return ""
}

func TestForwardAsOpenCodeLocalChatCompletions_Streaming(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"opencode-go/minimax-m3","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"ses_stream"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(openCodeLocalEventStreamForTest("ses_stream", "msg_stream", "OK", 5, 1))),
		},
		{
			StatusCode: http.StatusNoContent,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader("")),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := openCodeLocalTestAccount()

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.NotNil(t, result.FirstTokenMs)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "http://host.docker.internal:4096/session", upstream.requests[0].URL.String())
	require.Equal(t, "http://host.docker.internal:4096/event", upstream.requests[1].URL.String())
	require.Equal(t, http.MethodGet, upstream.requests[1].Method)
	require.Equal(t, "text/event-stream", upstream.requests[1].Header.Get("Accept"))
	require.Equal(t, HTTPUpstreamProfileOpenCodeEvent, HTTPUpstreamProfileFromContext(upstream.requests[1].Context()))
	require.Equal(t, openCodeLocalEventMinConns, upstream.accountConcurrencies[1])
	require.Equal(t, "http://host.docker.internal:4096/session/ses_stream/prompt_async", upstream.requests[2].URL.String())
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.requests[2].Context()))
	require.Equal(t, openCodeLocalHTTPConcurrency(account), upstream.accountConcurrencies[2])
	require.Len(t, upstream.bodies, 2)
	require.Contains(t, gjson.GetBytes(upstream.bodies[1], "parts.0.text").String(), "hi")
	require.Contains(t, rec.Body.String(), `"object":"chat.completion.chunk"`)
	require.Contains(t, rec.Body.String(), `"content":"OK"`)
	require.Contains(t, rec.Body.String(), `"usage":{"prompt_tokens":5,"completion_tokens":1`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestForwardOpenCodeLocalResponsesViaChatCompletions_StreamingUsesEventPromptAsync(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"opencode-go/minimax-m3","input":"只回复 OK","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"ses_resp_stream"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(openCodeLocalEventStreamForTest("ses_resp_stream", "msg_resp_stream", "OK", 7, 1))),
		},
		{
			StatusCode: http.StatusNoContent,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader("")),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := openCodeLocalTestAccount()

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.NotNil(t, result.FirstTokenMs)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 1, result.Usage.OutputTokens)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "http://host.docker.internal:4096/session", upstream.requests[0].URL.String())
	require.Equal(t, "http://host.docker.internal:4096/event", upstream.requests[1].URL.String())
	require.Equal(t, HTTPUpstreamProfileOpenCodeEvent, HTTPUpstreamProfileFromContext(upstream.requests[1].Context()))
	require.Equal(t, openCodeLocalEventMinConns, upstream.accountConcurrencies[1])
	require.Equal(t, "http://host.docker.internal:4096/session/ses_resp_stream/prompt_async", upstream.requests[2].URL.String())
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.requests[2].Context()))
	require.Equal(t, openCodeLocalHTTPConcurrency(account), upstream.accountConcurrencies[2])
	require.Len(t, upstream.bodies, 2)
	require.Contains(t, gjson.GetBytes(upstream.bodies[1], "parts.0.text").String(), "只回复 OK")
	require.Contains(t, rec.Body.String(), `"type":"response.output_text.delta"`)
	require.Contains(t, rec.Body.String(), `"delta":"OK"`)
	require.Contains(t, rec.Body.String(), `"type":"response.completed"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestForwardOpenCodeLocalResponsesViaChatCompletions_StreamsReasoningPart(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"opencode-go/minimax-m3","input":"先思考再回复 OK","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"ses_resp_reasoning"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(openCodeLocalEventStreamForTest("ses_resp_reasoning", "msg_resp_reasoning", "OK", 7, 1))),
		},
		{
			StatusCode: http.StatusNoContent,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader("")),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := openCodeLocalTestAccount()

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	out := rec.Body.String()
	require.Contains(t, out, `"type":"response.reasoning_summary_text.delta"`)
	require.Contains(t, out, `"delta":"ignore"`)
	require.Contains(t, out, `"type":"response.output_text.delta"`)
	require.Contains(t, out, `"delta":"OK"`)
	require.Contains(t, out, `"type":"response.completed"`)
}

func TestForwardOpenCodeLocalResponsesViaChatCompletions_KeepaliveDuringFilteredEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"opencode-go/minimax-m3","input":"只回复 OK","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	eventReader, eventWriter := io.Pipe()
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"ses_resp_keepalive"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       eventReader,
		},
		{
			StatusCode: http.StatusNoContent,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader("")),
		},
	}}
	cfg := rawChatCompletionsTestConfig()
	cfg.Gateway.StreamKeepaliveInterval = 1
	svc := &OpenAIGatewayService{
		cfg:          cfg,
		httpUpstream: upstream,
	}
	account := openCodeLocalTestAccount()

	done := make(chan error, 1)
	var result *OpenAIForwardResult
	go func() {
		var err error
		result, err = svc.Forward(context.Background(), c, account, body)
		done <- err
	}()

	time.Sleep(1100 * time.Millisecond)
	_, err := io.WriteString(eventWriter,
		"data: {\"type\":\"message.part.updated\",\"properties\":{\"sessionID\":\"ses_resp_keepalive\",\"part\":{\"id\":\"prt_tool\",\"type\":\"tool\"}}}\n\n"+
			"data: {\"type\":\"message.part.delta\",\"properties\":{\"sessionID\":\"ses_resp_keepalive\",\"partID\":\"prt_tool\",\"field\":\"text\",\"delta\":\"filtered\"}}\n\n")
	require.NoError(t, err)
	time.Sleep(1100 * time.Millisecond)
	_, err = io.WriteString(eventWriter, openCodeLocalEventStreamForTest("ses_resp_keepalive", "msg_resp_keepalive", "OK", 7, 1))
	require.NoError(t, err)
	require.NoError(t, eventWriter.Close())

	require.NoError(t, <-done)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	out := rec.Body.String()
	require.Contains(t, out, ":\n\n")
	require.Contains(t, out, `"type":"response.output_text.delta"`)
	require.Contains(t, out, `"delta":"OK"`)
	require.Contains(t, out, `"type":"response.completed"`)
}

func openCodeLocalEventStreamForTest(sessionID, messageID, text string, inputTokens, outputTokens int) string {
	return fmt.Sprintf(
		"data: {\"type\":\"message.part.updated\",\"properties\":{\"sessionID\":%q,\"part\":{\"id\":\"prt_text\",\"messageID\":%q,\"sessionID\":%q,\"type\":\"text\",\"text\":\"\"},\"time\":1}}\n\n"+
			"data: {\"type\":\"message.part.delta\",\"properties\":{\"sessionID\":%q,\"messageID\":%q,\"partID\":\"prt_text\",\"field\":\"text\",\"delta\":%q}}\n\n"+
			"data: {\"type\":\"message.part.updated\",\"properties\":{\"sessionID\":%q,\"part\":{\"id\":\"prt_reason\",\"messageID\":%q,\"sessionID\":%q,\"type\":\"reasoning\",\"text\":\"\"},\"time\":1}}\n\n"+
			"data: {\"type\":\"message.part.delta\",\"properties\":{\"sessionID\":%q,\"messageID\":%q,\"partID\":\"prt_reason\",\"field\":\"text\",\"delta\":\"ignore\"}}\n\n"+
			"data: {\"type\":\"message.part.updated\",\"properties\":{\"sessionID\":%q,\"part\":{\"id\":\"prt_finish\",\"type\":\"step-finish\",\"reason\":\"stop\",\"tokens\":{\"input\":%d,\"output\":%d,\"reasoning\":0,\"cache\":{\"read\":0,\"write\":0}}},\"time\":2}}\n\n"+
			"data: {\"type\":\"message.updated\",\"properties\":{\"sessionID\":%q,\"info\":{\"id\":%q,\"role\":\"assistant\",\"modelID\":\"minimax-m3\",\"providerID\":\"opencode-go\",\"finish\":\"stop\",\"tokens\":{\"input\":%d,\"output\":%d,\"reasoning\":0,\"cache\":{\"read\":0,\"write\":0}},\"time\":{\"created\":1781610521583,\"completed\":1781610526394}}}}\n\n"+
			"data: {\"type\":\"session.idle\",\"properties\":{\"sessionID\":%q}}\n\n",
		sessionID, messageID, sessionID,
		sessionID, messageID, text,
		sessionID, messageID, sessionID,
		sessionID, messageID,
		sessionID, inputTokens, outputTokens,
		sessionID, messageID, inputTokens, outputTokens,
		sessionID,
	)
}

func TestForwardOpenCodeLocalResponsesViaChatCompletions_NonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"opencode-go/minimax-m3","input":"只回复 OK","stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"ses_resp"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"info":{"id":"msg_resp","modelID":"minimax-m3","providerID":"opencode-go","finish":"stop","tokens":{"input":7,"output":1,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1781610521583,"completed":1781610526394}},"parts":[{"type":"text","text":"OK"}]}`,
			)),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := openCodeLocalTestAccount()

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "response", gjson.Get(rec.Body.String(), "object").String())
	require.Equal(t, "OK", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
	require.Equal(t, int64(7), gjson.Get(rec.Body.String(), "usage.input_tokens").Int())
	require.Contains(t, gjson.GetBytes(upstream.bodies[1], "parts.0.text").String(), "只回复 OK")
}

func TestForwardOpenCodeLocalAnthropicMessages_NonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"claude-sonnet-4-5","system":"short","messages":[{"role":"user","content":[{"type":"text","text":"只回复 OK"}]}],"max_tokens":64,"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"ses_anth"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"info":{"id":"msg_anth","modelID":"minimax-m3","providerID":"opencode-go","finish":"stop","tokens":{"input":9,"output":2,"reasoning":0,"cache":{"read":1,"write":0}},"time":{"created":1781610521583,"completed":1781610526394}},"parts":[{"type":"text","text":"OK"}]}`,
			)),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := openCodeLocalTestAccount()

	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "claude-sonnet-4-5", result.Model)
	require.Equal(t, "minimax-m3", result.BillingModel)
	require.Equal(t, "minimax-m3", result.UpstreamModel)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.CacheReadInputTokens)
	require.False(t, result.Stream)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "http://host.docker.internal:4096/session", upstream.requests[0].URL.String())
	require.Equal(t, "http://host.docker.internal:4096/session/ses_anth/message", upstream.requests[1].URL.String())
	require.Equal(t, "opencode-go", gjson.GetBytes(upstream.bodies[1], "model.providerID").String())
	require.Equal(t, "minimax-m3", gjson.GetBytes(upstream.bodies[1], "model.modelID").String())
	require.Contains(t, gjson.GetBytes(upstream.bodies[1], "parts.0.text").String(), "System:\nshort")
	require.Contains(t, gjson.GetBytes(upstream.bodies[1], "parts.0.text").String(), "只回复 OK")
	require.Equal(t, "message", gjson.Get(rec.Body.String(), "type").String())
	require.Equal(t, "assistant", gjson.Get(rec.Body.String(), "role").String())
	require.Equal(t, "claude-sonnet-4-5", gjson.Get(rec.Body.String(), "model").String())
	require.Equal(t, "OK", gjson.Get(rec.Body.String(), "content.0.text").String())
	require.Equal(t, int64(9), gjson.Get(rec.Body.String(), "usage.input_tokens").Int())
	require.Equal(t, int64(1), gjson.Get(rec.Body.String(), "usage.cache_read_input_tokens").Int())
}

func TestForwardOpenCodeGoOfficialChatCompletions_MinimaxUsesMessagesEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"opencode-go/minimax-m3","messages":[{"role":"user","content":"只回复 OK"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"X-Request-Id": []string{"req_opencode_go_messages"},
			},
			Body: io.NopCloser(strings.NewReader(openCodeGoAnthropicSSE("msg_opencode_go", "minimax-m3", "OK", 10, 2, 3))),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := openCodeGoOfficialTestAccount()

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "req_opencode_go_messages", result.RequestID)
	require.Equal(t, "opencode-go/minimax-m3", result.Model)
	require.Equal(t, "minimax-m3", result.BillingModel)
	require.Equal(t, "minimax-m3", result.UpstreamModel)
	require.Equal(t, 13, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)
	require.False(t, result.Stream)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://opencode.ai/zen/go/v1/messages", upstream.requests[0].URL.String())
	require.Equal(t, "sk-test", upstream.requests[0].Header.Get("x-api-key"))
	require.Empty(t, upstream.requests[0].Header.Get("authorization"))
	require.Equal(t, "2023-06-01", upstream.requests[0].Header.Get("anthropic-version"))
	require.Equal(t, "minimax-m3", gjson.GetBytes(upstream.bodies[0], "model").String())
	require.True(t, gjson.GetBytes(upstream.bodies[0], "stream").Bool())
	require.Equal(t, "chat.completion", gjson.Get(rec.Body.String(), "object").String())
	require.Equal(t, "opencode-go/minimax-m3", gjson.Get(rec.Body.String(), "model").String())
	require.Equal(t, "OK", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
	require.Equal(t, int64(13), gjson.Get(rec.Body.String(), "usage.prompt_tokens").Int())
}

func TestForwardOpenCodeGoOfficialResponses_KimiUsesChatCompletionsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"opencode-go/kimi-k2.7-code","input":"只回复 OK","stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req_opencode_go_chat"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_kimi","object":"chat.completion","created":1781610521,"model":"kimi-k2.7","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)),
		},
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := openCodeGoOfficialTestAccount()

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "opencode-go/kimi-k2.7-code", result.Model)
	require.Equal(t, "kimi-k2.7", result.BillingModel)
	require.Equal(t, "kimi-k2.7", result.UpstreamModel)
	require.Equal(t, 4, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://opencode.ai/zen/go/v1/chat/completions", upstream.requests[0].URL.String())
	require.Equal(t, "Bearer sk-test", upstream.requests[0].Header.Get("authorization"))
	require.Equal(t, "kimi-k2.7", gjson.GetBytes(upstream.bodies[0], "model").String())
	require.Equal(t, "response", gjson.Get(rec.Body.String(), "object").String())
	require.Equal(t, "OK", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
}

func rawChatCompletionsTestConfig() *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowInsecureHTTP: true,
			},
		},
	}
}

func rawChatCompletionsTestAccount() *Account {
	return &Account{
		ID:          101,
		Name:        "raw-openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "http://upstream.example",
		},
	}
}

func openCodeGoOfficialTestAccount() *Account {
	return &Account{
		ID:          293,
		Name:        "opencode-go-official-test",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"vendor":   "opencode-go",
			"base_url": "https://opencode.ai/zen/go/v1",
		},
	}
}

func openCodeLocalTestAccount() *Account {
	return &Account{
		ID:          292,
		Name:        "opencode-go-local-test",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"vendor":   "opencode-go",
			"base_url": "http://host.docker.internal:4096",
			"model_mapping": map[string]any{
				"opencode-go/minimax-m3": "minimax-m3",
				"minimax-m3":             "minimax-m3",
				"gpt-*":                  "minimax-m3",
				"claude-*":               "minimax-m3",
			},
		},
	}
}

func openCodeGoAnthropicSSE(id string, model string, text string, inputTokens int, outputTokens int, cacheReadTokens int) string {
	return strings.Join([]string{
		`event: message_start`,
		fmt.Sprintf(`data: {"type":"message_start","message":{"id":%q,"type":"message","role":"assistant","content":[],"model":%q,"stop_reason":"","usage":{"input_tokens":%d,"cache_read_input_tokens":%d}}}`, id, model, inputTokens, cacheReadTokens),
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		fmt.Sprintf(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text),
		``,
		`event: message_delta`,
		fmt.Sprintf(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":%d}}`, outputTokens),
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
}

func largeRawChatCompletionsBody() []byte {
	return []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"` +
		strings.Repeat("x", openAISilentRefusalMinRequestBodyBytes) +
		`"}],"stream":true}`)
}
