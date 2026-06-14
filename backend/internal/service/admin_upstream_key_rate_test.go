package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUpstreamKeyRateLoginAndFind(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		var payload map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "admin@example.com", payload["email"])
		require.Equal(t, "secret", payload["password"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"access_token":"admin-token","user":{"balance":12.5}}}`))
	})
	mux.HandleFunc("/api/v1/keys", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "Bearer admin-token", r.Header.Get("Authorization"))
		require.Equal(t, "1", r.URL.Query().Get("page"))
		require.Equal(t, "100", r.URL.Query().Get("page_size"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 0,
			"data": {
				"items": [
					{"id": 1, "key": "sk-other", "group": {"id": 10, "name": "other", "rate_multiplier": 0.5}},
					{"id": 2, "name": "target-key", "key": "sk-target", "group": {"id": 20, "name": "pro", "rate_multiplier": 1.25}}
				]
			}
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	input, err := normalizeResolveUpstreamKeyRateInput(ResolveUpstreamKeyRateInput{
		BaseURL:  server.URL + "/api/v1",
		Email:    " admin@example.com ",
		Password: " secret ",
		APIKey:   "Bearer sk-target",
	})
	require.NoError(t, err)

	login, err := loginUpstreamForAccess(context.Background(), server.Client(), input)
	require.NoError(t, err)
	require.Equal(t, "admin-token", login.Token)
	require.NotNil(t, login.Balance)
	require.Equal(t, 12.5, *login.Balance)

	result, err := findUpstreamKeyRate(context.Background(), server.Client(), input, login.Token)
	require.NoError(t, err)
	require.Equal(t, 1.25, result.RateMultiplier)
	require.Equal(t, "target-key", result.KeyName)
	require.Equal(t, "key", result.MatchedField)
	require.NotNil(t, result.KeyID)
	require.Equal(t, int64(2), *result.KeyID)
	require.NotNil(t, result.GroupID)
	require.Equal(t, int64(20), *result.GroupID)
	require.Equal(t, "pro", result.GroupName)
}

func TestSub2APIUpstreamKeyRateReadsProfileBalanceWhenLoginOmitsUser(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"access_token":"admin-token"}}`))
	})
	mux.HandleFunc("/api/v1/user/profile", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "Bearer admin-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"id":93,"email":"owner@example.com","balance":17.25}}`))
	})
	mux.HandleFunc("/api/v1/keys", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "Bearer admin-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 0,
			"data": {
				"items": [
					{"id": 2, "name": "target-key", "key": "sk-target", "group": {"id": 20, "name": "pro", "rate_multiplier": 1.25}}
				]
			}
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	input, err := normalizeResolveUpstreamKeyRateInput(ResolveUpstreamKeyRateInput{
		BaseURL:  server.URL,
		SiteType: UpstreamSiteTypeSub2API,
		Email:    "owner@example.com",
		Password: "secret",
		APIKey:   "sk-target",
	})
	require.NoError(t, err)

	loginResult, err := testUpstreamConsoleLoginBySiteType(context.Background(), server.Client(), input, UpstreamSiteTypeSub2API)
	require.NoError(t, err)
	require.True(t, loginResult.HasAccessToken)
	require.NotNil(t, loginResult.AccountBalance)
	require.Equal(t, 17.25, *loginResult.AccountBalance)

	result, err := resolveUpstreamKeyRateBySiteType(context.Background(), server.Client(), input, UpstreamSiteTypeSub2API)
	require.NoError(t, err)
	require.Equal(t, 1.25, result.RateMultiplier)
	require.NotNil(t, result.AccountBalance)
	require.Equal(t, 17.25, *result.AccountBalance)
}

