package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type modelHealthAccountRepoStub struct {
	AccountRepository
	accounts []Account
}

func (r *modelHealthAccountRepoStub) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]Account, error) {
	return append([]Account(nil), r.accounts...), nil
}

type modelHealthUsageRepoStub struct {
	UsageLogRepository
	observations           []ModelHealthObservation
	observationsByPlatform map[string][]ModelHealthObservation
	err                    error
	errorsByPlatform       map[string]error
	platforms              []string
}

func (r *modelHealthUsageRepoStub) ListModelHealthObservations(_ context.Context, _ *int64, platform string) ([]ModelHealthObservation, error) {
	r.platforms = append(r.platforms, platform)
	if err := r.errorsByPlatform[platform]; err != nil {
		return nil, err
	}
	if r.observationsByPlatform != nil {
		return append([]ModelHealthObservation(nil), r.observationsByPlatform[platform]...), nil
	}
	return append([]ModelHealthObservation(nil), r.observations...), r.err
}

func TestGetAvailableModelsDoesNotDependOnHealthEvidence(t *testing.T) {
	groupID := int64(6)
	account := Account{
		ID: 856, Platform: PlatformOpenCodeGo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"space-bunny-free": "space-bunny-free", "glm-5.3": "glm-5.3",
		}},
	}
	for _, tc := range []struct {
		name         string
		observations []ModelHealthObservation
		err          error
	}{
		{name: "从未调用或测试"},
		{name: "部分模型测试成功", observations: []ModelHealthObservation{{AccountID: 856, Model: "glm-5.3", CheckedAt: time.Now()}}},
		{name: "历史记录不扩大配置范围", observations: []ModelHealthObservation{{AccountID: 856, Model: "retired-model"}, {AccountID: 999, Model: "other-group-model"}}},
		{name: "健康记录读取失败", err: errors.New("health query failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := &modelHealthUsageRepoStub{observations: tc.observations, err: tc.err}
			svc := &GatewayService{
				accountRepo:  &modelHealthAccountRepoStub{accounts: []Account{account}},
				usageLogRepo: usage,
			}
			require.Equal(t, []string{"glm-5.3", "space-bunny-free"}, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
			require.Empty(t, usage.platforms)
		})
	}
}

func TestGetAvailableModelsUsesUpstreamCatalogWithoutStaticDefaults(t *testing.T) {
	groupID := int64(6)
	account := Account{ID: 253, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source: "upstream", SyncedAt: time.Now().UTC().Format(time.RFC3339),
		Models: []string{"openrouter/free", "stealth/space-bunny-alpha"},
	})
	repo := &modelHealthAccountRepoStub{accounts: []Account{account}}
	usage := &modelHealthUsageRepoStub{observations: []ModelHealthObservation{{AccountID: 253, Model: "retired-model"}}}
	svc := &GatewayService{accountRepo: repo, usageLogRepo: usage}
	want := []string{"openrouter/free", "stealth/space-bunny-alpha"}
	require.Equal(t, want, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))

	codex := &OpenAIGatewayService{accountRepo: repo, usageLogRepo: usage}
	group := &Group{ID: groupID, Platform: PlatformOpenAI}
	manifest, configured, err := codex.BuildGroupConfiguredCodexModelsManifest(context.Background(), group, "")
	require.NoError(t, err)
	require.True(t, configured)
	require.Equal(t, want, codexManifestModelSlugs(t, manifest.Body))

	// 后续测试成功只更新健康状态，不改变目录成员或客户端 ETag。
	usage.observations = []ModelHealthObservation{{AccountID: 253, Model: "stealth/space-bunny-alpha", CheckedAt: time.Now()}}
	after, configured, err := codex.BuildGroupConfiguredCodexModelsManifest(context.Background(), group, manifest.ETag)
	require.NoError(t, err)
	require.True(t, configured)
	require.True(t, after.NotModified)
	require.Equal(t, want, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
	require.Empty(t, usage.platforms)
}

func TestGetAvailableModelsKeepsConfiguredModelsDuringAccountErrorsAndCooldowns(t *testing.T) {
	groupID := int64(6)
	resetAt := time.Now().Add(time.Hour)
	accounts := []Account{
		{ID: 856, Platform: PlatformOpenCodeGo, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			RateLimitResetAt: &resetAt, TempUnschedulableUntil: &resetAt,
			Credentials: map[string]any{"model_mapping": map[string]any{"space-bunny-free": "space-bunny-free"}}},
		{ID: 253, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusError, Schedulable: true,
			ErrorMessage: "Payment required (402): Insufficient credits",
			Credentials:  map[string]any{"model_mapping": map[string]any{"openrouter/free": "openrouter/free"}}},
		{ID: 999, Platform: PlatformOpenAI, Status: StatusDisabled, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"disabled-model": "disabled-model"}}},
	}
	for i := range accounts {
		require.False(t, accounts[i].IsSchedulable())
	}
	svc := &GatewayService{accountRepo: &modelHealthAccountRepoStub{accounts: accounts}}
	require.Equal(t, []string{"openrouter/free", "space-bunny-free"}, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
}

func TestGetAvailableModelsKeepsAccountMappingRestrictions(t *testing.T) {
	groupID := int64(6)
	account := Account{ID: 253, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Extra:       map[string]any{"openai_passthrough": true},
		Credentials: map[string]any{"model_mapping": map[string]any{"openrouter/free": "openrouter/free"}},
	}
	account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source: "upstream", SyncedAt: time.Now().UTC().Format(time.RFC3339),
		Models: []string{"openrouter/free", "unselected-paid-model"},
	})
	svc := &GatewayService{accountRepo: &modelHealthAccountRepoStub{accounts: []Account{account}}}
	require.Equal(t, []string{"openrouter/free"}, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
}
