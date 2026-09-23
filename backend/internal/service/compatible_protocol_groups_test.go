//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 模型目录提供调度依据，客户端模型 ID 无需配置别名。
func TestCompatibleProtocolGroupsRouteOriginalModelIDs(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(6)
	otherGroupID := int64(7)
	kimi := Account{
		ID: 201, Name: "kimi", Platform: PlatformKimi, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials:   map[string]any{"api_key": "test-key", "api_protocol": APIProtocolChatCompletions},
		AccountGroups: []AccountGroup{{GroupID: groupID}},
	}
	kimi.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source: "upstream", SyncedAt: time.Now().UTC().Format(time.RFC3339), Models: []string{"kimi-k2.5"},
	})
	otherGroup := kimi
	otherGroup.ID = 202
	otherGroup.Priority = -10
	otherGroup.AccountGroups = []AccountGroup{{GroupID: otherGroupID}}
	unknown := Account{
		ID: 203, Platform: PlatformZhipu, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: -20,
		Credentials:   map[string]any{"api_key": "test-key"},
		AccountGroups: []AccountGroup{{GroupID: groupID}},
	}
	codex := Account{
		ID: 204, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials:   map[string]any{"api_key": "test-key"},
		AccountGroups: []AccountGroup{{GroupID: groupID}},
	}
	codex.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source: "upstream", SyncedAt: time.Now().UTC().Format(time.RFC3339), Models: []string{"gpt-5.4"},
	})
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{unknown, otherGroup, kimi, codex}}},
		cfg:                &config.Config{RunMode: config.RunModeStandard},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	for _, tc := range []struct {
		model     string
		accountID int64
	}{{"kimi-k2.5", kimi.ID}, {"gpt-5.4", codex.ID}} {
		t.Run(tc.model, func(t *testing.T) {
			selection, _, err := svc.SelectAccountWithSchedulerForCapability(
				context.Background(), &groupID, "", "", tc.model, nil,
				OpenAIUpstreamTransportAny, "", false, false, false,
			)
			require.NoError(t, err)
			require.Equal(t, tc.accountID, selection.Account.ID)
			require.Equal(t, tc.model, selection.Account.GetMappedModel(tc.model))
			selection.ReleaseFunc()
		})
	}
}

// 缺失模型目录的兼容来源不能吞掉其他供应商请求；明确的模型配置仍然有效。
func TestCompatibleProtocolGroupsRequireModelEvidence(t *testing.T) {
	for _, platform := range []string{PlatformKimi, PlatformZhipu, PlatformMiniMax, PlatformOpenCodeGo, PlatformDoubao, PlatformTraework, PlatformWorkbuddy, PlatformZcode} {
		t.Run(platform, func(t *testing.T) {
			account := &Account{Platform: platform, Type: AccountTypeAPIKey}
			require.True(t, account.IsMixedSchedulingEnabled())
			require.False(t, account.IsModelSupported("gpt-5.4"))
			account.Credentials = map[string]any{"model_mapping": map[string]any{"provider-model": "provider-model"}}
			require.True(t, account.IsModelSupported("provider-model"))
			require.False(t, account.IsModelSupported("gpt-5.4"))
			require.Equal(t, "provider-model", account.GetMappedModel("provider-model"))
		})
	}
}
