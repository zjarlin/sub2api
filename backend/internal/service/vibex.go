package service

import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (a *Account) IsVibex() bool { return a != nil && a.Platform == PlatformVibex }

func validateVibexCredentials(accountType string, credentials map[string]any) error {
	if accountType != AccountTypeAPIKey || !BuiltinAdapterEnabled() {
		return infraerrors.BadRequest("INVALID_VIBEX_CREDENTIALS", "VibeX requires an API key account and the built-in adapter enabled")
	}
	key, _ := credentials["api_key"].(string)
	protocol, _ := credentials["api_protocol"].(string)
	if strings.TrimSpace(key) == "" || (protocol != "" && protocol != APIProtocolChatCompletions) {
		return infraerrors.BadRequest("INVALID_VIBEX_CREDENTIALS", "VibeX requires the adapter key and chat_completions protocol")
	}
	return nil
}
