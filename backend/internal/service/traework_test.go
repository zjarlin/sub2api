//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func traeworkTestAccount() *Account {
	return &Account{ID: 601, Platform: PlatformTraework, Type: AccountTypeAPIKey, Status: StatusActive,
		Credentials: map[string]any{"base_url": "http://sub2api-traework:7864/v1", "api_key": "adapter-key", "api_protocol": APIProtocolChatCompletions}}
}

func TestTraeworkCredentialValidation(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	account := traeworkTestAccount()
	require.NoError(t, validateTraeworkCredentials(account.Platform, account.Type, account.Credentials))
	require.Error(t, validateTraeworkCredentials(account.Platform, AccountTypeOAuth, account.Credentials))
	account.Credentials["api_protocol"] = APIProtocolResponses
	require.Error(t, validateTraeworkCredentials(account.Platform, account.Type, account.Credentials))
	require.Equal(t, APIProtocolChatCompletions, account.GetAPIProtocol())
	require.Empty(t, account.GetAccountMode())
}

func TestTraeworkRequiresBuiltinAdapter(t *testing.T) {
	SetBuiltinAdapterConfig(nil)
	account := traeworkTestAccount()
	require.Error(t, validateTraeworkCredentials(account.Platform, account.Type, account.Credentials))
}

func TestTraeworkConnectionUsesBuiltinSidecar(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	account := traeworkTestAccount()
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
	c, recorder := newTestContext()

	require.NoError(t, svc.TestAccountConnection(c, account.ID, "", "hi", AccountTestModeDefault))
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "http://sub2api-traework:7864/v1/chat/completions", upstream.requests[0].URL.String())
	require.Equal(t, "Bearer adapter-key", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, DefaultTraeworkModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
}

func TestTraeworkCreateForcesSingleConcurrency(t *testing.T) {
	enableBuiltinAdapterForTest(t)
	repo := &upstreamBillingProbeAdminRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{}}
	svc := &adminServiceImpl{accountRepo: repo}
	created, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name: "traework", Platform: PlatformTraework, Type: AccountTypeAPIKey,
		Credentials: traeworkTestAccount().Credentials, Concurrency: 16, SkipDefaultGroupBind: true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, created.Concurrency)
}

func TestTraeworkAdapterProbeHasNoMaxTokens(t *testing.T) {
	body, err := providerTraeworkChatAdapter.buildBody(DefaultTraeworkModel, "hi")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "max_tokens").Exists())
	require.Equal(t, "choices.0.message.content", providerTraeworkChatAdapter.textPath)
}
