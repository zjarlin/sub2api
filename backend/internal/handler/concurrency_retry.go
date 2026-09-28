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

// 限流与可重放的普通故障遍历同模型候选；已失败账号仍由排除集合约束。
type openAIAccountSwitchBudget struct {
	limit             int
	failures          int
	replayable        bool
	requireCompatible bool
}

func tryRemainingOpenAIAccounts(account *service.Account, err *service.UpstreamFailoverError) bool {
	return account != nil && account.Platform == service.PlatformOpenAI &&
		err.ShouldRetryNextAccount() && !err.IsCredentialFailure() &&
		!err.RequestScopedTransient && err.Scope != service.GatewayFailureScopeRequest &&
		(err.StatusCode == http.StatusTooManyRequests || err.IsUpstreamConcurrencyLimited())
}

func (b *openAIAccountSwitchBudget) exhausted(account *service.Account, err *service.UpstreamFailoverError) bool {
	if err.IsAutoModelExcluded() {
		return false
	}
	if tryRemainingOpenAIAccounts(account, err) {
		return false
	}
	if b.replayable && ordinaryOpenAIAccountFailure(account, err) {
		b.requireCompatible = true
		return false
	}
	if b.failures >= b.limit {
		return true
	}
	b.failures++
	return false
}

func ordinaryOpenAIAccountFailure(account *service.Account, err *service.UpstreamFailoverError) bool {
	if !account.IsOpenAICompatible() || err == nil ||
		!err.ShouldRetryNextAccount() || err.IsCredentialFailure() ||
		err.RequestScopedTransient || err.Scope == service.GatewayFailureScopeRequest ||
		err.IsUpstreamConcurrencyLimited() {
		return false
	}
	return err.StatusCode == 0 || err.StatusCode == http.StatusRequestTimeout ||
		(err.StatusCode >= http.StatusInternalServerError && err.StatusCode <= 599)
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
