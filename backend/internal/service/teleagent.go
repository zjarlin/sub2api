package service

import (
	"regexp"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const DefaultTeleagentModel = "chat-pro"

var teleagentAccountKeyPattern = regexp.MustCompile(`^ta-[a-f0-9]{64}$`)

func DefaultTeleagentModelIDs() []string {
	return []string{DefaultTeleagentModel, "chat-flagship", "chat-flash"}
}

func (a *Account) IsTeleagent() bool {
	return a != nil && a.Platform == PlatformTeleagent
}

func validateTeleagentCredentials(accountType string, credentials map[string]any) error {
	if accountType != AccountTypeAPIKey {
		return infraerrors.BadRequest("INVALID_TELEAGENT_CREDENTIALS", "teleagent requires an API key account")
	}
	key, _ := credentials["api_key"].(string)
	key = strings.TrimSpace(key)
	if !teleagentAccountKeyPattern.MatchString(key) || key == builtinAdapterAPIKey(PlatformTeleagent) {
		return infraerrors.BadRequest("INVALID_TELEAGENT_CREDENTIALS", "teleagent requires a local account key from adapter login")
	}
	protocol, _ := credentials["api_protocol"].(string)
	if protocol != "" && protocol != APIProtocolChatCompletions {
		return infraerrors.BadRequest("INVALID_TELEAGENT_CREDENTIALS", "teleagent only supports chat_completions")
	}
	baseURL, _ := credentials["base_url"].(string)
	if strings.TrimSpace(baseURL) == "" && !BuiltinAdapterEnabled() {
		return infraerrors.BadRequest("INVALID_TELEAGENT_CREDENTIALS", "teleagent requires a base_url when the built-in adapter is disabled")
	}
	return nil
}
