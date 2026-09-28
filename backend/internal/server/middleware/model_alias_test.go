package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGlobalModelAliasesLoadsCompositeCatalogPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	policy := &service.ModelAliasPolicy{Groups: []service.ModelAliasGroup{{
		Canonical: "deepseek-v4.1-flash", Aliases: []string{"deepseek/deepseek-v4.1-flash"},
	}}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}})
		c.Request = c.Request.WithContext(service.WithModelAliases(c.Request.Context(), policy))
		c.Next()
	})
	router.Use(GlobalModelAliases(&service.SettingService{}))
	assertPolicy := func(c *gin.Context) {
		require.Same(t, policy, service.ModelAliasesFromContext(c.Request.Context()))
		c.Status(http.StatusOK)
	}
	router.GET("/v1/models", func(c *gin.Context) {
		assertPolicy(c)
		require.Empty(t, c.GetHeader("If-None-Match"))
		require.Equal(t, `"old-etag"`, c.GetString("model_alias_if_none_match"))
	})
	router.POST("/v1/responses", assertPolicy)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("If-None-Match", `"old-etag"`)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
}
