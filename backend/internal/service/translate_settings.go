package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/platform/translate"
)

const SettingKeyTranslateProviders = "translate_providers"

// TranslateProviderSettings 保存边缘翻译服务商配置。
// 密钥只落库、不回传明文，接口以 masked 形式返回是否已配置。
type TranslateProviderSettings struct {
	// Enabled 控制 /api/v1/translate 是否开放。
	Enabled bool `json:"enabled"`
	// Priority 是服务商尝试顺序，取值见 translate.Aggregator 注册名。
	Priority []string `json:"priority"`
	// Caiyun 免密钥公开源，默认启用。
	Caiyun struct {
		Enabled bool   `json:"enabled"`
		Token   string `json:"token,omitempty"`
	} `json:"caiyun"`
	Tencent struct {
		Enabled   bool   `json:"enabled"`
		SecretID  string `json:"secret_id,omitempty"`
		SecretKey string `json:"secret_key,omitempty"`
		Region    string `json:"region,omitempty"`
	} `json:"tencent"`
	Baidu struct {
		Enabled bool   `json:"enabled"`
		AppID   string `json:"app_id,omitempty"`
		Secret  string `json:"secret,omitempty"`
	} `json:"baidu"`
	Youdao struct {
		Enabled   bool   `json:"enabled"`
		AppKey    string `json:"app_key,omitempty"`
		AppSecret string `json:"app_secret,omitempty"`
	} `json:"youdao"`
	GoogleWeb struct {
		Enabled bool `json:"enabled"`
		translate.GoogleWebConfig
	} `json:"google_web"`
	MyMemory struct {
		Enabled bool `json:"enabled"`
		translate.MyMemoryConfig
	} `json:"mymemory"`
	LibreTranslate struct {
		Enabled bool `json:"enabled"`
		translate.LibreTranslateConfig
	} `json:"libretranslate"`
	HyMT struct {
		Enabled bool `json:"enabled"`
		translate.HyMTConfig
	} `json:"hymt"`
}

// DefaultTranslateProviderSettings 从实际环境继承，首次打开控制台不改变现有默认服务商。
func DefaultTranslateProviderSettings() *TranslateProviderSettings {
	cfg := translate.ConfigFromEnvironment()
	s := &TranslateProviderSettings{
		Enabled:  true,
		Priority: []string{"caiyun", "google_web", "mymemory", "libretranslate", "hymt", "tencent", "youdao", "baidu"},
	}
	s.Caiyun.Enabled = cfg.EnableFreeProviders || cfg.Caiyun != nil
	if cfg.Caiyun != nil {
		s.Caiyun.Token = cfg.Caiyun.Token
	}
	if cfg.GoogleWeb != nil {
		s.GoogleWeb.Enabled = true
		s.GoogleWeb.GoogleWebConfig = *cfg.GoogleWeb
	}
	if cfg.Tencent != nil {
		s.Tencent.Enabled = true
		s.Tencent.SecretID = cfg.Tencent.SecretID
		s.Tencent.SecretKey = cfg.Tencent.SecretKey
		s.Tencent.Region = cfg.Tencent.Region
	}
	if cfg.Baidu != nil {
		s.Baidu.Enabled = true
		s.Baidu.AppID = cfg.Baidu.AppID
		s.Baidu.Secret = cfg.Baidu.Secret
	}
	if cfg.Youdao != nil {
		s.Youdao.Enabled = true
		s.Youdao.AppKey = cfg.Youdao.AppKey
		s.Youdao.AppSecret = cfg.Youdao.AppSecret
	}
	if cfg.MyMemory != nil {
		s.MyMemory.Enabled = true
		s.MyMemory.MyMemoryConfig = *cfg.MyMemory
	}
	if cfg.LibreTranslate != nil {
		s.LibreTranslate.Enabled = true
		s.LibreTranslate.LibreTranslateConfig = *cfg.LibreTranslate
	}
	if cfg.HyMT != nil {
		s.HyMT.Enabled = true
		s.HyMT.HyMTConfig = *cfg.HyMT
	}
	return s
}

func (s *TranslateProviderSettings) Validate() error {
	if s == nil {
		return fmt.Errorf("translate settings must not be nil")
	}
	valid := map[string]bool{"caiyun": true, "google_web": true, "tencent": true, "baidu": true, "youdao": true, "mymemory": true, "libretranslate": true, "hymt": true}
	seen := map[string]bool{}
	for _, name := range s.Priority {
		if !valid[name] || seen[name] {
			return fmt.Errorf("unknown or duplicate translate provider in priority")
		}
		seen[name] = true
	}
	if len(seen) != len(valid) {
		return fmt.Errorf("priority must contain all translation adapters")
	}
	if s.Tencent.Enabled && (strings.TrimSpace(s.Tencent.SecretID) == "" || strings.TrimSpace(s.Tencent.SecretKey) == "") {
		return fmt.Errorf("tencent provider requires secret_id and secret_key")
	}
	if s.Baidu.Enabled && (strings.TrimSpace(s.Baidu.AppID) == "" || strings.TrimSpace(s.Baidu.Secret) == "") {
		return fmt.Errorf("baidu provider requires app_id and secret")
	}
	if s.Youdao.Enabled && (strings.TrimSpace(s.Youdao.AppKey) == "" || strings.TrimSpace(s.Youdao.AppSecret) == "") {
		return fmt.Errorf("youdao provider requires app_key and app_secret")
	}
	for _, instance := range []struct {
		enabled       bool
		name, address string
	}{{s.HyMT.Enabled, "hymt", s.HyMT.BaseURL}, {s.LibreTranslate.Enabled, "libretranslate", s.LibreTranslate.BaseURL}} {
		if !instance.enabled {
			continue
		}
		u, err := url.Parse(instance.address)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("%s requires a valid HTTP instance URL without credentials", instance.name)
		}
	}
	if s.GoogleWeb.ProxyURL != "" {
		u, err := url.Parse(s.GoogleWeb.ProxyURL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") || u.User != nil {
			return fmt.Errorf("google_web proxy must be an HTTP or SOCKS5 URL without credentials")
		}
	}
	return nil
}

