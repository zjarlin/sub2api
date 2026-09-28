package service

import (
	"context"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// 模型按当前最佳账号得分排序，同模型内继续择优；同分保留管理员配置顺序。
// 只读取已有负载和运行样本，不为评分发送额外上游请求。
func (s *OpenAIGatewayService) rankVisionFallbackCandidates(ctx context.Context, primary *Account, state *visionFallbackState, candidates []visionFallbackCandidate) []visionFallbackCandidate {
	eligible := make([]visionFallbackCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if state.failed[visionHelperID{candidate.account.ID, candidate.model}] ||
			!isOpenAICompatibleAccountEligibleForRequestBeforeProfit(ctx, candidate.account, candidate.account.Platform, candidate.model, false, "") ||
			s.isOpenAIAccountRequestRuntimeBlocked(candidate.account, candidate.model) ||
			s.isOpenAIAccountBlockedBySchedulingThreshold(ctx, candidate.account) {
			continue
		}
		eligible = append(eligible, candidate)
	}
	scheduler, ok := s.getOpenAIAccountScheduler(ctx).(*defaultOpenAIAccountScheduler)
	if !ok || len(eligible) < 2 {
		return eligible
	}
	loadReq := make([]AccountWithConcurrency, 0, len(eligible))
	seen := make(map[int64]bool)
	for _, candidate := range eligible {
		account := candidate.account
		if seen[account.ID] {
			continue
		}
		seen[account.ID] = true
		loadReq = append(loadReq, AccountWithConcurrency{ID: account.ID, MaxConcurrency: account.EffectiveLoadFactor()})
	}
	var loadMap map[int64]*AccountLoadInfo
	if s.concurrencyService != nil {
		// 负载读取失败时与主调度一致，继续使用运行样本和静态因子；真正调用仍须抢槽。
		var err error
		loadMap, err = s.concurrencyService.GetAccountsLoadBatch(ctx, loadReq)
		if err != nil {
			logger.FromContext(ctx).Warn("gateway.vision_helper_load_unavailable", zap.Error(err))
		}
	}
	plan := openAIAccountLoadPlan{candidates: make([]openAIAccountCandidateScore, 0, len(eligible))}
	for _, candidate := range eligible {
		load, known := loadMap[candidate.account.ID]
		if load == nil {
			load = &AccountLoadInfo{AccountID: candidate.account.ID}
			known = false
		}
		// HTTP 主账号已持有的槽可串行复用，不把该槽误算为助手的竞争负载。
		if known && primary != nil && candidate.account.ID == primary.ID && ctx.Value(visionFallbackPrimarySlotRequiredKey{}) != true {
			adjusted := *load
			adjusted.CurrentConcurrency = max(0, adjusted.CurrentConcurrency-1)
			adjusted.LoadRate = adjusted.CurrentConcurrency * 100 / max(1, candidate.account.EffectiveLoadFactor())
			load = &adjusted
		}
		model := canonicalOpenAIAccountSchedulingModel(candidate.account, candidate.model)
		errorRate, ttft, hasTTFT, samples := scheduler.stats.snapshotModel(candidate.account.ID, model)
		plan.candidates = append(plan.candidates, openAIAccountCandidateScore{
			account: candidate.account, loadInfo: load, loadKnown: known,
			errorRate: errorRate, ttft: ttft, hasTTFT: hasTTFT, samples: samples,
		})
	}
	scheduler.scoreOpenAIAccountLoadPlan(ctx, OpenAIAccountScheduleRequest{UseUpstreamTokenCost: true}, &plan)
	scores := make(map[visionHelperID]float64, len(eligible))
	modelScores := make(map[string]float64)
	modelOrder := make(map[string]int)
	listed := make(map[string]bool)
	for i, model := range state.aliases.CanonicalIDs(state.policy.Models) {
		listed[model] = true
		modelOrder[model] = i
	}
	for i, candidate := range eligible {
		score := plan.candidates[i].score
		scores[visionHelperID{candidate.account.ID, candidate.model}] = score
		model := state.aliases.Canonicalize(candidate.model)
		best, exists := modelScores[model]
		if !exists {
			if _, configured := modelOrder[model]; !configured {
				modelOrder[model] = len(modelOrder)
			}
		}
		if !exists || score > best {
			modelScores[model] = score
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		left, right := eligible[i], eligible[j]
		leftModel, rightModel := state.aliases.Canonicalize(left.model), state.aliases.Canonicalize(right.model)
		if listed[leftModel] != listed[rightModel] {
			return listed[leftModel]
		}
		if modelScores[leftModel] != modelScores[rightModel] {
			return modelScores[leftModel] > modelScores[rightModel]
		}
		if leftModel != rightModel {
			return modelOrder[leftModel] < modelOrder[rightModel]
		}
		leftScore, rightScore := scores[visionHelperID{left.account.ID, left.model}], scores[visionHelperID{right.account.ID, right.model}]
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if left.account.Priority != right.account.Priority {
			return left.account.Priority < right.account.Priority
		}
		return left.account.ID < right.account.ID
	})
	return eligible
}
