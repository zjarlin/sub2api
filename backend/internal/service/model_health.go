package service

import (
	"context"
	"strings"
	"time"
)

const modelHealthFreshness = 6 * time.Hour

// ModelHealthObservation records a real successful request or scheduled
// connectivity test for one account and public model ID.
type ModelHealthObservation struct {
	AccountID int64
	Model     string
	CheckedAt time.Time
}

// ModelHealthObservationReader supplies durable model-level health evidence.
// The usage repository implements this without expanding UsageLogRepository's
// broad interface and its test doubles.
type ModelHealthObservationReader interface {
	ListRecentModelHealthObservations(ctx context.Context, groupID *int64, platform string, since time.Time) ([]ModelHealthObservation, error)
}

func (s *GatewayService) ModelsRequireHealthCheck() bool {
	if s == nil || s.usageLogRepo == nil {
		return false
	}
	_, ok := s.usageLogRepo.(ModelHealthObservationReader)
	return ok
}

func recentHealthCheckedModels(
	ctx context.Context,
	usageLogRepo UsageLogRepository,
	groupID *int64,
	platform string,
	accounts []Account,
) ([]string, bool) {
	reader, ok := usageLogRepo.(ModelHealthObservationReader)
	if !ok {
		return nil, false
	}
	observations, err := reader.ListRecentModelHealthObservations(
		ctx,
		groupID,
		platform,
		time.Now().Add(-modelHealthFreshness),
	)
	if err != nil {
		return []string{}, true
	}

	accountsByID := make(map[int64]*Account, len(accounts))
	for i := range accounts {
		accountsByID[accounts[i].ID] = &accounts[i]
	}
	models := make(map[string]struct{}, len(observations))
	for _, observation := range observations {
		account := accountsByID[observation.AccountID]
		model := strings.TrimSpace(observation.Model)
		if account == nil || model == "" || len(model) > unsupportedModelKeyMaxBytes || !account.IsModelSupported(model) {
			continue
		}
		models[model] = struct{}{}
	}
	out := make([]string, 0, len(models))
	for model := range models {
		out = append(out, model)
	}
	return out, true
}

func (s *GatewayService) healthCheckedModels(
	ctx context.Context,
	groupID *int64,
	platform string,
	accounts []Account,
) ([]string, bool) {
	return recentHealthCheckedModels(ctx, s.usageLogRepo, groupID, platform, accounts)
}
