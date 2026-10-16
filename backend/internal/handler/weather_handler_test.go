package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/platform/weather"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func weatherRouter(handler *WeatherHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/weather/providers", handler.Providers)
	router.POST("/api/v1/weather", handler.Weather)
	return router
}

func TestWeatherProvidersListsBuiltInSources(t *testing.T) {
	handler := NewWeatherHandler(weather.NewAggregator(nil))
	recorder := httptest.NewRecorder()
	weatherRouter(handler).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/weather/providers", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Data struct {
			Providers []weather.ProviderInfo `json:"providers"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Providers, 4)
	names := make([]string, 0, 4)
	for _, provider := range payload.Data.Providers {
		names = append(names, provider.Name)
	}
	require.Contains(t, names, "openmeteo")
	require.Contains(t, names, "tianqi2345")
	for _, provider := range payload.Data.Providers {
		if provider.Name == "tianqi2345" {
			require.True(t, provider.History)
			require.False(t, provider.Current)
		}
	}
}

func TestWeatherRejectsInvalidRequests(t *testing.T) {
	handler := NewWeatherHandler(weather.NewAggregator(nil))
	router := weatherRouter(handler)

	for _, tc := range []struct {
		name string
		body string
		code int
	}{
		{"invalid json", "{", http.StatusBadRequest},
		{"missing location", `{"days":3}`, http.StatusBadRequest},
		{"days out of range", `{"location":"Luoyang","days":99}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/weather", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.code, recorder.Code)
		})
	}
}

func TestWeatherUnavailableWithoutProviders(t *testing.T) {
	handler := NewWeatherHandler(weather.NewAggregator(&weather.Config{EnableFreeProviders: false}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/weather", strings.NewReader(`{"location":"Luoyang"}`))
	request.Header.Set("Content-Type", "application/json")
	weatherRouter(handler).ServeHTTP(recorder, request)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestWeatherProvidersEndpointToleratesNilAggregator(t *testing.T) {
	handler := NewWeatherHandler(nil)
	recorder := httptest.NewRecorder()
	weatherRouter(handler).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/weather/providers", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"providers":[]`)
}
