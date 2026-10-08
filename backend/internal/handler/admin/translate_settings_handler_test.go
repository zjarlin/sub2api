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

type translateSettingsRepo struct {
	service.SettingRepository
	value string
}

func (r *translateSettingsRepo) GetValue(context.Context, string) (string, error) {
	return r.value, nil
}
func (r *translateSettingsRepo) Set(_ context.Context, _, value string) error {
	r.value = value
	return nil
}

func TestTranslateSettingsSavePreservesMaskedSecretsAndRuntimeStatus(t *testing.T) {
	t.Setenv("TRANSLATE_FREE_PROVIDERS", "false")
	t.Setenv("TRANSLATE_BAIDU_APP_ID", "test-app")
	t.Setenv("TRANSLATE_BAIDU_SECRET", "private-baidu")
	t.Setenv("TRANSLATE_HYMT_URL", "http://inference:8000")
	t.Setenv("TRANSLATE_HYMT_API_KEY", "private-hymt")
	gin.SetMode(gin.TestMode)
	repo := &translateSettingsRepo{}
	svc := service.NewSettingService(repo, &config.Config{})
	h := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)
	settings := service.DefaultTranslateProviderSettings()
	settings.Baidu.Secret = ""
	settings.HyMT.APIKey = "***"
	settings.Enabled = false
	data, err := json.Marshal(settings)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/translate/providers", bytes.NewReader(data))
	c.Request.Header.Set("Content-Type", "application/json")
	h.UpdateTranslateProviders(c)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "private-baidu")
	require.NotContains(t, rec.Body.String(), "private-hymt")
	stored, err := svc.GetTranslateProviderSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, "private-baidu", stored.Baidu.Secret)
	require.Equal(t, "private-hymt", stored.HyMT.APIKey)
	require.False(t, stored.Enabled)
	statusRec := httptest.NewRecorder()
	statusContext, _ := gin.CreateTestContext(statusRec)
	statusContext.Request = httptest.NewRequest(http.MethodGet, "/status", nil)
	NewVisionHandler(&config.Config{}, nil, svc, nil).GetStatus(statusContext)
	require.Contains(t, statusRec.Body.String(), `"translate_enabled":false`)
}