func TestSub2APIUpstreamRefreshesStoredLoginState(t *testing.T) {
	var loginCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&loginCalls, 1)
		http.Error(w, "login should not be called when refresh token works", http.StatusInternalServerError)
	})
	mux.HandleFunc("/api/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		var payload map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "rt-old", payload["refresh_token"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"refreshed-token","refresh_token":"rt-new","expires_in":3600}}`))
	})
	mux.HandleFunc("/api/v1/user/profile", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer refreshed-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"balance":9.5}}`))
	})
	mux.HandleFunc("/api/v1/keys", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer refreshed-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"id":2,"key":"sk-target","group":{"id":20,"name":"pro","rate_multiplier":0.25}}]}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	credentials := map[string]any{
		CredentialUpstreamSiteAccessToken:    "expired-token",
		CredentialUpstreamSiteRefreshToken:   "rt-old",
		CredentialUpstreamSiteTokenExpiresAt: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
	}
	input, err := normalizeResolveUpstreamKeyRateInput(ResolveUpstreamKeyRateInput{
		BaseURL:     server.URL,
		SiteType:    UpstreamSiteTypeSub2API,
		Email:       "owner@example.com",
		Password:    "secret",
		APIKey:      "sk-target",
		Credentials: credentials,
	})
	require.NoError(t, err)

	result, err := resolveUpstreamKeyRateBySiteType(context.Background(), server.Client(), input, UpstreamSiteTypeSub2API)
	require.NoError(t, err)
	require.Equal(t, 0.25, result.RateMultiplier)
	require.Equal(t, "refreshed-token", credentials[CredentialUpstreamSiteAccessToken])
	require.Equal(t, "rt-new", credentials[CredentialUpstreamSiteRefreshToken])
	require.NotEmpty(t, credentials[CredentialUpstreamSiteTokenExpiresAt])
	require.Equal(t, int32(0), atomic.LoadInt32(&loginCalls))
}

func TestNewAPIUpstreamReusesStoredSessionCookie(t *testing.T) {
	var loginCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&loginCalls, 1)
		http.Error(w, "login should not be called when session cookie is fresh", http.StatusInternalServerError)
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "9", r.Header.Get("New-Api-User"))
		require.Equal(t, "cached-session", cookieValue(t, r, "session"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"quota":500000}}`))
	})
	mux.HandleFunc("/api/user/groups", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "9", r.Header.Get("New-Api-User"))
		require.Equal(t, "cached-session", cookieValue(t, r, "session"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"cheap":{"ratio":0.05}}}`))
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "9", r.Header.Get("New-Api-User"))
		require.Equal(t, "cached-session", cookieValue(t, r, "session"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[{"id":42,"name":"target","key":"sk-****","group":"cheap"}]}`))
	})
	mux.HandleFunc("/api/token/42/key", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "9", r.Header.Get("New-Api-User"))
		require.Equal(t, "cached-session", cookieValue(t, r, "session"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"key":"sk-target"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	credentials := map[string]any{
		CredentialUpstreamSiteUserID:           "9",
		CredentialUpstreamSiteSessionCookie:    "session=cached-session",
		CredentialUpstreamSiteSessionExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}
	input, err := normalizeResolveUpstreamKeyRateInput(ResolveUpstreamKeyRateInput{
		BaseURL:     server.URL,
		SiteType:    UpstreamSiteTypeNewAPI,
		Username:    "owner",
		Email:       "owner",
		Password:    "secret",
		APIKey:      "sk-target",
		Credentials: credentials,
	})
	require.NoError(t, err)

	result, err := resolveUpstreamKeyRateBySiteType(context.Background(), server.Client(), input, UpstreamSiteTypeNewAPI)
	require.NoError(t, err)
	require.Equal(t, 0.05, result.RateMultiplier)
	require.NotNil(t, result.AccountBalance)
	require.Equal(t, 1.0, *result.AccountBalance)
	require.Equal(t, int32(0), atomic.LoadInt32(&loginCalls))
}

func TestNewAPIUpstreamLoginStoresSessionCookie(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "fresh-session", Path: "/", MaxAge: 2592000})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"owner","quota":250000}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	credentials := map[string]any{}
	input, err := normalizeResolveUpstreamKeyRateInput(ResolveUpstreamKeyRateInput{
		BaseURL:     server.URL,
		SiteType:    UpstreamSiteTypeNewAPI,
		Username:    "owner",
		Email:       "owner",
		Password:    "secret",
		APIKey:      "sk-target",
		Credentials: credentials,
	})
	require.NoError(t, err)

	login, err := loginNewAPIUpstream(context.Background(), server.Client(), input)
	require.NoError(t, err)
	require.Equal(t, "9", login.UserID)
	require.Equal(t, "session=fresh-session", credentials[CredentialUpstreamSiteSessionCookie])
	require.Equal(t, "9", credentials[CredentialUpstreamSiteUserID])
	require.NotEmpty(t, credentials[CredentialUpstreamSiteSessionExpiresAt])
}

