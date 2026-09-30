package service

import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// System One 的一个账号平台可以接入两种上游；model 决定请求使用哪一种，
// credentials.systemone_provider 保存账号实际连接的上游，避免把调度平台拆成 Laya/JEV 两个。
const (
	SystemOneProviderLaya = "laya"
	SystemOneProviderJev  = "jev"

	DefaultLayaModel = "laya"
	DefaultJevModel  = "typesafe/jev"
)

func DefaultLayaModelIDs() []string {
	return []string{DefaultLayaModel, "laya-english", "laya-multilingual"}
}
func DefaultJevModelIDs() []string { return []string{DefaultJevModel} }

// SystemOneProvider 返回账号实际连接的上游。旧平台账号按原平台推断，使未迁移缓存可安全读取。
func (a *Account) SystemOneProvider() string {
	if a == nil {
		return ""
	}
	if provider, _ := a.Credentials["systemone_provider"].(string); provider == SystemOneProviderLaya || provider == SystemOneProviderJev {
		return provider
	}
	switch a.Platform {
	case PlatformLaya:
		return SystemOneProviderLaya
	case PlatformJev:
		return SystemOneProviderJev
	default:
		return ""
	}
}

func (a *Account) IsLaya() bool { return a != nil && a.SystemOneProvider() == SystemOneProviderLaya }
func (a *Account) IsJev() bool  { return a != nil && a.SystemOneProvider() == SystemOneProviderJev }

func IsSystemOneDecisionPlatform(platform string) bool {
	return platform == PlatformSystemOne || platform == PlatformLaya || platform == PlatformJev
}

func validateSystemOneDecisionCredentials(platform, accountType string, credentials map[string]any) error {
	if !IsSystemOneDecisionPlatform(platform) {
		return nil
	}
	provider, _ := credentials["systemone_provider"].(string)
	if platform == PlatformLaya {
		provider = SystemOneProviderLaya
	} else if platform == PlatformJev {
		provider = SystemOneProviderJev
	}
	if provider != SystemOneProviderLaya && provider != SystemOneProviderJev {
		return infraerrors.BadRequest("INVALID_SYSTEMONE_CREDENTIALS", "systemone_provider must be laya or jev")
	}
	if accountType != AccountTypeAPIKey {
		return infraerrors.BadRequest("INVALID_SYSTEMONE_CREDENTIALS", "systemone requires an API key account connected to a built-in System One adapter")
	}
	if !BuiltinAdapterEnabled() {
		return infraerrors.BadRequest("INVALID_SYSTEMONE_CREDENTIALS", "systemone requires the built-in System One adapter; enable it in the server configuration")
	}
	key, _ := credentials["api_key"].(string)
	if provider == SystemOneProviderJev && strings.TrimSpace(key) == "" {
		return infraerrors.BadRequest("INVALID_SYSTEMONE_CREDENTIALS", "jev requires the adapter API key")
	}
	protocol, _ := credentials["api_protocol"].(string)
	if protocol != "" && protocol != APIProtocolSystemOne {
		return infraerrors.BadRequest("INVALID_SYSTEMONE_CREDENTIALS", "systemone only supports the systemone upstream protocol")
	}
	return nil
}
