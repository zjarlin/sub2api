package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// 在鉴权后加载请求快照。仅覆盖 OpenAI 兼容分组的文本 HTTP 与模型目录。
func GlobalModelAliases(settings *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := GetAPIKeyFromContext(c)
		path := strings.TrimSuffix(c.Request.URL.Path, "/")
		text := c.Request.Method == http.MethodPost && (strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/responses/compact") || strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/messages"))
		catalog := c.Request.Method == http.MethodGet && (strings.HasSuffix(path, "/models") || c.Param("model") != "")
		if !ok || key.Group == nil || service.NormalizeOpenAICompatiblePlatform(key.Group.Platform) != key.Group.Platform || (!text && !catalog) {
			c.Next()
			return
		}
		policy, err := settings.GetModelAliasPolicy(c.Request.Context())
		if err != nil {
			logger.FromContext(c.Request.Context()).Error("gateway.model_alias_unavailable", zap.Error(err))
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": "Model alias settings unavailable"}})
			return
		}
		c.Request = c.Request.WithContext(service.WithModelAliases(c.Request.Context(), policy))
		// 客户端 ETag 对应归一化后的完整目录，不能让上游提前返回旧的 304。
		if catalog && len(policy.Groups) > 0 {
			c.Set("model_alias_if_none_match", c.GetHeader("If-None-Match"))
			c.Request.Header.Del("If-None-Match")
		}
		c.Next()
	}
}
