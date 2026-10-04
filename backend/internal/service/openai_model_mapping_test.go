package service

import "testing"

func TestResolveOpenAIForwardModel(t *testing.T) {
	tests := []struct {
		name                        string
		account                     *Account
		requestedModel              string
		messagesDispatchMappedModel string
		expectedModel               string
	}{
		{
			name: "uses messages dispatch model for known claude family",
			account: &Account{
				Credentials: map[string]any{},
			},
			requestedModel:              "claude-opus-4-6",
			messagesDispatchMappedModel: "gpt-4o-mini",
			expectedModel:               "gpt-4o-mini",
		},
		{
			name: "uses exact messages dispatch model for unknown claude family",
			account: &Account{
				Credentials: map[string]any{},
			},
			requestedModel:              "claude-fable-5",
			messagesDispatchMappedModel: " gpt-5.6-sol ",
			expectedModel:               "gpt-5.6-sol",
		},
		{
			name:                        "nil account uses messages dispatch model",
			requestedModel:              "claude-fable-5",
			messagesDispatchMappedModel: "gpt-5.6-sol",
			expectedModel:               "gpt-5.6-sol",
		},
		{
			name:           "nil account without messages dispatch keeps requested model",
			requestedModel: "claude-fable-5",
			expectedModel:  "claude-fable-5",
		},
		{
			name: "ordinary unknown gpt model has no messages dispatch fallback",
			account: &Account{
				Credentials: map[string]any{},
			},
			requestedModel: "gpt6",
			expectedModel:  "gpt6",
		},
		{
			name: "account exact mapping overrides messages dispatch model",
			account: &Account{
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"claude-fable-5": "gpt-5.5",
					},
				},
			},
			requestedModel:              "claude-fable-5",
			messagesDispatchMappedModel: "gpt-5.6-sol",
			expectedModel:               "gpt-5.5",
		},
		{
			name: "account wildcard mapping overrides messages dispatch model",
			account: &Account{
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"claude-*": "gpt-5.4",
					},
				},
			},
			requestedModel:              "claude-fable-5",
			messagesDispatchMappedModel: "gpt-5.6-sol",
			expectedModel:               "gpt-5.4",
		},
		{
			name: "account passthrough mapping overrides messages dispatch model",
			account: &Account{
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"claude-fable-5": "claude-fable-5",
					},
				},
			},
			requestedModel:              "claude-fable-5",
			messagesDispatchMappedModel: "gpt-5.6-sol",
			expectedModel:               "claude-fable-5",
		},
		{
			name: "ordinary codex spark request keeps requested model",
			account: &Account{
				Credentials: map[string]any{},
			},
			requestedModel: "gpt-5.3-codex-spark",
			expectedModel:  "gpt-5.3-codex-spark",
		},
		{
			name: "ordinary gpt-5.5 request keeps requested model",
			account: &Account{
				Credentials: map[string]any{},
			},
			requestedModel: "gpt-5.5",
			expectedModel:  "gpt-5.5",
		},
		{
			name: "ordinary gpt-5.5-pro request keeps requested model",
			account: &Account{
				Credentials: map[string]any{},
			},
			requestedModel: "gpt-5.5-pro",
			expectedModel:  "gpt-5.5-pro",
		},
		{
			name: "ordinary compact-spelled gpt5.5 request keeps requested model",
			account: &Account{
				Credentials: map[string]any{},
			},
			requestedModel: "gpt5.5",
			expectedModel:  "gpt5.5",
		},
		{
			name: "ordinary namespaced gpt-5.5 request keeps requested model",
			account: &Account{
				Credentials: map[string]any{},
			},
			requestedModel: "openai/gpt-5.5",
			expectedModel:  "openai/gpt-5.5",
		},
		{
			name: "ordinary compact gpt-5.5 request keeps requested model",
			account: &Account{
				Credentials: map[string]any{},
			},
			requestedModel: "gpt-5.5-openai-compact",
			expectedModel:  "gpt-5.5-openai-compact",
		},
		{
			name: "whitespace-only messages dispatch model is ignored",
			account: &Account{
				Credentials: map[string]any{},
			},
			requestedModel:              "gpt-5.5",
			messagesDispatchMappedModel: "  ",
			expectedModel:               "gpt-5.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveOpenAIForwardModel(tt.account, tt.requestedModel, tt.messagesDispatchMappedModel); got != tt.expectedModel {
				t.Fatalf("resolveOpenAIForwardModel(...) = %q, want %q", got, tt.expectedModel)
			}
		})
	}
}

