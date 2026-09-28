package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// 复用 HTTP Responses 的请求转换与计费，只把事件写入当前 WebSocket。
func (s *OpenAIGatewayService) proxyResponsesViaChatWebSocketTurn(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	originalModel string,
	writeClientMessage func([]byte) error,
) (*OpenAIForwardResult, error) {
	if c == nil {
		return nil, errors.New("websocket chat bridge context is nil")
	}
	writer := &openAIWSChatBridgeWriter{
		ResponseWriter: c.Writer,
		header:         make(http.Header),
		status:         http.StatusOK,
		size:           -1,
		writeMessage:   writeClientMessage,
		originalModel:  originalModel,
	}
	originalWriter := c.Writer
	c.Writer = writer
	defer func() { c.Writer = originalWriter }()
	c.Set("openai_ws_http_bridge", true)
	observer := beginUpstreamResponseModelObservation(c)

	result, err := s.forwardResponsesViaRawChatCompletions(ctx, c, account, body)
	if result != nil {
		result.RequestID = writer.responseID
		result.ResponseID = writer.responseID
		result.OpenAIWSMode = true
		result.Model = originalModel
		result.RequestedReasoningEffort = CanonicalRequestedReasoningEffort(body, originalModel, result.UpstreamModel)
		result.UpstreamResponseModel = observer.Model()
		result.UpstreamResponseModelConflict = observer.Conflict()
		result.ResponseHeaders = cloneHeader(result.UpstreamHeaders)
		result.UpstreamTerminalEvent = writer.terminalEvent
		// CC 上游没有 response.id 存储，需要重放完整助手消息和工具项。
		result.wsReplayInput = writer.replay.AllItems()
		result.wsReplayInputExists = len(result.wsReplayInput) > 0
		result.wsAccountFailoverReplayInput = result.wsReplayInput
	}
	if writer.writeErr != nil {
		return result, writer.writeErr
	}
	if err == nil && writer.terminalEvent != "response.completed" && writer.terminalEvent != "response.incomplete" {
		err = errors.New("chat bridge ended without a terminal Responses event")
	}
	if err == nil {
		return result, nil
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) && writer.responseID == "" {
		return result, err
	}
	status := writer.status
	if status < http.StatusBadRequest {
		status = http.StatusBadGateway
	}
	message := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(writer.errorBody))
	if message == "" {
		message = "Upstream Chat Completions bridge failed"
	}
	event := buildOpenAIWSHTTPBridgeErrorEvent(status, message)
	if writeErr := writeClientMessage(event); writeErr != nil {
		return result, fmt.Errorf("write chat bridge error event: %w", writeErr)
	}
	markOpenAIWSClientVisibleFailure(c, "error", event)
	return result, err
}

// 隔离已升级连接的 HTTP writer，错误响应留给桥接入口转成 WebSocket error。
type openAIWSChatBridgeWriter struct {
	gin.ResponseWriter
	header        http.Header
	status        int
	size          int
	errorBody     []byte
	writeMessage  func([]byte) error
	writeErr      error
	responseID    string
	originalModel string
	terminalEvent string
	replay        openAIWSToolCallReplayCollector
}

func (w *openAIWSChatBridgeWriter) WriteResponsesEvent(event apicompat.ResponsesStreamEvent) error {
	if event.Response != nil {
		if w.responseID == "" {
			w.responseID = "resp_" + uuid.NewString()
		}
		event.Response.ID = w.responseID
		if w.originalModel != "" {
			event.Response.Model = w.originalModel
		}
	}
	message, err := json.Marshal(event)
	if err != nil {
		w.writeErr = err
		return err
	}
	if err := w.writeMessage(message); err != nil {
		w.writeErr = err
		return err
	}
	if isOpenAIWSTerminalEvent(event.Type) {
		w.terminalEvent = event.Type
	}
	w.replay.AddEvent(event.Type, message)
	w.WriteHeaderNow()
	w.size += len(message)
	return nil
}

func (w *openAIWSChatBridgeWriter) Header() http.Header { return w.header }
func (w *openAIWSChatBridgeWriter) Status() int         { return w.status }
func (w *openAIWSChatBridgeWriter) Size() int           { return w.size }
func (w *openAIWSChatBridgeWriter) Written() bool       { return w.size >= 0 }
func (w *openAIWSChatBridgeWriter) Flush()              { w.WriteHeaderNow() }

func (w *openAIWSChatBridgeWriter) WriteHeader(status int) {
	if !w.Written() && status > 0 {
		w.status = status
	}
}

func (w *openAIWSChatBridgeWriter) WriteHeaderNow() {
	if !w.Written() {
		w.size = 0
	}
}

func (w *openAIWSChatBridgeWriter) Write(body []byte) (int, error) {
	w.WriteHeaderNow()
	remaining := openAIWSHTTPBridgeErrorBodyLimitBytes - len(w.errorBody)
	if remaining > 0 {
		w.errorBody = append(w.errorBody, body[:min(remaining, len(body))]...)
	}
	w.size += len(body)
	return len(body), nil
}

func (w *openAIWSChatBridgeWriter) WriteString(body string) (int, error) {
	return w.Write([]byte(body))
}
