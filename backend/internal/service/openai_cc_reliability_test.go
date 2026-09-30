//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func forwardCCReliabilityTest(t *testing.T, path string, stream bool, reader io.ReadCloser, ctx context.Context) (*OpenAIForwardResult, error, *gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil).WithContext(ctx)
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": {"cc-reliability"}}, Body: reader}
	account := forceChatResponsesFallbackAccount()
	var result *OpenAIForwardResult
	var err error
	switch {
	case stream && path == "/v1/responses":
		result, err = svc.streamChatCompletionsAsResponses(c, resp, account, "glm-5", nil, nil, false, nil, "glm-5", "glm-5", nil, nil, time.Now(), nil, "", "", nil)
	case stream && path == "/v1/messages":
		result, err = svc.streamChatCompletionsAsAnthropic(c, resp, account, "glm-5", "glm-5", "glm-5", nil, nil, time.Now())
	case stream:
		result, err = svc.streamRawChatCompletions(c, resp, account, "glm-5", "glm-5", "glm-5", nil, nil, time.Now(), 20)
	case path == "/v1/responses":
		result, err = svc.bufferChatCompletionsAsResponses(c, resp, account, "glm-5", nil, nil, false, nil, "glm-5", "glm-5", nil, nil, time.Now())
	case path == "/v1/messages":
		result, err = svc.bufferChatCompletionsAsAnthropic(c, resp, account, "glm-5", "glm-5", "glm-5", nil, nil, time.Now())
	default:
		result, err = svc.bufferRawChatCompletions(c, resp, account, "glm-5", "glm-5", "glm-5", nil, nil, time.Now())
	}
	return result, err, c, rec
}

func TestCCReliabilityReadErrorBeforeOutputFailsOver(t *testing.T) {
	for _, path := range ccPreambleTestPaths {
		for _, stream := range []bool{true, false} {
			for _, cause := range []error{io.ErrUnexpectedEOF, errors.New("stream error: stream ID 7; INTERNAL_ERROR; received from private-peer")} {
				t.Run(path+"/"+cause.Error()+"/"+map[bool]string{true: "stream", false: "json"}[stream], func(t *testing.T) {
					payload := `{"choices":[{"message":{"content":"partial"}}]`
					if stream {
						payload = ccPreambleTestChunk(`{"role":"assistant","content":""}`)
					}
					reader := &openAIResponseFlushReadError{payload: []byte(payload), err: cause}
					result, err, c, rec := forwardCCReliabilityTest(t, path, stream, reader, context.Background())
					var failover *UpstreamFailoverError
					require.ErrorAs(t, err, &failover)
					require.Equal(t, http.StatusBadGateway, failover.StatusCode)
					require.Equal(t, "cc-reliability", failover.ResponseHeaders.Get("x-request-id"))
					require.NotContains(t, string(failover.ResponseBody), "private-peer")
					require.Nil(t, result)
					require.False(t, c.Writer.Written())
					require.Empty(t, rec.Body.String())
				})
			}
		}
	}
}

func TestCCReliabilityTruncatedOutputDoesNotReplayOrComplete(t *testing.T) {
	for _, path := range ccPreambleTestPaths {
		for _, readError := range []bool{false, true} {
			t.Run(path+"/"+map[bool]string{true: "reset", false: "eof"}[readError], func(t *testing.T) {
				payload := ccPreambleTestChunk(`{"content":"partial answer"}`)
				var reader io.ReadCloser = io.NopCloser(strings.NewReader(payload))
				if readError {
					reader = &openAIResponseFlushReadError{payload: []byte(payload), err: io.ErrUnexpectedEOF}
				}
				_, err, c, rec := forwardCCReliabilityTest(t, path, true, reader, context.Background())
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.NotErrorAs(t, err, &failover)
				require.True(t, c.Writer.Written())
				require.Contains(t, rec.Body.String(), "partial answer")
				require.NotContains(t, rec.Body.String(), "response.completed")
				require.NotContains(t, rec.Body.String(), "message_stop")
				require.NotContains(t, rec.Body.String(), "data: [DONE]")
			})
		}
	}
}

func TestCCReliabilityInvalidJSONFailsOver(t *testing.T) {
	for _, path := range ccPreambleTestPaths {
		for _, body := range []string{`{"choices":`, `<html>upstream restarting</html>`} {
			t.Run(path+"/"+body, func(t *testing.T) {
				result, err, c, rec := forwardCCReliabilityTest(t, path, false, io.NopCloser(strings.NewReader(body)), context.Background())
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.Equal(t, http.StatusBadGateway, failover.StatusCode)
				require.Nil(t, result)
				require.False(t, c.Writer.Written())
				require.Empty(t, rec.Body.String())
			})
		}
	}
}

func TestCCReliabilityConvertedFailurePreservesReportedUsage(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/messages"} {
		for _, readError := range []bool{false, true} {
			t.Run(path+"/"+map[bool]string{true: "reset", false: "eof"}[readError], func(t *testing.T) {
				payload := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2}}\n\n"
				var reader io.ReadCloser = io.NopCloser(strings.NewReader(payload))
				if readError {
					reader = &openAIResponseFlushReadError{payload: []byte(payload), err: io.ErrUnexpectedEOF}
				}
				result, err, _, rec := forwardCCReliabilityTest(t, path, true, reader, context.Background())
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.NotErrorAs(t, err, &failover)
				require.NotNil(t, result)
				require.Equal(t, 4, result.Usage.InputTokens)
				require.Equal(t, 2, result.Usage.OutputTokens)
				require.Contains(t, rec.Body.String(), "partial")
				require.NotContains(t, rec.Body.String(), "response.completed")
				require.NotContains(t, rec.Body.String(), "message_stop")
			})
		}
	}
}

func TestCCReliabilityCanceledReadDoesNotReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, path := range ccPreambleTestPaths {
		for _, stream := range []bool{true, false} {
			t.Run(path+"/"+map[bool]string{true: "stream", false: "json"}[stream], func(t *testing.T) {
				reader := &openAIResponseFlushReadError{err: context.Canceled}
				_, err, _, _ := forwardCCReliabilityTest(t, path, stream, reader, ctx)
				var failover *UpstreamFailoverError
				require.NotErrorAs(t, err, &failover)
			})
		}
	}
}