func TestResolveOpenAICompactForwardModel(t *testing.T) {
	tests := []struct {
		name          string
		account       *Account
		model         string
		expectedModel string
	}{
		{
			name:          "nil account keeps original model",
			account:       nil,
			model:         "gpt-5.4",
			expectedModel: "gpt-5.4",
		},
		{
			name: "missing compact mapping keeps original model",
			account: &Account{
				Credentials: map[string]any{},
			},
			model:         "gpt-5.4",
			expectedModel: "gpt-5.4",
		},
		{
			name: "exact compact mapping overrides model",
			account: &Account{
				Credentials: map[string]any{
					"compact_model_mapping": map[string]any{
						"gpt-5.4": "gpt-5.4-openai-compact",
					},
				},
			},
			model:         "gpt-5.4",
			expectedModel: "gpt-5.4-openai-compact",
		},
		{
			name: "wildcard compact mapping overrides model",
			account: &Account{
				Credentials: map[string]any{
					"compact_model_mapping": map[string]any{
						"gpt-5.*": "gpt-5-openai-compact",
					},
				},
			},
			model:         "gpt-5.4",
			expectedModel: "gpt-5-openai-compact",
		},
		{
			name: "passthrough compact mapping remains unchanged",
			account: &Account{
				Credentials: map[string]any{
					"compact_model_mapping": map[string]any{
						"gpt-5.4": "gpt-5.4",
					},
				},
			},
			model:         "gpt-5.4",
			expectedModel: "gpt-5.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveOpenAICompactForwardModel(tt.account, tt.model); got != tt.expectedModel {
				t.Fatalf("resolveOpenAICompactForwardModel(...) = %q, want %q", got, tt.expectedModel)
			}
		})
	}
}

func TestResolveOpenAIForwardMappedModels_CompactMappingPrecedence(t *testing.T) {
	conflictingMappings := map[string]any{
		"model_mapping":         map[string]any{"gpt-5.5": "gpt-5.4"},
		"compact_model_mapping": map[string]any{"gpt-5.5": "gpt-5.5-openai-compact"},
	}
	mappedOnlyCompact := map[string]any{
		"model_mapping":         map[string]any{"gpt-5.5": "gpt-5.4"},
		"compact_model_mapping": map[string]any{"gpt-5.4": "gpt-5.4-openai-compact"},
	}
	tests := []struct {
		name           string
		account        *Account
		requireCompact bool
		wantBilling    string
		wantUpstream   string
	}{
		{
			name: "compact uses client-visible model before ordinary mapping",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: conflictingMappings},
			requireCompact: true,
			wantBilling:    "gpt-5.4",
			wantUpstream:   "gpt-5.5-openai-compact",
		},
		{
			name: "non-compact uses ordinary mapping",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: conflictingMappings},
			wantBilling:  "gpt-5.4",
			wantUpstream: "gpt-5.4",
		},
		{
			name: "compact falls back to ordinary mapped model",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: mappedOnlyCompact},
			requireCompact: true,
			wantBilling:    "gpt-5.4",
			wantUpstream:   "gpt-5.4-openai-compact",
		},
		{
			name: "passthrough ignores ordinary mapping",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: conflictingMappings, Extra: map[string]any{"openai_passthrough": true}},
			requireCompact: true,
			wantBilling:    "gpt-5.5",
			wantUpstream:   "gpt-5.5-openai-compact",
		},
		{
			name: "raw chat fallback never applies compact mapping",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: conflictingMappings, Extra: map[string]any{"openai_responses_supported": false}},
			requireCompact: true,
			wantBilling:    "gpt-5.4",
			wantUpstream:   "gpt-5.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			billing, upstream := resolveOpenAIForwardMappedModels(tt.account, "gpt-5.5", tt.requireCompact)
			if billing != tt.wantBilling {
				t.Fatalf("billing model = %q, want %q", billing, tt.wantBilling)
			}
			if upstream != tt.wantUpstream {
				t.Fatalf("upstream model = %q, want %q", upstream, tt.wantUpstream)
			}
			if scheduler := resolveOpenAIAccountUpstreamModelForRequest(tt.account, "gpt-5.5", tt.requireCompact); scheduler != upstream {
				t.Fatalf("scheduler model %q disagrees with Forward model %q", scheduler, upstream)
			}
		})
	}
}

