package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 调度测试显式声明模型支持，不依赖空配置放行，也不改写模型限流键。
func testModelMapping(models ...string) map[string]any {
	mapping := make(map[string]any, len(models))
	for _, model := range models {
		mapping[model] = model
	}
	return mapping
}

func TestOpenAIModelRoutingRequiresEvidence(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		account := &Account{
			Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Extra: map[string]any{"openai_passthrough": passthrough},
		}
		require.False(t, account.IsModelSupported("q3-4b"))
		account.Credentials = map[string]any{"model_mapping": map[string]any{"gpt-*": "gpt-*"}}
		require.False(t, account.IsModelSupported("q3-4b"))
		require.True(t, account.IsModelSupported("gpt-5.4"))
		account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
			Source: "upstream", SyncedAt: time.Now().UTC().Format(time.RFC3339), Models: []string{"q3-4b"},
		})
		require.True(t, account.IsModelSupported("q3-4b"))
		require.False(t, account.IsModelSupported("gpt-5.4"))
	}
}

func TestOpenAIModelRoutingStaleCatalogDoesNotAllowUnknownModels(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Extra: map[string]any{"openai_passthrough": true},
	}
	account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source: "upstream", SyncedAt: time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339), Models: []string{"gpt-5.4"},
	})
	require.True(t, account.IsModelSupported("gpt-5.4"))
	require.False(t, account.IsModelSupported("q3-4b"))
	account.Credentials = map[string]any{"model_mapping": map[string]any{"q3-4b": "q3-4b"}}
	require.True(t, account.IsModelSupported("q3-4b"))
}

func TestOpenAIPassthroughCatalogChecksOriginalModel(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"public-model": "upstream-model"}},
		Extra:       map[string]any{"openai_passthrough": true},
	}
	account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source: "upstream", SyncedAt: time.Now().UTC().Format(time.RFC3339), Models: []string{"upstream-model"},
	})
	require.False(t, account.IsModelSupported("public-model"))
	require.True(t, account.IsModelSupported("upstream-model"))
	account.Extra["openai_passthrough"] = false
	require.True(t, account.IsModelSupported("public-model"))
}

func TestOpenAIModelRoutingRejectsUnknownPassthroughAcrossSchedulerPaths(t *testing.T) {
	for _, tc := range []struct {
		name     string
		advanced string
		sticky   bool
	}{
		{name: "legacy/fresh", advanced: "false"},
		{name: "legacy/sticky", advanced: "false", sticky: true},
		{name: "advanced/fresh", advanced: "true"},
		{name: "advanced/sticky", advanced: "true", sticky: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
			ctx := context.Background()
			groupID := int64(6)
			accounts := []Account{
				{
					ID: 840, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Status: StatusActive, Schedulable: true, Concurrency: 1,
					Extra:         map[string]any{"openai_passthrough": true},
					AccountGroups: []AccountGroup{{GroupID: groupID}},
				},
				{
					ID: 310, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 100,
					Credentials:   map[string]any{"model_mapping": map[string]any{"q3-4b": "q3-4b"}},
					AccountGroups: []AccountGroup{{GroupID: groupID}},
				},
			}
			cache := &schedulerTestGatewayCache{}
			var acquiredIDs []int64
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
				cache:       cache, cfg: newSchedulerTestOpenAIWSV2Config(),
				rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService(tc.advanced),
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &acquiredIDs}),
			}
			previousID, sessionHash := "", ""
			if tc.sticky {
				previousID, sessionHash = "resp_q3_wrong_account", "q3_session"
				cache.sessionBindings = map[string]int64{"openai:" + sessionHash: 840}
				err := svc.getOpenAIWSStateStore().BindResponseAccount(ctx, groupID, previousID, 840, time.Hour)
				require.NoError(t, err)
			}
			selection, _, err := svc.SelectAccountWithSchedulerForCapability(
				ctx, &groupID, previousID, sessionHash, "q3-4b", nil,
				OpenAIUpstreamTransportAny, "", false, false, true,
			)
			require.NoError(t, err)
			require.Equal(t, int64(310), selection.Account.ID)
			selection.ReleaseFunc()
			require.NotContains(t, acquiredIDs, int64(840))

			// 正确账号失效后，故障切换也不能把未知账号重新选入候选池。
			selection, _, err = svc.SelectAccountWithSchedulerForCapability(
				ctx, &groupID, "", "", "q3-4b", map[int64]struct{}{310: {}},
				OpenAIUpstreamTransportAny, "", false, false, true,
			)
			require.Error(t, err)
			require.Nil(t, selection)
			require.NotContains(t, acquiredIDs, int64(840))
		})
	}
}
