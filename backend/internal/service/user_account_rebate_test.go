package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeRebateRepo 记录结算调用，供服务层测试断言窗口推进与结果汇总。
type fakeRebateRepo struct {
	candidates []AccountRebateCandidate
	settleFn   func(in AccountRebateSettleInput) (*AccountRebateSettlement, error)
	calls      []AccountRebateSettleInput
}

func (f *fakeRebateRepo) ListCandidateAccounts(_ context.Context, _ bool, _ int) ([]AccountRebateCandidate, error) {
	return f.candidates, nil
}

func (f *fakeRebateRepo) SettleAccount(_ context.Context, in AccountRebateSettleInput) (*AccountRebateSettlement, error) {
	f.calls = append(f.calls, in)
	if f.settleFn != nil {
		return f.settleFn(in)
	}
	return &AccountRebateSettlement{AccountID: in.AccountID, OwnerUserID: in.OwnerUserID, Settled: true}, nil
}

func (f *fakeRebateRepo) ListRebateHistory(context.Context, int64, int, int) ([]AccountRebateRecord, int64, error) {
	return nil, 0, nil
}

func (f *fakeRebateRepo) ListAllRebateRecords(context.Context, string, int, int) ([]AccountRebateRecord, int64, error) {
	return nil, 0, nil
}

func (f *fakeRebateRepo) GetRebateSummary(context.Context, int64) (*AccountRebateSummary, error) {
	return nil, nil
}

// newRebateTestSettingService 复用同包已有的 fakeSettingRepo（openai_cyber_session_block_test.go）。
func newRebateTestSettingService(values map[string]string) *SettingService {
	return &SettingService{settingRepo: &fakeSettingRepo{vals: values}}
}

func newRebateTestService(t *testing.T, repo UserAccountRebateRepository, values map[string]string) *UserAccountRebateService {
	t.Helper()
	svc := NewUserAccountRebateService(repo, newRebateTestSettingService(values), nil, nil, nil, 0, 10*time.Minute, 100)
	require.NotNil(t, svc)
	return svc
}

// 总开关关闭时整轮跳过，且不触碰任何账号。
func TestUserAccountRebateRunOnceDisabled(t *testing.T) {
	repo := &fakeRebateRepo{candidates: []AccountRebateCandidate{{AccountID: 1, OwnerUserID: 7}}}
	svc := newRebateTestService(t, repo, map[string]string{
		SettingKeyUserAccountRebateEnabled: "false",
	})

	result, err := svc.RunOnce(context.Background())
	require.NoError(t, err)
	require.True(t, result.Skipped)
	require.Equal(t, "disabled", result.SkipReason)
	require.Empty(t, repo.calls)
}

// 开启后每个候选账号都会被结算，且窗口右边界滞后于当前时间。
func TestUserAccountRebateRunOnceSettlesCandidates(t *testing.T) {
	repo := &fakeRebateRepo{candidates: []AccountRebateCandidate{
		{AccountID: 1, OwnerUserID: 7},
		{AccountID: 2, OwnerUserID: 8},
	}}
	svc := newRebateTestService(t, repo, map[string]string{
		SettingKeyUserAccountRebateEnabled:    "true",
		SettingKeyUserAccountRebateRate:       "100",
		SettingKeyUserAccountRebateSharedOnly: "true",
	})

	before := time.Now().Add(-10 * time.Minute)
	result, err := svc.RunOnce(context.Background())
	require.NoError(t, err)
	require.False(t, result.Skipped)
	require.Equal(t, 2, result.Accounts)
	require.Equal(t, 100.0, result.RatePercent)
	require.Len(t, repo.calls, 2)
	for _, call := range repo.calls {
		require.True(t, call.WindowTo.After(before))
		require.True(t, call.WindowTo.Before(time.Now()))
		require.Equal(t, 100.0, call.RatePercent)
	}
}

