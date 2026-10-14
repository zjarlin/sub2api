package handler

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/platform/translate"
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
		// 明确限定离线出口，不把用户文本回退到公网翻译服务。
		for _, provider := range aggregator.AvailableProviders() {
			if provider != "hymt" && provider != "libretranslate" {
				continue
			}
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
func parseTranslateResponseInput(body []byte) (*translate.TranslateRequest, error) {
	var wire struct {
		Input    json.RawMessage   `json:"input"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, errors.New("Invalid JSON request")
	}

	for _, field := range []string{"previous_response_id", "conversation", "instructions", "tools", "background", "store"} {
		value := gjson.GetBytes(body, field)
		if !value.Exists() || value.Type == gjson.Null || value.Raw == "false" || value.Raw == "[]" || (value.Type == gjson.String && value.Str == "") {
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
		var messages []struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(wire.Input, &messages) != nil {
			return nil, errors.New("input must be text or user messages")
		}
		for _, message := range messages {
			if (message.Type != "" && message.Type != "message") || message.Role != "user" {
				return nil, errors.New("translate accepts only user text messages")
			}
			if json.Unmarshal(message.Content, &text) == nil {
				req.Text = append(req.Text, text)
				continue
			}
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(message.Content, &parts) != nil {
				return nil, errors.New("Invalid message content")
			}
			for _, part := range parts {
				if part.Type != "input_text" {
					return nil, errors.New("translate accepts only input_text content")
				}
				req.Text = append(req.Text, part.Text)
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
