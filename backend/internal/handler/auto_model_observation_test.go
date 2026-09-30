package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

func TestAutoModelObservationBounds(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	original := c.Writer
	w := &autoRouteObserverWriter{ResponseWriter: original, route: &service.AutoModelRouteObservation{State: "selected"}}
	w.Header().Set("Content-Type", "text/event-stream")
	w.capture([]byte("data: " + strings.Repeat("x", autoRouteObservationBodyLimit+1)))
	require.Empty(t, w.pending)
	w.capture([]byte("\n\ndata: {\"type\":\"response.completed\",\"response\":{\"model\":\"actual\"}}\n\n"))
	require.Equal(t, "completed", w.route.State)
}

func TestAutoModelObservationSuppliesQueryableIDsWithoutMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, selected, contentType, body, want string
		status                                  int
	}{
		{"success", "first", "application/json", `{"model":"actual","status":"completed"}`, "completed", http.StatusOK},
		{"http failure", "first", "application/json", `{"error":{"message":"fixture failure"}}`, "failed", http.StatusBadGateway},
		{"stream failure", "first", "text/event-stream", "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"fixture failure\"}}}\n\n", "failed", http.StatusOK},
		{"preflight failure", "", "application/json", `{"error":{"message":"no eligible model"}}`, "failed", http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &observationCache{}
			h := observationHandler(cache)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			requestBody := `{"model":"auto","input":"sensitive fixture input"}`
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(requestBody))
			c.Request.Header.Set("Authorization", "Bearer private-fixture-key")
			headers := c.Request.Header.Clone()
			c.Header("Access-Control-Expose-Headers", "ETag, Server-Timing")
			finish := h.observeAutoModelRoute(c, &service.APIKey{ID: 81}, tc.selected)
			c.Header("Content-Type", tc.contentType)
			c.Status(tc.status)
			_, err := c.Writer.WriteString(tc.body)
			require.NoError(t, err)
			finish()

			responseHeaders := recorder.Result().Header
			sessionID := responseHeaders.Get("X-Sub2API-Session-ID")
			runID := responseHeaders.Get("X-Sub2API-Run-ID")
			requestID := responseHeaders.Get("X-Sub2API-Request-ID")
			for _, id := range []string{sessionID, runID, requestID} {
				_, err = uuid.Parse(id)
				require.NoError(t, err)
			}
			exposed := strings.Join(responseHeaders.Values("Access-Control-Expose-Headers"), ", ")
			require.Contains(t, exposed, "ETag")
			require.Contains(t, exposed, "X-Sub2API-Session-ID")
			require.Contains(t, exposed, "X-Sub2API-Run-ID")
			require.Contains(t, exposed, "X-Sub2API-Request-ID")
			require.Equal(t, tc.status, recorder.Code)
			require.Equal(t, tc.body, recorder.Body.String())
			require.Equal(t, headers, c.Request.Header)
			body, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			require.Equal(t, requestBody, string(body))
			stored, err := json.Marshal(cache.routes)
			require.NoError(t, err)
			require.NotContains(t, string(stored), "sensitive fixture input")
			require.NotContains(t, string(stored), "private-fixture-key")

			query := url.Values{"session_id": {sessionID}, "run_id": {runID}}
			lookup := httptest.NewRecorder()
			queryContext, _ := gin.CreateTestContext(lookup)
			queryContext.Request = httptest.NewRequest(http.MethodGet, "/v1/auto/routes?"+query.Encode(), nil)
			queryContext.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 81})
			h.AutoModelRoutes(queryContext)
			require.Equal(t, http.StatusOK, lookup.Code)
			var response struct {
				Data []service.AutoModelRouteObservation `json:"data"`
			}
			require.NoError(t, json.Unmarshal(lookup.Body.Bytes(), &response))
			require.NotEmpty(t, response.Data)
			last := response.Data[len(response.Data)-1]
			require.Equal(t, tc.want, last.State)
			require.Equal(t, requestID, last.RequestID)
			require.Equal(t, runID, last.TurnID)
		})
	}
}