func TestCanonicalOpenAIAccountSchedulingModelMatchesForwardSemantics(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		model   string
		want    string
	}{
		{
			name:    "OpenAI OAuth applies Codex alias normalization",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth},
			model:   "gpt-5.6",
			want:    "gpt-5.6-sol",
		},
		{
			name: "OpenAI passthrough falls back to credentials mapping",
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"model_mapping": map[string]any{"public": "private"}},
				Extra:       map[string]any{"openai_passthrough": true}},
			model: "public",
			want:  "private",
		},
		{
			name:    "Grok OAuth does not inherit OpenAI Codex aliases",
			account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth},
			model:   "gpt-5.6",
			want:    "gpt-5.6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canonicalOpenAIAccountSchedulingModel(tt.account, tt.model); got != tt.want {
				t.Fatalf("canonical scheduling model = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveOpenAIErrorSchedulingModelPrefersActualUpstreamModel(t *testing.T) {
	if got := resolveOpenAIErrorSchedulingModel("gpt-5.4", "gpt-5.5-openai-compact"); got != "gpt-5.5-openai-compact" {
		t.Fatalf("error scheduling model = %q, want compact upstream model", got)
	}
	if got := resolveOpenAIErrorSchedulingModel("gpt-5.4", ""); got != "gpt-5.4" {
		t.Fatalf("empty upstream fallback = %q, want billing model", got)
	}
}