func TestListNewAPIUpstreamKeyGroupsShowsRates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "fresh-session", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"owner"}}`))
	})
	mux.HandleFunc("/api/user/groups", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "9", r.Header.Get("New-Api-User"))
		require.Equal(t, "fresh-session", cookieValue(t, r, "session"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"default": {"ratio": 1, "desc": "Default group"},
				"cheap": {"ratio": 0.05, "description": "Cheap group"},
				"auto": {"ratio": "自动"}
			}
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	groups, err := listNewAPIUpstreamKeyGroups(context.Background(), ResolveUpstreamKeyRateInput{
		BaseURL:  server.URL + "/v1",
		SiteType: UpstreamSiteTypeNewAPI,
		Username: "owner",
		Password: "secret",
		APIKey:   "sk-target",
	})
	require.NoError(t, err)
	require.Len(t, groups, 3)
	require.Equal(t, "cheap", groups[0].Name)
	require.Equal(t, 0.05, groups[0].RateMultiplier)
	require.Equal(t, "Cheap group", groups[0].Description)
	require.Equal(t, "auto", groups[1].Name)
	require.Equal(t, 1.0, groups[1].RateMultiplier)
	require.Equal(t, "default", groups[2].Name)
	require.Equal(t, 1.0, groups[2].RateMultiplier)
}

func cookieValue(t *testing.T, r *http.Request, name string) string {
	t.Helper()
	cookie, err := r.Cookie(name)
	require.NoError(t, err)
	return cookie.Value
}

func TestUpstreamKeyRateFindReportsMaskedKeyNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/keys", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"id":1,"key":"sk-****","group":{"rate_multiplier":2}}]}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	input, err := normalizeResolveUpstreamKeyRateInput(ResolveUpstreamKeyRateInput{
		BaseURL:  server.URL,
		Email:    "admin@example.com",
		Password: "secret",
		APIKey:   "sk-target",
		MaxPages: 1,
	})
	require.NoError(t, err)

	_, err = findUpstreamKeyRate(context.Background(), server.Client(), input, "admin-token")
	require.Error(t, err)
	require.Contains(t, err.Error(), "UPSTREAM_RATE_KEY_NOT_FOUND")
}

func TestUpstreamKeyRateExtractsItemLevelRateFallback(t *testing.T) {
	result, ok, err := matchUpstreamKeyRateItem(map[string]any{
		"apiKey":                "sk-target",
		"name":                  "target",
		"group_id":              float64(12),
		"group_name":            "fallback-group",
		"group_rate_multiplier": "0.75",
	}, "sk-target")

	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 0.75, result.RateMultiplier)
	require.Equal(t, "apiKey", result.MatchedField)
	require.NotNil(t, result.GroupID)
	require.Equal(t, int64(12), *result.GroupID)
	require.Equal(t, "fallback-group", result.GroupName)
}

func TestNewAPIUpstreamKeyRateLoginAndFind(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		var payload map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "owner", payload["username"])
		require.Equal(t, "secret", payload["password"])
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "ok", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"message":"success","data":{"id":42}}`))
	})
	mux.HandleFunc("/api/user/groups", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "42", r.Header.Get("New-Api-User"))
		_, err := r.Cookie("session")
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"default":{"ratio":1},"vip":{"ratio":0.6}}}`))
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "42", r.Header.Get("New-Api-User"))
		_, err := r.Cookie("session")
		require.NoError(t, err)
		require.Equal(t, "1", r.URL.Query().Get("p"))
		require.Equal(t, "100", r.URL.Query().Get("page_size"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"items": [
					{"id": 7, "name": "target-token", "key": "sk-****", "group": "vip"}
				]
			}
		}`))
	})
	mux.HandleFunc("/api/token/7/key", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "42", r.Header.Get("New-Api-User"))
		_, err := r.Cookie("session")
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"key":"sk-target"}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	input, err := normalizeResolveUpstreamKeyRateInput(ResolveUpstreamKeyRateInput{
		BaseURL:  server.URL + "/v1",
		SiteType: UpstreamSiteTypeNewAPI,
		Username: " owner ",
		Password: " secret ",
		APIKey:   "sk-target",
	})
	require.NoError(t, err)

	result, err := resolveUpstreamKeyRateBySiteType(context.Background(), server.Client(), input, UpstreamSiteTypeNewAPI)
	require.NoError(t, err)
	require.Equal(t, 0.6, result.RateMultiplier)
	require.Equal(t, "vip", result.GroupName)
	require.Equal(t, "target-token", result.KeyName)
	require.Equal(t, "token_key", result.MatchedField)
	require.NotNil(t, result.KeyID)
	require.Equal(t, int64(7), *result.KeyID)
}

