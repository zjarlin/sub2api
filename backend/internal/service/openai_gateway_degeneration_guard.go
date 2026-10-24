package service

import (
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

// chatDegenerationGuard 返回流式"退化重复"检测阈值；配置关闭（或未注入配置）时返回
// nil，调用方据此不下发检测。阈值来自 gateway.degeneration.*，可用环境变量覆盖：
//
//	GATEWAY_DEGENERATION_ENABLED / _MIN_TOTAL_RUNES / _WINDOW_RUNES /
//	_MIN_PERIOD / _MAX_PERIOD / _MIN_REPEATS
func (s *OpenAIGatewayService) chatDegenerationGuard() *apicompat.ChatDegenerationGuard {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.Degeneration.Enabled {
		return nil
	}
	cfg := s.cfg.Gateway.Degeneration
	guard := apicompat.ChatDegenerationGuard{
		MinTotalRunes:   cfg.MinTotalRunes,
		WindowRunes:     cfg.WindowRunes,
		MinPeriod:       cfg.MinPeriod,
		MaxPeriod:       cfg.MaxPeriod,
		MinRepeats:      cfg.MinRepeats,
		LineWindow:      cfg.LineWindow,
		LineMinCount:    cfg.LineMinCount,
		LineMaxDistinct: cfg.LineMaxDistinct,
		LineMaxRunes:    cfg.LineMaxRunes,

		MarkupMinCloses:  cfg.MarkupMinCloses,
		MarkupMinDensity: cfg.MarkupMinDensity,
	}.Normalize()
	return &guard
}

// clampDegenerationProneUpstreamMaxTokens 给"易退化重复"的模型（默认 deepseek 系的
// flash 兼容路径）钳制出站输出上限，缩小一次退化能烧掉的 token 与时长。
//
// 依据：2026-10-12 cline-pass 转发的 deepseek-v4.1-flash 在 35 万 token 上下文下连续
// 7 次跑到 65536 输出上限的纯复读，单次 8.6 分钟。检测熔断是主防线，这里是兜底——
// 退化若不是严格周期性的（熔断不命中）时仍然有界。
//
// 命中规则：客户端请求的模型名、计费模型名或出站上游模型名（任一，大小写不敏感）以
// gateway.degeneration.max_output_tokens_models 中的任一项为前缀，且 body 里的
// max_tokens / max_completion_tokens 超过 max_output_tokens_cap 时改写为该上限。
// cap <= 0 或列表为空表示不钳制；返回 clamp 后的 body 与实际生效的上限（未钳制返回 0）。
func (s *OpenAIGatewayService) clampDegenerationProneUpstreamMaxTokens(models []string, body []byte) ([]byte, int64) {
	if s == nil || s.cfg == nil || len(body) == 0 || !gjson.ValidBytes(body) {
		return body, 0
	}
	cfg := s.cfg.Gateway.Degeneration
	capValue := int64(cfg.MaxOutputTokensCap)
	if capValue <= 0 || !modelMatchesAnyPrefix(models, cfg.MaxOutputTokensModels) {
		return body, 0
	}

	clamped := false
	out := body
	for _, key := range []string{"max_completion_tokens", "max_tokens"} {
		value := gjson.GetBytes(out, key)
		if !value.Exists() || value.Type != gjson.Number || value.Int() <= capValue {
			continue
		}
		updated, err := sjson.SetBytes(out, key, capValue)
		if err != nil {
			return body, 0
		}
		out = updated
		clamped = true
	}
	if !clamped {
		return body, 0
	}
	logger.L().Debug("openai responses chat fallback: clamped upstream max tokens for degeneration-prone model",
		zap.Strings("models", models),
		zap.Int64("cap", capValue),
	)
	return out, capValue
}

// recordDegenerationAbort 为一次退化重复熔断记账号+模型维度的短期熔断。
//
// 退化是"账号+模型"级别的性质（同一个上游账号转发的同一模型卡在复读），不是整账号
// 故障：该账号的其它模型仍然可用。所以键必须与调度侧完全同源——调度用
// isOpenAIAccountModelRuntimeBlocked(account, requestedModel) 判定，内部走
// canonicalOpenAIAccountSchedulingModel；这里对同一个 requestedModel 做同样换算，
// 否则键不一致会导致熔断记了却拦不住（模型映射场景尤其明显）。
//
// 账号级健康计数由 classifyOpenAIAPIKeyHealthFailure 依据
// openAIUpstreamDegenerationError 单独累计，这里只做短期隔离。
func (s *OpenAIGatewayService) recordDegenerationAbort(account *Account, requestedModel string) {
	if s == nil || account == nil {
		return
	}
	canonicalModel := canonicalOpenAIAccountSchedulingModel(account, requestedModel)
	if canonicalModel == "" {
		return
	}
	decision := s.recordOpenAIAccountModelTransientFailure(account, canonicalModel, time.Now())
	if decision.FailureStreak <= 0 {
		return
	}
	logger.L().Warn("openai_degeneration_abort_state",
		zap.Int64("account_id", account.ID),
		zap.String("model", openAIAccountModelTransientModel(canonicalModel)),
		zap.Int("failure_streak", decision.FailureStreak),
		zap.Int64("cooldown_ms", decision.Cooldown.Milliseconds()),
		zap.String("block_scope", "account_model"),
	)
}

// modelMatchesAnyPrefix 判断任一候选模型名是否以任一配置前缀开头（大小写不敏感）。
func modelMatchesAnyPrefix(models, prefixes []string) bool {
	for _, model := range models {
		normalized := strings.ToLower(strings.TrimSpace(model))
		if normalized == "" {
			continue
		}
		for _, prefix := range prefixes {
			trimmed := strings.ToLower(strings.TrimSpace(prefix))
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(normalized, trimmed) {
				return true
			}
		}
	}
	return false
}
