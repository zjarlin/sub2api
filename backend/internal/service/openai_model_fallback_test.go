package service

import "testing"

func TestAccountIsModelSupported_OpenAIImplicitGpt55Fallback(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.4": "pool:smart",
			},
		},
	}

	if !account.IsModelSupported("gpt-5.5") {
		t.Fatal("gpt-5.5 should fall back to gpt-5.4 model support for OpenAI API key accounts")
	}
	if !account.IsModelSupported("openai/gpt-5.5") {
		t.Fatal("openai/gpt-5.5 should fall back to gpt-5.4 model support for OpenAI API key accounts")
	}
}

func TestAccountIsModelSupported_OpenAIImplicitGpt55FallbackDoesNotAffectOAuth(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.4": "pool:smart",
			},
		},
	}

	if account.IsModelSupported("gpt-5.5") {
		t.Fatal("gpt-5.5 should not silently fall back for OAuth accounts")
	}
}

func TestAccountResolveMappedModel_OpenAIImplicitGpt55Fallback(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.4": "pool:smart",
			},
		},
	}

	mappedModel, matched := account.ResolveMappedModel("gpt-5.5")
	if !matched {
		t.Fatal("gpt-5.5 should resolve through gpt-5.4 fallback mapping")
	}
	if mappedModel != "pool:smart" {
		t.Fatalf("ResolveMappedModel(gpt-5.5) = %q, want %q", mappedModel, "pool:smart")
	}
}

func TestResolveOpenAIForwardModel_OpenAIImplicitGpt55Fallback(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.4": "pool:smart",
			},
		},
	}

	if got := resolveOpenAIForwardModel(account, "gpt-5.5", ""); got != "pool:smart" {
		t.Fatalf("resolveOpenAIForwardModel(...) = %q, want %q", got, "pool:smart")
	}
}