// 返额比例被 100% 硬上限夹取，避免比例溢出导致平台返出超过实收。
func TestUserAccountRebateRateClamped(t *testing.T) {
	repo := &fakeRebateRepo{candidates: []AccountRebateCandidate{{AccountID: 1, OwnerUserID: 7}}}
	svc := newRebateTestService(t, repo, map[string]string{
		SettingKeyUserAccountRebateEnabled: "true",
		SettingKeyUserAccountRebateRate:    "500",
	})

	result, err := svc.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, AccountRebateRateMax, result.RatePercent)
	require.Equal(t, AccountRebateRateMax, repo.calls[0].RatePercent)
}

// 号主自用默认不计入返额。
func TestUserAccountRebateExcludesOwnerUsageByDefault(t *testing.T) {
	repo := &fakeRebateRepo{candidates: []AccountRebateCandidate{{AccountID: 1, OwnerUserID: 7}}}
	svc := newRebateTestService(t, repo, map[string]string{
		SettingKeyUserAccountRebateEnabled: "true",
	})

	_, err := svc.RunOnce(context.Background())
	require.NoError(t, err)
	require.False(t, repo.calls[0].IncludeOwnerUsage)
}

// 管理员显式开启后把号主自用也计入。
func TestUserAccountRebateIncludesOwnerUsageWhenEnabled(t *testing.T) {
	repo := &fakeRebateRepo{candidates: []AccountRebateCandidate{{AccountID: 1, OwnerUserID: 7}}}
	svc := newRebateTestService(t, repo, map[string]string{
		SettingKeyUserAccountRebateEnabled:      "true",
		SettingKeyUserAccountRebateIncludeOwner: "true",
	})

	_, err := svc.RunOnce(context.Background())
	require.NoError(t, err)
	require.True(t, repo.calls[0].IncludeOwnerUsage)
}

// 单个账号结算失败不应中断整轮，只计入错误数并继续。
func TestUserAccountRebateContinuesAfterSettleError(t *testing.T) {
	repo := &fakeRebateRepo{
		candidates: []AccountRebateCandidate{
			{AccountID: 1, OwnerUserID: 7},
			{AccountID: 2, OwnerUserID: 8},
		},
		settleFn: func(in AccountRebateSettleInput) (*AccountRebateSettlement, error) {
			if in.AccountID == 1 {
				return nil, errors.New("boom")
			}
			return &AccountRebateSettlement{AccountID: in.AccountID, Settled: true, RebateAmount: 1.5}, nil
		},
	}
	svc := newRebateTestService(t, repo, map[string]string{
		SettingKeyUserAccountRebateEnabled: "true",
	})

	result, err := svc.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Errors)
	require.Equal(t, 1, result.Settled)
	require.Equal(t, 1, result.Credited)
	require.InDelta(t, 1.5, result.TotalRebate, 1e-9)
}

// 比例为 0 时不产生任何返额，但仍推进结算游标。
func TestUserAccountRebateZeroRateProducesNoCredit(t *testing.T) {
	repo := &fakeRebateRepo{candidates: []AccountRebateCandidate{{AccountID: 1, OwnerUserID: 7}}}
	svc := newRebateTestService(t, repo, map[string]string{
		SettingKeyUserAccountRebateEnabled: "true",
		SettingKeyUserAccountRebateRate:    "0",
	})

	result, err := svc.RunOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, result.TotalRebate)
	require.Zero(t, result.Credited)
	require.NotEmpty(t, repo.calls)
}

func TestClampUserAccountRebateRate(t *testing.T) {
	require.Equal(t, AccountRebateRateMin, ClampUserAccountRebateRate(-1))
	require.Equal(t, AccountRebateRateMax, ClampUserAccountRebateRate(1000))
	require.Equal(t, 42.5, ClampUserAccountRebateRate(42.5))
}

func TestUserAccountRebatePaginationBounds(t *testing.T) {
	page, size := normalizeRebatePagination(0, 0)
	require.Equal(t, 1, page)
	require.Equal(t, 20, size)

	page, size = normalizeRebatePagination(3, 9999)
	require.Equal(t, 3, page)
	require.Equal(t, 200, size)
}
