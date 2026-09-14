package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// Other candidates are tried first. Only explicit busy-account exclusions are
// reopened after exhaustion; authentication/model/policy failures stay excluded.
type concurrencyRetry struct {
	accounts map[int64]struct{}
	attempts int
}

func (r *concurrencyRetry) record(id int64, err *service.UpstreamFailoverError) {
	if !err.IsUpstreamConcurrencyLimited() {
		delete(r.accounts, id)
		return
	}
	if r.accounts == nil {
		r.accounts = make(map[int64]struct{})
	}
	r.accounts[id] = struct{}{}
}

func (r *concurrencyRetry) retry(ctx context.Context, excluded map[int64]struct{}) bool {
	if len(r.accounts) == 0 || r.attempts >= 3 || ctx.Err() != nil {
		return false
	}
	r.attempts++
	logger.FromContext(ctx).Info("gateway.concurrency_capacity_wait", zap.Int("attempt", r.attempts), zap.Int("busy_accounts", len(r.accounts)))
	if !sleepWithContext(ctx, service.UpstreamConcurrencyCooldown) {
		return false
	}
	for id := range r.accounts {
		delete(excluded, id)
	}
	return true
}
