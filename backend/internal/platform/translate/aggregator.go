package translate

import (
	"context"
	"fmt"
	"log"
)

// Aggregator 翻译服务聚合器，按优先级调用多个 Translator，失败自动回退
type Aggregator struct {
	translators []Translator
}

// NewAggregator 根据配置创建聚合器，优先级: 腾讯 > 百度 > 有道
func NewAggregator(cfg *Config) *Aggregator {
	var translators []Translator

	if cfg.Tencent != nil && cfg.Tencent.SecretID != "" {
		translators = append(translators, NewTencentTranslator(cfg.Tencent))
		log.Println("[translate] registered provider: tencent")
	}
	if cfg.Baidu != nil && cfg.Baidu.AppID != "" {
		translators = append(translators, NewBaiduTranslator(cfg.Baidu))
		log.Println("[translate] registered provider: baidu")
	}
	if cfg.Youdao != nil && cfg.Youdao.AppKey != "" {
		translators = append(translators, NewYoudaoTranslator(cfg.Youdao))
		log.Println("[translate] registered provider: youdao")
	}

	return &Aggregator{translators: translators}
}

// Translate 按优先级尝试翻译，首个成功即返回
func (a *Aggregator) Translate(ctx context.Context, req *TranslateRequest) (*TranslateResponse, error) {
	if len(a.translators) == 0 {
		return nil, fmt.Errorf("translate: no providers configured")
	}

	var lastErr error
	for _, t := range a.translators {
		resp, err := t.Translate(ctx, req)
		if err != nil {
			log.Printf("[translate] %s failed: %v", t.Name(), err)
			lastErr = err
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("translate: all providers failed, last error: %w", lastErr)
}

// DetectLanguage 使用最高优先级的可用服务商检测语言
func (a *Aggregator) DetectLanguage(ctx context.Context, text string) (string, error) {
	if len(a.translators) == 0 {
		return "", fmt.Errorf("translate: no providers configured")
	}
	return a.translators[0].DetectLanguage(ctx, text)
}

// AvailableProviders 返回已注册的服务商名称列表
func (a *Aggregator) AvailableProviders() []string {
	names := make([]string, len(a.translators))
	for i, t := range a.translators {
		names[i] = t.Name()
	}
	return names
}
