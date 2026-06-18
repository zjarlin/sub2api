//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type fakeDoubaoWebExecutor struct {
	requests []doubaoWebRequest
	result   *doubaoWebResult
	err      error
}

func (f *fakeDoubaoWebExecutor) Complete(_ context.Context, req doubaoWebRequest) (*doubaoWebResult, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestDoubaoWebAccountDefaults(t *testing.T) {
	t.Parallel()

	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor": "doubao-web",
		},
	}

	require.True(t, accountUsesDoubaoWebReverse(account))
	require.True(t, account.ShouldUseOpenAIChatCompletionsUpstream())
	require.True(t, account.AllowsEmptyOpenAIApiKey())
	require.Equal(t, doubaoWebDefaultBaseURL, account.GetOpenAIBaseURL())
	require.Equal(t, "doubao", account.GetMappedModel("doubao:doubao"))
	require.Equal(t, "doubao-pro", account.GetMappedModel("doubao-pro"))
}

func TestDoubaoWebSessionIDs(t *testing.T) {
	t.Parallel()

	account := &Account{Credentials: map[string]any{
		"sessionid":   "sid-a",
		"session_ids": "sid-b, sid-a\nsid-c",
		"sessionids":  []any{"sid-d", "sid-b"},
	}}

	require.Equal(t, []string{"sid-a", "sid-d", "sid-b"}, doubaoWebSessionIDs(account)[:3])
	require.Equal(t, []string{"sid-a", "sid-d", "sid-b", "sid-c"}, doubaoWebSessionIDs(&Account{Credentials: map[string]any{
		"sessionid":   "sid-a",
		"sessionids":  []any{"sid-d", "sid-b"},
		"session_ids": "sid-b\nsid-c",
	}}))

	require.Equal(t, []string{"sid-api-key"}, doubaoWebSessionIDs(&Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor":  "doubao-web",
			"api_key": "sid-api-key",
		},
	}))
}

func TestDoubaoWebEnvBool(t *testing.T) {
	t.Setenv("CHROMEDP_NO_SANDBOX", "true")
	require.True(t, doubaoWebEnvBool("CHROMEDP_NO_SANDBOX"))

	t.Setenv("CHROMEDP_NO_SANDBOX", "yes")
	require.True(t, doubaoWebEnvBool("CHROMEDP_NO_SANDBOX"))

	t.Setenv("CHROMEDP_NO_SANDBOX", "0")
	require.False(t, doubaoWebEnvBool("CHROMEDP_NO_SANDBOX"))

	t.Setenv("CHROMEDP_NO_SANDBOX", "disabled")
	require.False(t, doubaoWebEnvBool("CHROMEDP_NO_SANDBOX"))
}

func TestDoubaoWebBrowserFetchResultAcceptsObject(t *testing.T) {
	encoded, err := doubaoWebBrowserFetchResultJSON(map[string]any{
		"status":     float64(200),
		"body":       map[string]any{"message": "ok"},
		"fetch_hook": map[string]any{"kind": "native"},
	})
	require.NoError(t, err)

	var resp doubaoWebBrowserFetchResponse
	require.NoError(t, json.Unmarshal([]byte(encoded), &resp))
	require.Equal(t, http.StatusOK, resp.Status)
	require.JSONEq(t, `{"message":"ok"}`, resp.Body)
	require.JSONEq(t, `{"kind":"native"}`, resp.FetchHook)
}

func TestDoubaoWebRequestFailureMessageIncludesBody(t *testing.T) {
	require.Equal(
		t,
		"doubao-web request failed with 0: JS error: Failed to fetch",
		doubaoWebRequestFailureMessage(0, "JS error: Failed to fetch"),
	)
}

func TestDoubaoWebJSONErrorLoginInvalid(t *testing.T) {
	status, message, ok := doubaoWebJSONError(`{"code":710012001,"msg":"登录已过期，请重新登录","message":"login invalid"}`)
	require.True(t, ok)
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "login invalid", message)
	require.True(t, doubaoWebErrorRetryable(status, `{"message":"login invalid","msg":"登录已过期，请重新登录"}`))
	require.Equal(t, "Doubao Web session 已过期，请更新 sessionid", doubaoWebNormalizeTestErrorMessage("登录已过期，请重新登录"))
}

