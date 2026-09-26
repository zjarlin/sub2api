package service

import (
	"context"
	"testing"
	"time"
)

func TestQoderOAuthGenerateAuthURLAndPollPending(t *testing.T) {
	svc := NewQoderOAuthService()
	result, err := svc.GenerateAuthURL(context.Background())
	if err != nil {
		t.Fatalf("GenerateAuthURL: %v", err)
	}
	if result.AuthURL == "" || result.SessionID == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if svc == nil {
		t.Fatal("nil service")
	}
	// Session must be registered for polling.
	if _, ok := svc.sessions.Load(result.SessionID); !ok {
		t.Fatal("session was not stored")
	}
}

func TestQoderOAuthPollTokenUnknownSession(t *testing.T) {
	svc := NewQoderOAuthService()
	if _, _, err := svc.PollToken(context.Background(), "missing"); err == nil {
		t.Fatal("expected unknown session to fail")
	}
}

func TestQoderOAuthSessionExpiry(t *testing.T) {
	svc := NewQoderOAuthService()
	svc.storeSession("expired", &QoderOAuthSession{
		Nonce:        "n",
		CodeVerifier: "v",
		CreatedAt:    time.Now().Add(-qoderOAuthSessionTTL - time.Minute),
	})
	if _, _, err := svc.PollToken(context.Background(), "expired"); err == nil {
		t.Fatal("expected expired session to fail")
	}
}

func TestValidateQoderOAuthCredentialsRequiresDeviceToken(t *testing.T) {
	if err := validateQoderCredentials(PlatformQoder, AccountTypeOAuth, map[string]any{"access_token": "device"}); err != nil {
		t.Fatalf("device token rejected: %v", err)
	}
	if err := validateQoderCredentials(PlatformQoder, AccountTypeOAuth, map[string]any{}); err == nil {
		t.Fatal("empty oauth credentials accepted")
	}
}

func TestQoderTokenRefresherEligibility(t *testing.T) {
	refresher := NewQoderTokenRefresher(NewQoderOAuthService())
	oauthAccount := &Account{Platform: PlatformQoder, Type: AccountTypeOAuth, Credentials: map[string]any{
		"access_token":  "device",
		"refresh_token": "rt",
	}}
	if !refresher.CanRefresh(oauthAccount) {
		t.Fatal("oauth qoder account with refresh token should be refreshable")
	}
	if refresher.CanRefresh(&Account{Platform: PlatformQoder, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "x"}}) {
		t.Fatal("apikey qoder account must not be refreshable")
	}
	if refresher.NeedsRefresh(oauthAccount, 0) {
		if expiresAt := oauthAccount.GetCredentialAsTime("expires_at"); expiresAt != nil {
			t.Fatalf("unexpected expires_at %v", expiresAt)
		}
	}
}
