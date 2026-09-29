package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const observationSession = "019ccb31-9520-7120-bc17-556e9a92d860"

type observationCache struct {
	service.UsageLogRepository
	routes []service.AutoModelRouteObservation
	keyID  int64
	err    error
}

func (s *observationCache) SaveAutoModelRoute(_ context.Context, keyID int64, route service.AutoModelRouteObservation) error {
	s.keyID = keyID
	route.AttemptedModels = append([]string(nil), route.AttemptedModels...)
	s.routes = append(s.routes, route)
	return s.err
}

func (s *observationCache) ListAutoModelRoutes(_ context.Context, keyID int64, sessionID, runID string) ([]service.AutoModelRouteObservation, error) {
	result := []service.AutoModelRouteObservation{}
	for _, route := range s.routes {
		if keyID == s.keyID && route.SessionID == sessionID && (runID == "" || route.RunID == runID) {
			result = append(result, route)
		}
	}
	return result, s.err
}

func observationHandler(cache *observationCache) *GatewayHandler {
	return &GatewayHandler{gatewayService: service.NewGatewayService(
		nil, nil, cache, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)}
}

func TestAutoModelObservationStreamPreservesWireAndTracksActualModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &observationCache{}
	h := observationHandler(cache)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.RequestID, "125732b5-c9e0-4038-a0a3-05cba6e71e9b"))
	c.Request.Header.Set("session_id", observationSession)
	c.Request.Header.Set("X-Codex-Turn-Metadata", `{"turn_id":"turn-1"}`)
	finish := h.observeAutoModelRoute(c, &service.APIKey{ID: 81}, "model-first")
	require.Len(t, cache.routes, 1)
	require.Equal(t, "125732b5-c9e0-4038-a0a3-05cba6e71e9b", cache.routes[0].RequestID)
	route := c.MustGet(autoRouteObservationKey).(*service.AutoModelRouteObservation)
	route.AttemptedModels = append(route.AttemptedModels, "model-next")
	c.Header("Content-Type", "text/event-stream")
	wire := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"actual-upstream\",\"status\":\"in_progress\"}}\r\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"actual-upstream\",\"status\":\"completed\",\"error\":null}}\n\n"
	for _, b := range []byte(wire) {
		_, err := c.Writer.Write([]byte{b})
		require.NoError(t, err)
	}
	finish()
	require.Equal(t, wire, recorder.Body.String())
	require.Equal(t, int64(81), cache.keyID)
	require.Equal(t, "completed", route.State)
	require.Equal(t, "actual-upstream", route.ResolvedModel)
	require.Equal(t, []string{"model-first", "model-next"}, route.AttemptedModels)
	require.Equal(t, "responding", cache.routes[1].State)
	require.Equal(t, "selected", cache.routes[0].State)
}

func TestAutoModelObservationTerminalStates(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body, want string
		status                        int
	}{
		{"json completed", "application/json", `{"model":"actual","status":"completed","error":null}`, "completed", 200},
		{"chat completed", "application/json", `{"model":"actual","choices":[{"finish_reason":"stop"}]}`, "completed", 200},
		{"http error", "application/json", `{"error":{"message":"failure"}}`, "failed", 400},
		{"stream failure", "text/event-stream", "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"failure\"}}}\n\n", "failed", 200},
		{"disconnect", "text/event-stream", "data: {\"response\":{\"model\":\"actual\"}}\n\n", "interrupted", 200},
		{"incomplete", "application/json", `{"model":"actual","status":"incomplete"}`, "interrupted", 200},
		{"empty choices", "application/json", `{"model":"actual","choices":[]}`, "interrupted", 200},
		{"chat stream", "text/event-stream", "data: {\"model\":\"actual\",\"choices\":[{}]}\n\ndata: [DONE]\n\n", "completed", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &observationCache{}
			h := observationHandler(cache)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+observationSession+`","turn_id":"turn-1"}`)
			finish := h.observeAutoModelRoute(c, &service.APIKey{ID: 81}, "first")
			c.Header("Content-Type", tc.contentType)
			c.Status(tc.status)
			_, err := c.Writer.WriteString(tc.body)
			require.NoError(t, err)
			finish()
			require.Equal(t, tc.want, cache.routes[len(cache.routes)-1].State)
			require.Equal(t, tc.body, recorder.Body.String())
		})
	}
}

