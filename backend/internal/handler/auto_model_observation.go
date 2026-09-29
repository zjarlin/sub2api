package handler

import (
	"bytes"
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const autoRouteObservationKey = "auto_route_observation"
const autoRouteObservationBodyLimit = 2 << 20

func (h *GatewayHandler) AutoModelRoutes(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Invalid API key"}})
		return
	}
	sessionID := c.Query("session_id")
	parsedSession, err := uuid.Parse(sessionID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "session_id must be a UUID"}})
		return
	}
	sessionID = parsedSession.String()
	runID := c.Query("run_id")
	if len(runID) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "run_id must not exceed 128 characters"}})
		return
	}
	store, err := h.gatewayService.AutoModelRouteStore()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Auto route observations unavailable"}})
		return
	}
	routes, err := store.ListAutoModelRoutes(c.Request.Context(), key.ID, sessionID, runID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Unable to read auto route observations"}})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": routes})
}

func (h *GatewayHandler) observeAutoModelRoute(c *gin.Context, key *service.APIKey, selected string) func() {
	metadata := gjson.Parse(c.GetHeader("X-Codex-Turn-Metadata"))
	sessionID := metadata.Get("thread_id").String()
	if sessionID == "" {
		sessionID = service.ExtractClientSessionID(c)
	}
	if sessionID == "" {
		sessionID = metadata.Get("session_id").String()
	}
	turnID := metadata.Get("turn_id").String()
	if turnID == "" {
		turnID = metadata.Get("run_id").String()
	}
	parsedSession, err := uuid.Parse(sessionID)
	if err != nil || len(turnID) == 0 || len(turnID) > 128 {
		return func() {}
	}
	sessionID = parsedSession.String()
	store, err := h.gatewayService.AutoModelRouteStore()
	if err != nil {
		return func() {}
	}
	now := time.Now().UnixMilli()
	requestID := contentModerationRequestID(c.Request.Context())
	if requestID == "" {
		requestID = uuid.NewString()
	}
	route := &service.AutoModelRouteObservation{
		RequestID: requestID, SessionID: sessionID, TurnID: turnID, RunID: turnID,
		RequestedModel: autoModelID, SelectedModel: selected, AttemptedModels: []string{selected},
		State: "selected", StartedAt: now, UpdatedAt: now,
	}
	if value, ok := c.Get(autoModelPlanKey); ok {
		route.Candidates, _ = value.([]service.AutoModelCandidate)
	}
	persist := func() {
		route.UpdatedAt = time.Now().UnixMilli()
		route.Revision++
		snapshot := *route
		snapshot.AttemptedModels = slices.Clone(route.AttemptedModels)
		h.submitMandatoryUsageRecordTask(c.Request.Context(), func(parent context.Context) {
			ctx, cancel := context.WithTimeout(parent, 250*time.Millisecond)
			defer cancel()
			if err := store.SaveAutoModelRoute(ctx, key.ID, snapshot); err != nil {
				logger.FromContext(ctx).Warn("gateway.auto_route_observation_failed", zap.Error(err))
			}
		})
	}
	persist()
	c.Set(autoRouteObservationKey, route)
	writer := &autoRouteObserverWriter{ResponseWriter: c.Writer, route: route, persist: persist}
	c.Writer = writer
	return func() {
		if !writer.streaming && len(writer.pending) > 0 {
			writer.observe(writer.pending)
		}
		if route.State == "selected" || route.State == "responding" {
			route.State = "interrupted"
		}
		if writer.Status() >= http.StatusBadRequest {
			route.State = "failed"
		}
		persist()
	}
}

type autoRouteObserverWriter struct {
	gin.ResponseWriter
	route       *service.AutoModelRouteObservation
	persist     func()
	pending     []byte
	streaming   bool
	discardLine bool
}

func (w *autoRouteObserverWriter) Write(body []byte) (int, error) {
	n, err := w.ResponseWriter.Write(body)
	w.capture(body[:n])
	return n, err
}

func (w *autoRouteObserverWriter) WriteString(body string) (int, error) {
	return w.Write([]byte(body))
}

func (w *autoRouteObserverWriter) capture(body []byte) {
	w.streaming = strings.Contains(w.Header().Get("Content-Type"), "text/event-stream")
	if !w.streaming {
		if !w.discardLine && len(w.pending)+len(body) <= autoRouteObservationBodyLimit {
			w.pending = append(w.pending, body...)
		} else {
			w.pending = nil
			w.discardLine = true
		}
		return
	}
	for len(body) > 0 {
		index := bytes.IndexByte(body, '\n')
		piece := body
		if index >= 0 {
			piece = body[:index]
		}
		if !w.discardLine && len(w.pending)+len(piece) <= autoRouteObservationBodyLimit {
			w.pending = append(w.pending, piece...)
		} else {
			w.pending = nil
			w.discardLine = true
		}
		if index < 0 {
			return
		}
		if !w.discardLine && bytes.HasPrefix(w.pending, []byte("data:")) {
			payload := bytes.TrimSpace(w.pending[5:])
			if bytes.Equal(payload, []byte("[DONE]")) && w.route.State == "responding" {
				w.route.State = "completed"
			} else {
				w.observe(payload)
			}
		}
		w.pending = nil
		w.discardLine = false
		body = body[index+1:]
	}
}

func (w *autoRouteObserverWriter) observe(body []byte) {
	if !gjson.ValidBytes(body) {
		return
	}
	previousModel := w.route.ResolvedModel
	event := gjson.ParseBytes(body)
	response := event
	if nested := event.Get("response"); nested.IsObject() {
		response = nested
	}
	model := response.Get("model").String()
	if model != "" && model != autoModelID && len(model) <= 512 {
		w.route.ResolvedModel = model
		if w.route.State == "selected" {
			w.route.State = "responding"
		}
	}
	if event.Get("type").String() == "response.completed" || response.Get("status").String() == "completed" ||
		(!w.streaming && len(response.Get("choices").Array()) > 0) {
		w.route.State = "completed"
	}
	if response.Get("error").Exists() && response.Get("error").Type != gjson.Null ||
		event.Get("type").String() == "response.failed" || event.Get("type").String() == "error" || response.Get("status").String() == "failed" {
		w.route.State = "failed"
	}
	if event.Get("type").String() == "response.incomplete" || response.Get("status").String() == "incomplete" {
		w.route.State = "interrupted"
	}
	if w.route.ResolvedModel != previousModel && w.persist != nil {
		w.persist()
	}
}
