package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type edgeContextAccounts struct {
	service.AdminService
	account *service.Account
	update  *service.UpdateAccountInput
}

func (s *edgeContextAccounts) ListAccountsForSchedulerScoreFilter(_ context.Context, platform, _, _, _ string, _ int64, _ string, _ service.AccountListFilters) ([]service.Account, error) {
	if platform == s.account.Platform {
		return []service.Account{*s.account}, nil
	}
	return nil, nil
}
func (s *edgeContextAccounts) GetAccount(context.Context, int64) (*service.Account, error) {
	return s.account, nil
}
func (s *edgeContextAccounts) UpdateAccount(_ context.Context, _ int64, input *service.UpdateAccountInput) (*service.Account, error) {
	s.update = input
	return s.account, nil
}

func TestEdgeContextsReadRuntimeAndMaskCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"models_loaded":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"tts":{"enabled":true,"upstream_url":"http://user:private@inference:9880/?key=private","timeout_seconds":180,"language":"zh"},"dub":{"enabled":true,"upstream_url":"http://dub:8000","timeout_seconds":7200,"max_upload_bytes":536870912},"unlisted_secret":"private"}`))
	}))
	defer upstream.Close()
	cfg := &config.Config{}
	cfg.Gateway.Vision.Enabled = true
	cfg.Gateway.Vision.URL = upstream.URL
	cfg.Gateway.Media.Enabled = true
	cfg.Gateway.Media.URL = upstream.URL
	accounts := &edgeContextAccounts{account: &service.Account{ID: 7, Name: "JEV", Platform: service.PlatformSystemOne, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{1}, Credentials: map[string]any{"base_url": "https://api.example.test/v1", "api_key": "private", "systemone_provider": "jev", "model_mapping": map[string]any{"typesafe/jev": "typesafe/jev"}}}}
	h := NewVisionHandler(cfg, nil, nil, accounts)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/contexts", nil)
	h.GetContexts(c)
	require.Equal(t, 200, rec.Code)
	require.NotContains(t, rec.Body.String(), "private")
	require.NotContains(t, rec.Body.String(), "unlisted_secret")
	var result struct {
		Data map[string]edgeServiceContext `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	require.True(t, result.Data["tts"].Enabled)
	require.True(t, result.Data["dub"].Configured)
	require.True(t, result.Data["jev"].Enabled)
	require.True(t, result.Data["jev"].Accounts[0].APIKeySet)
	require.False(t, result.Data["generation"].Enabled)
	require.Empty(t, result.Data["generation"].Accounts)
	require.Equal(t, "http://inference:9880/", result.Data["tts"].Fields[2].Value)
}

func TestEdgeContextAccountUpdatesAreScopedAndPreserveBlankKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := &edgeContextAccounts{account: &service.Account{ID: 7, Platform: service.PlatformSystemOne, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"systemone_provider": "jev", "api_key": "existing-private"}}}
	h := NewVisionHandler(&config.Config{}, nil, nil, accounts)
	call := func(serviceName, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Params = gin.Params{{Key: "service", Value: serviceName}, {Key: "id", Value: "7"}}
		c.Request = httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		h.UpdateContextAccount(c)
		return rec
	}
	require.Equal(t, 404, call("generation", `{"api_key":"new-private"}`).Code)
	require.Nil(t, accounts.update)
	rec := call("jev", `{"api_key":"","base_url":"https://untrusted.example"}`)
	require.Equal(t, 200, rec.Code)
	require.Empty(t, accounts.update.Credentials)
	require.NotContains(t, rec.Body.String(), "existing-private")
	require.Equal(t, 200, call("jev", `{"api_key":"new-private"}`).Code)
	require.Equal(t, map[string]any{"api_key": "new-private"}, accounts.update.Credentials)
	h.cfg.BuiltinAdapter.JevKey = "deployment-private"
	require.Equal(t, 400, call("jev", `{"api_key":"replacement"}`).Code)
}

func TestEdgeContextRuntimeDoesNotFollowRedirects(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/private", http.StatusFound)
	}))
	defer redirect.Close()
	var result map[string]any
	require.Error(t, readEdgeRuntime(context.Background(), redirect.URL, "/health", &result))
}