func TestNewAPIUpstreamKeyRateSupportsBearerLogin(t *testing.T) {
	const bearer = "Bearer console-token"
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		var payload map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "owner", payload["username"])
		require.Equal(t, "secret", payload["password"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"access_token":"console-token","user":{"id":55,"balance":88.75}}}`))
	})
	mux.HandleFunc("/api/user/groups", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, bearer, r.Header.Get("Authorization"))
		require.Equal(t, "55", r.Header.Get("New-Api-User"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"default":{"ratio":1},"low":{"ratio":0.35}}}`))
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, bearer, r.Header.Get("Authorization"))
		require.Equal(t, "55", r.Header.Get("New-Api-User"))
		require.Equal(t, "1", r.URL.Query().Get("p"))
		require.Equal(t, "100", r.URL.Query().Get("page_size"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"items": [
					{"id": 11, "name": "target-token", "key": "sk-****", "group": "low"}
				]
			}
		}`))
	})
	mux.HandleFunc("/api/token/11/key", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, bearer, r.Header.Get("Authorization"))
		require.Equal(t, "55", r.Header.Get("New-Api-User"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"key":"sk-target"}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	input, err := normalizeResolveUpstreamKeyRateInput(ResolveUpstreamKeyRateInput{
		BaseURL:  server.URL,
		SiteType: UpstreamSiteTypeNewAPI,
		Username: "owner",
		Password: "secret",
		APIKey:   "sk-target",
	})
	require.NoError(t, err)

	result, err := resolveUpstreamKeyRateBySiteType(context.Background(), server.Client(), input, UpstreamSiteTypeNewAPI)
	require.NoError(t, err)
	require.Equal(t, 0.35, result.RateMultiplier)
	require.Equal(t, "low", result.GroupName)
	require.Equal(t, "target-token", result.KeyName)
	require.Equal(t, "token_key", result.MatchedField)
	require.NotNil(t, result.KeyID)
	require.Equal(t, int64(11), *result.KeyID)
	require.NotNil(t, result.AccountBalance)
	require.Equal(t, 88.75, *result.AccountBalance)
}

func TestNewAPIUpstreamKeyRateMatchesRawTokenKeyWithoutSKPrefix(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "ok", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":88}}`))
	})
	mux.HandleFunc("/api/user/groups", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "88", r.Header.Get("New-Api-User"))
		_, err := r.Cookie("session")
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"gpt plus":{"ratio":0.05}}}`))
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "88", r.Header.Get("New-Api-User"))
		_, err := r.Cookie("session")
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":51,"name":"target-token","key":"sk-****4634","group":"gpt plus","remain_quota":1000000}]}}`))
	})
	mux.HandleFunc("/api/token/51/key", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "88", r.Header.Get("New-Api-User"))
		_, err := r.Cookie("session")
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"key":"m0jY0NMOMG9ugqSLToJb5FNPbkTmVt7Yz7mKg4E6yxwY4634"}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	input, err := normalizeResolveUpstreamKeyRateInput(ResolveUpstreamKeyRateInput{
		BaseURL:  server.URL,
		SiteType: UpstreamSiteTypeNewAPI,
		Username: "owner",
		Password: "secret",
		APIKey:   "sk-m0jY0NMOMG9ugqSLToJb5FNPbkTmVt7Yz7mKg4E6yxwY4634",
	})
	require.NoError(t, err)

	result, err := resolveUpstreamKeyRateBySiteType(context.Background(), server.Client(), input, UpstreamSiteTypeNewAPI)
	require.NoError(t, err)
	require.Equal(t, 0.05, result.RateMultiplier)
	require.Equal(t, "gpt plus", result.GroupName)
	require.Equal(t, "target-token", result.KeyName)
	require.Equal(t, "token_key", result.MatchedField)
	require.NotNil(t, result.AccountBalance)
	require.Equal(t, 2.0, *result.AccountBalance)
}

