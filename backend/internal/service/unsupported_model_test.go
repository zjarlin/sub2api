package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountModelSupportUsesPersistedNegativeCapability(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"public-model":  "upstream-model",
				"another-model": "another-upstream",
			},
		},
		Extra: map[string]any{
			UnsupportedModelsExtraKey: map[string]any{
				"upstream-model": map[string]any{"status_code": float64(404)},
			},
		},
	}

	require.False(t, account.IsModelSupported("public-model"))
	require.True(t, account.IsModelSupported("another-model"))
}

func TestAccountUnsupportedModelIgnoresStaleMappingInPassthroughMode(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"public-model": "stale-model"},
		},
		Extra: map[string]any{
			"openai_passthrough": true,
			UnsupportedModelsExtraKey: map[string]any{
				"public-model": map[string]any{"status_code": float64(404)},
			},
		},
	}

	require.False(t, account.IsModelSupported("public-model"))
	require.True(t, account.IsModelSupported("stale-model"))
	require.Equal(t, "public-model", observedUnsupportedModelKey(account, "public-model"))
}

func TestDeterministicUnsupportedModelError(t *testing.T) {
	require.True(t, isDeterministicUnsupportedModelError(404, []byte(`{"error":{"code":"model_not_found","message":"missing"}}`)))
	require.True(t, isDeterministicUnsupportedModelError(400, []byte(`{"error":{"message":"The model x is not supported"}}`)))
	require.False(t, isDeterministicUnsupportedModelError(400, []byte(`{"error":{"message":"Parameter tools is not supported for this model"}}`)))
	require.False(t, isDeterministicUnsupportedModelError(503, []byte(`{"error":{"message":"Service temporarily unavailable"}}`)))
}

func TestUnsupportedModelKeyRejectsOversizedValues(t *testing.T) {
	require.Empty(t, normalizeUnsupportedModelKey(string(make([]byte, unsupportedModelKeyMaxBytes+1))))
}
