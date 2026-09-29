//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func autoRoutingTestAccount(id int64, mapping map[string]any) Account {
	return Account{
		ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"model_mapping": mapping},
	}
}

func TestAutoModelFallbackCandidatesRejectMappedHighestTier(t *testing.T) {
	repo := newMockSettingRepo()
	settings := NewSettingService(repo, nil)
	require.NoError(t, settings.SetModelAliasPolicy(context.Background(), &ModelAliasPolicy{Groups: []ModelAliasGroup{{
		Canonical: "gpt-6-astra", Aliases: []string{"provider/expensive"},
	}}}))
	require.NoError(t, settings.SetModelFallbackPolicy(context.Background(), &ModelFallbackPolicy{Enabled: true, Tiers: []ModelCapabilityTier{
		{Name: "highest", Models: []string{"gpt-6-astra"}},
		{Name: "lower", Models: []string{"gpt-5.3-codex", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5"}},
	}}))
	ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	channel := newTestChannelService(makeStandardRepo(Channel{
		ID: 1, Status: StatusActive, GroupIDs: []int64{10},
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {"gpt-5.6-terra": "provider/expensive"}},
	}, map[int64]string{10: PlatformOpenAI}))
	svc := &OpenAIGatewayService{
		settingService: settings, channelService: channel,
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{autoRoutingTestAccount(1, map[string]any{
			"gpt-5.6-sol": "gpt-5.6-sol", "provider/expensive": "provider/expensive",
			"gpt-5.6-luna": "provider/expensive", "gpt-5.5": "gpt-5.5",
		})}},
	}
	groupID := int64(10)
	body := []byte(`{"input":"hi"}`)
	candidates, err := svc.ModelFallbackCandidates(ctx, &groupID, "gpt-5.3-codex", body)
	require.NoError(t, err)
	require.Equal(t, []ModelFallbackCandidate{{Model: "gpt-5.6-sol", Tier: "lower"}, {Model: "gpt-5.5", Tier: "lower"}}, candidates)
	manual, err := svc.ModelFallbackCandidates(context.Background(), &groupID, "gpt-5.3-codex", body)
	require.NoError(t, err)
	require.Equal(t, []ModelFallbackCandidate{
		{Model: "gpt-5.6-sol", Tier: "lower"}, {Model: "gpt-5.6-terra", Tier: "lower"},
		{Model: "gpt-5.6-luna", Tier: "lower"}, {Model: "gpt-5.5", Tier: "lower"},
		{Model: "gpt-6-astra", Tier: "highest"},
	}, manual)
}

func TestAutoModelFallbackCandidatesKeepExcludedTierSnapshot(t *testing.T) {
	repo := newMockSettingRepo()
	settings := NewSettingService(repo, nil)
	ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	require.NoError(t, settings.SetModelFallbackPolicy(context.Background(), &ModelFallbackPolicy{Enabled: true, Tiers: []ModelCapabilityTier{
		{Name: "new order", Models: []string{"gpt-5.6-sol", "gpt-6-astra", "gpt-5.5"}},
	}}))
	svc := &OpenAIGatewayService{
		settingService: settings,
		accountRepo:    schedulerTestOpenAIAccountRepo{accounts: []Account{autoRoutingTestAccount(1, testModelMapping("gpt-6-astra", "gpt-5.5"))}},
	}
	candidates, err := svc.ModelFallbackCandidates(ctx, nil, "gpt-5.6-sol", []byte(`{"input":"hi"}`))
	require.NoError(t, err)
	require.Equal(t, []ModelFallbackCandidate{{Model: "gpt-5.5", Tier: "new order"}}, candidates)
}

func TestAutoModelFallbackCandidatesRejectCompactHighestTier(t *testing.T) {
	repo := newMockSettingRepo()
	settings := NewSettingService(repo, nil)
	require.NoError(t, settings.SetModelFallbackPolicy(context.Background(), &ModelFallbackPolicy{Enabled: true, Tiers: []ModelCapabilityTier{
		{Name: "highest", Models: []string{"gpt-6-astra"}},
		{Name: "lower", Models: []string{"gpt-5.6-sol", "gpt-5.5", "gpt-5.3-codex"}},
	}}))
	ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	account := autoRoutingTestAccount(1, testModelMapping("gpt-5.5", "gpt-5.3-codex"))
	account.Type = AccountTypeOAuth
	account.Credentials["compact_model_mapping"] = map[string]any{"gpt-5.5": "gpt-6-astra"}
	svc := &OpenAIGatewayService{
		settingService: settings,
		accountRepo:    schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
	}
	body := []byte(`{"input":"hi"}`)
	compact := WithOpenAIForwardModel(ctx, "gpt-5.6-sol", true)
	candidates, err := svc.ModelFallbackCandidates(compact, nil, "gpt-5.6-sol", body)
	require.NoError(t, err)
	require.Equal(t, []ModelFallbackCandidate{{Model: "gpt-5.3-codex", Tier: "lower"}}, candidates)
	ordinary, err := svc.ModelFallbackCandidates(ctx, nil, "gpt-5.6-sol", body)
	require.NoError(t, err)
	expected := []ModelFallbackCandidate{{Model: "gpt-5.5", Tier: "lower"}, {Model: "gpt-5.3-codex", Tier: "lower"}}
	require.Equal(t, expected, ordinary)
	manual := WithOpenAIForwardModel(context.Background(), "gpt-5.6-sol", true)
	manualCandidates, err := svc.ModelFallbackCandidates(manual, nil, "gpt-5.6-sol", body)
	require.NoError(t, err)
	require.Equal(t, expected, manualCandidates)
}

