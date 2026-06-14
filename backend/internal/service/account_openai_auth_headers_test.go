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
	if got := account.GetOpenAIBaseURL(); got != "http://127.0.0.1:18081/v1" {
		t.Fatalf("base url = %q, want openai-local-proxy default base url", got)
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
	for _, vendor := range []string{"deepseek", "gemini", "mimo", "ollama", "openrouter", "trae"} {
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

func TestAccountDeepSeekDefaults(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor": "deepseek",
		},
	}

	if got := account.GetOpenAIBaseURL(); got != "https://api.deepseek.com" {
		t.Fatalf("base url = %q, want DeepSeek default base url", got)
	}
	if !account.ShouldUseOpenAIChatCompletionsUpstream() {
		t.Fatal("DeepSeek should use chat/completions upstream")
	}
	if got := account.GetOpenAIAuthHeaderName(); got != "authorization" {
		t.Fatalf("auth header name = %q, want %q", got, "authorization")
	}
	if got := account.GetOpenAIAuthScheme(); got != "bearer" {
		t.Fatalf("auth scheme = %q, want %q", got, "bearer")
	}
	mapping := account.GetModelMapping()
	expectedMappings := map[string]string{
		"gpt-5.5":           "deepseek-v4-pro",
		"gpt-5.4":           "deepseek-v4-flash",
		"deepseek-v4-pro":   "deepseek-v4-pro",
		"deepseek-v4-flash": "deepseek-v4-flash",
		"deepseek-chat":     "deepseek-v4-flash",
		"deepseek-reasoner": "deepseek-v4-flash",
	}
	for from, to := range expectedMappings {
		if got := mapping[from]; got != to {
			t.Fatalf("mapping[%q] = %q, want %q", from, got, to)
		}
	}
}

func TestAccountOpenRouterDefaults(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor": "openrouter",
		},
	}

	if got := account.GetOpenAIBaseURL(); got != "https://openrouter.ai/api/v1" {
		t.Fatalf("base url = %q, want OpenRouter default base url", got)
	}
	if !account.ShouldUseOpenAIChatCompletionsUpstream() {
		t.Fatal("OpenRouter should use chat/completions upstream")
	}
}

func TestAccountOpenRouterBaseURLUsesChatCompletionsWithoutVendor(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://openrouter.ai/api/v1",
		},
	}

	if !account.ShouldUseOpenAIChatCompletionsUpstream() {
		t.Fatal("OpenRouter base URL should use chat/completions upstream")
	}
}

func TestAccountOllamaDefaults(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor": "ollama",
		},
	}

	if !account.AllowsEmptyOpenAIApiKey() {
		t.Fatal("ollama should allow an empty API key")
	}
	if got := account.GetOpenAIBaseURL(); got != "http://127.0.0.1:11434/v1" {
		t.Fatalf("base url = %q, want ollama default base url when base_url is unset", got)
	}
	if got := account.GetOpenAIVendor(); got != "ollama" {
		t.Fatalf("vendor = %q, want ollama", got)
	}
}
