package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"access_token":"admin-token"}}`))
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

	token, err := loginUpstreamForAccessToken(context.Background(), server.Client(), input)
	require.NoError(t, err)
	require.Equal(t, "admin-token", token)

	result, err := findUpstreamKeyRate(context.Background(), server.Client(), input, token)
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
