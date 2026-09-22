package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIVisionValidationErrorDoesNotAppendTerminalEvent(t *testing.T) {
	for _, image := range []string{
		`{"type":"input_image","file_id":"file-provider-scoped"}`,
		`{"type":"input_image","image_url":"http://example.com/image.png"}`,
	} {
		t.Run(image, func(t *testing.T) {
			gateway, primary := newOpenAIVisionErrorGateway(false)
			body := []byte(fmt.Sprintf(`{"model":"deepseek-v4.1-flash","stream":true,"input":[{"role":"user","content":[%s]}]}`, image))
			c, recorder := newGinContextForEndpoint(t, EndpointResponses)
			c.Request.Body = http.NoBody
			before := c.Writer.Size()

			result, err := gateway.Forward(context.Background(), c, &primary, body)

			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.True(t, service.IsResponseCommitted(c))
			writtenBody := recorder.Body.String()
			// 服务层已完成 JSON 错误，外层兜底不得再拼接 SSE。
			handler := &OpenAIGatewayHandler{}
			if !openAIForwardErrorAlreadyCommunicated(c, before, err) {
				require.False(t, handler.ensureForwardErrorResponse(c, false))
			}
			require.Equal(t, writtenBody, recorder.Body.String())
			require.NotContains(t, writtenBody, "event:")
			var response struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, "invalid_request_error", response.Error.Type)
			require.Equal(t, err.Error(), response.Error.Message)
		})
	}
}

func TestOpenAIVisionValidationErrorAfterQueueHeartbeatUsesStreamProtocol(t *testing.T) {
	for _, endpoint := range []string{EndpointResponses, EndpointChatCompletions} {
		t.Run(endpoint, func(t *testing.T) {
			gateway, primary := newOpenAIVisionErrorGateway(false)
			body := []byte(`{"model":"deepseek-v4.1-flash","stream":true,"input":[{"role":"user","content":[{"type":"input_image","file_id":"file-scoped"}]}]}`)
			c, recorder := newGinContextForEndpoint(t, endpoint)
			c.Header("Content-Type", "text/event-stream")
			prefix := ": ping\n\n"
			written, writeErr := c.Writer.WriteString(prefix)
			require.NoError(t, writeErr)
			recordGatewayStreamHeartbeat(c, written)
			c.Writer.Flush()
			var err error
			if endpoint == EndpointChatCompletions {
				body = []byte(strings.ReplaceAll(strings.ReplaceAll(string(body), `"input":`, `"messages":`), `"input_image"`, `"image_url"`))
				_, err = gateway.ForwardAsChatCompletions(context.Background(), c, &primary, body, "", "")
			} else {
				_, err = gateway.Forward(context.Background(), c, &primary, body)
			}
			require.Error(t, err)
			require.True(t, service.IsResponseCommitted(c))
			handler := &OpenAIGatewayHandler{}
			require.False(t, handler.ensureForwardErrorResponse(c, true))
			require.Equal(t, http.StatusOK, recorder.Code)
			terminal := strings.TrimPrefix(recorder.Body.String(), prefix)
			if endpoint == EndpointResponses {
				_, responseError := parseResponsesFailedSSE(t, terminal)
				require.Equal(t, "invalid_request_error", responseError["code"])
				require.Equal(t, err.Error(), responseError["message"])
				return
			}
			require.True(t, strings.HasPrefix(terminal, "data:"))
			require.NotContains(t, terminal, "response.failed")
			require.JSONEq(t, fmt.Sprintf(`{"error":{"type":"invalid_request_error","message":%q}}`, err.Error()), strings.TrimSpace(strings.TrimPrefix(terminal, "data:")))
		})
	}
}

type visionKeepaliveSignalWriter struct {
	gin.ResponseWriter
	flushed chan struct{}
}

func (w *visionKeepaliveSignalWriter) Flush() {
	w.ResponseWriter.Flush()
	select {
	case w.flushed <- struct{}{}:
	default:
	}
}

