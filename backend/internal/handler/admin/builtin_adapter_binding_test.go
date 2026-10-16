//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type builtinBindingAdminStub struct {
	service.AdminService
	account *service.Account
	writes  int
}

func (s *builtinBindingAdminStub) GetAccount(context.Context, int64) (*service.Account, error) {
	return s.account, nil
}

func (s *builtinBindingAdminStub) UpdateAccountExtra(_ context.Context, _ int64, updates map[string]any) error {
	s.writes++
	for key, value := range updates {
		s.account.Extra[key] = value
	}
	return nil
}

func TestBuiltinAdapterBindingReusesSavedLoginAndPreservesConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer adapter-secret", r.Header.Get("Authorization"))
		if r.URL.Path == "/status" {
			_, _ = w.Write([]byte(`{"accounts":[{"uid":"saved-login","credits":79.75}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"checkins":{}}`))
	}))
	defer adapter.Close()
	service.SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, WorkbuddyURL: adapter.URL, WorkbuddyKey: "adapter-secret"})
	t.Cleanup(func() { service.SetBuiltinAdapterConfig(nil) })
	stub := &builtinBindingAdminStub{account: &service.Account{
		ID: 893, Platform: service.PlatformWorkbuddy, Type: service.AccountTypeAPIKey,
		Extra: map[string]any{"model_health_probe_enabled": true, "runtime_key": "preserve"},
	}}
	router := gin.New()
	handler := &AccountHandler{adminService: stub}
	router.PUT("/accounts/:id/sidecar/account", handler.BindBuiltinAdapterAccount)
	router.GET("/accounts/:id/sidecar/checkins", handler.GetBuiltinAdapterCheckins)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, r)
		return result
	}

	for _, body := range []string{`{"uid":"missing"}`, `{"uid":"   "}`, `{}`} {
		result := request(http.MethodPut, "/accounts/893/sidecar/account", body)
		require.GreaterOrEqual(t, result.Code, 400)
		require.Zero(t, stub.writes)
	}
	result := request(http.MethodPut, "/accounts/893/sidecar/account", `{"uid":"saved-login"}`)
	require.Equal(t, http.StatusOK, result.Code, result.Body.String())
	require.Equal(t, 1, stub.writes)
	require.Equal(t, "saved-login", stub.account.Extra["builtin_adapter_uid"])
	require.Equal(t, true, stub.account.Extra["model_health_probe_enabled"])
	require.Equal(t, "preserve", stub.account.Extra["runtime_key"])
	require.Equal(t, "no-store", result.Header().Get("Cache-Control"))
	require.NotContains(t, result.Body.String(), "adapter-secret")
	result = request(http.MethodGet, "/accounts/893/sidecar/checkins", "")
	require.Equal(t, http.StatusOK, result.Code)
	require.Contains(t, result.Body.String(), `"total_credits":79.75`)
	require.NotContains(t, result.Body.String(), `"binding_required":true`)
}
