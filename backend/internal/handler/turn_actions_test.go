//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type turnActionCache struct {
	service.UsageLogRepository
	value  service.TurnActionRecommendation
	digest string
	key    int64
	writes int
}

func (s *turnActionCache) ClaimTurnActionRecommendation(_ context.Context, key int64, digest string, value service.TurnActionRecommendation) (bool, error) {
	if s.digest != "" {
		return false, nil
	}
	s.key = key
	s.digest = digest
	s.value = value
	return true, nil
}
func (s *turnActionCache) SaveTurnActionRecommendation(_ context.Context, key int64, value service.TurnActionRecommendation) error {
	s.value = value
	s.writes++
	return nil
}
func (s *turnActionCache) TurnActionInputDigest(_ context.Context, key int64, session, run, id string) (string, error) {
	return s.digest, nil
}
func (s *turnActionCache) ListTurnActionRecommendations(_ context.Context, key int64, session, run, id string) ([]service.TurnActionRecommendation, error) {
	values := []service.TurnActionRecommendation{}
	if key == s.key && session == s.value.SessionID && run == s.value.RunID && (id == "" || id == s.value.ContextID) {
		values = append(values, s.value)
	}
	return values, nil
}
func turnActionHandler(cache *turnActionCache) *GatewayHandler {
	return &GatewayHandler{gatewayService: service.NewGatewayService(nil, nil, cache, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)}
}
func turnActionContext(method, url, body string, key int64) (*gin.Context, *httptest.ResponseRecorder) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(method, url, strings.NewReader(body))
	if key > 0 {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: key})
	}
	return c, response
}
func TestTurnActionEndpointPersistsAndDeduplicatesWithoutChangingInferenceWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &turnActionCache{}
	h := turnActionHandler(cache)
	body := `{"session_id":"` + observationSession + `","run_id":"turn-1","context_id":"` + strings.Repeat("a", 64) + `","features":{"git_changes":0,"git_conflicts":0,"git_ahead":0,"git_behind":0},"candidates":[]}`
	for i := 0; i < 2; i++ {
		c, response := turnActionContext(http.MethodPost, "/v1/turn/actions/recommend", body, 81)
		h.RecommendTurnActions(c)
		c.Writer.WriteHeaderNow()
		require.Equal(t, 200, response.Code)
		require.Equal(t, "/v1/turn/actions/recommend", c.Request.URL.Path)
	}
	require.Equal(t, 1, cache.writes)
	for _, key := range []int64{81, 82} {
		c, response := turnActionContext(http.MethodGet, "/v1/turn/actions?session_id="+observationSession+"&run_id=turn-1", "", key)
		h.TurnActions(c)
		c.Writer.WriteHeaderNow()
		require.Equal(t, 200, response.Code)
		var parsed struct {
			Data []service.TurnActionRecommendation `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &parsed))
		if key == 81 {
			require.Len(t, parsed.Data, 1)
		} else {
			require.Empty(t, parsed.Data)
		}
	}
	c, response := turnActionContext(http.MethodPost, "/v1/turn/actions/recommend", strings.Replace(body, `"git_changes":0`, `"git_changes":1`, 1), 81)
	h.RecommendTurnActions(c)
	c.Writer.WriteHeaderNow()
	require.Equal(t, 409, response.Code)
}
func TestTurnActionEndpointRejectsUnregisteredFieldsAndMissingIdentity(t *testing.T) {
	h := turnActionHandler(&turnActionCache{})
	for _, body := range []string{`{}`, `{"shell":"rm -rf /"}`, `{"session_id":"invalid"}`} {
		c, response := turnActionContext(http.MethodPost, "/v1/turn/actions/recommend", body, 81)
		h.RecommendTurnActions(c)
		c.Writer.WriteHeaderNow()
		require.Equal(t, 400, response.Code)
	}
	c, response := turnActionContext(http.MethodGet, "/v1/turn/actions", "", 0)
	h.TurnActions(c)
	c.Writer.WriteHeaderNow()
	require.Equal(t, 401, response.Code)
}
func TestTurnActionEndpointRecoversAbandonedPendingAsTimeout(t *testing.T) {
	cache := &turnActionCache{key: 81, value: service.TurnActionRecommendation{SessionID: observationSession, RunID: "turn-1", State: "pending", UpdatedAt: time.Now().Add(-time.Minute).UnixMilli()}}
	c, response := turnActionContext(http.MethodGet, "/v1/turn/actions?session_id="+observationSession+"&run_id=turn-1", "", 81)
	turnActionHandler(cache).TurnActions(c)
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"state":"timed_out"`)
}
func TestTurnActionInternalRelayReusesAuthenticationAndDoesNotWriteParentResponse(t *testing.T) {
	c, response := turnActionContext(http.MethodPost, "/v1/turn/actions/recommend", "{}", 0)
	status, _ := (&GatewayHandler{}).turnActionDecision(c, context.Background(), []byte(`{"model":"laya"}`))
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Empty(t, response.Body.String())
	require.Equal(t, "/v1/turn/actions/recommend", c.Request.URL.Path)
}
