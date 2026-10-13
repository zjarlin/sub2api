package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// 记录本轮转发模式、模型降级状态及推测的密文来源账号。
// 来源在选号前从粘性绑定读取；绑定缺失时以本请求首次尝试的账号为参照。
// 粘性绑定并非密文所有权证明，未知来源仍由上游错误后的恢复路径兜底。
// 按账号行 ID 保守比较：即使两行配置共享上游身份，换行也会剥离密文。
type openAIPassthroughFailoverState struct {
	passthroughSeen bool
	modelFallback   bool
	// reasoningSourceAccountID 是加密密文所属的上游账号；0 表示未知（无粘性绑定
	// 且本请求尚未尝试任何账号）。未知时不剥离，交由上游拒绝后的响应式恢复兜底。
	reasoningSourceAccountID int64
}

// 每次转发前从不可变的原始请求派生本次请求，并推进尝试状态。
// 换账号、模型降级或从透传切到非透传时，整块剔除携带密文的推理与压缩项。
// 同账号且无上述变化时保留历史；原始字节切片始终不被修改。
func (h *OpenAIGatewayHandler) deriveOpenAIForwardAttemptBody(
	reqLog *zap.Logger,
	canonicalBody []byte,
	account *service.Account,
	state *openAIPassthroughFailoverState,
) []byte {
	currentPassthrough := account.IsOpenAIPassthroughEnabled()
	if currentPassthrough {
		state.passthroughSeen = true
	}
	if state.reasoningSourceAccountID == 0 {
		state.reasoningSourceAccountID = account.ID
	}
	accountChanged := state.reasoningSourceAccountID != account.ID
	crossMode := state.modelFallback || (state.passthroughSeen && !currentPassthrough)
	if !crossMode && !accountChanged {
		return canonicalBody
	}

	sanitized, changed, err := service.SanitizeOpenAICrossModeFailoverReasoning(canonicalBody)
	if err != nil {
		if reqLog != nil {
			reqLog.Warn("openai.failover_cross_mode_reasoning_sanitize_failed",
				zap.Int64("account_id", account.ID),
				zap.Error(err),
			)
		}
		return canonicalBody
	}
	if !changed {
		return canonicalBody
	}
	if reqLog != nil {
		reqLog.Info("openai.failover_cross_mode_reasoning_stripped",
			zap.Int64("account_id", account.ID),
			zap.Bool("account_passthrough", currentPassthrough),
			zap.Bool("passthrough_seen", state.passthroughSeen),
			zap.Bool("model_fallback", state.modelFallback),
			zap.Bool("account_changed", accountChanged),
			zap.Int64("reasoning_source_account_id", state.reasoningSourceAccountID),
		)
	}
	return sanitized
}
