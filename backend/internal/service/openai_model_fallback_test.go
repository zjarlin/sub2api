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

func TestOpenAISchedulingExplicitMappingDoesNotUseVendorDefaultAlias(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor": "deepseek",
			"model_mapping": map[string]any{
				"deepseek-v4-pro": "deepseek-v4-pro",
			},
		},
	}

	if isOpenAIAccountModelSupportedForScheduling(account, "gpt-5.5") {
		t.Fatal("scheduler must not treat gpt-5.5 as supported when only deepseek-v4-pro is explicitly mapped")
	}
	if !isOpenAIAccountModelSupportedForScheduling(account, "deepseek-v4-pro") {
		t.Fatal("scheduler should support the explicitly mapped deepseek-v4-pro model")
	}
}

func TestOpenAIForwardModelStillUsesVendorDefaultAlias(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor": "deepseek",
			"model_mapping": map[string]any{
				"deepseek-v4-pro": "deepseek-v4-pro",
			},
		},
	}

	if got := resolveOpenAIForwardModel(account, "gpt-5.5", ""); got != "deepseek-v4-pro" {
		t.Fatalf("resolveOpenAIForwardModel(gpt-5.5) = %q, want deepseek-v4-pro", got)
	}
}

func TestOpenAISchedulingExplicitMappingRespectsVendorPrefix(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor": "deepseek",
			"model_mapping": map[string]any{
				"gpt-5.5": "deepseek-v4-pro",
			},
		},
	}

	if isOpenAIAccountModelSupportedForScheduling(account, "openai/gpt-5.5") {
		t.Fatal("scheduler must not route openai-prefixed models to a deepseek vendor account")
	}
}
