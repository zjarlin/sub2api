package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// bindModelSystemPrompts 在请求上下文里绑定一份模型提示词快照，
// 与模型别名同一次入口绑定，重试/降级共享同一份配置。
func (h *OpenAIGatewayHandler) bindModelSystemPrompts(c *gin.Context) error {
	ctx, _, err := h.gatewayService.BindModelSystemPrompts(c.Request.Context())
	if err != nil {
		requestLogger(c, "gateway.model_prompt").Error("gateway.model_prompt_unavailable", zap.Error(err))
		return err
	}
	c.Request = c.Request.WithContext(ctx)
	return nil
}

// applyModelSystemPrompt 按归一后的模型 ID 注入服务端配置的 system 提示词。
// 未配置或形态不匹配时原样返回，调用方无需区分。
func applyModelSystemPrompt(c *gin.Context, model string, body []byte) []byte {
	prompts := make([]string, 0, 2)
	policy := service.ModelSystemPromptsFromContext(c.Request.Context())
	if policy != nil {
		if prompt := policy.PromptFor(model); prompt != "" {
			prompts = append(prompts, prompt)
		}
	}
	if prompt := service.UserInputToolPromptForModel(model, body); prompt != "" {
		// 内置协议提示必须覆盖 Responses / Responses Lite / Chat Completions
		// 的所有入口，同时保留运维配置的按模型提示词。
		if !service.HasUserInputToolPrompt(body) {
			prompts = append(prompts, prompt)
		}
	}
	if len(prompts) == 0 {
		return body
	}
	updated, changed := service.InjectModelSystemPrompt(body, strings.Join(prompts, "\n\n"))
	if changed {
		requestLogger(c, "gateway.model_prompt").Debug("gateway.model_system_prompt_injected")
	}
	return updated
}