func TestAutoModelAccountAdmissionRejectsHighestTierMappings(t *testing.T) {
	ctx, err := (&SettingService{}).BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	for _, tc := range []struct {
		name           string
		requestedModel string
		accountModel   string
		compactModel   string
		compact        bool
		allowed        bool
	}{
		{name: "requested highest", requestedModel: "gpt-6-astra", accountModel: "gpt-5.5"},
		{name: "account highest", requestedModel: "gpt-5.5", accountModel: "gpt-6-astra"},
		{name: "account doubao", requestedModel: "gpt-5.5", accountModel: "volcengine/doubao-seed"},
		{name: "compact doubao", requestedModel: "gpt-5.5", accountModel: "gpt-5.5", compactModel: "doubao-pro", compact: true},
		{name: "compact highest", requestedModel: "gpt-5.5", accountModel: "gpt-5.5", compactModel: "gpt-6-astra", compact: true},
		{name: "compact mapping unused", requestedModel: "gpt-5.5", accountModel: "gpt-5.5", compactModel: "gpt-6-astra", allowed: true},
		{name: "allowed ordinary", requestedModel: "gpt-5.5", accountModel: "gpt-5.5", allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := autoRoutingTestAccount(1, map[string]any{tc.requestedModel: tc.accountModel})
			account.Type = AccountTypeOAuth
			account.Extra = map[string]any{"openai_compact_supported": true}
			if tc.compactModel != "" {
				account.Credentials["compact_model_mapping"] = map[string]any{tc.requestedModel: tc.compactModel}
			}
			for _, mode := range []string{"legacy", "advanced"} {
				t.Run(mode, func(t *testing.T) {
					check := func(checkCtx context.Context) (bool, string) {
						if mode == "legacy" {
							reason := openAICompatibleAccountEligibilityFailureReasonBeforeProfit(checkCtx, &account, PlatformOpenAI, tc.requestedModel, tc.compact, "")
							return reason == "", reason
						}
						scheduler := &defaultOpenAIAccountScheduler{}
						return scheduler.isAccountRequestCompatibleReason(checkCtx, &account, OpenAIAccountScheduleRequest{
							RequestedModel: tc.requestedModel, RequireCompact: tc.compact,
						})
					}
					allowed, reason := check(ctx)
					require.Equal(t, tc.allowed, allowed, reason)
					if !tc.allowed {
						require.Equal(t, "auto_model_excluded", reason)
					}
					manual, reason := check(context.Background())
					require.True(t, manual, reason)
				})
			}
		})
	}
}

func TestAutoModelSchedulerSkipsHighestTierAccountMappings(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, compact := range []bool{false, true} {
			t.Run(fmt.Sprintf("advanced_%t_compact_%t", advanced, compact), func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
				repo := newMockSettingRepo()
				repo.data[openAIAdvancedSchedulerSettingKey] = fmt.Sprint(advanced)
				settings := NewSettingService(repo, nil)
				ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
				require.NoError(t, err)
				accounts := []Account{
					autoRoutingTestAccount(1, map[string]any{"gpt-5.5": "gpt-6-astra"}),
					autoRoutingTestAccount(2, testModelMapping("gpt-5.5")),
				}
				groupID := int64(10)
				for i := range accounts {
					accounts[i].Priority = i
					accounts[i].AccountGroups = []AccountGroup{{GroupID: groupID}}
					accounts[i].Extra = map[string]any{"openai_compact_supported": true}
					if compact {
						accounts[i].Type = AccountTypeOAuth
					}
				}
				if compact {
					accounts[0].Credentials["model_mapping"] = testModelMapping("gpt-5.5")
					accounts[0].Credentials["compact_model_mapping"] = map[string]any{"gpt-5.5": "gpt-6-astra"}
				}
				require.Equal(t, "gpt-6-astra", ResolveOpenAIAccountUpstreamModelForRequest(&accounts[0], "gpt-5.5", compact))
				svc := &OpenAIGatewayService{
					settingService: settings, cfg: newSchedulerTestSubscriptionPriorityConfig(),
					rateLimitService:   &RateLimitService{settingService: settings},
					accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
					concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
				}
				require.Equal(t, advanced, svc.isOpenAIAdvancedSchedulerEnabled(ctx))
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-5.5", nil, OpenAIUpstreamTransportAny, compact)
				require.NoError(t, err)
				require.NotNil(t, selection)
				require.Equal(t, int64(2), selection.Account.ID)
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				manual, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "", "gpt-5.5", nil, OpenAIUpstreamTransportAny, compact)
				require.NoError(t, err)
				require.NotNil(t, manual)
				require.Equal(t, int64(1), manual.Account.ID)
				if manual.ReleaseFunc != nil {
					manual.ReleaseFunc()
				}
			})
		}
	}
}
