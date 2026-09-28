package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func visionScoringService(t *testing.T) *OpenAIGatewayService {
	t.Helper()
	cfg := visionTestConfig()
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights = config.GatewayOpenAIWSSchedulerScoreWeights{ErrorRate: 1, TTFT: 1, Load: 1}
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	return &OpenAIGatewayService{cfg: cfg, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService("true")}
}

func TestVisionFallbackScoringRanksModelsAndAccounts(t *testing.T) {
	svc := visionScoringService(t)
	scheduler := svc.getOpenAIAccountScheduler(context.Background())
	slow := visionTestAccount(1, "preferred", "text", "image")
	bad := visionTestAccount(2, "backup", "text", "image")
	good := visionTestAccount(3, "backup", "text", "image")
	fastMs, slowMs := 100, 5000
	for i := 0; i < 8; i++ {
		scheduler.ReportResult(slow.ID, slow.Name, false, &slowMs)
		scheduler.ReportResult(bad.ID, bad.Name, false, &slowMs)
		scheduler.ReportResult(good.ID, good.Name, true, &fastMs)
	}
	state := &visionFallbackState{policy: &VisionFallbackPolicy{Enabled: true, Models: []string{"preferred", "backup"}}, failed: map[visionHelperID]bool{}}
	candidates := visionFallbackCandidatesWithPolicy([]Account{slow, bad, good}, state.policy, nil, nil)
	ranked := svc.rankVisionFallbackCandidates(context.Background(), nil, state, candidates)
	require.Equal(t, int64(3), ranked[0].account.ID)
	require.Equal(t, "backup", ranked[1].model)
	require.Equal(t, "preferred", ranked[2].model)
	// 删除当前最佳账号后，重新从现存账号选择，配置中不持有账号 ID。
	candidates = visionFallbackCandidatesWithPolicy([]Account{slow, bad}, state.policy, nil, nil)
	ranked = svc.rankVisionFallbackCandidates(context.Background(), nil, state, candidates)
	require.Equal(t, int64(1), ranked[0].account.ID)
	state.failed[visionHelperID{1, "preferred"}] = true
	ranked = svc.rankVisionFallbackCandidates(context.Background(), nil, state, candidates)
	require.Len(t, ranked, 1)
	require.Equal(t, int64(2), ranked[0].account.ID)
}

func TestVisionFallbackScoringAliasesAndUnlistedBoundary(t *testing.T) {
	svc := visionScoringService(t)
	scheduler := svc.getOpenAIAccountScheduler(context.Background())
	account := visionTestAccount(1, "alias", "text", "image")
	account.Credentials["model_mapping"] = map[string]any{"alias": "upstream", "other": "other"}
	unlisted := visionTestAccount(2, "unlisted", "text", "image")
	for i := 0; i < 8; i++ {
		scheduler.ReportResult(1, "upstream", false, nil)
		scheduler.ReportResult(1, "other", true, nil)
		scheduler.ReportResult(2, "unlisted", true, nil)
	}
	state := &visionFallbackState{
		policy:  &VisionFallbackPolicy{Enabled: true, Models: []string{"canonical", "other"}, AllowUnlistedModels: true},
		aliases: &ModelAliasPolicy{Groups: []ModelAliasGroup{{Canonical: "canonical", Aliases: []string{"alias"}}}},
		failed:  map[visionHelperID]bool{},
	}
	candidates := []visionFallbackCandidate{{account: &account, model: "alias"}, {account: &account, model: "other"}, {account: &unlisted, model: "unlisted"}}
	ranked := svc.rankVisionFallbackCandidates(context.Background(), nil, state, candidates)
	require.Equal(t, "other", ranked[0].model)
	require.Equal(t, "alias", ranked[1].model)
	require.Equal(t, "unlisted", ranked[2].model)
}

type visionScoringLoadCache struct {
	ConcurrencyCache
	loads map[int64]*AccountLoadInfo
}

func (c visionScoringLoadCache) GetAccountsLoadBatch(context.Context, []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	return c.loads, nil
}

