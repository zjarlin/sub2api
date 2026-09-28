//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestVibexAccountIntegration(t *testing.T) {
	previous := builtinAdapterConfig.Load()
	t.Cleanup(func() { builtinAdapterConfig.Store(previous) })
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, VibexKey: "fixture-key"})
	credentials := map[string]any{"api_protocol": APIProtocolChatCompletions}
	applyBuiltinAdapterCredentials(PlatformVibex, credentials)
	require.Equal(t, "http://sub2api-vibex:7866/v1", credentials["base_url"])
	require.Equal(t, "fixture-key", credentials["api_key"])
	require.NoError(t, validateBuiltinChatCredentials(PlatformVibex, AccountTypeAPIKey, credentials))
	require.Error(t, validateBuiltinChatCredentials(PlatformVibex, AccountTypeOAuth, credentials))
	credentials["api_protocol"] = APIProtocolResponses
	require.Error(t, validateBuiltinChatCredentials(PlatformVibex, AccountTypeAPIKey, credentials))
	a := &Account{Platform: PlatformVibex, Type: AccountTypeAPIKey, Credentials: credentials}
	require.Equal(t, APIProtocolChatCompletions, a.GetAPIProtocol())
	require.True(t, IsCNProvider(PlatformVibex))
	require.True(t, IsAllowedQuotaPlatform(PlatformVibex))
	require.Equal(t, 1, normalizeAccountConcurrency(PlatformVibex, AccountTypeAPIKey, 8))
	require.True(t, mixedSchedulingTargetsPlatform(PlatformVibex, PlatformOpenAI))
	require.Empty(t, defaultModelsListCandidateIDs(PlatformVibex))
}
