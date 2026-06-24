package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// AccountOwnerUserIDFromContext returns the authenticated API key user ID that
// owns accounts on user-facing account management routes. Scheduling is
// intentionally owner-neutral: user-owned accounts participate in the same
// pools as admin accounts once bound to groups.
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

func IsAccountVisibleToContext(ctx context.Context, account *Account) bool {
	return account != nil
}

func FilterAccountsVisibleToContext(ctx context.Context, accounts []Account) []Account {
	return accounts
}
