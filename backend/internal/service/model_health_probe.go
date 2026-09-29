package service

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// 每十五分钟最多探测十个到期模型；每个账号/模型仍遵守自己的周期间隔。
	modelHealthProbeLimit       = 10
	modelHealthProbeInterval    = 15 * time.Minute
	modelHealthProbeWorkers     = 3
	modelHealthProbeTimeout     = 45 * time.Second
	modelHealthProbeLogCategory = "upstream_model_health_probe"
)

type modelHealthProbeCandidate struct {
	AccountID int64
	Model     string
	CheckedAt *time.Time
}

func (s *UpstreamModelRefreshService) probeDueModels(parent context.Context, accounts []Account) {
	reader, ok := s.accountRepo.(AccountModelHealthStateReader)
	if !ok {
		return
	}
	states, err := reader.ListAccountModelHealthStates(parent)
	if err != nil {
		slog.Warn(modelHealthProbeLogCategory+"_state_failed", "error", err)
		return
	}
	candidates := collectModelHealthProbeCandidates(accounts, states, time.Now(), modelHealthProbeLimit)
	if len(candidates) == 0 {
		return
	}

	sem := make(chan struct{}, modelHealthProbeWorkers)
	var wg sync.WaitGroup
	for _, candidate := range candidates {
		select {
		case <-parent.Done():
			wg.Wait()
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(candidate modelHealthProbeCandidate) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(parent, modelHealthProbeTimeout)
			defer cancel()
			// Re-read settings after queuing so a just-disabled account is not
			// tested from the catalog refresh's older account snapshot.
			account, err := s.accountRepo.GetByID(ctx, candidate.AccountID)
			if err != nil || account == nil || !account.IsSchedulable() || !account.allowsAutomaticModelProbe(candidate.Model) {
				return
			}
			result, runErr := s.syncer.RunTestBackground(ctx, candidate.AccountID, candidate.Model)
			if runErr != nil {
				slog.Warn(modelHealthProbeLogCategory+"_failed", "account_id", candidate.AccountID, "model", candidate.Model, "error", runErr)
				return
			}
			if result.Status != "success" {
				slog.Info(modelHealthProbeLogCategory+"_unhealthy", "account_id", candidate.AccountID, "model", candidate.Model, "error", result.ErrorMessage)
				return
			}
			if err := s.recoverProbedModel(ctx, account, candidate.Model); err != nil {
				slog.Warn(modelHealthProbeLogCategory+"_recovery_failed", "account_id", candidate.AccountID, "model", candidate.Model, "error", err)
			}
			slog.Info(modelHealthProbeLogCategory+"_healthy", "account_id", candidate.AccountID, "model", candidate.Model, "latency_ms", result.LatencyMs)
		}(candidate)
	}
	wg.Wait()
}

func (s *UpstreamModelRefreshService) probeHealth(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, upstreamModelRefreshRunTimeout)
	defer cancel()
	accounts, err := s.accountRepo.ListActive(ctx)
	if err != nil {
		slog.Warn(modelHealthProbeLogCategory+"_accounts_failed", "error", err)
		return
	}
	s.probeDueModels(ctx, accounts)
}

func collectModelHealthProbeCandidates(
	accounts []Account,
	states []AccountModelHealthState,
	now time.Time,
	limit int,
) []modelHealthProbeCandidate {
	if limit <= 0 {
		return nil
	}
	accountsByID := make(map[int64]*Account, len(accounts))
	for i := range accounts {
		accountsByID[accounts[i].ID] = &accounts[i]
	}
	checkedByPair := make(map[string]*time.Time, len(states))
	for _, state := range states {
		model := observedUnsupportedModelKey(accountsByID[state.AccountID], state.Model)
		key := modelHealthProbeKey(state.AccountID, model)
		checked := latestModelHealthCheck(state)
		if checked != nil && (checkedByPair[key] == nil || checked.After(*checkedByPair[key])) {
			checkedByPair[key] = checked
		}
	}

	// 每个账号/模型单独到期，避免一个模型的流量推迟同账号其他模型的探测。
	selected := make([]modelHealthProbeCandidate, 0)
	seen := make(map[string]bool)
	for i := range accounts {
		account := &accounts[i]
		policy := account.ModelProbePolicy()
		if !account.IsSchedulable() || !policy.Enabled || account.IsSyntheticUITest() {
			continue
		}
		models := configuredUpstreamModelsForCapabilitySync(account)
		if account.IsOpenAIPassthroughEnabled() {
			models = nil
			for model := range account.GetModelMapping() {
				if !strings.Contains(model, "*") {
					models = append(models, model)
				}
			}
			sort.Strings(models)
		}
		if snapshot := account.GetUpstreamSupportedModelsSnapshot(); snapshot != nil {
			models = append(models, snapshot.Models...)
		}
		for _, model := range models {
			model = strings.TrimSpace(model)
			if !isTextModelHealthProbeCandidate(model) || !account.allowsAutomaticModelProbe(model) {
				continue
			}
			key := modelHealthProbeKey(account.ID, model)
			if seen[key] {
				continue
			}
			seen[key] = true
			checkedAt := checkedByPair[key]
			if checkedAt != nil && checkedAt.After(now.Add(-policy.Interval)) {
				continue
			}
			selected = append(selected, modelHealthProbeCandidate{AccountID: account.ID, Model: model, CheckedAt: checkedAt})
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		left, right := selected[i], selected[j]
		if left.CheckedAt == nil || right.CheckedAt == nil {
			if left.CheckedAt == nil && right.CheckedAt != nil {
				return true
			}
			if left.CheckedAt != nil && right.CheckedAt == nil {
				return false
			}
		} else if !left.CheckedAt.Equal(*right.CheckedAt) {
			return left.CheckedAt.Before(*right.CheckedAt)
		}
		if left.AccountID != right.AccountID {
			return left.AccountID < right.AccountID
		}
		return left.Model < right.Model
	})
	if len(selected) > limit {
		selected = selected[:limit]
	}
	return selected
}

func latestModelHealthCheck(state AccountModelHealthState) *time.Time {
	if state.LastSuccessAt == nil {
		return state.LastFailureAt
	}
	if state.LastFailureAt == nil || state.LastSuccessAt.After(*state.LastFailureAt) {
		return state.LastSuccessAt
	}
	return state.LastFailureAt
}

func modelHealthProbeKey(accountID int64, model string) string {
	return strconv.FormatInt(accountID, 10) + "\x00" + strings.ToLower(strings.TrimSpace(model))
}

func isTextModelHealthProbeCandidate(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || len(model) > unsupportedModelKeyMaxBytes {
		return false
	}
	for _, marker := range []string{
		"audio", "embed", "embedding", "image", "moderation", "realtime",
		"rerank", "reward", "speech", "stt", "transcri", "tts", "video",
	} {
		if strings.Contains(model, marker) {
			return false
		}
	}
	return true
}
