package service

import "strings"

const defaultGeminiOpenAICompatModel = "gemini-2.5-pro"

var geminiOpenAICompatDefaultModelMapping = map[string]string{
	"gpt-5.5":              defaultGeminiOpenAICompatModel,
	"gpt-5.4":              defaultGeminiOpenAICompatModel,
	"gpt-5.4-mini":         defaultGeminiOpenAICompatModel,
	"gpt-5.4-nano":         defaultGeminiOpenAICompatModel,
	"gpt-5.3-codex":        defaultGeminiOpenAICompatModel,
	"gpt-5.3-codex-spark":  defaultGeminiOpenAICompatModel,
	"gpt-5.2":              defaultGeminiOpenAICompatModel,
	"gpt-5.2-codex":        defaultGeminiOpenAICompatModel,
	"gpt-5.1":              defaultGeminiOpenAICompatModel,
	"gpt-5.1-codex":        defaultGeminiOpenAICompatModel,
	"gpt-5.1-codex-max":    defaultGeminiOpenAICompatModel,
	"gpt-5-codex":          defaultGeminiOpenAICompatModel,
	"gpt-5":                defaultGeminiOpenAICompatModel,
	"gpt-4.1":              defaultGeminiOpenAICompatModel,
	"gpt-4.1-mini":         defaultGeminiOpenAICompatModel,
	"gpt-4o":               defaultGeminiOpenAICompatModel,
	"gpt-4o-mini":          defaultGeminiOpenAICompatModel,
	"codex-auto-review":    defaultGeminiOpenAICompatModel,
	"codex-mini-latest":    defaultGeminiOpenAICompatModel,
	"codex-mini":           defaultGeminiOpenAICompatModel,
	"openai/gpt-5.5":       defaultGeminiOpenAICompatModel,
	"openai/gpt-5.4":       defaultGeminiOpenAICompatModel,
	"openai/gpt-5.3-codex": defaultGeminiOpenAICompatModel,
	"openai/codex-mini":    defaultGeminiOpenAICompatModel,
}

func resolveGeminiForwardModel(account *Account, requestedModel string) string {
	trimmed := strings.TrimSpace(requestedModel)
	if trimmed == "" {
		return ""
	}
	if account != nil {
		if mapped, matched := account.ResolveMappedModel(trimmed); matched && strings.TrimSpace(mapped) != "" {
			return mapped
		}
	}
	if mapped := normalizeGeminiRequestedModel(trimmed); mapped != "" {
		return mapped
	}
	return trimmed
}

func normalizeGeminiRequestedModel(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "models/gemini-"):
		return strings.TrimPrefix(lower, "models/")
	case strings.Contains(lower, "/publishers/google/models/gemini-"):
		if idx := strings.LastIndex(lower, "/publishers/google/models/"); idx >= 0 {
			return lower[idx+len("/publishers/google/models/"):]
		}
	case strings.Contains(lower, "/models/gemini-"):
		if idx := strings.LastIndex(lower, "/models/"); idx >= 0 {
			return lower[idx+len("/models/"):]
		}
	case strings.HasPrefix(lower, "gemini-"):
		return lower
	}

	if mapped := geminiOpenAICompatDefaultModelMapping[lower]; mapped != "" {
		return mapped
	}
	if normalized, ok := normalizeKnownCodexModel(lower); ok {
		if mapped := geminiOpenAICompatDefaultModelMapping[normalized]; mapped != "" {
			return mapped
		}
	}
	if base, _, ok := splitOpenAICompatReasoningModel(lower); ok {
		if mapped := geminiOpenAICompatDefaultModelMapping[strings.ToLower(base)]; mapped != "" {
			return mapped
		}
		if normalized, ok := normalizeKnownCodexModel(base); ok {
			if mapped := geminiOpenAICompatDefaultModelMapping[normalized]; mapped != "" {
				return mapped
			}
		}
	}
	return ""
}

func geminiAccountSupportsRequestedModel(account *Account, requestedModel string) bool {
	if strings.TrimSpace(requestedModel) == "" {
		return true
	}
	if account == nil {
		return false
	}
	if account.IsModelSupported(requestedModel) {
		return true
	}
	mapped := resolveGeminiForwardModel(account, requestedModel)
	if mapped == "" || mapped == requestedModel {
		return false
	}
	return account.IsModelSupported(mapped)
}
