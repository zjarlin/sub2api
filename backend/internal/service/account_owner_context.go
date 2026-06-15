package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// AccountOwnerUserIDFromContext returns the authenticated API key user ID used
// to limit scheduling to that user's private accounts.
func AccountOwnerUserIDFromContext(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	userID, ok := ctx.Value(ctxkey.AccountOwnerUserID).(int64)
	if !ok || userID <= 0 {
		return 0
	}
	return userID
}

// IsAccountVisibleToContext enforces scheduling visibility:
// - normal scheduling contexts can see only global accounts (owner_user_id NULL)
// - personal-account contexts can see only accounts owned by that API key user
func IsAccountVisibleToContext(ctx context.Context, account *Account) bool {
	if account == nil {
		return false
	}
	ownerUserID := AccountOwnerUserIDFromContext(ctx)
	if ownerUserID <= 0 {
		return account.OwnerUserID == nil
	}
	return account.OwnerUserID != nil && *account.OwnerUserID == ownerUserID
}

func FilterAccountsVisibleToContext(ctx context.Context, accounts []Account) []Account {
	if len(accounts) == 0 {
		return accounts
	}
	filtered := make([]Account, 0, len(accounts))
	for i := range accounts {
		if IsAccountVisibleToContext(ctx, &accounts[i]) {
			filtered = append(filtered, accounts[i])
		}
	}
	return filtered
}
