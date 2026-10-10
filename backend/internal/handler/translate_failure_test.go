package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/platform/translate"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTranslationFailureDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewTranslateHandler(translate.NewAggregator(&translate.Config{HyMT: &translate.HyMTConfig{BaseURL: "http://unused.invalid"}}), nil)
	router := gin.New()
	router.POST("/translate", h.Translate)
	req := httptest.NewRequest(http.MethodPost, "/translate", strings.NewReader(`{"q":["hello"],"target":"invalid","provider":"hymt"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), `"reason":"translation_providers_failed"`)
	require.Contains(t, rec.Body.String(), `"provider":"hymt"`)
	require.Contains(t, rec.Body.String(), `"reason":"hymt: unsupported target language"`)
	require.NotContains(t, rec.Body.String(), "hello")
}
