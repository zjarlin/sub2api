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
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// enableBuiltinAdapterForTest 打开内置适配器配置并返回恢复函数。
func enableBuiltinAdapterForTest(t *testing.T) {
	t.Helper()
	cfg := &config.BuiltinAdapterConfig{
		Enabled:      true,
		DesktopURL:   "http://sub2api-desktop:8080",
		DesktopKey:   "builtin-desktop-key",
		TraeworkURL:  "http://sub2api-traework:7864",
		TraeworkKey:  "builtin-traework-key",
		WorkbuddyKey: "builtin-workbuddy-key",
		ZcodeKey:     "builtin-zcode-key",
	}
	SetBuiltinAdapterConfig(cfg)
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
}

func doubaoTestAccount() *Account {
	return &Account{ID: 501, Platform: PlatformDoubao, Type: AccountTypeAPIKey, Status: StatusActive,
		Credentials: map[string]any{"base_url": "http://upstream.example/v1", "api_key": "adapter-test-key", "api_protocol": APIProtocolChatCompletions}}
}

func TestDoubaoCredentialValidation(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	for _, tc := range []struct {
		name, key string
		value     any
	}{
		{"missing key", "api_key", " "}, {"unsupported protocol", "api_protocol", APIProtocolAnthropic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := doubaoTestAccount()
			account.Credentials[tc.key] = tc.value
			require.Error(t, validateDoubaoCredentials(account.Platform, account.Type, account.Credentials))
		})
	}
	account := doubaoTestAccount()
	require.NoError(t, validateDoubaoCredentials(account.Platform, account.Type, account.Credentials))
	require.Error(t, validateDoubaoCredentials(account.Platform, AccountTypeOAuth, account.Credentials))
	// 内置模式下地址由后端注入，账号不存默认公网端点。
	delete(account.Credentials, "base_url")
	require.Equal(t, "http://sub2api-desktop:8080", account.GetOpenAIBaseURL())
	account.Credentials["api_protocol"] = APIProtocolResponses
	require.Equal(t, APIProtocolResponses, account.GetAPIProtocol())
	require.True(t, account.SupportsNativeCNResponses())
}

func TestDoubaoRequiresBuiltinAdapter(t *testing.T) {
	SetBuiltinAdapterConfig(nil)
	account := doubaoTestAccount()
	require.Error(t, validateDoubaoCredentials(account.Platform, account.Type, account.Credentials))
}

func TestDoubaoResponsesStateIsNotStripped(t *testing.T) {
	account := doubaoTestAccount()
	body := []byte(`{"model":"doubao-pro","store":true,"previous_response_id":"resp_previous","input":[{"type":"function_call_output","call_id":"call_status","output":"ready"}]}`)
	require.Equal(t, body, normalizeDeepSeekResponsesRequestBody(account, body))
	require.False(t, shouldForwardOpenAIResponsesViaRawChatCompletions(account))
	for _, legacy := range []string{"", APIProtocolChatCompletions, APIProtocolAdaptive, APIProtocolResponses} {
		account.Credentials["api_protocol"] = legacy
		require.Equal(t, APIProtocolResponses, account.GetAPIProtocol())
	}
}

func TestBuiltinAdapterInjectsMissingCredentials(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	credentials := map[string]any{"api_protocol": APIProtocolChatCompletions}
	applyBuiltinAdapterCredentials(PlatformDoubao, credentials)
	require.Equal(t, "http://sub2api-desktop:8080/v1", credentials["base_url"])
	require.Equal(t, "builtin-desktop-key", credentials["api_key"])

	traework := map[string]any{}
	applyBuiltinAdapterCredentials(PlatformTraework, traework)
	require.Equal(t, "http://sub2api-traework:7864/v1", traework["base_url"])
	require.Equal(t, "builtin-traework-key", traework["api_key"])

	// 显式自定义地址不被覆盖。
	custom := map[string]any{"base_url": "http://custom.example/v1", "api_key": "custom-key"}
	applyBuiltinAdapterCredentials(PlatformDoubao, custom)
	require.Equal(t, "http://custom.example/v1", custom["base_url"])
	require.Equal(t, "custom-key", custom["api_key"])
}

