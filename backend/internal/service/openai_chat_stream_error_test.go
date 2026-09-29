//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 重放 Trae 实际返回的额度错误，以及其错误后追加的伪成功结束帧。
const traeQuotaErrorStream = "event: error\n" +
	`data: {"error":{"code":4008,"message":"solo error code=4008 msg=Your requests have exceeded the quota.","type":"upstream_error"}}` + "\n\n" +
	"data: [DONE]\n\n" +
	`data: {"choices":[{"delta":{},"finish_reason":"stop","index":0}]}` + "\n\ndata: [DONE]\n\n"

func forwardCCErrorTest(t *testing.T, path, stream string) (*OpenAIForwardResult, error, *gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	body := []byte(`{"model":"glm-5","stream":true,"max_tokens":32,"input":"hello","messages":[{"role":"user","content":"hello"}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(stream)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	var result *OpenAIForwardResult
	var err error
	switch path {
	case "/v1/responses":
		result, err = svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	case "/v1/messages":
		result, err = svc.ForwardAsAnthropic(context.Background(), c, forceChatMessagesFallbackAccount(), body, "", "")
	case "/v1/chat/completions":
		result, err = svc.ForwardAsChatCompletions(context.Background(), c, forceChatResponsesFallbackAccount(), body, "", "")
	default:
		t.Fatalf("unsupported test endpoint %s", path)
	}
	return result, err, c, rec
}

func TestCCStreamQuotaErrorBeforeOutputFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/responses", "/v1/messages", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			result, err, c, rec := forwardCCErrorTest(t, path, traeQuotaErrorStream)
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
			require.Contains(t, string(failoverErr.ResponseBody), "Your requests have exceeded the quota.")
			require.Nil(t, result, "额度拒绝不能触发成功用量记录")
			require.False(t, c.Writer.Written(), "故障转移前不能提交响应")
			require.Empty(t, rec.Body.String())
			require.Equal(t, http.StatusTooManyRequests, c.GetInt(OpsUpstreamStatusCodeKey))
			require.Contains(t, c.GetString(OpsUpstreamErrorMessageKey), "4008")
		})
	}
}

func TestCCStreamQuotaErrorAfterOutputIsVisible(t *testing.T) {
	gin.SetMode(gin.TestMode)
	partial := `data: {"id":"chatcmpl_partial","model":"glm-5","choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n"
	for _, path := range []string{"/v1/responses", "/v1/messages", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			result, err, c, rec := forwardCCErrorTest(t, path, partial+traeQuotaErrorStream)
			require.Error(t, err)
			var failoverErr *UpstreamFailoverError
			require.False(t, errors.As(err, &failoverErr))
			require.Nil(t, result)
			require.True(t, IsResponseCommitted(c))
			require.Equal(t, http.StatusTooManyRequests, c.GetInt(OpsUpstreamStatusCodeKey))
			failures := GetOpsStreamErrors(c)
			require.Len(t, failures, 1)
			require.True(t, failures[0].CountTowardsSLA, "HTTP 200 内的错误仍必须计为失败")
			require.Equal(t, "4008", failures[0].Code)
			output := rec.Body.String()
			require.Contains(t, output, "partial")
			require.Equal(t, 1, strings.Count(output, "Your requests have exceeded the quota."))
			require.NotContains(t, output, "response.completed")
			require.NotContains(t, output, "message_stop")
			require.NotContains(t, output, `"finish_reason":"stop"`)
			require.NotContains(t, output, "[DONE]")
			if path == "/v1/responses" {
				require.Contains(t, output, "event: response.failed")
				require.Contains(t, output, `"code":"4008"`)
				require.Contains(t, output, `"id":"chatcmpl_partial"`)
			}
		})
	}
}

func TestCCStreamNonRetryableErrorBeforeOutputIsVisible(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stream := "event: error\n" + `data: {"type":"invalid_request_error","code":"invalid_roles","message":"invalid roles"}` + "\n\ndata: [DONE]\n\n"
	for _, path := range []string{"/v1/responses", "/v1/messages", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			result, err, c, rec := forwardCCErrorTest(t, path, stream)
			require.Error(t, err)
			require.Nil(t, result)
			require.True(t, IsResponseCommitted(c))
			require.GreaterOrEqual(t, rec.Code, 400)
			require.Equal(t, "invalid roles", gjson.Get(rec.Body.String(), "error.message").String())
		})
	}
}

func TestCCStreamFailurePreservesReportedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stream := `data: {"id":"chatcmpl_partial","model":"glm-5","choices":[{"index":0,"delta":{"content":"partial"}}],"usage":{"prompt_tokens":8,"completion_tokens":2}}` + "\n\n" + traeQuotaErrorStream
	for _, path := range []string{"/v1/responses", "/v1/messages", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			result, err, _, _ := forwardCCErrorTest(t, path, stream)
			require.ErrorContains(t, err, "upstream response failed:")
			require.NotNil(t, result)
			require.Equal(t, 8, result.Usage.InputTokens)
			require.Equal(t, 2, result.Usage.OutputTokens)
		})
	}
}
