package translate

import "context"

// TranslateRequest 统一翻译请求，遵循 ISO 639-1 语言代码标准
type TranslateRequest struct {
	Text       []string `json:"q"`      // 待翻译文本列表
	SourceLang string   `json:"source"` // 源语言，ISO 639-1，空表示自动检测
	TargetLang string   `json:"target"` // 目标语言，ISO 639-1，必填
	Format     string   `json:"format,omitempty"` // "text" 或 "html"，默认 "text"
}

// TranslateResponse 统一翻译响应
type TranslateResponse struct {
	Translations []TranslationResult `json:"translations"`
	Provider     string              `json:"provider"` // 实际使用的服务商名称
}

// TranslationResult 单条翻译结果
type TranslationResult struct {
	Text             string `json:"text"`                       // 翻译后文本
	DetectedLanguage string `json:"detected_language,omitempty"` // 自动检测到的源语言（仅当 source 为空时返回）
}

// Translator 翻译服务商适配器接口
type Translator interface {
	// Translate 执行翻译
	Translate(ctx context.Context, req *TranslateRequest) (*TranslateResponse, error)
	// DetectLanguage 检测文本语言
	DetectLanguage(ctx context.Context, text string) (string, error)
	// Name 返回服务商名称
	Name() string
}

// Config 翻译服务配置
type Config struct {
	Tencent *TencentConfig `json:"tencent,omitempty"`
	Baidu   *BaiduConfig   `json:"baidu,omitempty"`
	Youdao  *YoudaoConfig  `json:"youdao,omitempty"`
}

// TencentConfig 腾讯云翻译配置
type TencentConfig struct {
	SecretID  string `json:"secret_id"`
	SecretKey string `json:"secret_key"`
	Region    string `json:"region"` // 默认 ap-guangzhou
}

// BaiduConfig 百度翻译配置
type BaiduConfig struct {
	AppID  string `json:"app_id"`
	Secret string `json:"secret"`
}

// YoudaoConfig 有道智云翻译配置
type YoudaoConfig struct {
	AppKey    string `json:"app_key"`
	AppSecret string `json:"app_secret"`
}
