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

func (r *modelHealthAccountRepoStub) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
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

func TestGetAvailableModelsRequiresRecentHealthEvidence(t *testing.T) {
	groupID := int64(6)
	account := Account{
		ID:       820,
		Platform: PlatformOpenAI,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"gpt-healthy":    "gpt-healthy",
			"gpt-unverified": "gpt-unverified",
		}},
	}
	svc := &GatewayService{
		accountRepo: &modelHealthAccountRepoStub{accounts: []Account{account}},
		usageLogRepo: &modelHealthUsageRepoStub{observations: []ModelHealthObservation{
			{AccountID: 820, Model: "gpt-healthy", CheckedAt: time.Now()},
			{AccountID: 999, Model: "gpt-other-account", CheckedAt: time.Now()},
		}},
	}

	require.True(t, svc.ModelsRequireHealthCheck())
	require.Equal(t, []string{"gpt-healthy"}, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
}

func TestGetAvailableModelsFailsClosedWhenHealthEvidenceCannotBeRead(t *testing.T) {
	groupID := int64(6)
	svc := &GatewayService{
		accountRepo: &modelHealthAccountRepoStub{accounts: []Account{{
			ID:          820,
			Platform:    PlatformOpenAI,
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-configured": "gpt-configured"}},
		}}},
		usageLogRepo: &modelHealthUsageRepoStub{err: errors.New("health query failed")},
	}

	require.Empty(t, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
}

func TestGetAvailableModelsOpenAIPassthroughAdvertisesDefaultModelsWithoutHealthHistory(t *testing.T) {
	groupID := int64(6)
	svc := &GatewayService{
		accountRepo: &modelHealthAccountRepoStub{accounts: []Account{{
			ID:       820,
			Platform: PlatformOpenAI,
			Extra:    map[string]any{"openai_passthrough": true},
		}}},
		usageLogRepo: &modelHealthUsageRepoStub{err: errors.New("health query must be bypassed")},
	}

	models := svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI)

	require.Contains(t, models, "gpt-6-astra")
}

func TestGetAvailableModelsIncludesHistoricallyVerifiedUnusedModels(t *testing.T) {
	groupID := int64(6)
	account := Account{
		ID:       820,
		Platform: PlatformOpenAI,
	}
	svc := &GatewayService{
		accountRepo: &modelHealthAccountRepoStub{accounts: []Account{account}},
		usageLogRepo: &modelHealthUsageRepoStub{observations: []ModelHealthObservation{{
			AccountID: account.ID,
			Model:     "gpt-unused",
			CheckedAt: time.Now().AddDate(-1, 0, 0),
		}}},
	}

	require.Equal(t, []string{"gpt-unused"}, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
}

func TestGetAvailableModelsExcludesHistoricallyVerifiedModelsMissingFromFreshCatalog(t *testing.T) {
	groupID := int64(6)
	account := Account{
		ID:       820,
		Platform: PlatformOpenAI,
		Extra:    map[string]any{},
	}
	account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source:   "upstream",
		SyncedAt: time.Now().UTC().Format(time.RFC3339),
		Models:   []string{"gpt-current"},
	})
	svc := &GatewayService{
		accountRepo: &modelHealthAccountRepoStub{accounts: []Account{account}},
		usageLogRepo: &modelHealthUsageRepoStub{observations: []ModelHealthObservation{{
			AccountID: account.ID,
			Model:     "gpt-retired",
			CheckedAt: time.Now().AddDate(-1, 0, 0),
		}}},
	}

	require.Empty(t, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
}

func TestGetAvailableModelsReadsHealthFromBoundCompatibleSourcePlatforms(t *testing.T) {
	groupID := int64(7)
	openAI := Account{
		ID:       821,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"gpt-healthy": "gpt-healthy",
		}},
	}
	zcode := Account{ID: 822, Platform: PlatformZcode, Type: AccountTypeAPIKey}
	zcode.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source:   "upstream",
		SyncedAt: time.Now().UTC().Format(time.RFC3339),
		Models:   []string{"glm-5.3"},
	})
	unrelated := Account{
		ID:          823,
		Platform:    PlatformAnthropic,
		Credentials: map[string]any{"model_mapping": map[string]any{"claude-unrelated": "claude-opus-4-8"}},
	}
	usageRepo := &modelHealthUsageRepoStub{observationsByPlatform: map[string][]ModelHealthObservation{
		PlatformOpenAI: {{AccountID: openAI.ID, Model: "gpt-healthy", CheckedAt: time.Now()}},
		PlatformZcode:  {{AccountID: zcode.ID, Model: "glm-5.3", CheckedAt: time.Now()}},
		PlatformAnthropic: {{
			AccountID: unrelated.ID,
			Model:     "claude-unrelated",
			CheckedAt: time.Now(),
		}},
	}}
	svc := &GatewayService{
		accountRepo:  &modelHealthAccountRepoStub{accounts: []Account{openAI, zcode, unrelated}},
		usageLogRepo: usageRepo,
	}

	require.Equal(
		t,
		[]string{"glm-5.3", "gpt-healthy"},
		svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI),
	)
	require.Equal(t, []string{PlatformOpenAI, PlatformZcode}, usageRepo.platforms)
}
