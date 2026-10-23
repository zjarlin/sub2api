package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/platform/translate"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// TranslateModelMiddleware 在鉴权和白名单校验后处理离线翻译虚拟模型。
func (h *TranslateHandler) TranslateModelMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h == nil || c.Request.Method != http.MethodPost || !strings.HasSuffix(c.Request.URL.Path, "/responses") {
			c.Next()
			return
		}
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			translateModelError(c, 400, "invalid_request_error", "Unable to read request", nil)
			return
		}
		requestmodel.ResetRequestBody(c.Request, body)
		if gjson.GetBytes(body, "model").String() != "translate" {
			c.Next()
			return
		}
		models := requestmodel.FromBodyCandidates(c.FullPath(), c.GetHeader("Content-Type"), body)
		if len(models) != 1 || models[0] != "translate" {
			translateModelError(c, 400, "invalid_request_error", "model must have one unambiguous value", nil)
			return
		}
		req, err := parseTranslateResponseInput(body)
		if err != nil {
			translateModelError(c, 400, "invalid_request_error", err.Error(), nil)
			return
		}
		aggregator, err := h.configuredAggregator(c.Request.Context())
		if err != nil || aggregator == nil {
			translateModelError(c, 503, "translation_unavailable", "Unable to load translation configuration", nil)
			return
		}
		var attempts []translate.TranslationAttempt
		// 优先保证译文质量：混元翻译失败后才使用 LibreTranslate，不回退公网。
		for _, provider := range []string{"hymt", "libretranslate"} {
			req.Provider = provider
			result, err := aggregator.Translate(c.Request.Context(), req)
			if err == nil {
				parts := make([]string, len(result.Translations))
				for i, part := range result.Translations {
					parts[i] = part.Text
				}
				c.Header("X-Translation-Provider", result.Provider)
				writeVerticalReply(c, body, "translate", strings.Join(parts, "\n\n"), nil)
				c.Abort()
				return
			}
			var chain *translate.ChainError
			if errors.As(err, &chain) {
				attempts = append(attempts, chain.Attempts...)
			}
			if c.Request.Context().Err() != nil {
				c.Abort()
				return
			}
		}
		translateModelError(c, 502, "translation_providers_failed", "No offline translation provider succeeded", attempts)
	}
}

func translateModelError(c *gin.Context, status int, code, message string, attempts []translate.TranslationAttempt) {
	kind := "invalid_request_error"
	if status >= 500 {
		kind = "server_error"
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": kind, "code": code, "message": message, "attempts": attempts}})
}

// 使用标准 metadata 携带目标语言，避免猜测原文中的翻译指令。
// Codex 会在 input 前注入 developer/system 消息和 environment_context，并附带 tools、
// tool_choice、reasoning 等客户端字段；这些字段对翻译没有意义，直接忽略。
// 只翻译最后一条 user 消息，避免把环境上下文或历史对话一起送去翻译。
func parseTranslateResponseInput(body []byte) (*translate.TranslateRequest, error) {
	var wire struct {
		Input    json.RawMessage   `json:"input"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, errors.New("Invalid JSON request")
	}

	// 仍拒绝依赖服务端会话状态或服务端存储的字段；客户端能力字段直接忽略。
	for _, field := range []string{"previous_response_id", "conversation", "background", "store"} {
		value := gjson.GetBytes(body, field)
		if !value.Exists() || value.Type == gjson.Null || value.Raw == "false" || value.Raw == "[]" || value.Raw == "{}" || (value.Type == gjson.String && value.Str == "") {
			continue
		}
		return nil, errors.New(field + " is not supported by translate")
	}
	if value := gjson.GetBytes(body, "stream"); value.Exists() && value.Type != gjson.True && value.Type != gjson.False {
		return nil, errors.New("stream must be a boolean")
	}

	req := &translate.TranslateRequest{SourceLang: wire.Metadata["source_language"], TargetLang: wire.Metadata["target_language"], Format: "text"}
	if req.TargetLang == "" {
		req.TargetLang = "zh"
	}

	var text string
	if json.Unmarshal(wire.Input, &text) == nil {
		req.Text = []string{text}
	} else {
		type inputItem struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		var items []inputItem
		if json.Unmarshal(wire.Input, &items) != nil {
			return nil, errors.New("input must be text or messages")
		}
		// 取最后一条 user 消息：Codex 会把 environment_context 也作为 user 消息注入，
		// 当前轮的用户输入始终是最后一条。
		var user *inputItem
		for i := len(items) - 1; i >= 0; i-- {
			item := &items[i]
			if item.Type != "" && item.Type != "message" {
				continue
			}
			if item.Role != "user" {
				continue
			}
			user = item
			break
		}
		if user == nil {
			return nil, errors.New("translate requires a user message")
		}
		if json.Unmarshal(user.Content, &text) == nil {
			req.Text = []string{text}
		} else {
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(user.Content, &parts) != nil {
				return nil, errors.New("Invalid message content")
			}
			for _, part := range parts {
				// 图片等非文本内容不参与翻译；全部为图片时会在下面报缺少文本。
				if part.Type == "input_text" {
					req.Text = append(req.Text, part.Text)
				}
			}
		}
	}
	if len(req.Text) == 0 || len(req.Text) > 16 {
		return nil, errors.New("translate requires between 1 and 16 text items")
	}
	for _, text := range req.Text {
		if strings.TrimSpace(text) == "" || len(text) > 4096 {
			return nil, errors.New("Each text item must contain 1 to 4096 UTF-8 bytes")
		}
	}
	return req, nil
}

// 翻译目录只在离线出口已配置且分组白名单允许时发布。
func (h *GatewayHandler) translationModelAvailable(ctx context.Context, group *service.Group) bool {
	if h.settingService == nil || group == nil || (group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows("translate")) {
		return false
	}
	aggregator, err := h.settingService.GetTranslationAggregator(ctx)
	if err != nil || aggregator == nil {
		return false
	}
	for _, provider := range aggregator.AvailableProviders() {
		if provider == "hymt" || provider == "libretranslate" {
			return true
		}
	}
	return false
}
