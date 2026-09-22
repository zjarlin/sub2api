//go:build unit

package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminVisionFallbackPolicyRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &tierPolicyRepo{}
	h := &SettingHandler{settingService: service.NewSettingService(repo, nil)}
	router := gin.New()
	router.GET("/policy", h.GetVisionFallbackPolicy)
	router.PUT("/policy", h.UpdateVisionFallbackPolicy)
	valid := `{"enabled":true,"models":["preferred","backup"],"allow_unlisted_models":false,"candidate_timeout_seconds":15,"timeout_seconds":40}`
	put := httptest.NewRecorder()
	router.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/policy", strings.NewReader(valid)))
	require.Equal(t, http.StatusOK, put.Code, put.Body.String())
	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/policy", nil))
	require.JSONEq(t, put.Body.String(), get.Body.String())
	stored := repo.value
	for _, invalid := range []string{`{`, `{"enabled":true}`, strings.Replace(valid, `"backup"`, `"preferred"`, 1), strings.Replace(valid, `:15`, `:0`, 1)} {
		result := httptest.NewRecorder()
		router.ServeHTTP(result, httptest.NewRequest(http.MethodPut, "/policy", strings.NewReader(invalid)))
		require.Equal(t, http.StatusBadRequest, result.Code)
		require.Equal(t, stored, repo.value)
	}
	repo.value = `{"enabled":true}`
	broken := httptest.NewRecorder()
	router.ServeHTTP(broken, httptest.NewRequest(http.MethodGet, "/policy", nil))
	require.Equal(t, http.StatusInternalServerError, broken.Code)
}
