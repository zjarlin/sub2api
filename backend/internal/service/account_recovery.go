package service

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func (s *OpenAIGatewayService) selectOpenAIRecoveryAccount(ctx context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	decision := OpenAIAccountScheduleDecision{Layer: openAIAccountScheduleLayerRecoveryProbe}
	if s == nil || s.accountRepo == nil || NormalizeOpenAICompatiblePlatform(req.Platform) != PlatformOpenAI {
		return nil, decision, nil
	}
	recoveryRepo, ok := s.accountRepo.(AccountRecoveryCandidateRepository)
	if !ok {
		return nil, decision, nil
	}

	queryGroupID := req.GroupID
	includeGrouped := false
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		queryGroupID = nil
		includeGrouped = true
	}
	accounts, err := recoveryRepo.ListAccountRecoveryCandidates(ctx, queryGroupID, []string{PlatformOpenAI}, includeGrouped)
	if err != nil {
		return nil, decision, err
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		iRank := openAIRecoveryModelRank(&accounts[i], req.RequestedModel)
		jRank := openAIRecoveryModelRank(&accounts[j], req.RequestedModel)
		if iRank != jRank {
			return iRank < jRank
		}
		if accounts[i].Priority != accounts[j].Priority {
			return accounts[i].Priority < accounts[j].Priority
		}
		return accounts[i].ID < accounts[j].ID
	})

	for i := range accounts {
		account := &accounts[i]
		if !accountAllowsAutomaticRecovery(account) {
			continue
		}
		if account.IsSchedulable() {
			continue
		}
		if _, excluded := req.ExcludedIDs[account.ID]; excluded {
			continue
		}
		if account.AutoPauseOnExpired && account.ExpiresAt != nil && !time.Now().Before(*account.ExpiresAt) {
			continue
		}
		if !account.IsOpenAICompatible() || !account.IsModelSupported(req.RequestedModel) {
			continue
		}
		if req.RequirePrivacySet && !account.IsPrivacySet() {
			continue
		}
		if req.RequireCompact && openAICompactSupportTier(account) == 0 {
			continue
		}
		if !accountSupportsOpenAICapabilities(account, req.RequiredCapability, req.RequiredImageCapability) ||
			!s.isOpenAIAccountTransportCompatible(account, req.RequiredTransport) {
			continue
		}
		if req.GroupID != nil && s.needsUpstreamChannelRestrictionCheck(ctx, req.GroupID) &&
			s.isUpstreamModelRestrictedByChannel(ctx, *req.GroupID, account, req.RequestedModel, req.RequireCompact) {
			continue
		}
		if vetoed, _ := openAIProfitControlVetoReason(ctx, account); vetoed {
			continue
		}

		acquired, acquireErr := s.tryAcquireAccountSlot(ctx, account.ID, account.Concurrency)
		if acquireErr != nil {
			if ctx.Err() != nil {
				return nil, decision, ctx.Err()
			}
			continue
		}
		if acquired == nil || !acquired.Acquired {
			continue
		}
		account.recoveryProbe = true
		decision.CandidateCount = len(accounts)
		decision.SelectedAccountID = account.ID
		decision.SelectedAccountType = account.Type
		slog.Warn("openai recovery account selected", "account_id", account.ID, "model", req.RequestedModel)
		return attachSelectionProfitGate(ctx, &AccountSelectionResult{
			Account:     account,
			Acquired:    true,
			ReleaseFunc: acquired.ReleaseFunc,
		}), decision, nil
	}
	return nil, decision, nil
}

// A closed scheduling switch is an administrative boundary, even when the
// account also has an error or a temporary runtime block.
func accountAllowsAutomaticRecovery(account *Account) bool {
	return account != nil && account.Schedulable &&
		(account.Status == StatusActive || account.Status == StatusError)
}

func openAIRecoveryModelRank(account *Account, requestedModel string) int {
	if account == nil {
		return 3
	}
	if _, matched := account.ResolveMappedModel(requestedModel); matched {
		return 0
	}
	if account.IsOpenAIPassthroughEnabled() {
		return 1
	}
	return 2
}
