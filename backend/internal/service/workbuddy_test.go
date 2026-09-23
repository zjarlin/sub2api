//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func workbuddyTestAccount() *Account {
	return &Account{ID: 601, Platform: PlatformWorkbuddy, Type: AccountTypeAPIKey, Status: StatusActive,
		Credentials: map[string]any{"base_url": "http://sub2api-workbuddy:7863/v1", "api_key": "adapter-key", "api_protocol": APIProtocolChatCompletions}}
}

func TestWorkbuddyCredentialValidation(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	account := workbuddyTestAccount()
	require.NoError(t, validateBuiltinChatCredentials(account.Platform, account.Type, account.Credentials))
	require.Error(t, validateBuiltinChatCredentials(account.Platform, AccountTypeOAuth, account.Credentials))
	account.Credentials["api_protocol"] = APIProtocolResponses
	require.Error(t, validateBuiltinChatCredentials(account.Platform, account.Type, account.Credentials))
	require.Equal(t, APIProtocolChatCompletions, account.GetAPIProtocol())
	require.Empty(t, account.GetAccountMode())
}

func TestWorkbuddyRequiresBuiltinAdapter(t *testing.T) {
	SetBuiltinAdapterConfig(nil)
	account := workbuddyTestAccount()
	require.Error(t, validateBuiltinChatCredentials(account.Platform, account.Type, account.Credentials))
}

func TestWorkbuddyConnectionUsesBuiltinSidecar(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	account := workbuddyTestAccount()
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
	c, recorder := newTestContext()

	require.NoError(t, svc.TestAccountConnection(c, account.ID, "", "hi", AccountTestModeDefault))
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "http://sub2api-workbuddy:7863/v1/chat/completions", upstream.requests[0].URL.String())
	require.Equal(t, "Bearer adapter-key", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, DefaultWorkbuddyModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
}

func TestWorkbuddyCreateForcesSingleConcurrency(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	repo := &upstreamBillingProbeAdminRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{}}
	svc := &adminServiceImpl{accountRepo: repo}
	created, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name: "workbuddy", Platform: PlatformWorkbuddy, Type: AccountTypeAPIKey,
		Credentials: workbuddyTestAccount().Credentials, Concurrency: 16, SkipDefaultGroupBind: true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, created.Concurrency)
}

func TestWorkbuddyAdapterProbeHasNoMaxTokens(t *testing.T) {
	body, err := providerTraeworkChatAdapter.buildBody(DefaultWorkbuddyModel, "hi")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "max_tokens").Exists())
	require.Equal(t, "choices.0.message.content", providerTraeworkChatAdapter.textPath)
}