func TestNormalizeCodexModel(t *testing.T) {
	cases := map[string]string{
		"gpt-5.3-codex-spark":       "gpt-5.3-codex-spark",
		"gpt-5.3-codex-spark-high":  "gpt-5.3-codex-spark",
		"gpt-5.3-codex-spark-xhigh": "gpt-5.3-codex-spark",
		"gpt-5.3":                   "gpt-5.3-codex",
		"gpt-image-2":               "gpt-image-2",
		"gpt-5.4-nano":              "gpt-5.4-nano",
		"gpt-5.4-nano-high":         "gpt-5.4-nano",
		"gpt6":                      "gpt6",
		"claude-opus-4-6":           "claude-opus-4-6",
	}

	for input, expected := range cases {
		if got := normalizeCodexModel(input); got != expected {
			t.Fatalf("normalizeCodexModel(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestNormalizeOpenAIModelForUpstream(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		model   string
		want    string
	}{
		{
			name:    "oauth routes bare GPT-5.6 alias to Sol",
			account: &Account{Type: AccountTypeOAuth},
			model:   "gpt-5.6",
			want:    "gpt-5.6-sol",
		},
		{
			name:    "oauth routes provider-prefixed GPT-5.6 alias to Sol",
			account: &Account{Type: AccountTypeOAuth},
			model:   "openai/gpt-5.6",
			want:    "gpt-5.6-sol",
		},
		{
			name:    "oauth preserves unknown non codex model",
			account: &Account{Type: AccountTypeOAuth},
			model:   "gemini-3-flash-preview",
			want:    "gemini-3-flash-preview",
		},
		{
			name:    "oauth preserves invalid gpt model",
			account: &Account{Type: AccountTypeOAuth},
			model:   "gpt6",
			want:    "gpt6",
		},
		{
			name:    "oauth normalizes known codex alias",
			account: &Account{Type: AccountTypeOAuth},
			model:   "gpt-5.4-high",
			want:    "gpt-5.4",
		},
		{
			name:    "oauth preserves GPT-5.5 Pro model",
			account: &Account{Type: AccountTypeOAuth},
			model:   "openai/gpt-5.5-pro",
			want:    "gpt-5.5-pro",
		},
		{
			name:    "oauth preserves codex auto review model",
			account: &Account{Type: AccountTypeOAuth},
			model:   "codex-auto-review",
			want:    "codex-auto-review",
		},
		{
			name:    "apikey preserves official bare GPT-5.6 alias",
			account: &Account{Type: AccountTypeAPIKey},
			model:   "gpt-5.6",
			want:    "gpt-5.6",
		},
		{
			name:    "apikey preserves custom compatible model",
			account: &Account{Type: AccountTypeAPIKey},
			model:   "gemini-3-flash-preview",
			want:    "gemini-3-flash-preview",
		},
		{
			name:    "apikey preserves official non codex model",
			account: &Account{Type: AccountTypeAPIKey},
			model:   "gpt-4.1",
			want:    "gpt-4.1",
		},
		{
			name:    "deepseek strips claude code long context suffix",
			account: &Account{Type: AccountTypeAPIKey, Platform: PlatformDeepseek},
			model:   "deepseek-flash[1m]",
			want:    "deepseek-flash",
		},
		{
			name:    "deepseek strips duplicated long context suffix",
			account: &Account{Type: AccountTypeAPIKey, Platform: PlatformDeepseek},
			model:   "deepseek-flash[1M][1m]",
			want:    "deepseek-flash",
		},
		{
			name:    "deepseek preserves plain model",
			account: &Account{Type: AccountTypeAPIKey, Platform: PlatformDeepseek},
			model:   "deepseek-flash",
			want:    "deepseek-flash",
		},
		{
			name:    "non deepseek preserves long context suffix",
			account: &Account{Type: AccountTypeAPIKey, Platform: PlatformOpenAI},
			model:   "deepseek-flash[1m]",
			want:    "deepseek-flash[1m]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeOpenAIModelForUpstream(tt.account, tt.model); got != tt.want {
				t.Fatalf("normalizeOpenAIModelForUpstream(...) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUsageBillingModelCandidatesPreserveCodexAutoReviewModel(t *testing.T) {
	candidates := usageBillingModelCandidates("codex-auto-review")

	expected := []string{"codex-auto-review"}
	if len(candidates) != len(expected) {
		t.Fatalf("usageBillingModelCandidates(codex-auto-review) = %#v, want %#v", candidates, expected)
	}
	for i := range expected {
		if candidates[i] != expected[i] {
			t.Fatalf("usageBillingModelCandidates(codex-auto-review) = %#v, want %#v", candidates, expected)
		}
	}
}

func TestUsageBillingModelCandidatesPreserveGPT55ProModel(t *testing.T) {
	candidates := usageBillingModelCandidates("openai/gpt-5.5-pro")

	expected := []string{"openai/gpt-5.5-pro", "gpt-5.5-pro"}
	if len(candidates) != len(expected) {
		t.Fatalf("usageBillingModelCandidates(openai/gpt-5.5-pro) = %#v, want %#v", candidates, expected)
	}
	for i := range expected {
		if candidates[i] != expected[i] {
			t.Fatalf("usageBillingModelCandidates(openai/gpt-5.5-pro) = %#v, want %#v", candidates, expected)
		}
	}
}

// TestResolveOpenAIAccountUpstreamModel_PassthroughCredentialsMappingFallback
// 验证透传账号在 globalModelMapping 缺失时，回退到 credentials model_mapping
// 将规范名转换为上游认识的模型名，避免原样透传导致 404/429。
func TestResolveOpenAIAccountUpstreamModel_PassthroughCredentialsMappingFallback(t *testing.T) {
	tests := []struct {
		name     string
		account  *Account
		model    string
		want     string
	}{
		{
			name: "passthrough uses credentials mapping when global missing",
			account: &Account{
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"deepseek-v4.1-flash": "DeepSeek-V4.1-Flash",
						"qwen3.8-max":         "qwen3.8-max",
					},
				},
				Extra: map[string]any{"openai_passthrough": true},
			},
			model: "deepseek-v4.1-flash",
			want:  "DeepSeek-V4.1-Flash",
		},
		{
			name: "passthrough prefers global over credentials mapping",
			account: &Account{
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"deepseek-v4.1-flash": "credentials-form",
					},
				},
				Extra:              map[string]any{"openai_passthrough": true},
				globalModelMapping: map[string]string{"deepseek-v4.1-flash": "global-form"},
			},
			model: "deepseek-v4.1-flash",
			want:  "global-form",
		},
		{
			name: "passthrough without any mapping returns original",
			account: &Account{
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Credentials: map[string]any{},
				Extra:       map[string]any{"openai_passthrough": true},
			},
			model: "deepseek-v4.1-flash",
			want:  "deepseek-v4.1-flash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveOpenAIAccountUpstreamModelForRequest(tt.account, tt.model, false)
			if got != tt.want {
				t.Errorf("resolveOpenAIAccountUpstreamModelForRequest() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResolveOpenAIAccountUpstreamModel_PassthroughCatalogCaseFallback 验证透传账号
// 在没有任何显式映射时，通过上游目录的大小写不敏感匹配还原上游认识的精确模型名。
func TestResolveOpenAIAccountUpstreamModel_PassthroughCatalogCaseFallback(t *testing.T) {
	account := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{},
		Extra:       map[string]any{"openai_passthrough": true},
	}
	account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Models: []string{"DeepSeek-V4.1-Flash", "Qwen3.8-Max"},
	})

	tests := []struct {
		model string
		want  string
	}{
		{"deepseek-v4.1-flash", "DeepSeek-V4.1-Flash"},
		{"qwen3.8-max", "Qwen3.8-Max"},
		{"unknown-model", "unknown-model"}, // not in catalog, returned as-is
	}

	for _, tt := range tests {
		got := resolveOpenAIAccountUpstreamModelForRequest(account, tt.model, false)
		if got != tt.want {
			t.Errorf("model=%q: got %q, want %q", tt.model, got, tt.want)
		}
	}
}
