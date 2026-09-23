//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type tierPolicyRepo struct {
	service.SettingRepository
	value string
}

func (r *tierPolicyRepo) GetValue(context.Context, string) (string, error) { return r.value, nil }
func (r *tierPolicyRepo) Set(_ context.Context, _ string, value string) error {
	r.value = value
	return nil
}

func TestAdminModelFallbackPolicyRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &tierPolicyRepo{}
	h := &SettingHandler{settingService: service.NewSettingService(repo, nil)}
	router := gin.New()
	router.GET("/policy", h.GetModelFallbackPolicy)
	router.PUT("/policy", h.UpdateModelFallbackPolicy)
	valid := `{"enabled":true,"tiers":[{"name":"top","models":["a","b"]},{"name":"low","models":["c"]}]}`
	put := httptest.NewRecorder()
	router.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/policy", strings.NewReader(valid)))
	require.Equal(t, http.StatusOK, put.Code, put.Body.String())
	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/policy", nil))
	require.JSONEq(t, put.Body.String(), get.Body.String())
	stored := repo.value
	invalid := httptest.NewRecorder()
	router.ServeHTTP(invalid, httptest.NewRequest(http.MethodPut, "/policy", strings.NewReader(`{"enabled":true,"tiers":[{"name":"top","models":["a","a"]}]}`)))
	require.Equal(t, http.StatusBadRequest, invalid.Code)
	require.Equal(t, stored, repo.value)
}