func TestUpstreamConsoleLoginTestSucceedsWithNewAPICookie(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		var payload map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "owner", payload["username"])
		require.Equal(t, "secret", payload["password"])
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "ok", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":66,"quota":11314582}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	result, err := testUpstreamConsoleLogin(context.Background(), TestUpstreamConsoleLoginInput{
		BaseURL:  server.URL + "/v1",
		SiteType: UpstreamSiteTypeNewAPI,
		Username: "owner",
		Password: "secret",
	})
	require.NoError(t, err)
	require.Equal(t, UpstreamSiteTypeNewAPI, result.SiteType)
	require.Equal(t, "66", result.UserID)
	require.True(t, result.HasSessionCookie)
	require.False(t, result.HasAccessToken)
	require.NotNil(t, result.AccountBalance)
	require.InDelta(t, 22.629164, *result.AccountBalance, 0.000001)
}

func TestNewAPIUpstreamKeyRateReadsSelfBalanceAndAutoGroup(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "ok", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":123,"username":"owner","group":"auto"}}`))
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "123", r.Header.Get("New-Api-User"))
		_, err := r.Cookie("session")
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":123,"quota":1000000}}`))
	})
	mux.HandleFunc("/api/user/groups", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "123", r.Header.Get("New-Api-User"))
		_, err := r.Cookie("session")
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"auto":{"ratio":"自动"}}}`))
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "123", r.Header.Get("New-Api-User"))
		_, err := r.Cookie("session")
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"items": [
					{"id": 21, "name": "target-token", "key": "sk-****", "group": "auto", "remain_quota": 3000}
				]
			}
		}`))
	})
	mux.HandleFunc("/api/token/21/key", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "123", r.Header.Get("New-Api-User"))
		_, err := r.Cookie("session")
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"key":"sk-target"}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	input, err := normalizeResolveUpstreamKeyRateInput(ResolveUpstreamKeyRateInput{
		BaseURL:  server.URL,
		SiteType: UpstreamSiteTypeNewAPI,
		Username: "owner",
		Password: "secret",
		APIKey:   "sk-target",
	})
	require.NoError(t, err)

	result, err := resolveUpstreamKeyRateBySiteType(context.Background(), server.Client(), input, UpstreamSiteTypeNewAPI)
	require.NoError(t, err)
	require.Equal(t, 1.0, result.RateMultiplier)
	require.Equal(t, "auto", result.GroupName)
	require.NotNil(t, result.AccountBalance)
	require.Equal(t, 2.0, *result.AccountBalance)
}