func TestVisionFallbackScoringUsesLoadAndReusesPrimarySlot(t *testing.T) {
	svc := visionScoringService(t)
	first := visionTestAccount(1, "first", "text", "image")
	second := visionTestAccount(2, "second", "text", "image")
	loads := map[int64]*AccountLoadInfo{1: {AccountID: 1, CurrentConcurrency: 1, LoadRate: 100}, 2: {AccountID: 2}}
	svc.concurrencyService = NewConcurrencyService(visionScoringLoadCache{loads: loads})
	state := &visionFallbackState{policy: &VisionFallbackPolicy{Enabled: true, Models: []string{"first", "second"}}, failed: map[visionHelperID]bool{}}
	candidates := []visionFallbackCandidate{{account: &first, model: "first"}, {account: &second, model: "second"}}
	ranked := svc.rankVisionFallbackCandidates(context.Background(), nil, state, candidates)
	require.Equal(t, int64(2), ranked[0].account.ID)
	ranked = svc.rankVisionFallbackCandidates(context.Background(), &first, state, candidates)
	require.Equal(t, int64(1), ranked[0].account.ID)
	require.Equal(t, 100, loads[1].LoadRate)
	ctx := context.WithValue(context.Background(), visionFallbackPrimarySlotRequiredKey{}, true)
	ranked = svc.rankVisionFallbackCandidates(ctx, &first, state, candidates)
	require.Equal(t, int64(2), ranked[0].account.ID)
}

func TestVisionFallbackScoringDisabledPreservesConfiguredOrder(t *testing.T) {
	svc := visionScoringService(t)
	scheduler := svc.getOpenAIAccountScheduler(context.Background())
	first := visionTestAccount(1, "first", "text", "image")
	second := visionTestAccount(2, "second", "text", "image")
	for i := 0; i < 8; i++ {
		scheduler.ReportResult(1, "first", false, nil)
		scheduler.ReportResult(2, "second", true, nil)
	}
	svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("false")
	state := &visionFallbackState{policy: &VisionFallbackPolicy{Enabled: true, Models: []string{"first", "second"}}}
	candidates := []visionFallbackCandidate{{account: &first, model: "first"}, {account: &second, model: "second"}}
	require.Equal(t, candidates, svc.rankVisionFallbackCandidates(context.Background(), nil, state, candidates))
}

func TestVisionFallbackScoringLearnsFromRealCallsWithoutFanout(t *testing.T) {
	svc := visionScoringService(t)
	primary := visionTestAccount(1, "text-model", "text")
	bad := visionTestAccount(2, "bad", "text", "image")
	good := visionTestAccount(3, "good", "text", "image")
	good.Credentials["model_mapping"] = map[string]any{"good": "upstream-good"}
	good.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
		"upstream-good": {ID: "upstream-good", InputModalities: []string{"text", "image"}},
	}})
	svc.accountRepo = &countingCodexModelsAccountRepo{accounts: []Account{bad, good}}
	var calls []int64
	svc.httpUpstream = &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
		calls = append(calls, accountID)
		if accountID == bad.ID {
			return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"temporarily unavailable"}}`))}, nil
		}
		return visionTestResponse("upstream-good", "识别成功"), nil
	}}
	for turn := int64(0); turn < 2; turn++ {
		body := []byte(visionTestInput)
		c, _ := visionTestContext(body, 9+turn, 7)
		_, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
		require.NoError(t, err)
	}
	// 第二次使用不同 API Key 避免命中描述缓存；历史成功模型提前，失败模型不再消耗调用。
	require.Equal(t, []int64{2, 3, 3}, calls)
	_, _, _, samples := svc.openaiAccountStats.snapshotModel(good.ID, "upstream-good")
	require.Equal(t, int64(2), samples)
	_, wrongKey := svc.openaiAccountStats.models.Load(openAIAccountModelRuntimeKey{accountID: good.ID, model: good.Name})
	require.False(t, wrongKey)
}
