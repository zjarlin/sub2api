package service

import "testing"

func TestValidateAPIMode_AllowsGeminiResponses(t *testing.T) {
	if err := validateAPIMode(MonitorProviderGemini, MonitorAPIModeResponses); err != nil {
		t.Fatalf("gemini responses api mode should be valid: %v", err)
	}
}

func TestProviderAdapterFor_GeminiResponsesUsesOpenAIResponsesWire(t *testing.T) {
	adapter, apiMode, ok := providerAdapterFor(MonitorProviderGemini, MonitorAPIModeResponses)
	if !ok {
		t.Fatal("expected gemini responses adapter")
	}
	if apiMode != MonitorAPIModeResponses {
		t.Fatalf("apiMode = %q, want %q", apiMode, MonitorAPIModeResponses)
	}
	if got := adapter.buildPath("gemini-2.5-pro"); got != providerOpenAIResponsesPath {
		t.Fatalf("path = %q, want %q", got, providerOpenAIResponsesPath)
	}
	headers := adapter.buildHeaders("sk-test")
	if got := headers["Authorization"]; got != "Bearer sk-test" {
		t.Fatalf("Authorization header = %q", got)
	}
}

func TestValidateReplaceRequestBody_GeminiNativeDoesNotRequireChatMessages(t *testing.T) {
	err := validateReplaceRequestBody(MonitorProviderGemini, MonitorAPIModeChatCompletions, map[string]any{
		"contents": []any{map[string]any{"parts": []any{map[string]any{"text": "hi"}}}},
	})
	if err != nil {
		t.Fatalf("gemini native replace body should not require OpenAI messages: %v", err)
	}
}
