package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/platform/translate"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type liveTranslateSettingsRepo struct {
	service.SettingRepository
	value string
}

func (r *liveTranslateSettingsRepo) GetValue(context.Context, string) (string, error) {
	return r.value, nil
}
func (r *liveTranslateSettingsRepo) Set(_ context.Context, _, value string) error {
	r.value = value
	return nil
}

func TestTranslateHandlerUsesUpdatedSettingsWithoutRestart(t *testing.T) {
	t.Setenv("TRANSLATE_FREE_PROVIDERS", "false")
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-test-key" {
			t.Error("inference credential not applied")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "Hello"}, "finish_reason": "stop"}}})
	}))
	defer upstream.Close()
	repo := &liveTranslateSettingsRepo{}
	svc := service.NewSettingService(repo, &config.Config{})
	h := NewTranslateHandler(translate.NewAggregator(&translate.Config{}), svc)
	router := gin.New()
	router.POST("/translate", h.Translate)
	router.GET("/providers", h.Providers)
	settings := service.DefaultTranslateProviderSettings()
	settings.HyMT.Enabled = true
	settings.HyMT.BaseURL = upstream.URL
	settings.HyMT.APIKey = "private-test-key"
	require.NoError(t, svc.SetTranslateProviderSettings(context.Background(), settings))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/translate", strings.NewReader(`{"q":["你好"],"target":"en","provider":"hymt"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"text":"Hello"`)
	settings.Enabled = false
	require.NoError(t, svc.SetTranslateProviderSettings(context.Background(), settings))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/providers", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"providers":[]`)
}

func TestTranslateHandlerUnknownProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewTranslateHandler(translate.NewAggregator(&translate.Config{}), nil)
	r.POST("/translate", h.Translate)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/translate", strings.NewReader(`{"q":["Hello"],"target":"zh-CN","provider":"unknown"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}
}