func TestDoubaoWebShouldRecreateWorker(t *testing.T) {
	require.True(t, doubaoWebShouldRecreateWorker(context.Canceled))
	require.True(t, doubaoWebShouldRecreateWorker(errors.New("context canceled")))
	require.False(t, doubaoWebShouldRecreateWorker(errors.New("login invalid")))
}

func TestDoubaoWebParseSSEBody(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"event: SSE_ACK",
		`data: {"ack_client_meta":{"conversation_id":"conv-1","section_id":"sec-1"},"query_list":[{"message_index":7}]}`,
		"",
		"event: STREAM_MSG_NOTIFY",
		`data: {"meta":{"index_in_conv":8},"content":{"content_block":[{"block_type":10000,"content":{"text_block":{"text":"你好"}}}]}}`,
		"",
		"event: CHUNK_DELTA",
		`data: {"text":"，世界"}`,
		"",
		"event: STREAM_CHUNK",
		`data: {"patch_op":[{"patch_object":102,"patch_value":{"content":"{\"text\":\"第二段\"}"}}]}`,
		"",
		"event: SSE_REPLY_END",
		`data: {"end_type":1}`,
		"",
	}, "\n")

	parsed, err := doubaoWebParseSSEBody(raw)
	require.NoError(t, err)
	require.Equal(t, "你好，世界第二段", parsed.OutputText)
	require.Equal(t, []string{"你好", "，世界", "第二段"}, parsed.Deltas)
	require.Equal(t, "conv-1", parsed.SessionMeta.ConversationID)
	require.Equal(t, "sec-1", parsed.SessionMeta.SectionID)
	require.NotNil(t, parsed.SessionMeta.LastMessageIndex)
	require.Equal(t, 8, *parsed.SessionMeta.LastMessageIndex)
}

func TestDoubaoWebBuildPayload(t *testing.T) {
	t.Parallel()

	session := &doubaoWebSession{
		SessionID:           "session-1",
		LocalConversationID: "local-test",
		BotID:               "7338",
	}
	payload := doubaoWebBuildPayload(session, "test prompt")

	require.Equal(t, "local-test", gjson.GetBytes(mustJSON(t, payload), "client_meta.local_conversation_id").String())
	require.Equal(t, "", gjson.GetBytes(mustJSON(t, payload), "client_meta.conversation_id").String())
	require.True(t, gjson.GetBytes(mustJSON(t, payload), "option.need_create_conversation").Bool())
	require.Equal(t, "test prompt", gjson.GetBytes(mustJSON(t, payload), "messages.0.content_block.0.content.text_block.text").String())
}

