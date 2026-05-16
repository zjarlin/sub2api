package service

import "testing"

func TestAccountBuildOpenAIAuthHeaders_DefaultBearer(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
	}
	account.Credentials = map[string]any{
		"api_key": "sk-test",
	}

	headers := account.BuildOpenAIAuthHeaders("sk-test")
	if got := headers.Get("authorization"); got != "Bearer sk-test" {
		t.Fatalf("authorization header = %q, want %q", got, "Bearer sk-test")
	}
	if got := account.GetOpenAIVendor(); got != "" {
		t.Fatalf("vendor = %q, want empty", got)
	}
	if got := account.GetOpenAIAuthHeaderName(); got != "authorization" {
		t.Fatalf("auth header name = %q, want %q", got, "authorization")
	}
	if got := account.GetOpenAIAuthScheme(); got != "bearer" {
		t.Fatalf("auth scheme = %q, want %q", got, "bearer")
	}
}

func TestAccountBuildOpenAIAuthHeaders_MimoDefaultsToRawAPIKey(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
	}
	account.Credentials = map[string]any{
		"api_key": "mimo-live-key",
		"vendor":  "mimo",
	}

	headers := account.BuildOpenAIAuthHeaders("mimo-live-key")
	if got := headers.Get("api-key"); got != "mimo-live-key" {
		t.Fatalf("api-key header = %q, want %q", got, "mimo-live-key")
	}
	if got := headers.Get("authorization"); got != "" {
		t.Fatalf("authorization header = %q, want empty", got)
	}
	if got := account.GetOpenAIAuthHeaderName(); got != "api-key" {
		t.Fatalf("auth header name = %q, want %q", got, "api-key")
	}
	if got := account.GetOpenAIAuthScheme(); got != "raw" {
		t.Fatalf("auth scheme = %q, want %q", got, "raw")
	}
}

func TestAccountBuildOpenAIAuthHeaders_CustomHeaderAndScheme(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
	}
	account.Credentials = map[string]any{
		"api_key":     "token-123",
		"vendor":      "custom",
		"auth_header": "X-Api-Key",
		"auth_scheme": "Token",
	}

	headers := account.BuildOpenAIAuthHeaders("token-123")
	if got := headers.Get("x-api-key"); got != "token token-123" {
		t.Fatalf("x-api-key header = %q, want %q", got, "token token-123")
	}
	if got := account.GetOpenAIAuthHeaderName(); got != "x-api-key" {
		t.Fatalf("auth header name = %q, want %q", got, "x-api-key")
	}
	if got := account.GetOpenAIAuthScheme(); got != "token" {
		t.Fatalf("auth scheme = %q, want %q", got, "token")
	}
}

func TestAccountOpenAILocalProxyDefaults(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor": "openai-local-proxy",
		},
	}

	if !account.AllowsEmptyOpenAIApiKey() {
		t.Fatal("openai-local-proxy should allow an empty proxy auth token")
	}
	if account.ShouldUseOpenAIChatCompletionsUpstream() {
		t.Fatal("openai-local-proxy should expose its own Responses compatibility")
	}
	mapping := account.GetModelMapping()
	expectedMappings := map[string]string{
		"smart":               "pool:smart",
		"gpt-4o":              "pool:smart",
		"gpt-4o-mini":         "pool:smart",
		"gpt-5.4":             "pool:smart",
		"doubao":              "doubao:doubao",
		"doubao-pro":          "doubao:doubao-pro",
		"kimi-k2.5":           "kimi:kimi-k2.5",
		"gemini-2.5-flash":    "gemini:gemini-2.5-flash",
		"mimo-v2.5":           "mimo:mimo-v2.5",
		"claude-sonnet-4":     "opencode/nemotron-3-super-free",
		"opencode/big-pickle": "opencode/big-pickle",
	}

	for model, expected := range expectedMappings {
		if got := mapping[model]; got != expected {
			t.Fatalf("mapping[%q] = %q, want %q", model, got, expected)
		}
	}
}

func TestAccountOpenAIVendorChatCompletionsPreference(t *testing.T) {
	for _, vendor := range []string{"gemini", "mimo", "trae"} {
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"vendor": vendor,
			},
		}
		if !account.ShouldUseOpenAIChatCompletionsUpstream() {
			t.Fatalf("vendor %q should prefer chat/completions upstream", vendor)
		}
	}
}
