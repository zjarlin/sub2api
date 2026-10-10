package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/platform/translate"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// TranslateHandler 翻译服务 HTTP 处理器
type TranslateHandler struct {
	aggregator *translate.Aggregator
	settings   *service.SettingService
}

// NewTranslateHandler 创建翻译处理器
func NewTranslateHandler(aggregator *translate.Aggregator, settings *service.SettingService) *TranslateHandler {
	return &TranslateHandler{aggregator: aggregator, settings: settings}
}

func (h *TranslateHandler) configuredAggregator(ctx context.Context) (*translate.Aggregator, error) {
	if h.settings == nil {
		return h.aggregator, nil
	}
	return h.settings.GetTranslationAggregator(ctx)
}

// Translate 处理翻译请求
// POST /api/v1/translate
func (h *TranslateHandler) Translate(c *gin.Context) {
	var req translate.TranslateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	if len(req.Text) == 0 {
		response.BadRequest(c, "Field 'q' is required and must not be empty")
		return
	}
	if req.TargetLang == "" {
		response.BadRequest(c, "Field 'target' is required")
		return
	}

	aggregator, err := h.configuredAggregator(c.Request.Context())
	if err != nil {
		response.InternalError(c, "Unable to load translation configuration")
		return
	}
	resp, err := aggregator.Translate(c.Request.Context(), &req)
	if err != nil {
		if errors.Is(err, translate.ErrProviderUnavailable) {
			response.BadRequest(c, "Unknown or unavailable translation provider")
			return
		}
		var chain *translate.ChainError
		if errors.As(err, &chain) {
			c.JSON(http.StatusBadGateway, response.Response{
				Code: http.StatusBadGateway, Message: chain.Error(), Reason: "translation_providers_failed",
				Data: gin.H{"attempts": chain.Attempts},
			})
			return
		}
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, resp)
}

// Providers 返回可用的翻译服务商列表
// GET /api/v1/translate/providers
func (h *TranslateHandler) Providers(c *gin.Context) {
	aggregator, err := h.configuredAggregator(c.Request.Context())
	if err != nil {
		response.InternalError(c, "Unable to load translation configuration")
		return
	}
	response.Success(c, gin.H{
		"providers": aggregator.AvailableProviders(),
		"health":    aggregator.ProviderHealth(),
	})
}
