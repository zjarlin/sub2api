package service

import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const DefaultWorkbuddyModel = "glm-5.2"

func DefaultWorkbuddyModelIDs() []string { return []string{DefaultWorkbuddyModel} }

func (a *Account) IsWorkbuddy() bool { return a != nil && a.Platform == PlatformWorkbuddy }

// validateBuiltinChatCredentials 统一校验内置适配器账号。
func validateBuiltinChatCredentials(platform, accountType string, credentials map[string]any) error {
	if platform == PlatformTraework {
		return validateTraeworkCredentials(platform, accountType, credentials)
	}
	if platform == PlatformZcode {
		return validateZcodeCredentials(platform, accountType, credentials)
	}
	if platform == PlatformQoder {
		return validateQoderCredentials(platform, accountType, credentials)
	}
	// System One 决策模型（Laya / JEV）与上述适配器同属内置账号池。
	if IsSystemOneDecisionPlatform(platform) {
		return validateSystemOneDecisionCredentials(platform, accountType, credentials)
	}
	if platform != PlatformWorkbuddy {
		return nil
	}
	if accountType != AccountTypeAPIKey || !BuiltinAdapterEnabled() {
		return infraerrors.BadRequest("INVALID_WORKBUDDY_CREDENTIALS", "workbuddy requires an API key account and the built-in adapter enabled")
	}
	key, _ := credentials["api_key"].(string)
	if strings.TrimSpace(key) == "" {
		return infraerrors.BadRequest("INVALID_WORKBUDDY_CREDENTIALS", "workbuddy requires the adapter API key")
	}
	protocol, _ := credentials["api_protocol"].(string)
	if protocol != "" && protocol != APIProtocolChatCompletions {
		return infraerrors.BadRequest("INVALID_WORKBUDDY_CREDENTIALS", "workbuddy only supports chat_completions")
	}
	return nil
}
