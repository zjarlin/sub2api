package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func modelCandidateTestAccount(id int64, mapping map[string]any) Account {
	return Account{
		ID:       id,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": mapping,
		},
		Status:      StatusActive,
		Schedulable: true,
	}
}

func TestOpenAIModelCandidateIndexInvalidatesWithDirectoryAndAliases(t *testing.T) {
	cache := newOpenAIModelCandidateIndexCache()
	policy := &ModelAliasPolicy{Groups: []ModelAliasGroup{{Canonical: "canonical", Aliases: []string{"alias"}}}}
	ctx := WithModelAliases(context.Background(), policy)
	accounts := accountsWithModelAliases(ctx, []Account{
		modelCandidateTestAccount(1, map[string]any{"alias": "upstream"}),
		modelCandidateTestAccount(2, map[string]any{"other": "other"}),
	})

	lookup := func(accounts []Account, policy *ModelAliasPolicy, model string) []int64 {
		ids, err := cache.candidateIDs(accounts, nil, PlatformOpenAI, OpenAIRequestProtocolResponses, model, policy)
		require.NoError(t, err)
		return ids
	}
	require.Equal(t, []int64{1}, lookup(accounts, policy, "canonical"))
	require.Equal(t, []int64{1}, lookup(accounts, policy, "alias"))
	require.Empty(t, lookup(accounts, policy, "ALIAS"))

	changedMapping := accountsWithModelAliases(ctx, []Account{
		modelCandidateTestAccount(1, map[string]any{"other": "other"}),
		modelCandidateTestAccount(2, map[string]any{"canonical": "upstream"}),
	})
	require.Equal(t, []int64{2}, lookup(changedMapping, policy, "canonical"))

	changedCatalog := []Account{modelCandidateTestAccount(3, map[string]any{"other": "other"})}
	changedCatalog[0].SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{Models: []string{"canonical"}})
	require.Equal(t, []int64{3}, lookup(changedCatalog, policy, "canonical"))
	changedCatalog[0].SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{Models: []string{"different"}})
	require.Empty(t, lookup(changedCatalog, policy, "canonical"))

	withoutAlias := &ModelAliasPolicy{}
	rawAccounts := []Account{
		modelCandidateTestAccount(1, map[string]any{"alias": "upstream"}),
		modelCandidateTestAccount(2, map[string]any{"other": "other"}),
	}
	require.Empty(t, lookup(rawAccounts, withoutAlias, "canonical"))
	require.Equal(t, []int64{1}, lookup(rawAccounts, withoutAlias, "alias"))
	require.Equal(t, []int64{1, 2}, lookup(append(accounts[:1:1], modelCandidateTestAccount(2, map[string]any{"canonical": "upstream"})), policy, "canonical"))
}

func TestOpenAIModelCandidateIndexSeparatesGroupAndProtocol(t *testing.T) {
	cache := newOpenAIModelCandidateIndexCache()
	accounts := []Account{modelCandidateTestAccount(1, map[string]any{"model": "upstream"})}
	groupOne, groupTwo := int64(1), int64(2)
	for _, protocol := range []OpenAIRequestProtocol{
		OpenAIRequestProtocolResponses,
		OpenAIRequestProtocolChatCompletions,
		OpenAIRequestProtocolMessages,
	} {
		ids, err := cache.candidateIDs(accounts, &groupOne, PlatformOpenAI, protocol, "model", nil)
		require.NoError(t, err)
		require.Equal(t, []int64{1}, ids)
	}
	_, err := cache.candidateIDs(accounts, &groupTwo, PlatformOpenAI, OpenAIRequestProtocolMessages, "model", nil)
	require.NoError(t, err)
	require.Len(t, cache.entries, 4)
	secondGroupAccounts := []Account{modelCandidateTestAccount(2, map[string]any{"model": "upstream"})}
	ids, err := cache.candidateIDs(secondGroupAccounts, &groupTwo, PlatformOpenAI, OpenAIRequestProtocolMessages, "model", nil)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, ids)
	ids, err = cache.candidateIDs(secondGroupAccounts, &groupOne, PlatformOpenAI, OpenAIRequestProtocolMessages, "model", nil)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, ids)

	ctx := WithOpenAIRequestProtocol(context.Background(), OpenAIRequestProtocolMessages)
	require.Equal(t, OpenAIRequestProtocolMessages, openAIRequestProtocol(ctx, OpenAIEndpointCapabilityChatCompletions))
	require.Equal(t, OpenAIRequestProtocolResponses, openAIRequestProtocol(context.Background(), OpenAIEndpointCapabilityResponses))
}

