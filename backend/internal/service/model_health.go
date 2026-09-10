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
	now := time.Now()
	models := make(map[string]struct{})
	for i := range accounts {
		addFreshAccountCatalogModels(models, &accounts[i], now)
	}
	observations, err := reader.ListRecentModelHealthObservations(
		ctx,
		groupID,
		platform,
		now.Add(-modelHealthFreshness),
	)

	accountsByID := make(map[int64]*Account, len(accounts))
	for i := range accounts {
		accountsByID[accounts[i].ID] = &accounts[i]
	}
	if err == nil {
		for _, observation := range observations {
			addHealthCheckedModel(models, accountsByID[observation.AccountID], observation.Model)
		}
	}
	out := make([]string, 0, len(models))
	for model := range models {
		out = append(out, model)
	}
	return out, true
}

func addFreshAccountCatalogModels(models map[string]struct{}, account *Account, now time.Time) {
	snapshot := account.GetUpstreamSupportedModelsSnapshot()
	if !upstreamSupportedModelsSnapshotFresh(snapshot, now) {
		return
	}
	mapping := account.GetModelMapping()
	for _, model := range snapshot.Models {
		model = strings.TrimSpace(model)
		if len(mapping) == 0 || account.IsOpenAIPassthroughEnabled() || mappingSupportsRequestedModel(mapping, model) {
			addHealthCheckedModel(models, account, model)
		}
	}
	for publicModel := range mapping {
		if strings.Contains(publicModel, "*") {
			continue
		}
		addHealthCheckedModel(models, account, publicModel)
	}
}

func addHealthCheckedModel(models map[string]struct{}, account *Account, model string) {
	model = strings.TrimSpace(model)
	if account == nil || model == "" || len(model) > unsupportedModelKeyMaxBytes || !account.IsModelSupported(model) {
		return
	}
	models[model] = struct{}{}
}

func (s *GatewayService) healthCheckedModels(
	ctx context.Context,
	groupID *int64,
	platform string,
	accounts []Account,
) ([]string, bool) {
	return recentHealthCheckedModels(ctx, s.usageLogRepo, groupID, platform, accounts)
}
