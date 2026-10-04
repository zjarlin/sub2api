package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/platform/translate"

	"github.com/gin-gonic/gin"
)

// TranslateHandler 翻译服务 HTTP 处理器
type TranslateHandler struct {
	aggregator *translate.Aggregator
}

// NewTranslateHandler 创建翻译处理器
func NewTranslateHandler(aggregator *translate.Aggregator) *TranslateHandler {
	return &TranslateHandler{aggregator: aggregator}
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

	resp, err := h.aggregator.Translate(c.Request.Context(), &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, resp)
}

// Providers 返回可用的翻译服务商列表
// GET /api/v1/translate/providers
func (h *TranslateHandler) Providers(c *gin.Context) {
	response.Success(c, gin.H{
		"providers": h.aggregator.AvailableProviders(),
	})
}
