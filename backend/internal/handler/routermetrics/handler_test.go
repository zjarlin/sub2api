package routermetrics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fixtureSource struct {
	groups []int64
	calls  int
}

func (f *fixtureSource) ParseFilter(_ string, _, _ []string, groups []int64) (service.ChannelMonitorV2Filter, error) {
	f.groups = groups
	return service.ChannelMonitorV2Filter{GroupIDs: groups}, nil
}
func (f *fixtureSource) RoutingModels(context.Context, service.ChannelMonitorV2Filter) (*service.ChannelMonitorV2List[service.ChannelMonitorV2ModelRow], error) {
	f.calls++
	return &service.ChannelMonitorV2List[service.ChannelMonitorV2ModelRow]{Coverage: service.ChannelMonitorV2Coverage{DataThrough: time.Now()}, Items: []service.ChannelMonitorV2ModelRow{
		{Model: "model-a", Metrics: service.ChannelMonitorV2Metric{SuccessRequests: 8, ErrorRequests: 2}},
		{Model: "model-a", Metrics: service.ChannelMonitorV2Metric{SuccessRequests: 1, ErrorRequests: 1}},
		{Model: "model-empty"},
	}}, nil
}
func TestMetricsKeyScopeAndRates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	source := &fixtureSource{}
	engine := gin.New()
	hash := sha256.Sum256([]byte("dedicated-test-key"))
	Register(engine.Group("/api/v1"), source, hex.EncodeToString(hash[:]), 6)
	get := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/router/models/health?group_id=1", nil)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	require.Equal(t, 401, get("").Code)
	require.Equal(t, 401, get("ordinary-inference-key").Code)
	require.Zero(t, source.calls)
	out := get("dedicated-test-key")
	require.Equal(t, 200, out.Code)
	var snap Snapshot
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &snap))
	require.Equal(t, []int64{6}, source.groups)
	require.Equal(t, int64(12), snap.Models[0].Samples)
	require.InDelta(t, 0.75, *snap.Models[0].SuccessRate, 0.0001)
	require.Nil(t, snap.Models[1].SuccessRate)
	get("dedicated-test-key")
	require.Equal(t, 1, source.calls)
}
func TestMetricsDisabledWithoutCompleteConfig(t *testing.T) {
	for _, group := range []int64{0, 6} {
		engine := gin.New()
		Register(engine.Group("/api/v1"), &fixtureSource{}, "", group)
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, httptest.NewRequest("GET", "/api/v1/router/models/health", nil))
		require.Equal(t, 503, out.Code)
	}
}
