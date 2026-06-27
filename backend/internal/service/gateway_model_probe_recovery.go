package service

import (
	"context"
	"log/slog"
)

func (s *GatewayService) tryRecoverGroupModelAccountByProbe(ctx context.Context, groupID *int64, platform string, allowMixedScheduling bool, requestedModel string, excludedIDs map[int64]struct{}) bool {
	if s == nil || s.accountRepo == nil || s.accountModelProbe == nil || groupID == nil {
		return false
	}
	accounts, err := s.accountRepo.ListByGroup(ctx, *groupID)
	if err != nil {
		slog.Warn("group_model_probe_recovery.list_group_failed",
			"component", "service.gateway",
			"group_id", derefGroupID(groupID),
			"model", requestedModel,
			"error", err)
		return false
	}

	var schedGroup *Group
	if s.groupRepo != nil {
		schedGroup, _ = s.groupRepo.GetByID(ctx, *groupID)
	}

	return tryRecoverGroupModelAccountByProbe(ctx, groupModelProbeRecoveryInput{
		AccountRepo:       s.accountRepo,
		RateLimitService:  s.rateLimitService,
		SchedulerSnapshot: s.schedulerSnapshot,
		AccountProbe:      s.accountModelProbe,
		GroupID:           groupID,
		RequestedModel:    requestedModel,
		ExcludedIDs:       excludedIDs,
		Accounts:          accounts,
		Component:         "service.gateway",
		AllowCandidate: func(ctx context.Context, account *Account) bool {
			if !s.isAccountAllowedForPlatform(account, platform, allowMixedScheduling) {
				return false
			}
			if schedGroup != nil && schedGroup.RequirePrivacySet && !account.IsPrivacySet() {
				return false
			}
			if requestedModel != "" && !s.isModelSupportedByAccountWithContext(ctx, account, requestedModel) {
				return false
			}
			return true
		},
	})
}
