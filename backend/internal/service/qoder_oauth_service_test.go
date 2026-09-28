package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newQoderOAuthTestService(t *testing.T, mr *miniredis.Miniredis) *QoderOAuthService {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return ProvideQoderOAuthService(client)
}

func TestQoderOAuthAcrossReplicas(t *testing.T) {
	mr := miniredis.RunT(t)
	svc := newQoderOAuthTestService(t, mr)
	result, err := svc.GenerateAuthURL(context.Background())
	require.NoError(t, err)
	link, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	query := link.Query()
	var complete atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/deviceToken/poll", r.URL.Path)
		require.Equal(t, query.Get("nonce"), r.URL.Query().Get("nonce"))
		require.Equal(t, "S256", r.URL.Query().Get("challenge_method"))
		digest := sha256.Sum256([]byte(r.URL.Query().Get("verifier")))
		require.Equal(t, query.Get("challenge"), base64.RawURLEncoding.EncodeToString(digest[:]))
		if !complete.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"token":"device-token","refresh_token":"refresh-token","expires_at":"2030-01-02T03:04:05Z","expires_in":3600}`))
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("QODER_OPENAPI_BASE_URL", upstream.URL)

	// 生成、等待和完成分别交给独立服务实例，复现负载均衡及进程重建。
	other := newQoderOAuthTestService(t, mr)
	token, done, err := other.PollToken(context.Background(), result.SessionID)
	require.NoError(t, err)
	require.False(t, done)
	require.Nil(t, token)
	complete.Store(true)
	restarted := newQoderOAuthTestService(t, mr)
	token, done, err = restarted.PollToken(context.Background(), result.SessionID)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, "device-token", token.AccessToken)
	require.Equal(t, "refresh-token", token.RefreshToken)
	require.Equal(t, time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC).Unix(), token.ExpiresAt)
	_, _, err = svc.PollToken(context.Background(), result.SessionID)
	require.Equal(t, "QODER_OAUTH_SESSION_NOT_FOUND", infraerrors.Reason(err))
}

func TestQoderOAuthPollTokenUnknownSession(t *testing.T) {
	svc := newQoderOAuthTestService(t, miniredis.RunT(t))
	if _, _, err := svc.PollToken(context.Background(), "missing"); err == nil {
		t.Fatal("expected unknown session to fail")
	}
}

func TestQoderOAuthSessionExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	svc := newQoderOAuthTestService(t, mr)
	err := svc.sessions.Set(context.Background(), "expired", &QoderOAuthSession{
		Nonce:        "n",
		CodeVerifier: "v",
		CreatedAt:    time.Now().Add(-qoderOAuthSessionTTL - time.Minute),
	})
	require.NoError(t, err)
	if _, _, err := svc.PollToken(context.Background(), "expired"); err == nil {
		t.Fatal("expected expired session to fail")
	}
	result, err := svc.GenerateAuthURL(context.Background())
	require.NoError(t, err)
	mr.FastForward(qoderOAuthSessionTTL)
	require.False(t, mr.Exists("oauth:session:qoder:"+result.SessionID))
	_, _, err = svc.PollToken(context.Background(), result.SessionID)
	require.Equal(t, "QODER_OAUTH_SESSION_NOT_FOUND", infraerrors.Reason(err))
}

func TestQoderOAuthRedisFailureDoesNotBecomeMissingSession(t *testing.T) {
	mr := miniredis.RunT(t)
	svc := newQoderOAuthTestService(t, mr)
	result, err := svc.GenerateAuthURL(context.Background())
	require.NoError(t, err)
	mr.SetError("ERR unavailable")
	_, _, err = svc.PollToken(context.Background(), result.SessionID)
	require.Equal(t, http.StatusServiceUnavailable, infraerrors.Code(err))
	require.Equal(t, "QODER_OAUTH_SESSION_STORE_FAILED", infraerrors.Reason(err))
	result, err = svc.GenerateAuthURL(context.Background())
	require.Nil(t, result)
	require.Equal(t, "QODER_OAUTH_SESSION_STORE_FAILED", infraerrors.Reason(err))
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
	refresher := NewQoderTokenRefresher(NewQoderOAuthService(nil))
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