func TestOpenAIModelCandidateIndexRetainsOnlyDirectoryIDs(t *testing.T) {
	cache := newOpenAIModelCandidateIndexCache()
	account := modelCandidateTestAccount(1, map[string]any{"model": "upstream"})
	accounts := []Account{account}
	firstID, err := openAIModelCandidateDirectoryID(accounts, nil)
	require.NoError(t, err)
	ids, err := cache.candidateIDs(accounts, nil, PlatformOpenAI, OpenAIRequestProtocolChatCompletions, "model", nil)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, ids)

	accounts[0].Status = StatusDisabled
	accounts[0].Concurrency = 0
	accounts[0].Priority = 100
	secondID, err := openAIModelCandidateDirectoryID(accounts, nil)
	require.NoError(t, err)
	require.Equal(t, firstID, secondID)
	ids, err = cache.candidateIDs(accounts, nil, PlatformOpenAI, OpenAIRequestProtocolChatCompletions, "model", nil)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, ids)
	require.Len(t, cache.entries, 1)
}

func TestOpenAIModelCandidateIndexExpiresAndBoundsEntries(t *testing.T) {
	cache := newOpenAIModelCandidateIndexCache()
	now := time.Now()
	cache.now = func() time.Time { return now }
	cache.ttl = time.Second
	cache.maxEntries = 2
	accounts := []Account{modelCandidateTestAccount(1, map[string]any{"a": "a", "b": "b", "c": "c"})}
	for _, model := range []string{"a", "b", "c"} {
		_, err := cache.candidateIDs(accounts, nil, PlatformOpenAI, OpenAIRequestProtocolChatCompletions, model, nil)
		require.NoError(t, err)
	}
	require.Len(t, cache.entries, 2)
	now = now.Add(2 * time.Second)
	_, err := cache.candidateIDs(accounts, nil, PlatformOpenAI, OpenAIRequestProtocolChatCompletions, "a", nil)
	require.NoError(t, err)
	require.Len(t, cache.entries, 1)
}

func TestOpenAIAccountMaySupportModelFromDirectoryIsConservative(t *testing.T) {
	cases := []struct {
		name    string
		account Account
		model   string
	}{
		{"unknown directory", modelCandidateTestAccount(1, nil), "new-model"},
		{"exact mapping", modelCandidateTestAccount(2, map[string]any{"model": "upstream"}), "model"},
		{"wildcard mapping", modelCandidateTestAccount(3, map[string]any{"model-*": "upstream"}), "model-v2"},
		{"gemini normalized", Account{ID: 4, Platform: PlatformGemini, Credentials: map[string]any{"model_mapping": map[string]any{"gemini-3.1-pro-preview": "target"}}}, "gemini-3.1-pro-preview-customtools"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			require.True(t, openAIAccountMaySupportModelFromDirectory(&testCase.account, []string{testCase.model}))
		})
	}

	passthrough := modelCandidateTestAccount(5, map[string]any{"model": "upstream"})
	passthrough.Extra = map[string]any{"openai_passthrough": true}
	require.True(t, passthrough.IsModelSupported("model"))
	require.True(t, openAIAccountMaySupportModelFromDirectory(&passthrough, []string{"model"}))

	grok := Account{ID: 6, Platform: PlatformGrok}
	require.True(t, grok.IsModelSupported("grok"))
	require.True(t, openAIAccountMaySupportModelFromDirectory(&grok, []string{"grok"}))

	deepseek := Account{ID: 7, Platform: PlatformDeepseek}
	require.True(t, deepseek.IsModelSupported("deepseek-flash"))
	require.True(t, openAIAccountMaySupportModelFromDirectory(&deepseek, []string{"deepseek-flash"}))
}

func TestOpenAIModelCandidateIndexFeedsAdvancedSchedulerWithAlias(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(9001)
	accounts := []Account{
		modelCandidateTestAccount(1, map[string]any{"other": "other"}),
		modelCandidateTestAccount(2, map[string]any{"alias": "upstream"}),
	}
	for i := range accounts {
		accounts[i].AccountGroups = []AccountGroup{{GroupID: groupID}}
		accounts[i].Concurrency = 1
	}
	service := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              &schedulerTestGatewayCache{},
		cfg:                &config.Config{},
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	policy := &ModelAliasPolicy{Groups: []ModelAliasGroup{{Canonical: "canonical", Aliases: []string{"alias"}}}}
	ctx := WithModelAliases(context.Background(), policy)
	ctx = WithOpenAIRequestProtocol(ctx, OpenAIRequestProtocolMessages)
	selection, _, err := service.SelectAccountWithSchedulerForCapability(
		ctx, &groupID, "", "", "canonical", nil,
		OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions,
		false, false, true, PlatformOpenAI,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(2), selection.Account.ID)
	selection.ReleaseFunc()
}
