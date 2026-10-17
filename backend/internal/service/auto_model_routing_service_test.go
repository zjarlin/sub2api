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
	// 与上游差异（合并引入）：GPT-5.5 已从 Codex 订阅侧退役，OAuth/setup-token 账号
	// 映射到 gpt-5.5 会先被退役守卫拦成 model_not_supported，盖过本用例关心的
	// 「compact 映射到最高档 ⇒ auto_model_excluded」判定。这里改用同为低档、
	// 但未退役的 gpt-5.4 作为账号侧普通模型，其余断言语义不变。
	require.NoError(t, settings.SetModelFallbackPolicy(context.Background(), &ModelFallbackPolicy{Enabled: true, Tiers: []ModelCapabilityTier{
		{Name: "highest", Models: []string{"gpt-6-astra"}},
		{Name: "lower", Models: []string{"gpt-5.6-sol", "gpt-5.4", "gpt-5.3-codex"}},
	}}))
	ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	account := autoRoutingTestAccount(1, testModelMapping("gpt-5.4", "gpt-5.3-codex"))
	account.Type = AccountTypeOAuth
	account.Credentials["compact_model_mapping"] = map[string]any{"gpt-5.4": "gpt-6-astra"}
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
	expected := []ModelFallbackCandidate{{Model: "gpt-5.4", Tier: "lower"}, {Model: "gpt-5.3-codex", Tier: "lower"}}
	require.Equal(t, expected, ordinary)
	manual := WithOpenAIForwardModel(context.Background(), "gpt-5.6-sol", true)
	manualCandidates, err := svc.ModelFallbackCandidates(manual, nil, "gpt-5.6-sol", body)
	require.NoError(t, err)
	require.Equal(t, expected, manualCandidates)
}

func TestAutoModelAccountAdmissionRejectsHighestTierMappings(t *testing.T) {
	ctx, err := (&SettingService{}).BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	// 与上游差异（合并引入）：GPT-5.5 已从 Codex 订阅侧退役，OAuth 账号把请求映射到
	// gpt-5.5 时先被退役守卫拦成 model_not_supported，盖过本用例要验的最高档排除语义。
	// 账号侧普通模型因此改用未退役的 gpt-5.4；判定顺序与断言（auto_model_excluded、
	// 手工模式放行）仍与上游一致。
	for _, tc := range []struct {
		name           string
		requestedModel string
		accountModel   string
		compactModel   string
		compact        bool
		allowed        bool
	}{
		{name: "requested highest", requestedModel: "gpt-6-astra", accountModel: "gpt-5.4"},
		{name: "account highest", requestedModel: "gpt-5.4", accountModel: "gpt-6-astra"},
		{name: "account doubao", requestedModel: "gpt-5.4", accountModel: "volcengine/doubao-seed"},
		{name: "compact doubao", requestedModel: "gpt-5.4", accountModel: "gpt-5.4", compactModel: "doubao-pro", compact: true},
		{name: "compact highest", requestedModel: "gpt-5.4", accountModel: "gpt-5.4", compactModel: "gpt-6-astra", compact: true},
		{name: "compact mapping unused", requestedModel: "gpt-5.4", accountModel: "gpt-5.4", compactModel: "gpt-6-astra", allowed: true},
		{name: "allowed ordinary", requestedModel: "gpt-5.4", accountModel: "gpt-5.4", allowed: true},
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
				// 与上游差异（合并引入）：OAuth 账号映射到 gpt-5.5 会被订阅退役守卫拦下
				// （见 TestGPT55RetirementSchedulerAndDiagnosis），此处用未退役的 gpt-5.4
				// 保持「最高档映射账号该被跳过」的调度断言。
				accounts := []Account{
					autoRoutingTestAccount(1, map[string]any{"gpt-5.4": "gpt-6-astra"}),
					autoRoutingTestAccount(2, testModelMapping("gpt-5.4")),
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
					accounts[0].Credentials["model_mapping"] = testModelMapping("gpt-5.4")
					accounts[0].Credentials["compact_model_mapping"] = map[string]any{"gpt-5.4": "gpt-6-astra"}
				}
				require.Equal(t, "gpt-6-astra", ResolveOpenAIAccountUpstreamModelForRequest(&accounts[0], "gpt-5.4", compact))
				svc := &OpenAIGatewayService{
					settingService: settings, cfg: newSchedulerTestSubscriptionPriorityConfig(),
					rateLimitService:   &RateLimitService{settingService: settings},
					accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
					concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
				}
				require.Equal(t, advanced, svc.isOpenAIAdvancedSchedulerEnabled(ctx))
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-5.4", nil, OpenAIUpstreamTransportAny, compact)
				require.NoError(t, err)
				require.NotNil(t, selection)
				require.Equal(t, int64(2), selection.Account.ID)
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				manual, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "", "gpt-5.4", nil, OpenAIUpstreamTransportAny, compact)
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

func TestModelFallbackAutoSentinelExpandsToRankedAutoCandidates(t *testing.T) {
	repo := newMockSettingRepo()
	settings := NewSettingService(repo, nil)
	require.NoError(t, settings.SetModelFallbackPolicy(context.Background(), &ModelFallbackPolicy{
		Enabled: true, AutoFallback: true,
		Tiers: []ModelCapabilityTier{
			{Name: "highest", Models: []string{"gpt-6-astra"}},
			{Name: "guard", Models: []string{"meta/llama-guard-4-12b"}},
		},
	}))
	// 账号目录里同时有：guard 兄弟模型（不该被 auto 选中）、决策/专有模型（被过滤）、
	// 以及两个档位内文本模型（应按 Auto 性价比顺序作为兜底候选）。
	svc := &OpenAIGatewayService{
		settingService: settings,
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{autoRoutingTestAccount(1, map[string]any{
			"meta/llama-guard-4-12b":                       "meta/llama-guard-4-12b",
			"nvidia/llama-3.1-nemoguard-8b-content-safety": "nvidia/llama-3.1-nemoguard-8b-content-safety",
			"gpt-5.3-codex":                                "gpt-5.3-codex",
			"gpt-5.5":                                      "gpt-5.5",
			"text-embedding-3-large":                       "text-embedding-3-large",
		})}},
	}
	groupID := int64(10)
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	// 显式请求 guard 模型：档位内没有兄弟候选可用（未评级 guard 兄弟被 auto 文本判定排除），
	// 末尾 auto 兜底展开为可用的通用文本模型。
	candidates, err := svc.ModelFallbackCandidates(context.Background(), &groupID, "meta/llama-guard-4-12b", body)
	require.NoError(t, err)
	require.NotEmpty(t, candidates)
	last := candidates[len(candidates)-1]
	require.Equal(t, ModelFallbackAutoModel, last.Tier)
	require.Contains(t, []string{"gpt-5.3-codex", "gpt-5.5"}, last.Model)
	// guard/embedding 一律不能出现在降级链里。
	for _, candidate := range candidates {
		require.NotContains(t, candidate.Model, "guard")
		require.NotContains(t, candidate.Model, "embedding")
	}

	// 未评级模型没有任何档位候选，auto 兜底独立成立。
	untiered, err := svc.ModelFallbackCandidates(context.Background(), &groupID, "totally-unknown-model", body)
	require.NoError(t, err)
	require.NotEmpty(t, untiered)
	for _, candidate := range untiered {
		require.Equal(t, ModelFallbackAutoModel, candidate.Tier)
	}
}
