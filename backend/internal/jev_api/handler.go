package jev_api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Handler 暴露 TypeSafe / JEV System One 的中继端点。
type Handler struct {
	provider TargetProvider
}

// NewHandler 构造中继 handler；provider 通常由内容审计服务适配器提供。
func NewHandler(provider TargetProvider) *Handler {
	return &Handler{provider: provider}
}

// Relay 处理 POST /v1/systemone：客户端已通过网关鉴权，这里只做原样转发。
func (h *Handler) Relay(c *gin.Context) {
	if h == nil || h.provider == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "TypeSafe relay is not configured"}})
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, int64(maxRequestBytes)+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "failed to read request body"}})
		return
	}
	if len(body) > maxRequestBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "request body too large"}})
		return
	}
	model, err := ReadModel(body)
	if err != nil || model != ModelID {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "request body must be a JSON object"}})
		return
	}
	status, payload, err := Relay(c.Request.Context(), h.provider, body)
	if err != nil {
		code := http.StatusBadGateway
		message := "TypeSafe upstream unavailable"
		if errors.Is(err, ErrRelayUnavailable) {
			code = http.StatusServiceUnavailable
			message = "TypeSafe relay has no configured API key"
		}
		// 不回显上游正文，避免泄漏用户输入或凭据。
		c.JSON(code, gin.H{"error": gin.H{"type": "upstream_error", "message": message}})
		return
	}
	if status < 200 || status >= 300 {
		c.JSON(status, gin.H{"error": gin.H{"type": "upstream_error", "message": "TypeSafe upstream returned an error"}})
		return
	}
	c.Data(status, "application/json", payload)
}

// IsKnownSystemOneModel 报告模型名是否属于 System One 决策协议。
//
// 分两类上游：
//   - Laya（本地 edge-laya）：公开名 `laya` 表示自动选检查点，
//     `laya-english` / `laya-multilingual` 指定检查点；
//   - JEV / TypeSafe：沿用上游的 `typesafe/jev`。
//
// 名字同时充当平台选择器，见 handler.systemOnePlatformForModel。
func IsKnownSystemOneModel(model string) bool {
	if model == ModelID {
		return true
	}
	return model == LayaModelID || strings.HasPrefix(model, LayaModelID+"-")
}

// RewriteModel 只改写顶层 model 字段，保留其余请求语义不变。
// 供网关在 JEV 不可用时把同一份请求转交给 Laya；调用方只传入已校验的模型名。
func RewriteModel(body []byte, model string) ([]byte, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' || !IsKnownSystemOneModel(model) || hasDuplicateModelKey(trimmed) {
		return nil, errors.New("invalid System One request body")
	}
	current, err := ReadModel(trimmed)
	if err != nil {
		return nil, err
	}
	if current == model {
		return body, nil
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &request); err != nil {
		return nil, err
	}
	encodedModel, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	request["model"] = encodedModel
	return json.Marshal(request)
}

// ReadModel 只读取单一、明确的顶层模型 ID；拒绝大小写变体和重复键。
func ReadModel(body []byte) (string, error) {
	trimmed := bytes.TrimSpace(body)
	var request struct {
		Model string `json:"model"`
	}
	if len(trimmed) == 0 || trimmed[0] != '{' || json.Unmarshal(trimmed, &request) != nil || hasDuplicateModelKey(trimmed) {
		return "", errors.New("invalid System One request body")
	}
	if IsKnownSystemOneModel(request.Model) {
		return request.Model, nil
	}
	return "", errors.New("unsupported System One model")
}

func hasDuplicateModelKey(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if _, err := decoder.Token(); err != nil {
		return true
	}
	count := 0
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return true
		}
		if strings.EqualFold(key.(string), "model") {
			if key.(string) != "model" {
				return true
			}
			count++
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return true
		}
	}
	closing, err := decoder.Token()
	return err != nil || closing != json.Delim('}') || count != 1
}

// Register 把中继端点挂到已鉴权的 /v1 路由组上。
func Register(group *gin.RouterGroup, provider TargetProvider) {
	if group == nil {
		return
	}
	group.POST("/systemone", NewHandler(provider).Relay)
}
