package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCommonReadinessChecksDependencies(t *testing.T) {
	for _, tc := range []struct {
		name       string
		checkReady func(context.Context) error
		wantStatus int
	}{
		{name: "ready", checkReady: func(context.Context) error { return nil }, wantStatus: http.StatusOK},
		{name: "dependency_failed", checkReady: func(context.Context) error { return errors.New("database down") }, wantStatus: http.StatusServiceUnavailable},
		{name: "unconfigured", wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			RegisterCommonRoutes(router, tc.checkReady)
			ready := httptest.NewRecorder()
			router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
			require.Equal(t, tc.wantStatus, ready.Code)
			live := httptest.NewRecorder()
			router.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/health", nil))
			require.Equal(t, http.StatusOK, live.Code)
		})
	}
}
