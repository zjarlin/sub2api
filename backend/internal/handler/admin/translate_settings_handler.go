package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// GetTranslateProviders 返回翻译服务商配置（密钥脱敏）。
// GET /api/v1/admin/translate/providers
func (h *SettingHandler) GetTranslateProviders(c *gin.Context) {
	settings, err := h.settingService.GetTranslateProviderSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings.Masked())
}

// UpdateTranslateProviders 保存翻译服务商配置。
// PUT /api/v1/admin/translate/providers
func (h *SettingHandler) UpdateTranslateProviders(c *gin.Context) {
	current, err := h.settingService.GetTranslateProviderSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var req service.TranslateProviderSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	// 前端回传脱敏占位符时，保留库中已存的真实密钥。
	mergeTranslateSecrets(&req, current)
	if err := h.settingService.SetTranslateProviderSettings(c.Request.Context(), &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, req.Masked())
}

// mergeTranslateSecrets 把未变更的密钥字段从已存配置补回，避免脱敏回传覆盖真实值。
func mergeTranslateSecrets(req, current *service.TranslateProviderSettings) {
	const mask = "***"
	if req.Tencent.SecretKey == "" || req.Tencent.SecretKey == mask {
		req.Tencent.SecretKey = current.Tencent.SecretKey
	}
	if req.Tencent.SecretID == "" || req.Tencent.SecretID == mask {
		req.Tencent.SecretID = current.Tencent.SecretID
	}
	if req.Baidu.Secret == "" || req.Baidu.Secret == mask {
		req.Baidu.Secret = current.Baidu.Secret
	}
	if req.Baidu.AppID == "" || req.Baidu.AppID == mask {
		req.Baidu.AppID = current.Baidu.AppID
	}
	if req.Youdao.AppSecret == "" || req.Youdao.AppSecret == mask {
		req.Youdao.AppSecret = current.Youdao.AppSecret
	}
	if req.Youdao.AppKey == "" || req.Youdao.AppKey == mask {
		req.Youdao.AppKey = current.Youdao.AppKey
	}
	if req.Caiyun.Token == "" || req.Caiyun.Token == mask {
		req.Caiyun.Token = current.Caiyun.Token
	}
	if req.MyMemory.APIKey == "" || req.MyMemory.APIKey == mask {
		req.MyMemory.APIKey = current.MyMemory.APIKey
	}
	if req.LibreTranslate.APIKey == "" || req.LibreTranslate.APIKey == mask {
		req.LibreTranslate.APIKey = current.LibreTranslate.APIKey
	}
	if req.HyMT.APIKey == "" || req.HyMT.APIKey == mask {
		req.HyMT.APIKey = current.HyMT.APIKey
	}
}
