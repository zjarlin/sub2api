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
	modelHealthProbeLimit       = 10
	modelHealthProbeWorkers     = 3
	modelHealthProbeTimeout     = 45 * time.Second
	modelHealthProbeFreshness   = 24 * time.Hour
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
			result, runErr := s.syncer.RunTestBackground(ctx, candidate.AccountID, candidate.Model)
			if runErr != nil {
				slog.Warn(modelHealthProbeLogCategory+"_failed", "account_id", candidate.AccountID, "model", candidate.Model, "error", runErr)
				return
			}
			if result.Status != "success" {
				slog.Info(modelHealthProbeLogCategory+"_unhealthy", "account_id", candidate.AccountID, "model", candidate.Model, "error", result.ErrorMessage)
				return
			}
			slog.Info(modelHealthProbeLogCategory+"_healthy", "account_id", candidate.AccountID, "model", candidate.Model, "latency_ms", result.LatencyMs)
		}(candidate)
	}
	wg.Wait()
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
	stateByPair := make(map[string]AccountModelHealthState, len(states))
	for _, state := range states {
		stateByPair[modelHealthProbeKey(state.AccountID, state.Model)] = state
	}

	buckets := make([][]modelHealthProbeCandidate, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if !account.IsSchedulable() {
			continue
		}
		snapshot := account.GetUpstreamSupportedModelsSnapshot()
		if snapshot == nil {
			continue
		}
		bucket := make([]modelHealthProbeCandidate, 0, len(snapshot.Models))
		for _, model := range snapshot.Models {
			model = strings.TrimSpace(model)
			if !isTextModelHealthProbeCandidate(model) {
				continue
			}
			state, exists := stateByPair[modelHealthProbeKey(account.ID, model)]
			checkedAt := latestModelHealthCheck(state)
			if exists && checkedAt != nil && checkedAt.After(now.Add(-modelHealthProbeFreshness)) {
				continue
			}
			bucket = append(bucket, modelHealthProbeCandidate{AccountID: account.ID, Model: model, CheckedAt: checkedAt})
		}
		sort.Slice(bucket, func(i, j int) bool {
			left, right := bucket[i], bucket[j]
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
			return left.Model < right.Model
		})
		if len(bucket) > 0 {
			buckets = append(buckets, bucket)
		}
	}

	selected := make([]modelHealthProbeCandidate, 0, limit)
	for round := 0; len(selected) < limit; round++ {
		added := false
		for _, bucket := range buckets {
			if round >= len(bucket) {
				continue
			}
			selected = append(selected, bucket[round])
			added = true
			if len(selected) == limit {
				break
			}
		}
		if !added {
			break
		}
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
