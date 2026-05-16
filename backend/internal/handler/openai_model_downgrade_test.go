package handler

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestResolveOpenAIRequestDowngradeModel_APIKey429(t *testing.T) {
	account := &service.Account{
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.4": "pool:smart",
			},
		},
	}

	model, ok := resolveOpenAIRequestDowngradeModel(account, "gpt-5.5", &service.UpstreamFailoverError{StatusCode: http.StatusTooManyRequests})
	if !ok {
		t.Fatal("expected gpt-5.5 request to downgrade on openai apikey 429")
	}
	if model != "gpt-5.4" {
		t.Fatalf("downgraded model = %q, want gpt-5.4", model)
	}
}

func TestResolveOpenAIRequestDowngradeModel_OAuthDisabled(t *testing.T) {
	account := &service.Account{
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.4": "pool:smart",
			},
		},
	}

	if model, ok := resolveOpenAIRequestDowngradeModel(account, "gpt-5.5", &service.UpstreamFailoverError{StatusCode: http.StatusTooManyRequests}); ok {
		t.Fatalf("unexpected downgrade for oauth account: %q", model)
	}
}

func TestResolveOpenAIRequestDowngradeModel_Non429Disabled(t *testing.T) {
	account := &service.Account{
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.4": "pool:smart",
			},
		},
	}

	if model, ok := resolveOpenAIRequestDowngradeModel(account, "gpt-5.5", &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway}); ok {
		t.Fatalf("unexpected downgrade for non-429 failover: %q", model)
	}
}