func TestDoubaoCreateDuplicateAndUpdate(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	repo := &upstreamBillingProbeAdminRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{}}
	svc := &adminServiceImpl{accountRepo: repo}
	input := &CreateAccountInput{Name: "doubao", Platform: PlatformDoubao, Type: AccountTypeAPIKey, Credentials: doubaoTestAccount().Credentials, Concurrency: 20, SkipDefaultGroupBind: true}
	created, err := svc.CreateAccount(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, 1, created.Concurrency)
	copied, err := buildAccountForCreate(input, nil)
	require.NoError(t, err)
	require.Equal(t, 1, copied.Concurrency)
	concurrency := 12
	updated, err := svc.UpdateAccount(context.Background(), created.ID, &UpdateAccountInput{Concurrency: &concurrency, Credentials: map[string]any{"base_url": "http://new-adapter.example/v1"}})
	require.NoError(t, err)
	require.Equal(t, 1, updated.Concurrency)
	require.Equal(t, "adapter-test-key", updated.GetCredential("api_key"))
	require.Equal(t, "http://new-adapter.example/v1", updated.GetOpenAIBaseURL())
	// 内置模式允许清空自定义地址，回落到内置地址。
	reverted, err := svc.UpdateAccount(context.Background(), created.ID, &UpdateAccountInput{Credentials: map[string]any{"base_url": ""}})
	require.NoError(t, err)
	require.Equal(t, "http://sub2api-desktop:8080/v1", reverted.GetOpenAIBaseURL())
}

func TestDoubaoConnectionAndPlatform(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	account := doubaoTestAccount()
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
	c, recorder := newTestContext()
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "", "hi", AccountTestModeDefault))
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "http://upstream.example/v1/chat/completions", upstream.requests[0].URL.String())
	require.Equal(t, "Bearer adapter-test-key", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, DefaultDoubaoTestModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
	for _, model := range []string{"doubao-chat-turbo", "doubao/custom"} {
		platform, ok := DetectModelPlatform(model)
		require.True(t, ok)
		require.Equal(t, PlatformDoubao, platform)
	}
	require.NoError(t, validateProvider(PlatformDoubao))
	require.NoError(t, validateCheckMode(PlatformDoubao, MonitorCheckModeProbe))
	require.Error(t, validateCheckMode(PlatformDoubao, MonitorCheckModeQuota))
	body, err := providerDoubaoChatAdapter.buildBody(DefaultDoubaoTestModel, "hi")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "max_tokens").Exists())
}

