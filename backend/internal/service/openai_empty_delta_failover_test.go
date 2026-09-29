//go:build unit

package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIEmptyDeltaKeepsStreamReplayable(t *testing.T) {
	eventTypes := []string{
		"response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta",
		"response.refusal.delta", "response.function_call_arguments.delta", "response.custom_tool_call_input.delta",
	}
	for _, eventType := range eventTypes {
		for _, passthrough := range []bool{false, true} {
			for _, delta := range []string{"", " ", "partial output"} {
				t.Run(fmt.Sprintf("%s/passthrough_%t/delta_%q", eventType, passthrough, delta), func(t *testing.T) {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/responses", nil)
					svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
					account := &Account{ID: 847, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
					stream := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"failed_attempt\"}}\n\n" +
						fmt.Sprintf("event: %s\ndata: {\"type\":%q,\"delta\":%q}\n\n", eventType, eventType, delta) +
						"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"upstream_error\",\"message\":\"Upstream service temporarily unavailable\"}}}\n\n"
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}
					var err error
					if passthrough {
						_, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.6-terra", "gpt-5.6-terra")
					} else {
						_, err = svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.6-terra", "gpt-5.6-terra")
					}
					require.Error(t, err)
					var failover *UpstreamFailoverError
					if delta == "" {
						require.ErrorAs(t, err, &failover)
						require.Equal(t, http.StatusBadGateway, failover.StatusCode)
						require.False(t, c.Writer.Written())
						return
					}
					require.NotErrorAs(t, err, &failover)
					require.True(t, c.Writer.Written())
				})
			}
		}
	}
}

func TestOpenAIMalformedDeltaRemainsNonReplayable(t *testing.T) {
	for _, payload := range []string{`{`, `{}`, `{"delta":null}`, `{"delta":{}}`, `{"delta":42}`} {
		require.True(t, openAIStreamDataStartsClientOutput(payload, "response.output_text.delta"), payload)
	}
}
