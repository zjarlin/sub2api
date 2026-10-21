package service

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// 树中各上游叶子独立冷却；同账号其他型号与其他基础地址继续可用。
func (s *RateLimitService) handleModelAlias429(ctx context.Context, a *Account, status int, headers http.Header, body []byte, model string) bool {
	if s == nil || s.accountRepo == nil || a == nil || a.Type != AccountTypeAPIKey || status != http.StatusTooManyRequests {
		return false
	}
	p := ModelAliasesFromContext(ctx)
	if p == nil && s.settingService != nil {
		p, _ = s.settingService.GetModelAliasPolicy(ctx)
	}
	if p == nil {
		p = &ModelAliasPolicy{Groups: a.modelAliasGroups}
	}
	canonical := p.Canonicalize(model)
	// 凭据、钱包与明确的账号总配额仍交给既有账号保护；原生供应商保留其专用窗口处理。
	message := strings.ToLower(extractUpstreamErrorMessage(body))
	if a.Platform != PlatformOpenAI || isOpenAIHTTPUpstreamAccessStateError(status, "", body) ||
		strings.Contains(message, "insufficient credits") || strings.Contains(message, "insufficient balance") ||
		strings.Contains(message, "account quota") || strings.Contains(message, "余额不足") {
		return false
	}
	// 调用点可能传入已还原的实际模型，也可能传入规范名。
	if !p.isTreeGroup(canonical) {
		return false
	}
	target := model
	if mapped := a.globalModelMapping[model]; mapped != "" {
		target = mapped
	}
	if target == "" {
		return false
	}
	now := time.Now()
	reset := parseRetryAfterResetTime(headers, now)
	if stamp := parseOpenAIRateLimitResetTime(body); reset == nil && stamp != nil {
		t := time.Unix(*stamp, 0)
		reset = &t
	}
	if reset == nil || !reset.After(now) {
		cooldown, enabled := s.get429FallbackCooldown(ctx, a)
		if !enabled {
			cooldown = 5 * time.Second
		}
		t := now.Add(cooldown)
		reset = &t
	}
	key := modelAliasRateLimitKey(a, target)
	if err := s.accountRepo.SetModelRateLimit(ctx, a.ID, key, *reset, "model_alias_leaf_429"); err != nil {
		slog.Warn("model_alias_rate_limit_persist_failed", "account_id", a.ID, "model", target, "error", err)
	}
	return true
}

// 先记录真实失败，再学习账号/基础地址/精确模型的短期状态，最后遍历同义词。
// c 只需提供请求上下文和错误链；放在服务层便于 HTTP、流式与 WebSocket 共用。
func (s *OpenAIGatewayService) TryNextModelAlias(ctx context.Context, c *gin.Context, a *Account, model string, failure *UpstreamFailoverError) bool {
	if c == nil || a == nil || failure == nil {
		return false
	}
	if ctx == nil {
		ctx = c.Request.Context()
	}
	target := ResolveOpenAIAccountUpstreamModelForRequest(a, model, false)
	recordOpsFailoverAccount(c, a, target, failure)
	if !HasModelAliasAlternatives(ctx, model) || !failure.ShouldRetryNextAccount() || failure.IsCredentialFailure() ||
		failure.RequestScopedTransient || failure.Scope == GatewayFailureScopeRequest || ctx.Err() != nil {
		return false
	}
	if s != nil && s.rateLimitService != nil && s.rateLimitService.accountRepo != nil &&
		a.IsOpenAICompatible() && a.Type == AccountTypeAPIKey {
		s.learnModelAliasFailure(ctx, a, target, failure)
	}
	return TryNextModelAlias(ctx, a, model, failure.StatusCode)
}

// 学习只缩短下一次绕路：确定性不支持保留 30 分钟能力否定，限流保留上游窗口，
// 超时/5xx 按连续失败逐步退避；成功响应由调度器清空连续失败计数。
func (s *OpenAIGatewayService) learnModelAliasFailure(ctx context.Context, a *Account, target string, failure *UpstreamFailoverError) {
	if s == nil || s.rateLimitService == nil || s.rateLimitService.accountRepo == nil || a == nil || failure == nil || target == "" {
		return
	}
	key := modelAliasRateLimitKey(a, target)
	now := time.Now()
	var cooldown time.Duration
	reason := "model_alias_leaf_transient"
	switch {
	case shouldCooldownOpenAITransientUpstreamError(failure.StatusCode, failure.ResponseBody) || failure.StatusCode == http.StatusRequestTimeout:
		decision := s.getOpenAIAccountModelTransientState().recordFailure(a.ID, key, now)
		cooldown = 5 * time.Second
		if decision.Cooldown > cooldown {
			cooldown = decision.Cooldown
		}
	default:
		return
	}
	if err := s.rateLimitService.accountRepo.SetModelRateLimit(ctx, a.ID, key, now.Add(cooldown), reason); err != nil {
		slog.Warn("model_alias_failure_learning_failed", "account_id", a.ID, "model", target, "error", err)
	}
}
