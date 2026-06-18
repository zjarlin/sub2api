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
	for _, vendor := range []string{"deepseek", "gemini", "mimo", "ollama", "opencode", "openrouter", "trae"} {
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

func TestAccountOpenCodeVendorUsesChatCompletions(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor": "opencode",
		},
	}

	if !account.ShouldUseOpenAIChatCompletionsUpstream() {
		t.Fatal("OpenCode vendor should use chat/completions upstream")
	}
	if got := account.GetOpenAIBaseURL(); got != "https://opencode.ai/zen/v1" {
		t.Fatalf("base url = %q, want OpenCode Zen default base url", got)
	}
	mapping := account.GetModelMapping()
	expectedMappings := map[string]string{
		"deepseek-v4-flash-free": "deepseek-v4-flash-free",
		"big-pickle":             "big-pickle",
		"gpt-*":                  "deepseek-v4-flash-free",
		"claude-*":               "deepseek-v4-flash-free",
	}
	for from, to := range expectedMappings {
		if got := mapping[from]; got != to {
			t.Fatalf("mapping[%q] = %q, want %q", from, got, to)
		}
	}
	if got := account.GetMappedModel("gpt-5.4"); got != "deepseek-v4-flash-free" {
		t.Fatalf("mapped gpt-5.4 = %q, want deepseek-v4-flash-free", got)
	}
	if account.IsModelSupported("deepseek-v4-pro") {
		t.Fatal("OpenCode Zen default mapping should only support free models and mapped GPT/Claude aliases")
	}
}

func TestAccountOpenCodeGoVendorUsesMiniMaxMapping(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor": "opencode-go",
		},
	}

	if account.ShouldUseOpenAIChatCompletionsUpstream() {
		t.Fatal("official OpenCode Go vendor should use its protocol-specific endpoint mapping")
	}
	if got := account.GetOpenAIBaseURL(); got != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("base url = %q, want official OpenCode Go default base url", got)
	}
	mapping := account.GetModelMapping()
	expectedMappings := map[string]string{
		"opencode-go/minimax-m3":      "minimax-m3",
		"minimax-m3":                  "minimax-m3",
		"opencode-go/kimi-k2.7-code":  "kimi-k2.7",
		"opencode-go/deepseek-v4-pro": "deepseek-v4-pro",
		"opencode-go/qwen3.7-max":     "qwen3.7-max",
		"gpt-*":                       "minimax-m3",
		"claude-*":                    "minimax-m3",
	}
	for from, to := range expectedMappings {
		if got := mapping[from]; got != to {
			t.Fatalf("mapping[%q] = %q, want %q", from, got, to)
		}
	}
	if got := account.GetMappedModel("gpt-5.4"); got != "minimax-m3" {
		t.Fatalf("mapped gpt-5.4 = %q, want minimax-m3", got)
	}
	if got := account.GetMappedModel("opencode-go/minimax-m3"); got != "minimax-m3" {
		t.Fatalf("mapped opencode-go/minimax-m3 = %q, want minimax-m3", got)
	}
}

func TestAccountOpenCodeGoLocalServerUsesLocalChatPreference(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor":   "opencode-go",
			"base_url": "http://host.docker.internal:4096",
		},
	}

	if !account.ShouldUseOpenAIChatCompletionsUpstream() {
		t.Fatal("local OpenCode serve account should use local server routing")
	}
}

func TestAccountChatCompletionsBaseURLPreferencesWithoutVendor(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
	}{
		{name: "opencode zen", baseURL: "https://opencode.ai/zen/v1"},
		{name: "opencode go", baseURL: "https://opencode.ai/zen/go/v1"},
		{name: "opencode legacy", baseURL: "https://api.opencode.ai/v1"},
		{name: "opencode local serve", baseURL: "http://host.docker.internal:4096"},
		{name: "openrouter", baseURL: "https://openrouter.ai/api/v1"},
		{name: "gemini openai compat", baseURL: "https://generativelanguage.googleapis.com/v1beta/openai/"},
		{name: "mimo", baseURL: "https://api.xiaomimimo.com/v1"},
		{name: "mimo token plan", baseURL: "https://api-mimo-share-token.xiaomimimo.com/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
				Credentials: map[string]any{
					"base_url": tt.baseURL,
				},
			}

			if !account.ShouldUseOpenAIChatCompletionsUpstream() {
				t.Fatalf("base URL %q should use chat/completions upstream", tt.baseURL)
			}
		})
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

func TestAccountGeminiDefaults(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor": "gemini",
		},
	}

	if got := account.GetOpenAIBaseURL(); got != "https://generativelanguage.googleapis.com/v1beta/openai" {
		t.Fatalf("base url = %q, want Gemini OpenAI-compatible default base url", got)
	}
	if !account.ShouldUseOpenAIChatCompletionsUpstream() {
		t.Fatal("Gemini should use chat/completions upstream")
	}
}

func TestAccountMimoDefaults(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"vendor": "mimo",
		},
	}

	if got := account.GetOpenAIBaseURL(); got != "https://api.xiaomimimo.com/v1" {
		t.Fatalf("base url = %q, want MiMo OpenAI-compatible default base url", got)
	}
	if !account.ShouldUseOpenAIChatCompletionsUpstream() {
		t.Fatal("MiMo should use chat/completions upstream")
	}
	if got := account.GetOpenAIAuthHeaderName(); got != "api-key" {
		t.Fatalf("auth header name = %q, want %q", got, "api-key")
	}
	if got := account.GetOpenAIAuthScheme(); got != "raw" {
		t.Fatalf("auth scheme = %q, want %q", got, "raw")
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
