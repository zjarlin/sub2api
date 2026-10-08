package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 加密推理历史允许换模型；若所有候选仍耗尽，容量提示不能退化为不明确的通用错误。
func TestGatewayCapacityHintWithEncryptedReasoningHistory(t *testing.T) {
	for _, streamStarted := range []bool{false, true} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"gpt-6.1-sol","input":[{"type":"reasoning","encrypted_content":"opaque"}]}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
		key := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}
		require.Empty(t, modelFallbackReplayBlockReason(c, key, "gpt-6.1-sol", body))
		message := "Concurrency limit exceeded for user, please retry later"
		failure := &service.UpstreamFailoverError{
			StatusCode:             http.StatusServiceUnavailable,
			ResponseBody:           []byte(`{"response":{"error":{"code":"gateway_concurrency_limit","message":"Concurrency limit exceeded for user, please retry later"}}}`),
			RequestScopedTransient: true, RetryableOnSameAccount: true,
			ClientStatusCode: http.StatusServiceUnavailable, ClientMessage: message,
		}
		(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, failure, streamStarted)
		require.Contains(t, recorder.Body.String(), message)
		require.Contains(t, recorder.Body.String(), "server_error")
		require.NotContains(t, recorder.Body.String(), "Upstream service temporarily unavailable")
		if streamStarted {
			require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed"))
		} else {
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		}
	}
}
