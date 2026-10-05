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

func TestAdminSearchFallbackPolicyRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &tierPolicyRepo{}
	h := &SettingHandler{settingService: service.NewSettingService(repo, nil)}
	router := gin.New()
	router.GET("/policy", h.GetSearchFallbackPolicy)
	router.PUT("/policy", h.UpdateSearchFallbackPolicy)
	valid := `{"enabled":true,"models":["preferred","backup"],"require_verified":true,"candidate_timeout_seconds":15,"timeout_seconds":40}`
	put := httptest.NewRecorder()
	router.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/policy", strings.NewReader(valid)))
	require.Equal(t, http.StatusOK, put.Code, put.Body.String())
	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/policy", nil))
	require.JSONEq(t, put.Body.String(), get.Body.String())
	stored := repo.value
	for _, invalid := range []string{`{`, strings.Replace(valid, `"backup"`, `"preferred"`, 1), strings.Replace(valid, `:15`, `:0`, 1), strings.Replace(valid, `:40`, `:301`, 1)} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/policy", strings.NewReader(invalid)))
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Equal(t, stored, repo.value)
	}
}
