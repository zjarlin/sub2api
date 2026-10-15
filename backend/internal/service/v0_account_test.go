package service

import "testing"

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
