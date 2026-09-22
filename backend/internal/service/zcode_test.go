//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func zcodeTestAccount() *Account {
	return &Account{ID: 701, Platform: PlatformZcode, Type: AccountTypeAPIKey, Status: StatusActive,
		Credentials: map[string]any{"base_url": "http://sub2api-zcode:7865/v1", "api_key": "adapter-key", "api_protocol": APIProtocolChatCompletions}}
}

func TestZcodeCredentialValidation(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	account := zcodeTestAccount()
	require.NoError(t, validateZcodeCredentials(account.Platform, account.Type, account.Credentials))
	require.Error(t, validateZcodeCredentials(account.Platform, AccountTypeOAuth, account.Credentials))
	account.Credentials["api_protocol"] = APIProtocolResponses
	require.Error(t, validateZcodeCredentials(account.Platform, account.Type, account.Credentials))
	require.Equal(t, APIProtocolChatCompletions, account.GetAPIProtocol())
	require.Empty(t, account.GetAccountMode())
}

func TestZcodeRequiresBuiltinAdapter(t *testing.T) {
	SetBuiltinAdapterConfig(nil)
	account := zcodeTestAccount()
	require.Error(t, validateZcodeCredentials(account.Platform, account.Type, account.Credentials))
}

func TestZcodeConnectionUsesBuiltinSidecar(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	account := zcodeTestAccount()
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
	c, recorder := newTestContext()

	require.NoError(t, svc.TestAccountConnection(c, account.ID, "", "hi", AccountTestModeDefault))
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "http://sub2api-zcode:7865/v1/chat/completions", upstream.requests[0].URL.String())
	require.Equal(t, "Bearer adapter-key", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, DefaultZcodeModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
}

func TestZcodeCreateForcesSingleConcurrency(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	repo := &upstreamBillingProbeAdminRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{}}
	svc := &adminServiceImpl{accountRepo: repo}
	created, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name: "zcode", Platform: PlatformZcode, Type: AccountTypeAPIKey,
		Credentials: map[string]any{}, Concurrency: 16, SkipDefaultGroupBind: true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, created.Concurrency)
	require.Equal(t, "http://sub2api-zcode:7865/v1", created.GetOpenAIBaseURL())
	require.Equal(t, "builtin-zcode-key", created.GetCredential("api_key"))
	// 更新存量账号时也修复旧并发值，即使本次没有显式传入并发字段。
	created.Concurrency = 16
	updated, err := svc.UpdateAccount(context.Background(), created.ID, &UpdateAccountInput{Name: "renamed zcode"})
	require.NoError(t, err)
	require.Equal(t, 1, updated.Concurrency)
}

func TestZcodeModelDiscoveryUsesBuiltinSidecar(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	account, err := buildAccountForCreate(&CreateAccountInput{
		Platform: PlatformZcode, Type: AccountTypeAPIKey, Credentials: map[string]any{},
	}, nil)
	require.NoError(t, err)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"glm-5.3"},{"id":"glm-5.3-flash"}]}`)),
	}}
	// 内置 sidecar 使用容器网络 HTTP，与部署配置中的允许 HTTP 开关一致。
	cfg := upstreamModelSyncTestConfig()
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := &AccountTestService{httpUpstream: upstream, cfg: cfg}
	models, err := svc.FetchUpstreamSupportedModels(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{"glm-5.3", "glm-5.3-flash"}, models)
	require.Equal(t, "http://sub2api-zcode:7865/v1/models", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer builtin-zcode-key", upstream.lastReq.Header.Get("Authorization"))
}

func TestZcodeBulkUpdateRejectsInvalidChangesBeforeWriting(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	concurrency := 2
	for _, input := range []*BulkUpdateAccountsInput{
		{AccountIDs: []int64{701}, Concurrency: &concurrency},
		{AccountIDs: []int64{701}, Credentials: map[string]any{"api_protocol": APIProtocolResponses}},
	} {
		repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{701: zcodeTestAccount()}}
		_, err := (&adminServiceImpl{accountRepo: repo}).BulkUpdateAccounts(context.Background(), input)
		require.Error(t, err)
		require.Empty(t, repo.bulkUpdates)
	}
}

func TestZcodeMonitorUsesChatAdapterAndProtectsRequestFields(t *testing.T) {
	require.NoError(t, validateProvider(MonitorProviderZcode))
	require.NoError(t, validateCheckMode(MonitorProviderZcode, MonitorCheckModeProbe))
	require.ErrorIs(t, validateCheckMode(MonitorProviderZcode, MonitorCheckModeQuota), ErrChannelMonitorAccountNotSupportable)
	require.ErrorIs(t, validateCheckMode(MonitorProviderZcode, MonitorCheckModeQuotaProbe), ErrChannelMonitorAccountNotSupportable)

	upstream := &openAICaptureHandler{}
	endpoint := setupFakeOpenAI(t, upstream)
	result := runCheckForModel(context.Background(), MonitorProviderZcode, endpoint, "adapter-key", DefaultZcodeModel, &CheckOptions{
		BodyOverrideMode: MonitorBodyOverrideModeMerge,
		BodyOverride:     map[string]any{"model": "wrong-model", "messages": []any{}, "stream": true},
	})
	require.Equal(t, MonitorStatusOperational, result.Status, result.Message)
	require.Equal(t, providerOpenAIPath, upstream.lastPath)
	require.Equal(t, "Bearer adapter-key", upstream.lastHeaders.Get("Authorization"))
	require.Equal(t, DefaultZcodeModel, upstream.lastBody["model"])
	require.Equal(t, false, upstream.lastBody["stream"])
	require.NotContains(t, upstream.lastBody, "max_tokens")
	require.Error(t, validateReplaceRequestBody(MonitorProviderZcode, MonitorAPIModeChatCompletions, map[string]any{"model": DefaultZcodeModel}))
}

// ZCode 复用 OpenAI 网关，并默认可作为 openai/Codex 分组的混合调度来源。
func TestZcodeMixedSchedulingIntoOpenAI(t *testing.T) {
	require.True(t, SupportsMixedScheduling(PlatformZcode))
	require.Equal(t, []string{PlatformOpenAI}, MixedSchedulingTargetPlatforms(PlatformZcode))
	acc := mixedSchedulingAccount(PlatformZcode, true)
	require.True(t, acc.IsMixedSchedulingEnabled())
	require.True(t, openAIAccountMatchesPlatform(acc, PlatformOpenAI))
	require.False(t, openAIAccountMatchesPlatform(mixedSchedulingAccount(PlatformZcode, false), PlatformOpenAI))
}