func TestAutoModelObservationPreservesValidIDsAndReplacesInvalidMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, sessionHeader, metadata, wantSession, wantRun string
	}{
		{name: "malformed metadata", metadata: `not-json`},
		{name: "session only", sessionHeader: observationSession, wantSession: observationSession},
		{name: "run only", metadata: `{"run_id":"run-a"}`, wantRun: "run-a"},
		{name: "invalid session", metadata: `{"thread_id":"not-a-uuid","turn_id":"turn-a"}`, wantRun: "turn-a"},
		{name: "native thread", sessionHeader: "019ccb31-9520-7120-bc17-556e9a92d861", metadata: `{"thread_id":"` + observationSession + `","turn_id":"turn-a"}`, wantSession: observationSession, wantRun: "turn-a"},
		{name: "metadata session and run", metadata: `{"session_id":"` + observationSession + `","run_id":"run-a"}`, wantSession: observationSession, wantRun: "run-a"},
		{name: "overlong run", sessionHeader: observationSession, metadata: `{"turn_id":"` + strings.Repeat("x", 129) + `"}`, wantSession: observationSession},
		{name: "control character run", sessionHeader: observationSession, metadata: `{"turn_id":"run\r\ninjected"}`, wantSession: observationSession},
		{name: "blank run", sessionHeader: observationSession, metadata: `{"turn_id":"  "}`, wantSession: observationSession},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &observationCache{}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("session_id", tc.sessionHeader)
			c.Request.Header.Set("X-Codex-Turn-Metadata", tc.metadata)
			observationHandler(cache).observeAutoModelRoute(c, &service.APIKey{ID: 81}, "first")()
			require.NotEmpty(t, cache.routes)
			route := cache.routes[0]
			for _, id := range []struct{ got, want string }{{route.SessionID, tc.wantSession}, {route.RunID, tc.wantRun}} {
				if id.want != "" {
					require.Equal(t, id.want, id.got)
					continue
				}
				_, err := uuid.Parse(id.got)
				require.NoError(t, err)
			}
			require.Equal(t, route.SessionID, c.Writer.Header().Get("X-Sub2API-Session-ID"))
			require.Equal(t, route.RunID, c.Writer.Header().Get("X-Sub2API-Run-ID"))
		})
	}
}

func TestAutoModelObservationRecordsPreflightFailure(t *testing.T) {
	cache := &observationCache{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.RequestID, "319e0350-6d27-4962-b6bb-f2e9c3197b99"))
	c.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+observationSession+`","turn_id":"turn-1"}`)
	c.Set("virtual_model_id", "auto")
	c.Set(autoModelPlanKey, []service.AutoModelCandidate{{Model: "gpt-5.5", Platform: service.PlatformOpenAI, Reason: "no_compatible_account"}})

	finish := observationHandler(cache).observeAutoModelRoute(c, &service.APIKey{ID: 81}, "")
	c.Status(http.StatusServiceUnavailable)
	finish()

	require.NotEmpty(t, cache.routes)
	route := cache.routes[len(cache.routes)-1]
	require.Equal(t, "319e0350-6d27-4962-b6bb-f2e9c3197b99", route.RequestID)
	require.Equal(t, "auto", route.RequestedModel)
	require.Equal(t, "failed", route.State)
	require.Empty(t, route.SelectedModel)
	require.Empty(t, route.AttemptedModels)
	require.Equal(t, []service.AutoModelCandidate{{Model: "gpt-5.5", Platform: service.PlatformOpenAI, Reason: "no_compatible_account"}}, route.Candidates)
}

func TestAutoModelObservationMiddlewarePolicyFailureWithoutMetadata(t *testing.T) {
	cache := &observationCache{}
	h := observationHandler(cache)
	h.settingService = service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{
		service.SettingKeyAutoModelPolicy: `{invalid`,
	}}, nil)
	key := &service.APIKey{ID: 81, Group: &service.Group{ID: 71, Platform: service.PlatformOpenAI}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), key)
	}, h.AutoModelMiddleware(nil))
	forwarded := false
	router.POST("/v1/responses", func(c *gin.Context) {
		forwarded = true
		c.Status(http.StatusOK)
	})
	router.GET("/v1/auto/routes", h.AutoModelRoutes)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"auto","input":"fixture"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Auto model routing policy is unavailable")
	require.False(t, forwarded)
	require.NotEmpty(t, cache.routes)
	last := cache.routes[len(cache.routes)-1]
	require.Equal(t, "failed", last.State)
	require.Empty(t, last.SelectedModel)
	require.Empty(t, last.AttemptedModels)
	require.Equal(t, "auto", last.RequestedModel)
	responseHeaders := recorder.Result().Header
	require.Equal(t, last.SessionID, responseHeaders.Get("X-Sub2API-Session-ID"))
	require.Equal(t, last.RunID, responseHeaders.Get("X-Sub2API-Run-ID"))
	require.Equal(t, last.RequestID, responseHeaders.Get("X-Sub2API-Request-ID"))

	query := url.Values{"session_id": {last.SessionID}, "run_id": {last.RunID}}
	lookup := httptest.NewRecorder()
	router.ServeHTTP(lookup, httptest.NewRequest(http.MethodGet, "/v1/auto/routes?"+query.Encode(), nil))
	require.Equal(t, http.StatusOK, lookup.Code)
	var response struct {
		Data []service.AutoModelRouteObservation `json:"data"`
	}
	require.NoError(t, json.Unmarshal(lookup.Body.Bytes(), &response))
	require.NotEmpty(t, response.Data)
	require.Equal(t, "failed", response.Data[len(response.Data)-1].State)
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
