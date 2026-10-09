package service

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// 用户自带账号返额：号主通过「我的账号」贡献的上游账号被平台真实消耗掉的用量，
// 按配置比例折算成站内余额返还给号主。
//
// 结算基数为「倍率前成本」与「用户实付」的较小值，因此返额恒不超过平台实收：
// 号主无法通过调高账号计费倍率（rate_multiplier 由号主可写）放大返额，
// 也无法通过开小号互相刷量套返额。

const (
	// AccountRebateRateDefault 默认返额比例（百分比）。100% = 等价交换。
	AccountRebateRateDefault = 100.0
	// AccountRebateRateMin / AccountRebateRateMax 返额比例上下界。
	AccountRebateRateMin = 0.0
	AccountRebateRateMax = 100.0

	accountRebateCandidateLimitDefault = 200
	accountRebateCandidateLimitMax     = 2000
)

// ClampUserAccountRebateRate 对外暴露的返额比例夹取，供 handler 层复用同一套边界，
// 避免管理端与后台解析对 100% 上限产生分歧。
func ClampUserAccountRebateRate(value float64) float64 {
	return clampUserAccountRebateRate(value)
}

// AccountRebateCandidate 参与返额结算的自带账号。
type AccountRebateCandidate struct {
	AccountID   int64
	OwnerUserID int64
}

// AccountRebateSettleInput 单账号单窗口结算输入。
type AccountRebateSettleInput struct {
	AccountID         int64
	OwnerUserID       int64
	WindowTo          time.Time
	RatePercent       float64
	IncludeOwnerUsage bool
}

// AccountRebateSettlement 单账号单窗口结算结果。
type AccountRebateSettlement struct {
	AccountID     int64
	OwnerUserID   int64
	WindowFrom    time.Time
	WindowTo      time.Time
	Requests      int64
	ConsumedBasis float64
	RatePercent   float64
	RebateAmount  float64
	BalanceAfter  *float64
	Settled       bool
	Skipped       bool
}

