package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *GatewayHandler) TurnActions(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	session, run, contextID := c.Query("session_id"), c.Query("run_id"), c.Query("context_id")
	if err := service.ValidateTurnActionIdentity(session, run, contextID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	session = uuid.MustParse(session).String()
	store, err := h.gatewayService.TurnActionStore()
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	values, err := store.ListTurnActionRecommendations(c.Request.Context(), key.ID, session, run, contextID)
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	for index := range values {
		// 进程中断留下的占位记录不会永久显示为判断中，也不会自动再调用模型。
		if values[index].State == "pending" && time.Now().UnixMilli()-values[index].UpdatedAt > 3000 {
			values[index].State = "timed_out"
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": values})
}

func (h *GatewayHandler) RecommendTurnActions(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	var input service.TurnActionRecommendInput
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 128*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		c.Status(http.StatusBadRequest)
		return
	}
	if err := input.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	input.SessionID = uuid.MustParse(input.SessionID).String()
	store, err := h.gatewayService.TurnActionStore()
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	normalized, err := json.Marshal(input)
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256(normalized)
	digest := hex.EncodeToString(sum[:])
	result := service.NewTurnActionRecommendation(input)
	claimed, err := store.ClaimTurnActionRecommendation(ctx, key.ID, digest, result)
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	if !claimed {
		previousDigest, err := store.TurnActionInputDigest(ctx, key.ID, input.SessionID, input.RunID, input.ContextID)
		if err != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		if previousDigest != digest {
			c.JSON(http.StatusConflict, gin.H{"error": "context_id already used for different candidates"})
			return
		}
		values, err := store.ListTurnActionRecommendations(ctx, key.ID, input.SessionID, input.RunID, input.ContextID)
		if err != nil || len(values) != 1 {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		result = values[0]
	} else {
		result.State = "completed"
		if len(input.Candidates) > 0 {
			payload, err := service.TurnActionDecisionRequest(input)
			if err != nil {
				result.State = "failed"
			} else {
				status, response := h.turnActionDecision(c, ctx, payload)
				result.State = "failed"
				if status >= 200 && status < 300 {
					if judged, err := service.ApplyTurnActionDecision(input, response); err == nil {
						result = judged
					}
				}
				if ctx.Err() != nil {
					result.State = "timed_out"
				}
			}
		}
		result.UpdatedAt = time.Now().UnixMilli()
		// 客户端预算到期后仍限时保存终态，后续 GET 能读取实际超时结果。
		persistCtx, stop := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 250*time.Millisecond)
		defer stop()
		if err := store.SaveTurnActionRecommendation(persistCtx, key.ID, result); err != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}

// 内部子请求沿用 System One 的选号、并发槽、额度检查与真实账号用量记录。
// 不另建绕过鉴权的决策客户端，也不修改外部 Responses 请求。
func (h *GatewayHandler) turnActionDecision(c *gin.Context, ctx context.Context, payload []byte) (int, []byte) {
	child := c.Copy()
	child.Request = c.Request.Clone(ctx)
	child.Request.Body = io.NopCloser(bytes.NewReader(payload))
	child.Request.ContentLength = int64(len(payload))
	writer := &turnActionDecisionWriter{header: make(http.Header), status: http.StatusOK}
	child.Writer = writer
	h.SystemOneRelay(child)
	return writer.status, writer.body.Bytes()
}

type turnActionDecisionWriter struct {
	gin.ResponseWriter
	header  http.Header
	body    bytes.Buffer
	status  int
	written bool
}

func (w *turnActionDecisionWriter) Header() http.Header { return w.header }
func (w *turnActionDecisionWriter) WriteHeader(code int) {
	if !w.written {
		w.status = code
	}
}
func (w *turnActionDecisionWriter) WriteHeaderNow() { w.written = true }
func (w *turnActionDecisionWriter) Write(value []byte) (int, error) {
	w.written = true
	return w.body.Write(value)
}
func (w *turnActionDecisionWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}
func (w *turnActionDecisionWriter) Status() int { return w.status }
func (w *turnActionDecisionWriter) Size() int {
	if !w.written {
		return -1
	}
	return w.body.Len()
}
func (w *turnActionDecisionWriter) Written() bool { return w.written }
