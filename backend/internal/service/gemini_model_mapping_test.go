package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeGeminiRequestedModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "openai gpt alias maps to gemini pro", input: "gpt-5.5", want: "gemini-2.5-pro"},
		{name: "codex alias maps to gemini pro", input: "gpt-5.3-codex", want: "gemini-2.5-pro"},
		{name: "reasoning suffix maps by base model", input: "gpt-5.4-xhigh", want: "gemini-2.5-pro"},
		{name: "models prefix normalizes", input: "models/gemini-2.5-pro", want: "gemini-2.5-pro"},
		{name: "vertex path normalizes", input: "projects/p/locations/us/publishers/google/models/gemini-2.5-flash", want: "gemini-2.5-flash"},
		{name: "gemini model keeps normalized", input: "Gemini-2.5-Pro", want: "gemini-2.5-pro"},
		{name: "unknown remains unresolved", input: "my-custom-model", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, normalizeGeminiRequestedModel(tt.input))
		})
	}
}

func TestResolveGeminiForwardModel(t *testing.T) {
	t.Parallel()

	t.Run("uses default openai alias mapping", func(t *testing.T) {
		account := &Account{Platform: PlatformGemini, Type: AccountTypeOAuth}
		require.Equal(t, "gemini-2.5-pro", resolveGeminiForwardModel(account, "gpt-5.5"))
	})

	t.Run("account explicit mapping wins", func(t *testing.T) {
		account := &Account{
			Platform: PlatformGemini,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5.5": "gemini-2.5-flash",
				},
			},
		}
		require.Equal(t, "gemini-2.5-flash", resolveGeminiForwardModel(account, "gpt-5.5"))
	})

	t.Run("unknown passthrough", func(t *testing.T) {
		account := &Account{Platform: PlatformGemini, Type: AccountTypeOAuth}
		require.Equal(t, "custom-model", resolveGeminiForwardModel(account, "custom-model"))
	})
}

func TestGeminiAccountSupportsRequestedModel_OpenAIAlias(t *testing.T) {
	t.Parallel()

	account := &Account{
		Platform: PlatformGemini,
		Status:   StatusActive,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gemini-2.5-pro": "gemini-2.5-pro",
			},
		},
	}

	require.True(t, geminiAccountSupportsRequestedModel(account, "gpt-5.5"))
	require.False(t, geminiAccountSupportsRequestedModel(account, "custom-model"))
}

func TestGeminiModelRateLimitUsesOpenAIAliasUpstreamModel(t *testing.T) {
	t.Parallel()

	resetAt := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	account := &Account{
		Platform: PlatformGemini,
		Extra: map[string]any{
			modelRateLimitsKey: map[string]any{
				"gemini-2.5-pro": map[string]any{
					"rate_limit_reset_at": resetAt,
				},
			},
		},
	}

	require.True(t, account.isModelRateLimitedWithContext(context.Background(), "gpt-5.5"))
	require.Equal(t, "gemini-2.5-pro", modelRateLimitKeyForUpstreamModelNotFound(context.Background(), account, "gpt-5.5"))
}