// ToTranslateConfig 转换为聚合器配置，按 Priority 顺序注册已启用的服务商。
func (s *TranslateProviderSettings) ToTranslateConfig() *translate.Config {
	cfg := &translate.Config{}
	if s == nil || !s.Enabled {
		return cfg
	}
	cfg.Priority = translate.OrderPriority(s.Priority)
	if s.Caiyun.Enabled {
		cfg.EnableFreeProviders = true
		cfg.Caiyun = &translate.CaiyunConfig{Token: s.Caiyun.Token}
	}
	if s.Tencent.Enabled {
		cfg.Tencent = &translate.TencentConfig{SecretID: s.Tencent.SecretID, SecretKey: s.Tencent.SecretKey, Region: s.Tencent.Region}
	}
	if s.Baidu.Enabled {
		cfg.Baidu = &translate.BaiduConfig{AppID: s.Baidu.AppID, Secret: s.Baidu.Secret}
	}
	if s.Youdao.Enabled {
		cfg.Youdao = &translate.YoudaoConfig{AppKey: s.Youdao.AppKey, AppSecret: s.Youdao.AppSecret}
	}
	if s.GoogleWeb.Enabled {
		cfg.GoogleWeb = &s.GoogleWeb.GoogleWebConfig
	}
	if s.MyMemory.Enabled {
		cfg.MyMemory = &s.MyMemory.MyMemoryConfig
	}
	if s.LibreTranslate.Enabled {
		cfg.LibreTranslate = &s.LibreTranslate.LibreTranslateConfig
	}
	if s.HyMT.Enabled {
		cfg.HyMT = &s.HyMT.HyMTConfig
	}
	return cfg
}

// Masked 返回脱敏副本，用于回传给前端。
func (s *TranslateProviderSettings) Masked() map[string]any {
	if s == nil {
		return map[string]any{}
	}
	mask := func(v string) string {
		if v == "" {
			return ""
		}
		return "***"
	}
	return map[string]any{
		"enabled":        s.Enabled,
		"priority":       translate.OrderPriority(s.Priority),
		"caiyun":         map[string]any{"enabled": s.Caiyun.Enabled, "token_set": s.Caiyun.Token != ""},
		"tencent":        map[string]any{"enabled": s.Tencent.Enabled, "secret_id": mask(s.Tencent.SecretID), "secret_key_set": s.Tencent.SecretKey != "", "region": s.Tencent.Region},
		"baidu":          map[string]any{"enabled": s.Baidu.Enabled, "app_id": s.Baidu.AppID, "secret_set": s.Baidu.Secret != ""},
		"youdao":         map[string]any{"enabled": s.Youdao.Enabled, "app_key": s.Youdao.AppKey, "app_secret_set": s.Youdao.AppSecret != ""},
		"google_web":     map[string]any{"enabled": s.GoogleWeb.Enabled, "proxy_url": s.GoogleWeb.ProxyURL},
		"mymemory":       map[string]any{"enabled": s.MyMemory.Enabled, "email": s.MyMemory.Email, "api_key_set": s.MyMemory.APIKey != ""},
		"libretranslate": map[string]any{"enabled": s.LibreTranslate.Enabled, "base_url": s.LibreTranslate.BaseURL, "api_key_set": s.LibreTranslate.APIKey != ""},
		"hymt":           map[string]any{"enabled": s.HyMT.Enabled, "base_url": s.HyMT.BaseURL, "api_key_set": s.HyMT.APIKey != ""},
	}
}

func (s *SettingService) GetTranslateProviderSettings(ctx context.Context) (*TranslateProviderSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultTranslateProviderSettings(), nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyTranslateProviders)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && raw == "") {
		return DefaultTranslateProviderSettings(), nil
	}
	if err != nil {
		return nil, err
	}
	settings := DefaultTranslateProviderSettings()
	if err := json.Unmarshal([]byte(raw), settings); err != nil {
		return nil, fmt.Errorf("decode translate providers: %w", err)
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	return settings, nil
}

func (s *SettingService) SetTranslateProviderSettings(ctx context.Context, settings *TranslateProviderSettings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeyTranslateProviders, string(data))
}

// GetTranslationAggregator 读取共享配置，仅在配置变化时重建客户端，复用连接并支持多实例即时生效。
func (s *SettingService) GetTranslationAggregator(ctx context.Context) (*translate.Aggregator, error) {
	settings, err := s.GetTranslateProviderSettings(ctx)
	if err != nil {
		return nil, err
	}
	cfg := settings.ToTranslateConfig()
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256(encoded)
	s.translateRuntimeMu.Lock()
	defer s.translateRuntimeMu.Unlock()
	if s.translateRuntime == nil || s.translateRuntimeHash != fingerprint {
		s.translateRuntime = translate.NewAggregator(cfg)
		s.translateRuntimeHash = fingerprint
	}
	return s.translateRuntime, nil
}
