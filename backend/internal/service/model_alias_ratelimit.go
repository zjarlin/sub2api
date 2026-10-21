package service

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
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