// AccountRebateRecord 返额流水（展示用）。
type AccountRebateRecord struct {
	ID            int64     `json:"id"`
	AccountID     int64     `json:"account_id"`
	AccountName   string    `json:"account_name"`
	WindowFrom    time.Time `json:"window_from"`
	WindowTo      time.Time `json:"window_to"`
	Requests      int64     `json:"requests"`
	ConsumedBasis float64   `json:"consumed_basis"`
	RatePercent   float64   `json:"rate_percent"`
	RebateAmount  float64   `json:"rebate_amount"`
	BalanceAfter  *float64  `json:"balance_after,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	OwnerEmail    string    `json:"owner_email,omitempty"`
	OwnerUsername string    `json:"owner_username,omitempty"`
	OwnerUserID   int64     `json:"owner_user_id,omitempty"`
}

// AccountRebateSummary 号主返额概览。
type AccountRebateSummary struct {
	OwnerUserID        int64   `json:"owner_user_id"`
	TotalRebated       float64 `json:"total_rebated"`
	TotalConsumedBasis float64 `json:"total_consumed_basis"`
	SettleCount        int64   `json:"settle_count"`
	OwnedAccountCount  int64   `json:"owned_account_count"`
}

// UserAccountRebateRepository 返额结算与流水的持久化接口。
type UserAccountRebateRepository interface {
	ListCandidateAccounts(ctx context.Context, requireShared bool, limit int) ([]AccountRebateCandidate, error)
	SettleAccount(ctx context.Context, in AccountRebateSettleInput) (*AccountRebateSettlement, error)
	ListRebateHistory(ctx context.Context, ownerUserID int64, limit, offset int) ([]AccountRebateRecord, int64, error)
	ListAllRebateRecords(ctx context.Context, search string, limit, offset int) ([]AccountRebateRecord, int64, error)
	GetRebateSummary(ctx context.Context, ownerUserID int64) (*AccountRebateSummary, error)
}

// UserAccountRebateService 周期结算自带账号返额，并提供用户/管理员查询接口。
type UserAccountRebateService struct {
	repo         UserAccountRebateRepository
	setting      *SettingService
	billingCache *BillingCacheService
	authCache    APIKeyAuthCacheInvalidator
	cfg          *config.Config

	interval   time.Duration
	safetyLag  time.Duration
	batchLimit int

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewUserAccountRebateService 构造返额结算服务。
// interval <= 0 时 Start() 直接返回（不启动），便于通过配置关闭。
func NewUserAccountRebateService(
	repo UserAccountRebateRepository,
	setting *SettingService,
	billingCache *BillingCacheService,
	authCache APIKeyAuthCacheInvalidator,
	cfg *config.Config,
	interval time.Duration,
	safetyLag time.Duration,
	batchLimit int,
) *UserAccountRebateService {
	if batchLimit <= 0 {
		batchLimit = accountRebateCandidateLimitDefault
	}
	if batchLimit > accountRebateCandidateLimitMax {
		batchLimit = accountRebateCandidateLimitMax
	}
	return &UserAccountRebateService{
		repo:         repo,
		setting:      setting,
		billingCache: billingCache,
		authCache:    authCache,
		cfg:          cfg,
		interval:     interval,
		safetyLag:    safetyLag,
		batchLimit:   batchLimit,
		stopCh:       make(chan struct{}),
	}
}

// Start 启动周期结算。总开关关闭、间隔非正或依赖缺失时都不启动。
func (s *UserAccountRebateService) Start() {
	if s == nil || s.repo == nil || s.setting == nil || s.cfg == nil {
		return
	}
	if s.interval <= 0 {
		return
	}
	logger.LegacyPrintf("service.account_rebate", "started (interval=%s lag=%s batch=%d)", s.interval, s.safetyLag, s.batchLimit)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		// 启动后先等一个周期，避开进程启动峰。
		for {
			select {
			case <-ticker.C:
				s.runOnce(context.Background())
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop 停止周期结算。
func (s *UserAccountRebateService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

// RunOnce 立刻执行一轮结算（管理端手动触发）。
func (s *UserAccountRebateService) RunOnce(ctx context.Context) (*AccountRebateRunResult, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	return s.runOnce(ctx)
}

// AccountRebateRunResult 一轮结算的汇总结果。
type AccountRebateRunResult struct {
	Accounts    int       `json:"accounts"`
	Settled     int       `json:"settled"`
	Credited    int       `json:"credited"`
	TotalRebate float64   `json:"total_rebate"`
	Errors      int       `json:"errors"`
	Skipped     bool      `json:"skipped"`
	SkipReason  string    `json:"skip_reason,omitempty"`
	RatePercent float64   `json:"rate_percent"`
	WindowTo    time.Time `json:"window_to"`
}

// runOnce 结算一批候选账号。总开关关闭时返回 Skipped 结果而不是报错。
func (s *UserAccountRebateService) runOnce(ctx context.Context) (*AccountRebateRunResult, error) {
	enabled, ratePercent, requireShared, includeOwner := s.policy(ctx)
	result := &AccountRebateRunResult{RatePercent: ratePercent}
	if !enabled {
		result.Skipped = true
		result.SkipReason = "disabled"
		return result, nil
	}

	windowTo := time.Now().Add(-s.safetyLag)
	result.WindowTo = windowTo

	candidates, err := s.repo.ListCandidateAccounts(ctx, requireShared, s.batchLimit)
	if err != nil {
		return result, err
	}
	result.Accounts = len(candidates)

	for _, candidate := range candidates {
		settlement, err := s.repo.SettleAccount(ctx, AccountRebateSettleInput{
			AccountID:         candidate.AccountID,
			OwnerUserID:       candidate.OwnerUserID,
			WindowTo:          windowTo,
			RatePercent:       ratePercent,
			IncludeOwnerUsage: includeOwner,
		})
		if err != nil {
			result.Errors++
			logger.LegacyPrintf("service.account_rebate", "settle failed account_id=%d err=%v", candidate.AccountID, err)
			continue
		}
		if settlement == nil || settlement.Skipped {
			continue
		}
		result.Settled++
		if settlement.RebateAmount <= 0 {
			continue
		}
		result.Credited++
		result.TotalRebate += settlement.RebateAmount
		s.invalidateOwnerCaches(ctx, settlement.OwnerUserID)
	}
	if result.TotalRebate > 0 {
		logger.LegacyPrintf("service.account_rebate",
			"settled accounts=%d credited=%d rebate=%.8f rate=%.2f%%",
			result.Settled, result.Credited, result.TotalRebate, ratePercent)
	}
	return result, nil
}

// invalidateOwnerCaches 返额入账后失效余额相关缓存，否则用户面板仍显示旧余额。
func (s *UserAccountRebateService) invalidateOwnerCaches(ctx context.Context, ownerUserID int64) {
	if ownerUserID <= 0 {
		return
	}
	if s.authCache != nil {
		s.authCache.InvalidateAuthCacheByUserID(ctx, ownerUserID)
	}
	if s.billingCache != nil {
		if err := s.billingCache.InvalidateUserBalance(ctx, ownerUserID); err != nil {
			logger.LegacyPrintf("service.account_rebate", "invalidate balance cache failed user_id=%d err=%v", ownerUserID, err)
		}
	}
}

// policy 读取当前返额政策。任一读取失败都回退到安全默认值。
func (s *UserAccountRebateService) policy(ctx context.Context) (enabled bool, ratePercent float64, requireShared bool, includeOwnerUsage bool) {
	if s.setting == nil {
		return false, AccountRebateRateDefault, true, false
	}
	return s.setting.IsUserAccountRebateEnabled(ctx),
		s.setting.GetUserAccountRebateRatePercent(ctx),
		s.setting.IsUserAccountRebateSharedOnly(ctx),
		s.setting.IsUserAccountRebateIncludeOwnerUsage(ctx)
}

// GetSummary 返回号主返额概览。
func (s *UserAccountRebateService) GetSummary(ctx context.Context, ownerUserID int64) (*AccountRebateSummary, error) {
	if s == nil || s.repo == nil || ownerUserID <= 0 {
		return &AccountRebateSummary{OwnerUserID: ownerUserID}, nil
	}
	return s.repo.GetRebateSummary(ctx, ownerUserID)
}

// ListHistory 返回号主返额流水（分页）。
func (s *UserAccountRebateService) ListHistory(ctx context.Context, ownerUserID int64, page, pageSize int) ([]AccountRebateRecord, int64, error) {
	if s == nil || s.repo == nil || ownerUserID <= 0 {
		return []AccountRebateRecord{}, 0, nil
	}
	page, pageSize = normalizeRebatePagination(page, pageSize)
	return s.repo.ListRebateHistory(ctx, ownerUserID, pageSize, (page-1)*pageSize)
}

// ListAllRecords 返回全部返额流水（管理端）。
func (s *UserAccountRebateService) ListAllRecords(ctx context.Context, search string, page, pageSize int) ([]AccountRebateRecord, int64, error) {
	if s == nil || s.repo == nil {
		return []AccountRebateRecord{}, 0, nil
	}
	page, pageSize = normalizeRebatePagination(page, pageSize)
	return s.repo.ListAllRebateRecords(ctx, search, pageSize, (page-1)*pageSize)
}

func normalizeRebatePagination(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return page, pageSize
}
