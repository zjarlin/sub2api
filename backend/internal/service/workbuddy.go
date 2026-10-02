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
	if platform == PlatformCursor {
		if accountType != AccountTypeAPIKey {
			return infraerrors.BadRequest("INVALID_CURSOR_CREDENTIALS", "cursor requires an API key account")
		}
		key, _ := credentials["api_key"].(string)
		if strings.TrimSpace(key) == "" {
			return infraerrors.BadRequest("INVALID_CURSOR_CREDENTIALS", "cursor requires a Cursor Dashboard API key")
		}
		protocol, _ := credentials["api_protocol"].(string)
		if protocol != "" && protocol != APIProtocolChatCompletions {
			return infraerrors.BadRequest("INVALID_CURSOR_CREDENTIALS", "cursor only supports chat_completions")
		}
		baseURL, _ := credentials["base_url"].(string)
		if strings.TrimSpace(baseURL) == "" && !BuiltinAdapterEnabled() {
			return infraerrors.BadRequest("INVALID_CURSOR_CREDENTIALS", "cursor requires a base_url when the built-in adapter is disabled")
		}
		return nil
	}
	if platform == PlatformWindsurf {
		if accountType != AccountTypeAPIKey {
			return infraerrors.BadRequest("INVALID_WINDSURF_CREDENTIALS", "windsurf requires an API key account")
		}
		key, _ := credentials["api_key"].(string)
		if strings.TrimSpace(key) == "" {
			return infraerrors.BadRequest("INVALID_WINDSURF_CREDENTIALS", "windsurf requires a Windsurf session token")
		}
		protocol, _ := credentials["api_protocol"].(string)
		if protocol != "" && protocol != APIProtocolChatCompletions {
			return infraerrors.BadRequest("INVALID_WINDSURF_CREDENTIALS", "windsurf only supports chat_completions")
		}
		baseURL, _ := credentials["base_url"].(string)
		if strings.TrimSpace(baseURL) == "" && !BuiltinAdapterEnabled() {
			return infraerrors.BadRequest("INVALID_WINDSURF_CREDENTIALS", "windsurf requires a base_url when the built-in adapter is disabled")
		}
		return nil
	}
	if platform == PlatformArena {
		return validateArenaCredentials(accountType, credentials)
	}
	if platform == PlatformVibex {
		return validateVibexCredentials(accountType, credentials)
	}
	if platform == PlatformTraework {
		return validateTraeworkCredentials(platform, accountType, credentials)
	}
	if platform == PlatformZcode {
		return validateZcodeCredentials(platform, accountType, credentials)
	}
	if platform == PlatformDeepseekWeb {
		if accountType != AccountTypeAPIKey || !BuiltinAdapterEnabled() {
			return infraerrors.BadRequest("INVALID_DEEPSEEK_WEB_CREDENTIALS", "deepseek_web requires the built-in adapter")
		}
		key, _ := credentials["api_key"].(string)
		if strings.TrimSpace(key) == "" {
			return infraerrors.BadRequest("INVALID_DEEPSEEK_WEB_CREDENTIALS", "deepseek_web requires the adapter key")
		}
		protocol, _ := credentials["api_protocol"].(string)
		if protocol != "" && protocol != APIProtocolChatCompletions {
			return infraerrors.BadRequest("INVALID_DEEPSEEK_WEB_CREDENTIALS", "deepseek_web only supports chat_completions")
		}
		return nil
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