func TestAutoModelObservationBoundsAndOptionalMetadata(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	cache := &observationCache{}
	original := c.Writer
	observationHandler(cache).observeAutoModelRoute(c, &service.APIKey{ID: 81}, "first")()
	require.Same(t, original, c.Writer)
	require.Empty(t, cache.routes)
	w := &autoRouteObserverWriter{ResponseWriter: original, route: &service.AutoModelRouteObservation{State: "selected"}}
	w.Header().Set("Content-Type", "text/event-stream")
	w.capture([]byte("data: " + strings.Repeat("x", autoRouteObservationBodyLimit+1)))
	require.Empty(t, w.pending)
	w.capture([]byte("\n\ndata: {\"type\":\"response.completed\",\"response\":{\"model\":\"actual\"}}\n\n"))
	require.Equal(t, "completed", w.route.State)
}

func TestAutoModelObservationPrefersNativeThreadOverRootSession(t *testing.T) {
	cache := &observationCache{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("session_id", "019ccb31-9520-7120-bc17-556e9a92d861")
	c.Request.Header.Set("X-Codex-Turn-Metadata", `{"thread_id":"`+observationSession+`","turn_id":"turn-1"}`)
	observationHandler(cache).observeAutoModelRoute(c, &service.APIKey{ID: 81}, "first")()
	require.Equal(t, observationSession, cache.routes[0].SessionID)
	require.Equal(t, "turn-1", cache.routes[0].RunID)
}

func TestAutoModelObservationDatabaseFailurePreservesResponse(t *testing.T) {
	cache := &observationCache{err: errors.New("database unavailable")}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"auto","input":"hello"}`))
	c.Request.Header.Set("X-Codex-Turn-Metadata", `{"thread_id":"`+observationSession+`","turn_id":"run-a"}`)
	headers := c.Request.Header.Clone()
	finish := observationHandler(cache).observeAutoModelRoute(c, &service.APIKey{ID: 81}, "deepseek-v4.1-flash")
	payload := `{"model":"deepseek-v4.1-flash","status":"completed","output":[]}`
	c.Header("Content-Type", "application/json")
	c.Status(http.StatusCreated)
	_, err := c.Writer.WriteString(payload)
	require.NoError(t, err)
	finish()
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, payload, recorder.Body.String())
	require.Equal(t, headers, c.Request.Header)
	require.Equal(t, "run-a", cache.routes[0].RunID)
	require.Greater(t, cache.routes[len(cache.routes)-1].Revision, cache.routes[0].Revision)
}

func TestAutoModelRoutesFiltersRunWithinOwnedSession(t *testing.T) {
	cache := &observationCache{keyID: 81, routes: []service.AutoModelRouteObservation{
		{SessionID: observationSession, RunID: "run-a"}, {SessionID: observationSession, RunID: "run-b"},
	}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/auto/routes?session_id="+observationSession+"&run_id=run-b", nil)
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 81})
	observationHandler(cache).AutoModelRoutes(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"run_id":"run-b"`)
	require.NotContains(t, recorder.Body.String(), `"run_id":"run-a"`)
}

func TestAutoModelRoutesRequiresKeyAndSessionAndDoesNotExposeOtherKeys(t *testing.T) {
	cache := &observationCache{keyID: 81, routes: []service.AutoModelRouteObservation{{SessionID: observationSession}}}
	h := observationHandler(cache)
	for _, tc := range []struct {
		key     int64
		session string
		want    int
		empty   bool
	}{
		{0, observationSession, 401, false}, {81, "bad", 400, false},
		{81, observationSession, 200, false}, {82, observationSession, 200, true},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/auto/routes?session_id="+tc.session, nil)
		if tc.key > 0 {
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: tc.key})
		}
		h.AutoModelRoutes(c)
		require.Equal(t, tc.want, recorder.Code)
		if tc.empty {
			require.JSONEq(t, `{"object":"list","data":[]}`, recorder.Body.String())
		}
	}
	cache.err = errors.New("secret redis detail")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/auto/routes?session_id="+observationSession, nil)
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 81})
	h.AutoModelRoutes(c)
	require.Equal(t, 503, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "secret")
}
