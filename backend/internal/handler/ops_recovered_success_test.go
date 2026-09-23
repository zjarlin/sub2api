package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsModelFallbackOutcomeClassification(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		stream        bool
		response      string
		wantRecovered bool
	}{
		{"success_without_upstream_error", http.StatusOK, false, `{"status":"completed"}`, true},
		{"exhausted", http.StatusBadGateway, false, `{"error":{"type":"upstream_error","message":"No models remain"}}`, false},
		{"stream_failed_after_http_200", http.StatusOK, true, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"type\":\"upstream_error\",\"message\":\"No models remain\"}}}\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 2)
			gin.SetMode(gin.TestMode)
			ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.POST("/v1/responses", func(c *gin.Context) {
				setOpsRequestContext(c, "requested-model", tc.stream)
				service.RecordOpsModelFallback(c, "requested-model", "fallback-model", "lower")
				c.Set(opsAccountIDKey, int64(42))
				c.Set(opsUpstreamModelKey, "fallback-model")
				contentType := "application/json"
				if tc.stream {
					contentType = "text/event-stream"
				}
				c.Data(tc.status, contentType, []byte(tc.response))
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
			require.Equal(t, int64(1), OpsErrorLogQueueLength())
			entry := (<-opsErrorLogQueue).entry
			require.Equal(t, tc.wantRecovered, entry.ErrorType == "recovered_upstream")
			if tc.wantRecovered {
				require.Equal(t, http.StatusOK, entry.StatusCode)
				require.EqualValues(t, 42, *entry.AccountID)
				require.Equal(t, "requested-model", entry.RequestedModel)
				require.Equal(t, "fallback-model", entry.UpstreamModel)
				require.Contains(t, entry.ErrorMessage, "Recovered model fallback")
				return
			}
			require.GreaterOrEqual(t, entry.StatusCode, 400)
		})
	}
}
