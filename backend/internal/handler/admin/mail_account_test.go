package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type mailAdminStub struct {
	service.AdminService
	account *service.Account
	proxy   *service.Proxy
}

func (s mailAdminStub) GetAccount(context.Context, int64) (*service.Account, error) {
	return s.account, nil
}
func (s mailAdminStub) GetProxy(context.Context, int64) (*service.Proxy, error) { return s.proxy, nil }

func TestMailAdminActionUsesSavedProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/folders", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"folders":[]}`))
	}))
	defer upstream.Close()
	proxyID := int64(7)
	cfg := &config.Config{}
	cfg.Gateway.Mail.Enabled = true
	cfg.Gateway.Mail.URL = upstream.URL
	h := &AccountHandler{cfg: cfg, adminService: mailAdminStub{
		account: &service.Account{Platform: service.PlatformMail, Type: service.AccountTypeAPIKey, Status: service.StatusActive, ProxyID: &proxyID, Credentials: map[string]any{"email": "test@gmail.com", "mail_provider": "google", "api_key": "saved-secret"}},
		proxy:   &service.Proxy{Protocol: "socks5", Host: "proxy.test", Port: 1080, Status: service.StatusActive},
	}}
	router := gin.New()
	router.POST("/:id/mail/:action", h.MailAction)
	out := httptest.NewRecorder()
	router.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/4/mail/folders", strings.NewReader(`{"proxy":"http://override:80","credential":"override"}`)))
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Equal(t, "saved-secret", received["credential"])
	require.Equal(t, "socks5://proxy.test:1080", received["proxy"])
	require.NotContains(t, out.Body.String(), "saved-secret")
	out = httptest.NewRecorder()
	router.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/4/mail/internal", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusNotFound, out.Code)
}
