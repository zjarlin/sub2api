package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/platform/weather"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterWeatherRoutesRegistersBothEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlerSet := &handler.Handlers{Weather: handler.NewWeatherHandler(weather.NewAggregator(nil))}
	noop := middleware2.APIKeyAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterWeatherRoutes(router, handlerSet, noop)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/weather/providers", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "openmeteo")

	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/weather", strings.NewReader(`{"days":3}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestRegisterWeatherRoutesToleratesMissingHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	noop := middleware2.APIKeyAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterWeatherRoutes(router, &handler.Handlers{}, noop)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/weather/providers", nil))
	require.Equal(t, http.StatusNotFound, recorder.Code)
}
