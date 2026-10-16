package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestV0AccountValidation(t *testing.T) {
	for _, credentials := range []map[string]any{nil, {}, {"api_key": ""}, {"api_key": "secret", "base_url": "http://api.v0.dev/v2"}, {"api_key": "secret", "base_url": "https://internal.example/v2"}, {"api_key": "secret", "base_url": "https://api.v0.dev/v2?x=y"}} {
		if ValidateV0Account(PlatformV0, AccountTypeAPIKey, credentials) == nil {
			t.Fatalf("accepted invalid credentials")
		}
	}
	if err := ValidateV0Account(PlatformV0, AccountTypeOAuth, map[string]any{"api_key": "secret"}); err == nil {
		t.Fatal("accepted OAuth")
	}
	if err := ValidateV0Account(PlatformV0, AccountTypeAPIKey, map[string]any{"api_key": "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateV0Account(PlatformV0, AccountTypeAPIKey, map[string]any{"api_key": "secret", "base_url": V0DefaultBaseURL}); err != nil {
		t.Fatal(err)
	}
}

// v0 没有上游 /models 探测端点，模型目录必须由网关内置，否则后台模型列表为空。
func TestV0ModelCatalogMatchesDocumentedTiers(t *testing.T) {
	ids := V0DefaultModelIDs()
	require.Equal(t, []string{"v0-mini", "v0-pro", "v0-max", "v0-max-fast"}, ids)
	for _, model := range V0ModelCatalog() {
		require.NotEmpty(t, model.DisplayName)
	}
}