func TestForwardAsDoubaoWebChatCompletions_NonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)

	fake := &fakeDoubaoWebExecutor{result: &doubaoWebResult{
		ID:            "chatcmpl_doubao",
		SessionID:     "resp_doubao_session",
		Content:       "OK",
		Deltas:        []string{"O", "K"},
		FinishReason:  "stop",
		InputTokens:   10,
		OutputTokens:  2,
		Created:       1781610521,
		UpstreamModel: "doubao",
	}}
	restore := swapDoubaoWebExecutor(fake)
	defer restore()

	body := []byte(`{"model":"doubao:doubao","messages":[{"role":"system","content":"short"},{"role":"user","content":"只回复 OK"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	account := doubaoWebTestAccount()

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "doubao:doubao", result.Model)
	require.Equal(t, "doubao", result.BillingModel)
	require.Equal(t, "doubao", result.UpstreamModel)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.False(t, result.Stream)
	require.Len(t, fake.requests, 1)
	require.Equal(t, doubaoWebDefaultBaseURL, fake.requests[0].BaseURL)
	require.Equal(t, "sid-test", fake.requests[0].SessionID)
	require.Equal(t, "doubao", fake.requests[0].Model)
	require.Equal(t, "bot-test", fake.requests[0].DefaultBotID)
	require.Contains(t, fake.requests[0].Prompt, "System instructions:")
	require.Contains(t, fake.requests[0].Prompt, "只回复 OK")
	require.Equal(t, "chat.completion", gjson.Get(rec.Body.String(), "object").String())
	require.Equal(t, "OK", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
}

func TestForwardDoubaoWebResponsesViaChatCompletions_NonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)

	fake := &fakeDoubaoWebExecutor{result: &doubaoWebResult{
		ID:            "chatcmpl_doubao_resp",
		SessionID:     "resp_doubao_session",
		Content:       "OK",
		Deltas:        []string{"OK"},
		FinishReason:  "stop",
		InputTokens:   7,
		OutputTokens:  1,
		Created:       1781610521,
		UpstreamModel: "doubao",
	}}
	restore := swapDoubaoWebExecutor(fake)
	defer restore()

	body := []byte(`{"model":"doubao","instructions":"short","input":"只回复 OK","previous_response_id":"prev-session","stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	account := doubaoWebTestAccount()

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "doubao", result.Model)
	require.Equal(t, "doubao", result.UpstreamModel)
	require.Equal(t, "resp_doubao_session", result.ResponseID)
	require.False(t, result.Stream)
	require.Len(t, fake.requests, 1)
	require.Equal(t, "prev-session", fake.requests[0].PreviousID)
	require.Contains(t, fake.requests[0].Prompt, "short")
	require.Contains(t, fake.requests[0].Prompt, "只回复 OK")
	require.Equal(t, "response", gjson.Get(rec.Body.String(), "object").String())
	require.Equal(t, "resp_doubao_session", gjson.Get(rec.Body.String(), "id").String())
	require.Equal(t, "OK", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
	require.Equal(t, int64(7), gjson.Get(rec.Body.String(), "usage.input_tokens").Int())
}

func TestForwardAsDoubaoWebChatCompletions_Streaming(t *testing.T) {
	gin.SetMode(gin.TestMode)

	fake := &fakeDoubaoWebExecutor{result: &doubaoWebResult{
		ID:            "chatcmpl_doubao_stream",
		SessionID:     "resp_doubao_stream",
		Content:       "OK",
		Deltas:        []string{"O", "K"},
		FinishReason:  "stop",
		InputTokens:   2,
		OutputTokens:  2,
		Created:       1781610521,
		UpstreamModel: "doubao",
	}}
	restore := swapDoubaoWebExecutor(fake)
	defer restore()

	body := []byte(`{"model":"doubao","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	result, err := svc.ForwardAsChatCompletions(context.Background(), c, doubaoWebTestAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Contains(t, rec.Body.String(), `"object":"chat.completion.chunk"`)
	require.Contains(t, rec.Body.String(), `"content":"O"`)
	require.Contains(t, rec.Body.String(), `"content":"K"`)
	require.Contains(t, rec.Body.String(), `"usage":{"prompt_tokens":2,"completion_tokens":2`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestDoubaoWebBaseURLUsesAllowlistValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	fake := &fakeDoubaoWebExecutor{result: &doubaoWebResult{
		ID:           "chatcmpl_should_not_run",
		SessionID:    "resp_should_not_run",
		Content:      "NO",
		FinishReason: "stop",
		Created:      1781610521,
	}}
	restore := swapDoubaoWebExecutor(fake)
	defer restore()

	body := []byte(`{"model":"doubao","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	svc := &OpenAIGatewayService{cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
		Enabled:       true,
		UpstreamHosts: []string{"www.doubao.com"},
	}}}}
	account := doubaoWebTestAccount()
	account.Credentials["base_url"] = "https://evil.example"

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.Nil(t, result)
	require.Error(t, err)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.Empty(t, fake.requests)
}

func doubaoWebTestAccount() *Account {
	return &Account{
		ID:          901,
		Name:        "doubao-web",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"vendor":        "doubao-web",
			"sessionid":     "sid-test",
			"doubao_bot_id": "bot-test",
		},
	}
}

func swapDoubaoWebExecutor(next doubaoWebExecutor) func() {
	prev := globalDoubaoWebExecutor
	globalDoubaoWebExecutor = next
	return func() {
		globalDoubaoWebExecutor = prev
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := marshalOpenAIUpstreamJSON(value)
	require.NoError(t, err)
	return data
}
