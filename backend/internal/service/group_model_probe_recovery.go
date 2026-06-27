package service

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

type groupModelProbeRecoveryAttemptedKeyType struct{}

// AccountModelProbe 负责在调度兜底路径中按账号+模型做真实连通性探测。
type AccountModelProbe interface {
	ProbeAccountModel(ctx context.Context, accountID int64, modelID string, mode string) (*ScheduledTestResult, error)
}

var groupModelProbeRecoveryAttemptedKey = groupModelProbeRecoveryAttemptedKeyType{}

const groupModelProbeRecoveryPerAccountTimeout = 20 * time.Second

type groupModelProbeRecoveryInput struct {
	AccountRepo       AccountRepository
	RateLimitService  *RateLimitService
	SchedulerSnapshot *SchedulerSnapshotService
	AccountProbe      AccountModelProbe
	GroupID           *int64
	RequestedModel    string
	ExcludedIDs       map[int64]struct{}
	Accounts          []Account
	ProbeMode         string
	Component         string
	AllowCandidate    func(ctx context.Context, account *Account) bool
}

func groupModelProbeRecoveryAlreadyAttempted(ctx context.Context) bool {
	return ctx != nil && ctx.Value(groupModelProbeRecoveryAttemptedKey) == true
}

func withGroupModelProbeRecoveryAttempted(ctx context.Context) context.Context {
	return context.WithValue(ctx, groupModelProbeRecoveryAttemptedKey, true)
}

func tryRecoverGroupModelAccountByProbe(ctx context.Context, input groupModelProbeRecoveryInput) bool {
	modelID := strings.TrimSpace(input.RequestedModel)
	if !canRunGroupModelProbeRecovery(ctx, input, modelID) {
		return false
	}

	accounts := FilterAccountsVisibleToContext(ctx, input.Accounts)
	if len(accounts) == 0 {
		return false
	}

	mode := strings.TrimSpace(input.ProbeMode)
	if mode == "" {
		mode = AccountTestModeDefault
	}
	component := strings.TrimSpace(input.Component)
	if component == "" {
		component = "service.gateway"
	}

	for i := range accounts {
		account := &accounts[i]
		if !isGroupModelProbeRecoveryCandidate(ctx, input, account) {
			continue
		}
		result, err := runGroupModelProbe(ctx, input.AccountProbe, account.ID, modelID, mode)
		if err != nil {
			slog.Warn("group_model_probe_recovery.test_failed",
				"component", component,
				"group_id", derefGroupID(input.GroupID),
				"account_id", account.ID,
				"model", modelID,
				"error", err)
			continue
		}
		if result == nil || result.Status != "success" {
			errorMessage := ""
			if result != nil {
				errorMessage = result.ErrorMessage
			}
			slog.Info("group_model_probe_recovery.test_not_success",
				"component", component,
				"group_id", derefGroupID(input.GroupID),
				"account_id", account.ID,
				"model", modelID,
				"status", func() string {
					if result == nil {
						return ""
					}
					return result.Status
				}(),
				"error", errorMessage)
			continue
		}
		if err := recoverGroupModelProbeAccount(ctx, input, account.ID); err != nil {
			slog.Warn("group_model_probe_recovery.recover_failed",
				"component", component,
				"group_id", derefGroupID(input.GroupID),
				"account_id", account.ID,
				"model", modelID,
				"error", err)
			continue
		}
		slog.Info("group_model_probe_recovery.recovered",
			"component", component,
			"group_id", derefGroupID(input.GroupID),
			"account_id", account.ID,
			"model", modelID)
		return true
	}

	return false
}

func canRunGroupModelProbeRecovery(ctx context.Context, input groupModelProbeRecoveryInput, modelID string) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	if groupModelProbeRecoveryAlreadyAttempted(ctx) {
		return false
	}
	if input.AccountRepo == nil || input.AccountProbe == nil || input.GroupID == nil || *input.GroupID <= 0 {
		return false
	}
	return modelID != ""
}

func isGroupModelProbeRecoveryCandidate(ctx context.Context, input groupModelProbeRecoveryInput, account *Account) bool {
	if account == nil || account.ID <= 0 || !account.IsActive() {
		return false
	}
	if input.ExcludedIDs != nil {
		if _, excluded := input.ExcludedIDs[account.ID]; excluded {
			return false
		}
	}
	if input.AllowCandidate != nil && !input.AllowCandidate(ctx, account) {
		return false
	}
	return true
}

func runGroupModelProbe(ctx context.Context, probe AccountModelProbe, accountID int64, modelID string, mode string) (*ScheduledTestResult, error) {
	probeCtx, cancel := context.WithTimeout(ctx, groupModelProbeRecoveryPerAccountTimeout)
	defer cancel()
	return probe.ProbeAccountModel(probeCtx, accountID, modelID, mode)
}

func recoverGroupModelProbeAccount(ctx context.Context, input groupModelProbeRecoveryInput, accountID int64) error {
	if input.RateLimitService != nil {
		if _, err := input.RateLimitService.RecoverAccountAfterSuccessfulTest(ctx, accountID); err != nil {
			return err
		}
	} else if err := input.AccountRepo.ClearTempUnschedulable(ctx, accountID); err != nil {
		return err
	}

	if err := input.AccountRepo.SetSchedulable(ctx, accountID, true); err != nil {
		return err
	}

	if input.SchedulerSnapshot == nil {
		return nil
	}
	fresh, err := input.AccountRepo.GetByID(ctx, accountID)
	if err != nil {
		return err
	}
	if fresh == nil {
		return ErrAccountNotFound
	}
	if err := input.SchedulerSnapshot.UpdateAccountInCache(ctx, fresh); err != nil {
		return err
	}
	return input.SchedulerSnapshot.rebuildByAccount(ctx, fresh, []int64{*input.GroupID}, "group_model_probe_recovery", nil)
}
