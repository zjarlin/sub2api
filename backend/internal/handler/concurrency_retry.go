package handler

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

func openAILocalCapacityFailover() *service.UpstreamFailoverError {
	return &service.UpstreamFailoverError{
		StatusCode:        http.StatusTooManyRequests,
		Stage:             service.GatewayFailureStageRouting,
		ClientStatusCode:  http.StatusTooManyRequests,
		ClientMessage:     "Concurrency limit exceeded for account, please retry later",
		Scope:             service.GatewayFailureScopeAccount,
		NextAccountAction: service.NextAccountRetry,
	}
}

// 限流时遍历同模型候选，不消耗普通故障的切换预算；已失败账号仍由排除集合约束。
type openAIAccountSwitchBudget struct {
	limit    int
	failures int
}

func tryRemainingOpenAIAccounts(account *service.Account, err *service.UpstreamFailoverError) bool {
	return account != nil && account.CanUseOpenAIAccount429SwitchBudget() &&
		err.ShouldRetryNextAccount() && !err.IsCredentialFailure() &&
		!err.RequestScopedTransient && err.Scope != service.GatewayFailureScopeRequest &&
		(err.StatusCode == http.StatusTooManyRequests || err.IsUpstreamConcurrencyLimited())
}

func (b *openAIAccountSwitchBudget) exhausted(account *service.Account, err *service.UpstreamFailoverError) bool {
	if tryRemainingOpenAIAccounts(account, err) {
		return false
	}
	if b.failures >= b.limit {
		return true
	}
	b.failures++
	return false
}

// Other candidates are tried first. Only explicit busy-account exclusions are
// reopened after exhaustion; authentication/model/policy failures stay excluded.
type concurrencyRetry struct {
	accounts map[int64]struct{}
	attempts int
}

func (r *concurrencyRetry) record(id int64, err *service.UpstreamFailoverError) {
	if !err.IsUpstreamConcurrencyLimited() && (err == nil || err.Stage != service.GatewayFailureStageRouting) {
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