func TestNewAPIUpstreamKeyRateFallsBackToTokenRemainQuota(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/login", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "ok", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":"91"}}`))
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "91", r.Header.Get("New-Api-User"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":123}}`))
	})
	mux.HandleFunc("/api/user/groups", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "91", r.Header.Get("New-Api-User"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"vip":{"ratio":0.2}}}`))
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "91", r.Header.Get("New-Api-User"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":31,"name":"target-token","key":"sk-****","group":"vip","remain_quota":1500000}]}}`))
	})
	mux.HandleFunc("/api/token/31/key", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "91", r.Header.Get("New-Api-User"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"key":"sk-target"}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	input, err := normalizeResolveUpstreamKeyRateInput(ResolveUpstreamKeyRateInput{
		BaseURL:  server.URL,
		SiteType: UpstreamSiteTypeNewAPI,
		Username: "owner",
		Password: "secret",
		APIKey:   "sk-target",
	})
	require.NoError(t, err)

	result, err := resolveUpstreamKeyRateBySiteType(context.Background(), server.Client(), input, UpstreamSiteTypeNewAPI)
	require.NoError(t, err)
	require.Equal(t, 0.2, result.RateMultiplier)
	require.NotNil(t, result.AccountBalance)
	require.Equal(t, 3.0, *result.AccountBalance)
}