func TestOpenAIVisionValidationErrorStopsCompactHeartbeat(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("committed_%t", committed), func(t *testing.T) {
			gateway, primary := newOpenAIVisionErrorGateway(false)
			body := []byte(`{"model":"deepseek-v4.1-flash","stream":true,"input":[{"role":"user","content":[{"type":"input_image","file_id":"file-scoped"}]}]}`)
			c, recorder := newGinContextForEndpoint(t, EndpointResponses)
			writer := &visionKeepaliveSignalWriter{ResponseWriter: c.Writer, flushed: make(chan struct{}, 1)}
			c.Writer = writer
			service.MarkOpenAICompactClientStream(c)
			interval := time.Hour
			if committed {
				interval = time.Millisecond
			}
			stop := service.StartOpenAICompactSSEKeepalive(c, interval)
			defer stop()
			if committed {
				select {
				case <-writer.flushed:
				case <-time.After(time.Second):
					t.Fatal("compact 心跳未写出")
				}
			}

			_, err := gateway.Forward(context.Background(), c, &primary, body)

			require.Error(t, err)
			require.Equal(t, committed, service.StopOpenAICompactSSEKeepaliveCommitted(c))
			handler := &OpenAIGatewayHandler{}
			require.False(t, handler.ensureForwardErrorResponse(c, committed))
			if committed {
				require.Equal(t, http.StatusOK, recorder.Code)
				terminalIndex := strings.Index(recorder.Body.String(), "event: response.failed\n")
				require.GreaterOrEqual(t, terminalIndex, 0)
				_, responseError := parseResponsesFailedSSE(t, recorder.Body.String()[terminalIndex:])
				require.Equal(t, err.Error(), responseError["message"])
				return
			}
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.JSONEq(t, fmt.Sprintf(`{"error":{"type":"invalid_request_error","message":%q}}`, err.Error()), recorder.Body.String())
		})
	}
}

func TestOpenAIVisionAvailabilityFailureFinalizesOnce(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		helperPresent bool
		status        int
		message       string
	}{
		{"no_native_helper", false, http.StatusOK, ""},
		{"helper_failed", true, http.StatusBadGateway, "The vision helper could not describe the image; please retry later"},
	} {
		for _, streamStarted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream_started_%t", scenario.name, streamStarted), func(t *testing.T) {
				gateway, primary := newOpenAIVisionErrorGateway(scenario.helperPresent)
				body := []byte(fmt.Sprintf(`{"model":"deepseek-v4.1-flash","stream":%t,"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]}`, streamStarted))
				c, recorder := newGinContextForEndpoint(t, EndpointResponses)
				groupID := int64(7)
				c.Set("api_key", &service.APIKey{ID: 9, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI}})
				setOpsRequestContext(c, "deepseek-v4.1-flash", streamStarted)
				prefix := ""
				if streamStarted {
					prefix = ": ping\n\n"
					_, err := c.Writer.WriteString(prefix)
					require.NoError(t, err)
					c.Writer.Flush()
				}

				result, err := gateway.Forward(context.Background(), c, &primary, body)

				var failoverErr *service.UpstreamFailoverError
					if !scenario.helperPresent {
						require.NoError(t, err)
					require.Nil(t, result)
					require.NotContains(t, string(body), "input_image")
					require.Equal(t, prefix, recorder.Body.String())
					return
				}
				require.ErrorAs(t, err, &failoverErr)
				require.True(t, failoverErr.ShouldRetryNextAccount())
				require.False(t, failoverErr.ShouldReportAccountScheduleFailure())
				require.Equal(t, scenario.status, failoverErr.ClientStatusCode)
				require.Equal(t, prefix, recorder.Body.String(), "视觉助手失败必须留给外层重试或结束响应")
				handler := &OpenAIGatewayHandler{}
				handler.handleFailoverExhausted(c, failoverErr, streamStarted)

				if streamStarted {
					require.Equal(t, http.StatusOK, recorder.Code)
					require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed\n"))
					response, responseError := parseResponsesFailedSSE(t, strings.TrimPrefix(recorder.Body.String(), prefix))
					require.Equal(t, "deepseek-v4.1-flash", response["model"])
					require.Equal(t, scenario.message, responseError["message"])
					return
				}
				require.Equal(t, scenario.status, recorder.Code)
				require.NotContains(t, recorder.Body.String(), "event:")
				require.JSONEq(t, fmt.Sprintf(`{"error":{"type":"api_error","message":%q}}`, scenario.message), recorder.Body.String())
			})
		}
	}
}

func newOpenAIVisionErrorGateway(helperPresent bool) (*service.OpenAIGatewayService, service.Account) {
	primary := service.Account{
		ID: 1, Name: "primary", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"}},
	}
	accounts := []service.Account{primary}
	if helperPresent {
		helper := service.Account{
			ID: 2, Name: "vision-helper", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1,
			Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{"vision-model": "vision-model"}},
			Extra:       map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceResponses)},
		}
		helper.SetUpstreamModelMetadataSnapshot(service.UpstreamModelMetadataSnapshot{Models: map[string]service.UpstreamModelMetadata{
			"vision-model": {ID: "vision-model", InputModalities: []string{"text", "image"}},
		}})
		accounts = append(accounts, helper)
	}
	cfg := &config.Config{Gateway: config.GatewayConfig{VisionFallback: config.GatewayVisionFallbackConfig{Enabled: true}}}
	repo := codexModelsFailoverAccountRepo{accounts: accounts}
	upstream := &codexModelsFailoverHTTPUpstream{statuses: map[int64]int{2: http.StatusBadGateway}}
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	return gateway, primary
}
