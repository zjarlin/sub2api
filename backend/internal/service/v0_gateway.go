package service

import "context"

// SelectV0Account 复用账号并发调度；已有会话只允许命中原账号。
func (s *GatewayService) SelectV0Account(ctx context.Context, groupID *int64, accountID, userID int64) (*AccountSelectionResult, error) {
	excluded := map[int64]struct{}{}
	if accountID > 0 {
		accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, *groupID)
		if err != nil {
			return nil, err
		}
		found := false
		for _, account := range accounts {
			if account.ID == accountID && account.Platform == PlatformV0 {
				found = true
			} else {
				excluded[account.ID] = struct{}{}
			}
		}
		if !found {
			return nil, ErrNoAvailableAccounts
		}
	}
	selection, err := s.SelectAccountWithLoadAwareness(ctx, groupID, "", "", excluded, "", userID)
	if err != nil {
		return nil, err
	}
	if selection == nil || selection.Account == nil {
		return nil, ErrNoAvailableAccounts
	}
	if selection.Account.Platform != PlatformV0 || accountID > 0 && selection.Account.ID != accountID {
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		return nil, ErrNoAvailableAccounts
	}
	return selection, nil
}