func TestSwitchNewAPIUpstreamKeyGroupPreservesTokenFields(t *testing.T) {
	tokenGroup := "vip"
	putCalled := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "ok", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"quota":1000000}}`))
	})
	mux.HandleFunc("/api/user/groups", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "42", r.Header.Get("New-Api-User"))
		_, err := r.Cookie("session")
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"vip":{"ratio":0.6},"GPT 超低渠道":{"ratio":0.05}}}`))
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "42", r.Header.Get("New-Api-User"))
		_, err := r.Cookie("session")
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			putCalled = true
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, float64(338), payload["id"])
			require.Equal(t, "cx", payload["name"])
			require.Equal(t, float64(123456), payload["remain_quota"])
			require.Equal(t, float64(-1), payload["expired_time"])
			require.Equal(t, true, payload["unlimited_quota"])
			require.Equal(t, true, payload["model_limits_enabled"])
			require.Equal(t, "gpt-4o,gpt-4.1", payload["model_limits"])
			require.Equal(t, "127.0.0.1", payload["allow_ips"])
			require.Equal(t, "GPT 超低渠道", payload["group"])
			require.Equal(t, true, payload["cross_group_retry"])
			require.Equal(t, float64(1), payload["status"])
			tokenGroup = "GPT 超低渠道"
			_, _ = w.Write([]byte(`{"success":true,"message":"success"}`))
			return
		}
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "1", r.URL.Query().Get("p"))
		require.Equal(t, "100", r.URL.Query().Get("page_size"))
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"items": [
					{
						"id": 338,
						"name": "cx",
						"key": "sk-****",
						"group": "` + tokenGroup + `",
						"remain_quota": 123456,
						"expired_time": -1,
						"unlimited_quota": true,
						"model_limits_enabled": true,
						"model_limits": "gpt-4o,gpt-4.1",
						"allow_ips": "127.0.0.1",
						"cross_group_retry": true,
						"status": 1
					}
				]
			}
		}`))
	})
	mux.HandleFunc("/api/token/338/key", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "42", r.Header.Get("New-Api-User"))
		_, err := r.Cookie("session")
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"key":"sk-target"}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	result, err := switchNewAPIUpstreamKeyGroup(context.Background(), ResolveUpstreamKeyRateInput{
		BaseURL:  server.URL + "/v1",
		SiteType: UpstreamSiteTypeNewAPI,
		Username: "owner",
		Password: "secret",
		APIKey:   "sk-target",
	}, "GPT 超低渠道")
	require.NoError(t, err)
	require.True(t, putCalled)
	require.Equal(t, 0.05, result.RateMultiplier)
	require.Equal(t, "GPT 超低渠道", result.GroupName)
	require.Equal(t, "cx", result.KeyName)
	require.NotNil(t, result.AccountBalance)
	require.Equal(t, 2.0, *result.AccountBalance)
}

func TestListSub2APIUpstreamKeyGroupsUsesAvailableGroups(t *testing.T) {
	var newAPIGroupsCalled atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		var payload map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "owner@example.com", payload["email"])
		require.Equal(t, "secret", payload["password"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"access_token":"sub2api-token","expires_in":3600}}`))
	})
	mux.HandleFunc("/api/v1/groups/available", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "Bearer sub2api-token", r.Header.Get("Authorization"))
		require.Equal(t, "Asia/Shanghai", r.URL.Query().Get("timezone"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 0,
			"message": "success",
			"data": [
				{"id": 20, "name": "gpt plus", "rate_multiplier": 0.05, "description": "cheap group"},
				{"id": 10, "name": "default", "rate_multiplier": 1, "description": "default group"}
			]
		}`))
	})
	mux.HandleFunc("/api/user/groups", func(w http.ResponseWriter, r *http.Request) {
		newAPIGroupsCalled.Store(true)
		http.Error(w, "new-api groups endpoint should not be called for sub2api", http.StatusInternalServerError)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	groups, err := listUpstreamKeyGroups(context.Background(), ResolveUpstreamKeyRateInput{
		BaseURL:  server.URL + "/v1",
		SiteType: UpstreamSiteTypeAuto,
		Username: "owner@example.com",
		Password: "secret",
		APIKey:   "sk-target",
	})
	require.NoError(t, err)
	require.False(t, newAPIGroupsCalled.Load())
	require.Len(t, groups, 2)
	require.Equal(t, "gpt plus", groups[0].Name)
	require.Equal(t, 0.05, groups[0].RateMultiplier)
	require.Equal(t, "cheap group", groups[0].Description)
	require.NotNil(t, groups[0].ID)
	require.Equal(t, int64(20), *groups[0].ID)
	require.Equal(t, "default", groups[1].Name)
	require.Equal(t, 1.0, groups[1].RateMultiplier)
	require.NotNil(t, groups[1].ID)
	require.Equal(t, int64(10), *groups[1].ID)
}

func TestListSub2APIUpstreamKeyGroupsSupportsMapPayload(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"sub2api-token"}}`))
	})
	mux.HandleFunc("/api/v1/groups/available", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer sub2api-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 0,
			"data": {
				"premium": {"id": 8, "rate_multiplier": "0.2", "desc": "Premium"},
				"auto": {"id": 9, "ratio": "自动"},
				"no-rate": {"id": 10}
			}
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	groups, err := listSub2APIUpstreamKeyGroups(context.Background(), ResolveUpstreamKeyRateInput{
		BaseURL:  server.URL,
		SiteType: UpstreamSiteTypeSub2API,
		Username: "owner@example.com",
		Password: "secret",
		APIKey:   "sk-target",
	})
	require.NoError(t, err)
	require.Len(t, groups, 2)
	require.Equal(t, "premium", groups[0].Name)
	require.Equal(t, 0.2, groups[0].RateMultiplier)
	require.NotNil(t, groups[0].ID)
	require.Equal(t, int64(8), *groups[0].ID)
	require.Equal(t, "auto", groups[1].Name)
	require.Equal(t, 1.0, groups[1].RateMultiplier)
	require.NotNil(t, groups[1].ID)
	require.Equal(t, int64(9), *groups[1].ID)
}

