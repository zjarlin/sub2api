package service

import (
	"strings"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// 内置适配器配置在进程启动时注入一次，之后只读，供账号地址解析与密钥补全使用。
//
//nolint:gochecknoglobals // 启动期写入、运行期只读的进程级配置。
var builtinAdapterConfig atomic.Pointer[config.BuiltinAdapterConfig]

// SetBuiltinAdapterConfig 在服务装配阶段注入内置适配器配置。
func SetBuiltinAdapterConfig(cfg *config.BuiltinAdapterConfig) {
	if cfg == nil {
		builtinAdapterConfig.Store(nil)
		return
	}
	clone := *cfg
	builtinAdapterConfig.Store(&clone)
}

// BuiltinAdapterEnabled 报告是否启用了内置适配器。
func BuiltinAdapterEnabled() bool {
	cfg := builtinAdapterConfig.Load()
	return cfg != nil && cfg.Enabled
}

// BuiltinAdapterBaseURLForPlatform 返回平台对应的内置适配器地址；
// 未启用或平台不适用时返回空串。供 handler 在账号未显式配置地址时兜底。
func BuiltinAdapterBaseURLForPlatform(platform string) string {
	return builtinAdapterBaseURL(platform)
}

// builtinAdapterBaseURL 返回平台对应的内置适配器地址；未启用或平台不适用时返回空串。
func builtinAdapterBaseURL(platform string) string {
	cfg := builtinAdapterConfig.Load()
	if cfg == nil || !cfg.Enabled {
		return ""
	}
	switch platform {
	case PlatformDoubao:
		return strings.TrimRight(cfg.DesktopBaseURL(), "/")
	case PlatformTraework:
		return strings.TrimRight(cfg.TraeworkBaseURL(), "/")
	case PlatformWorkbuddy:
		return strings.TrimRight(cfg.WorkbuddyBaseURL(), "/")
	case PlatformZcode:
		return strings.TrimRight(cfg.ZcodeBaseURL(), "/")
	case PlatformLaya:
		return strings.TrimRight(cfg.LayaBaseURL(), "/")
	case PlatformJev:
		return strings.TrimRight(cfg.JevBaseURL(), "/")
	default:
		return ""
	}
}

// builtinAdapterAPIKey 返回平台对应的内置适配器共享密钥。
func builtinAdapterAPIKey(platform string) string {
	cfg := builtinAdapterConfig.Load()
	if cfg == nil || !cfg.Enabled {
		return ""
	}
	switch platform {
	case PlatformDoubao:
		return strings.TrimSpace(cfg.DesktopKey)
	case PlatformTraework:
		return strings.TrimSpace(cfg.TraeworkKey)
	case PlatformWorkbuddy:
		return strings.TrimSpace(cfg.WorkbuddyKey)
	case PlatformZcode:
		return strings.TrimSpace(cfg.ZcodeKey)
	case PlatformLaya:
		return strings.TrimSpace(cfg.LayaKey)
	case PlatformJev:
		return strings.TrimSpace(cfg.JevKey)
	default:
		return ""
	}
}

// applyBuiltinAdapterCredentials 在内置模式下补齐适配器地址与密钥。
// 显式填写的值优先，仅补齐缺失项，避免覆盖用户自定义中转地址。
func applyBuiltinAdapterCredentials(platform string, credentials map[string]any) {
	if credentials == nil {
		return
	}
	baseURL := builtinAdapterBaseURL(platform)
	if baseURL == "" {
		return
	}
	if existing, _ := credentials["base_url"].(string); strings.TrimSpace(existing) == "" {
		credentials["base_url"] = baseURL + "/v1"
	}
	if existing, _ := credentials["api_key"].(string); strings.TrimSpace(existing) == "" {
		if key := builtinAdapterAPIKey(platform); key != "" {
			credentials["api_key"] = key
		}
	}
}
