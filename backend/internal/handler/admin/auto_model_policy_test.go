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

func TestAdminAutoModelWhitelistRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &tierPolicyRepo{}
	h := &SettingHandler{settingService: service.NewSettingService(repo, nil)}
	router := gin.New()
	router.GET("/policy", h.GetAutoModelPolicy)
	router.PUT("/policy", h.UpdateAutoModelPolicy)
	valid := `{"blacklist":["doubao*"],"whitelist":["gpt-5.5","provider/private*"]}`
	put := httptest.NewRecorder()
	router.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/policy", strings.NewReader(valid)))
	require.Equal(t, http.StatusOK, put.Code, put.Body.String())
	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/policy", nil))
	require.Equal(t, http.StatusOK, get.Code)
	require.JSONEq(t, put.Body.String(), get.Body.String())
	require.Equal(t, "gpt-5.5", gjson.Get(get.Body.String(), "data.whitelist.0").String())
	// 空白名单表示不限制，仍可显式保存并清空。
	stored := repo.value
	for _, invalid := range []string{`{"blacklist":["doubao*"],"whitelist":["bad*rule"]}`, `{"blacklist":["doubao*"],"whitelist":["a b"]}`, `{"blacklist":["doubao*"],"whitelist":["a","A"]}`} {
		result := httptest.NewRecorder()
		router.ServeHTTP(result, httptest.NewRequest(http.MethodPut, "/policy", strings.NewReader(invalid)))
		require.Equal(t, http.StatusBadRequest, result.Code)
		require.Equal(t, stored, repo.value)
	}
	cleared := httptest.NewRecorder()
	router.ServeHTTP(cleared, httptest.NewRequest(http.MethodPut, "/policy", strings.NewReader(`{"blacklist":["doubao*"],"whitelist":[]}`)))
	require.Equal(t, http.StatusOK, cleared.Code)
	require.False(t, gjson.Get(cleared.Body.String(), "data.whitelist").Exists())
}

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
