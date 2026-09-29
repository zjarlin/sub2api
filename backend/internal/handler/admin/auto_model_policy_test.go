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
	"github.com/tidwall/gjson"
)

func TestAdminAutoModelBlacklistRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &tierPolicyRepo{}
	h := &SettingHandler{settingService: service.NewSettingService(repo, nil)}
	router := gin.New()
	router.GET("/policy", h.GetAutoModelPolicy)
	router.PUT("/policy", h.UpdateAutoModelPolicy)
	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/policy", nil))
	require.Equal(t, http.StatusOK, get.Code)
	require.Equal(t, "doubao*", gjson.Get(get.Body.String(), "data.blacklist.0").String())
	for _, body := range []string{`{"blacklist":["doubao*","gpt-5.5"]}`, `{"blacklist":[]}`} {
		put := httptest.NewRecorder()
		router.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/policy", strings.NewReader(body)))
		require.Equal(t, http.StatusOK, put.Code, put.Body.String())
		get = httptest.NewRecorder()
		router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/policy", nil))
		require.JSONEq(t, put.Body.String(), get.Body.String())
	}
	stored := repo.value
	invalid := httptest.NewRecorder()
	router.ServeHTTP(invalid, httptest.NewRequest(http.MethodPut, "/policy", strings.NewReader(`{"blacklist":["bad*rule"]}`)))
	require.Equal(t, http.StatusBadRequest, invalid.Code)
	require.Equal(t, stored, repo.value)
}
