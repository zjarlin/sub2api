package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// NVIDIA Kimi K3 只支持 low/high/max；按不低于请求强度的原生档位适配。
// none 不在这里转为启用推理，空值与未知值保持上游原有校验行为。
func normalizeNVIDIAKimiK3ReasoningEffort(ctx context.Context, c *gin.Context, account *Account, model string, body []byte) ([]byte, *string, error) {
	if account == nil || account.Platform != PlatformOpenAI {
		return body, nil, nil
	}
	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	if normalizedModel != "kimi-k3" && normalizedModel != "moonshotai/kimi-k3" {
		return body, nil, nil
	}
	base, err := url.Parse(account.GetOpenAIBaseURL())
	if err != nil || !strings.EqualFold(base.Hostname(), "integrate.api.nvidia.com") {
		return body, nil, nil
	}
	field := gjson.GetBytes(body, "reasoning_effort")
	if field.Type != gjson.String {
		return body, nil, nil
	}
	var effort string
	switch normalizeEffortToken(field.String()) {
	case "minimal", "low":
		effort = "low"
	case "medium", "high":
		effort = "high"
	case "xhigh", "extrahigh", "max":
		effort = "max"
	default:
		return body, nil, nil
	}
	if err := checkNVIDIAKimiReasoningCeiling(ctx, c, effort); err != nil {
		return body, nil, err
	}
	if effort == field.String() {
		return body, &effort, nil
	}
	normalized, err := sjson.SetBytes(body, "reasoning_effort", effort)
	if err != nil {
		return nil, nil, fmt.Errorf("normalize NVIDIA Kimi K3 reasoning effort: %w", err)
	}
	return normalized, &effort, nil
}

// 原生档位向上适配不能越过真实分组上限；Auto 换候选，显式模型报告本地策略冲突。
func checkNVIDIAKimiReasoningCeiling(ctx context.Context, c *gin.Context, effort string) error {
	maxEffort := ""
	if ctx != nil {
		if policy, ok := ctx.Value(openAIReasoningEffortPolicyContextKey{}).(openAIReasoningEffortPolicy); ok {
			maxEffort = policy.maxEffort
		}
	}
	// Responses 和原生 Chat 在 handler 应用策略但不绑定上下文，需读取同一 API key 的分组。
	if maxEffort == "" && c != nil {
		if group := apiKeyGroup(getAPIKeyFromContext(c)); group != nil {
			maxEffort = group.MaxReasoningEffort
		}
	}
	maxRank, hasMax := reasoningEffortRank(maxEffort)
	actualRank, _ := reasoningEffortRank(effort)
	if !hasMax || actualRank <= maxRank {
		return nil
	}
	err := &ReasoningEffortOverLimitError{Requested: effort, Max: NormalizeMaxReasoningEffort(maxEffort)}
	if !IsAutoModelRouting(ctx) {
		return err
	}
	return &UpstreamFailoverError{
		StatusCode: http.StatusBadRequest, ClientStatusCode: http.StatusBadRequest, ClientMessage: err.Error(),
		Stage: GatewayFailureStageInference, Scope: GatewayFailureScopeAccount,
		Reason: AutoModelCapabilityMismatchReason, NextAccountAction: NextAccountRetry,
		SkipAccountScheduleFailure: true,
	}
}