func TestDoubaoChatNormalizesLegacyTokenLimitAtForwardingBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits string
		want   int64
	}{
		{"legacy", `"max_tokens":128`, 128},
		{"modern", `"max_completion_tokens":256`, 256},
		{"explicit_modern_wins", `"max_tokens":128,"max_completion_tokens":256`, 256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"doubao-pro","messages":[{"role":"user","content":"hi"}],"stream":false,` + tc.limits + `}`)
			c, _ := newTestContext()
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl-test","object":"chat.completion","model":"doubao-pro","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			_, err := svc.ForwardAsChatCompletions(context.Background(), c, doubaoTestAccount(), body, "", "")
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(upstream.lastBody, "max_tokens").Exists())
			require.Equal(t, tc.want, gjson.GetBytes(upstream.lastBody, "max_completion_tokens").Int())
			require.Equal(t, "hi", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
		})
	}
}

func TestDoubaoResponsesToolRoundtrip(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"doubao-pro","input":"read status","tools":[{"type":"namespace","name":"local","tools":[{"type":"function","name":"read_status","parameters":{"type":"object","properties":{}}}]}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"resp_doubao","object":"response","created_at":1,"status":"completed","model":"doubao-pro","output":[{"id":"fc_status","type":"function_call","call_id":"call-status","namespace":"local","name":"read_status","arguments":"{}","status":"completed"}]}`))}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	_, err := svc.Forward(context.Background(), c, doubaoTestAccount(), body)
	require.NoError(t, err)
	require.Equal(t, "http://upstream.example/v1/responses", upstream.lastReq.URL.String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "messages").Exists())
	require.NotEmpty(t, upstream.lastReq.Header.Get("X-Desktop-Session"))
	require.Equal(t, "namespace", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
	require.Equal(t, "read_status", gjson.GetBytes(upstream.lastBody, "tools.0.tools.0.name").String())
	require.Equal(t, "function_call", gjson.Get(rec.Body.String(), "output.0.type").String())
	require.Equal(t, "call-status", gjson.Get(rec.Body.String(), "output.0.call_id").String())
	require.Equal(t, "local", gjson.Get(rec.Body.String(), "output.0.namespace").String())

	body = []byte(`{"model":"doubao-pro","input":[{"role":"user","content":"read status"},{"type":"function_call","call_id":"call-status","namespace":"local","name":"read_status","arguments":"{}"},{"type":"function_call_output","call_id":"call-status","output":"ready"}],"stream":false}`)
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream.resp.Body = io.NopCloser(strings.NewReader(`{"id":"resp_doubao_2","object":"response","created_at":1,"status":"completed","model":"doubao-pro","output":[{"id":"msg_ready","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ready","annotations":[]}]}]}`))
	_, err = svc.Forward(context.Background(), c, doubaoTestAccount(), body)
	require.NoError(t, err)
	require.Equal(t, "http://upstream.example/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "function_call_output", gjson.GetBytes(upstream.lastBody, "input.2.type").String())
	require.Equal(t, "call-status", gjson.GetBytes(upstream.lastBody, "input.2.call_id").String())
	require.Equal(t, "local", gjson.GetBytes(upstream.lastBody, "input.1.namespace").String())
	require.Equal(t, "ready", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())

	body = []byte(`{"model":"doubao-pro","store":true,"previous_response_id":"resp_doubao","input":[{"type":"function_call_output","call_id":"call-status","output":"ready"}],"stream":false}`)
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	SetOpenAIHTTPResponseOwner(c, 1, 101)
	upstream.resp.Body = io.NopCloser(strings.NewReader(`{"id":"resp_doubao_3","object":"response","status":"completed","model":"doubao-pro","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ready"}]}],"usage":null}`))
	_, err = svc.Forward(context.Background(), c, doubaoTestAccount(), body)
	require.NoError(t, err)
	require.Equal(t, "resp_doubao", gjson.GetBytes(upstream.lastBody, "previous_response_id").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.Equal(t, "call-status", gjson.GetBytes(upstream.lastBody, "input.0.call_id").String())
	owned, err := svc.ValidateOpenAIHTTPResponseOwner(context.Background(), 0, "resp_doubao_3", 1, 101)
	require.NoError(t, err)
	require.True(t, owned)
	owned, err = svc.ValidateOpenAIHTTPResponseOwner(context.Background(), 0, "resp_doubao_3", 2, 202)
	require.NoError(t, err)
	require.False(t, owned)
}

func TestDoubaoHTTPContinuationSticksToOriginalAccount(t *testing.T) {
	ctx := context.Background()
	groupID := int64(9)
	account := *doubaoTestAccount()
	account.Schedulable = true
	account.Concurrency = 1
	account.Credentials["model_mapping"] = testModelMapping("doubao-pro")
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		cache:       &schedulerTestGatewayCache{}, cfg: &config.Config{},
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	require.True(t, account.SupportsHTTPResponsesContinuation())
	require.False(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}).SupportsHTTPResponsesContinuation())
	require.False(t, (&Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey}).SupportsHTTPResponsesContinuation())
	require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, groupID, "resp_doubao_sticky", account.ID, time.Hour))
	selection, decision, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID,
		"resp_doubao_sticky", "", "doubao-pro", nil, OpenAIUpstreamTransportAny,
		OpenAIEndpointCapabilityResponses, false, false, true, PlatformDoubao)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, account.ID, selection.Account.ID)
	require.True(t, decision.StickyPreviousHit)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestDoubaoFetchModels(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"doubao-chat-turbo"},{"id":"doubao-auto"},{"id":"doubao-pro"}]}`))}}
	svc := &AccountTestService{httpUpstream: upstream, cfg: rawChatCompletionsTestConfig()}
	models, err := svc.FetchUpstreamSupportedModels(context.Background(), doubaoTestAccount())
	require.NoError(t, err)
	require.ElementsMatch(t, DefaultDoubaoModelIDs(), models)
	require.Equal(t, "http://upstream.example/v1/models", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer adapter-test-key", upstream.lastReq.Header.Get("Authorization"))
}

func TestDoubaoBulkUpdateRejectsInvalidChangesBeforeWriting(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	concurrency := 2
	for _, input := range []*BulkUpdateAccountsInput{
		{AccountIDs: []int64{501}, Concurrency: &concurrency},
		{AccountIDs: []int64{501}, Credentials: map[string]any{"api_protocol": APIProtocolAnthropic}},
	} {
		repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{501: doubaoTestAccount()}}
		_, err := (&adminServiceImpl{accountRepo: repo}).BulkUpdateAccounts(context.Background(), input)
		require.Error(t, err)
		require.Empty(t, repo.bulkUpdates)
	}
}
