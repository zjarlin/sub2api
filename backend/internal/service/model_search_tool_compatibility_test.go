package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelAccountSearchToolsPreserveNativeProtocol(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account Account
		allowed bool
	}{
		{"native", Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_responses_supported": true}}, true},
		{"codex", Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, true},
		{"chat bridge", Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_responses_supported": false}}, false},
		{"zcode", Account{Platform: PlatformZcode, Type: AccountTypeAPIKey}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, body := range []string{
				`{"tools":[{"type":"web_search"}]}`,
				`{"tools":[{"type":"web_search_preview"}],"tool_choice":{"type":"web_search_preview"}}`,
				`{"tools":[{"type":"namespace","name":"search","tools":[{"type":"web_search"}]}]}`,
				`{"input":[{"type":"additional_tools","tools":[{"type":"web_search"}]}]}`,
			} {
				require.Equal(t, tc.allowed, ModelAccountCompatible(&tc.account, "model", []byte(body)))
				require.Equal(t, tc.allowed, AutoModelRequestAccountCompatible(context.Background(), &tc.account, "model", []byte(body)))
				require.Equal(t, tc.allowed, ModelFallbackAccountCompatible(&tc.account, "model", []byte(body)))
			}
			body := []byte(`{"tools":[{"type":"function","name":"web_search","parameters":{"type":"object","properties":{"type":{"const":"web_search"}}}}]}`)
			require.True(t, ModelAccountCompatible(&tc.account, "model", body), "客户端函数工具不要求服务端搜索")
		})
	}
}

func TestSearchToolsMatchGrokAndOpenCodeActualProtocol(t *testing.T) {
	body := []byte(`{"tools":[{"type":"web_search"}]}`)
	grok := &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey}
	require.True(t, ModelAccountCompatible(grok, "grok-4.5", body))
	require.False(t, ModelAccountCompatible(grok, "grok-4.5", []byte(`{"input":[{"type":"additional_tools","tools":[{"type":"web_search_preview"}]}]}`)))
	account := &Account{Platform: PlatformOpenCodeGo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"public-chat": "deepseek-v4.1-flash", "public-native": "gpt-5.5"}}}
	require.False(t, ModelAccountCompatible(account, "public-chat", body))
	require.True(t, ModelAccountCompatible(account, "public-native", body))
	account.Credentials["protocol_rules"] = []any{map[string]any{"pattern": "gpt-*", "protocol": "chat_completions"}}
	require.False(t, ModelAccountCompatible(account, "public-native", body))
}

func TestSearchToolsRejectOpenRouterHostedSearch(t *testing.T) {
	body := []byte(`{"tools":[{"type":"web_search"}]}`)
	openrouter := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://openrouter.ai/api/v1",
			"model_mapping": map[string]any{
				"deepseek-v4.1-flash": "cohere/north-mini-code:free",
			},
		},
	}
	require.False(t, ModelAccountCompatible(openrouter, "deepseek-v4.1-flash", body))
	require.False(t, AutoModelRequestAccountCompatible(context.Background(), openrouter, "deepseek-v4.1-flash", body))
	require.False(t, ModelFallbackAccountCompatible(openrouter, "deepseek-v4.1-flash", body))

	plain := []byte(`{"input":"hello"}`)
	require.True(t, ModelAccountCompatible(openrouter, "deepseek-v4.1-flash", plain))

	clientFunction := []byte(`{"tools":[{"type":"function","name":"web_search"}]}`)
	require.True(t, ModelAccountCompatible(openrouter, "deepseek-v4.1-flash", clientFunction))
}

func TestAccountUsesOpenRouterMatchesOnlyConfiguredHost(t *testing.T) {
	require.True(t, accountUsesOpenRouter(&Account{Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://openrouter.ai/api/v1"}}))
	require.True(t, accountUsesOpenRouter(&Account{Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://proxy.openrouter.ai/v1"}}))
	require.False(t, accountUsesOpenRouter(&Account{Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://openrouter.ai.example.com/v1"}}))
	require.False(t, accountUsesOpenRouter(&Account{Type: AccountTypeOAuth, Credentials: map[string]any{"base_url": "https://openrouter.ai/api/v1"}}))
}
