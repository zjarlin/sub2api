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
	observations []ModelHealthObservation
	err          error
}

func (r *modelHealthUsageRepoStub) ListRecentModelHealthObservations(context.Context, *int64, string, time.Time) ([]ModelHealthObservation, error) {
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