func TestSwitchSub2APIUpstreamKeyGroupUpdatesRemoteKeyGroup(t *testing.T) {
	keyGroupID := int64(10)
	putCalled := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"sub2api-token","user":{"balance":99.5}}}`))
	})
	mux.HandleFunc("/api/v1/groups/available", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "Bearer sub2api-token", r.Header.Get("Authorization"))
		require.Equal(t, "Asia/Shanghai", r.URL.Query().Get("timezone"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 0,
			"data": [
				{"id": 10, "name": "default", "rate_multiplier": 1},
				{"id": 20, "name": "gpt plus", "rate_multiplier": 0.05, "description": "cheap group"}
			]
		}`))
	})
	mux.HandleFunc("/api/v1/keys", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "Bearer sub2api-token", r.Header.Get("Authorization"))
		require.Equal(t, "1", r.URL.Query().Get("page"))
		require.Equal(t, "100", r.URL.Query().Get("page_size"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 0,
			"data": {
				"items": [
					{
						"id": 202,
						"name": "cx",
						"key": "sk-target",
						"group": {"id": ` + strconv.FormatInt(keyGroupID, 10) + `, "name": "` + sub2APITestGroupName(keyGroupID) + `", "rate_multiplier": ` + sub2APITestRate(keyGroupID) + `}
					}
				]
			}
		}`))
	})
	mux.HandleFunc("/api/v1/keys/202", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer sub2api-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{
				"code": 0,
				"data": {
					"id": 202,
					"name": "cx",
					"status": "active",
					"quota": 12345,
					"rate_limit_5h": 50,
					"rate_limit_1d": 100,
					"rate_limit_7d": 300,
					"ip_whitelist": ["127.0.0.1"],
					"ip_blacklist": ["10.0.0.1"],
					"expires_at": "2027-01-02T03:04:05Z"
				}
			}`))
		case http.MethodPut:
			putCalled = true
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, float64(20), payload["group_id"])
			require.Equal(t, []any{"127.0.0.1"}, payload["ip_whitelist"])
			require.Equal(t, []any{"10.0.0.1"}, payload["ip_blacklist"])
			require.NotContains(t, payload, "name")
			require.NotContains(t, payload, "status")
			require.NotContains(t, payload, "quota")
			require.NotContains(t, payload, "rate_limit_5h")
			require.NotContains(t, payload, "rate_limit_1d")
			require.NotContains(t, payload, "rate_limit_7d")
			require.NotContains(t, payload, "expires_at")
			keyGroupID = 20
			_, _ = w.Write([]byte(`{"code":0,"message":"success"}`))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	result, err := switchUpstreamKeyGroup(context.Background(), ResolveUpstreamKeyRateInput{
		BaseURL:  server.URL,
		SiteType: UpstreamSiteTypeSub2API,
		Username: "owner@example.com",
		Password: "secret",
		APIKey:   "sk-target",
	}, "gpt plus")
	require.NoError(t, err)
	require.True(t, putCalled)
	require.Equal(t, UpstreamSiteTypeSub2API, result.SiteType)
	require.Equal(t, 0.05, result.RateMultiplier)
	require.Equal(t, "gpt plus", result.GroupName)
	require.NotNil(t, result.GroupID)
	require.Equal(t, int64(20), *result.GroupID)
	require.Equal(t, "cx", result.KeyName)
	require.NotNil(t, result.KeyID)
	require.Equal(t, int64(202), *result.KeyID)
	require.NotNil(t, result.AccountBalance)
	require.Equal(t, 99.5, *result.AccountBalance)
}

func TestSwitchSub2APIUpstreamKeyGroupReportsCloudflareHTMLAsJSONError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/keys/202", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer sub2api-token", r.Header.Get("Authorization"))
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":202,"name":"cx"}}`))
		case http.MethodPut:
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
			w.WriteHeader(520)
			_, _ = w.Write([]byte(`<html><body>The origin web server returned an invalid or incomplete response to Cloudflare.</body></html>`))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	err := updateSub2APIKeyGroup(context.Background(), server.Client(), server.URL, "sub2api-token", nil, 202, 20)
	require.Error(t, err)
	require.Contains(t, err.Error(), "UPSTREAM_GROUP_SWITCH_FAILED")
	require.Contains(t, err.Error(), "non-JSON HTTP 520")
}

func sub2APITestGroupName(groupID int64) string {
	if groupID == 20 {
		return "gpt plus"
	}
	return "default"
}

func sub2APITestRate(groupID int64) string {
	if groupID == 20 {
		return "0.05"
	}
	return "1"
}
