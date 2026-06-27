package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
)

func isOpenAISelectionRecoverableByProbe(err error) bool {
	if err == nil || isOpenAISelectionAbortError(err) {
		return false
	}
	var selectionErr *OpenAISelectionError
	if errors.As(err, &selectionErr) && selectionErr.Phase == "channel_pricing_restriction" {
		return false
	}
	if errors.Is(err, ErrNoAvailableAccounts) || errors.Is(err, ErrNoAvailableCompactAccounts) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "no available openai accounts")
}

func (s *OpenAIGatewayService) tryRecoverGroupModelAccountByProbe(ctx context.Context, groupID *int64, requestedModel string, excludedIDs map[int64]struct{}, requireCompact bool, requiredCapability OpenAIEndpointCapability, requiredImageCapability OpenAIImagesCapability) bool {
	if s == nil || s.accountRepo == nil || s.accountModelProbe == nil || groupID == nil {
		return false
	}
	accounts, err := s.accountRepo.ListByGroup(ctx, *groupID)
	if err != nil {
		slog.Warn("group_model_probe_recovery.list_group_failed",
			"component", "service.openai_gateway",
			"group_id", derefGroupID(groupID),
			"model", requestedModel,
			"error", err)
		return false
	}
	accounts = FilterAccountsVisibleToContext(ctx, accounts)
	explicitModelScope := openAIAccountsHaveExplicitModelSupport(accounts, requestedModel)
	probeMode := AccountTestModeDefault
	if requireCompact {
		probeMode = AccountTestModeCompact
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
		ProbeMode:         probeMode,
		Component:         "service.openai_gateway",
		AllowCandidate: func(ctx context.Context, account *Account) bool {
			if account == nil || !account.IsOpenAI() {
				return false
			}
			if paused, _ := shouldAutoPauseOpenAIAccountByQuota(ctx, account); paused {
				return false
			}
			if requestedModel != "" && !isOpenAIAccountModelSupportedForScheduling(account, requestedModel) {
				return false
			}
			if !openAIAccountAllowedByExplicitModelScope(account, requestedModel, explicitModelScope) {
				return false
			}
			if !accountSupportsOpenAICapabilities(account, requiredCapability, requiredImageCapability) {
				return false
			}
			if requireCompact && openAICompactSupportTier(account) == 0 {
				return false
			}
			return !s.isOpenAIAccountRuntimeBlocked(account)
		},
	})
}
