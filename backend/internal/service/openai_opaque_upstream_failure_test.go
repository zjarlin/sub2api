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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const opaqueUpstream400 = `{"error":{"code":"upstream_error","message":"请求未能完成，请检查请求参数、模型名称或输入内容后重试。","param":"","type":"upstream_error"}}`
const earlyUpstreamFailed = `{"response":{"error":{"code":"upstream_error","message":"Upstream request failed"},"id":"resp_failed","model":"deepseek-v4.1-flash","object":"response","output":[],"status":"failed"},"sequence_number":0,"type":"response.failed"}`

func TestOpaqueUpstreamFailurePreservesSpecificClientErrors(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"upstream_error","type":"upstream_error","param":"input","message":"Upstream request failed"}}`,
		`{"error":{"code":"invalid_request_error","type":"upstream_error","message":"Upstream request failed"}}`,
		`{"error":{"code":"upstream_error","type":"invalid_request_error","message":"Upstream request failed"}}`,
		`{"error":{"code":"upstream_error","type":"upstream_error","message":"Invalid schema for response_format"}}`,
		`{"error":{"code":"upstream_error","type":"upstream_error","message":"Your input exceeds the context window"}}`,
		`{"error":{"code":"upstream_error","type":"upstream_error","message":"blocked by content policy"}}`,
		`{"error":{"code":"invalid_request","message":"bad request"},"echo":{"code":"upstream_error","type":"upstream_error","message":"Upstream request failed"}}`,
	} {
		require.False(t, isOpenAIOpaqueUpstreamFailure(400, []byte(body)), body)
	}
	require.False(t, isOpenAIOpaqueUpstreamFailure(422, []byte(opaqueUpstream400)))
	require.True(t, isOpenAIOpaqueUpstreamFailure(400, []byte(opaqueUpstream400)))
	failover := newOpenAIUpstreamFailoverError(400, nil, []byte(opaqueUpstream400), "", true)
	require.False(t, failover.RetryableOnSameAccount)
	require.True(t, failover.ShouldRetryNextAccount())
}

func TestOpaqueUpstream400AllowsSameModelNextAccount(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "passthrough"}[passthrough], func(t *testing.T) {
			body := []byte(`{"model":"deepseek-v4.1-flash","stream":false,"input":"synthetic check"}`)
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				newOpenAIRejectedFieldTestResponse(400, opaqueUpstream400),
				newOpenAIRejectedFieldTestResponse(200, `{"id":"resp_ok","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`),
			}}
			svc := newOpenAIRejectedFieldTestService(upstream)
			account := newOpenAIRejectedFieldTestAccount()
			// 未确认 Responses 支持时直接换账号；兼容重试的链路单独覆盖。
			delete(account.Extra, openai_compat.ExtraKeyResponsesSupported)
			account.Extra["openai_passthrough"] = passthrough
			c := newOpenAIRejectedFieldTestContext(body)
			_, err := svc.Forward(context.Background(), c, account, body)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.False(t, failover.RetryableOnSameAccount)
			require.False(t, c.Writer.Written())
			account.ID++
			_, err = svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.Len(t, upstream.bodies, 2)
			for _, sent := range upstream.bodies {
				require.Equal(t, "deepseek-v4.1-flash", gjson.GetBytes(sent, "model").String())
				require.Equal(t, "synthetic check", gjson.GetBytes(sent, "input").String())
			}
		})
	}
}

func TestOpaqueUpstream400AfterProtocolRetryAllowsNextAccount(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4.1-flash","stream":false,"input":"synthetic check"}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(400, opaqueUpstream400),
		newOpenAIRejectedFieldTestResponse(400, opaqueUpstream400),
		newOpenAIRejectedFieldTestResponse(200, `{"id":"resp_ok","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"recovered"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`),
	}}
	svc := newOpenAIRejectedFieldTestService(upstream)
	account := newOpenAIRejectedFieldTestAccount()
	c := newOpenAIRejectedFieldTestContext(body)
	_, err := svc.Forward(context.Background(), c, account, body)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.True(t, failover.ShouldRetryNextAccount())
	require.False(t, failover.RetryableOnSameAccount)
	require.False(t, c.Writer.Written())
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
	require.Equal(t, "/v1/chat/completions", upstream.requests[1].URL.Path)
	require.Equal(t, "synthetic check", gjson.GetBytes(upstream.bodies[1], "messages.0.content").String())
	account.ID++
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.Equal(t, "deepseek-v4.1-flash", result.Model)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "/v1/responses", upstream.requests[2].URL.Path)
	require.Equal(t, "synthetic check", gjson.GetBytes(upstream.bodies[2], "input").String())
	for _, sent := range upstream.bodies {
		require.Equal(t, "deepseek-v4.1-flash", gjson.GetBytes(sent, "model").String())
	}
}

func TestEarlyUpstreamFailureRecoversWithoutLeakingFailedEvent(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "passthrough"}[passthrough], func(t *testing.T) {
			body := []byte(`{"model":"deepseek-v4.1-flash","stream":true,"input":"synthetic check"}`)
			failure := "event: response.failed\ndata: " + earlyUpstreamFailed + "\n\n"
			success := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"model\":\"deepseek-v4.1-flash\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"recovered\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
			upstream := &httpUpstreamRecorder{}
			for _, stream := range []string{failure, success} {
				upstream.responses = append(upstream.responses, &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(stream)),
				})
			}
			svc := newOpenAIRejectedFieldTestService(upstream)
			account := newOpenAIRejectedFieldTestAccount()
			account.Extra["openai_passthrough"] = passthrough
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
			_, err := svc.Forward(context.Background(), c, account, body)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.False(t, c.Writer.Written())
			account.ID++
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.Equal(t, "deepseek-v4.1-flash", result.Model)
			require.Len(t, upstream.requests, 2)
			require.Contains(t, recorder.Body.String(), "response.completed")
			require.Contains(t, recorder.Body.String(), "recovered")
			require.NotContains(t, recorder.Body.String(), "response.failed")
			require.NotContains(t, recorder.Body.String(), "resp_failed")
		})
	}
}

func TestEarlyUpstreamFailureRemainsUncommitted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, preamble := range []string{"", "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_failed\"}}\n\n"} {
		for _, transportError := range []bool{false, true} {
			for _, passthrough := range []bool{false, true} {
				body := preamble + "data: " + earlyUpstreamFailed + "\n\n"
				var reader io.ReadCloser = io.NopCloser(strings.NewReader(body))
				if transportError {
					reader = &openAIResponseFlushReadError{payload: []byte(body)}
				}
				svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}
				account := &Account{ID: 820, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
				var err error
				if passthrough {
					_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				} else {
					_, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				}
				var failover *UpstreamFailoverError
				require.True(t, errors.As(err, &failover), "preamble=%t transport=%t passthrough=%t error=%v", preamble != "", transportError, passthrough, err)
				require.False(t, c.Writer.Written())
			}
		}
	}
}

func TestEarlyUpstreamFailureAfterQueueHeartbeat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		heartbeat := ": ping\n\n"
		n, err := c.Writer.WriteString(heartbeat)
		require.NoError(t, err)
		c.Set(ctxkey.GatewayStreamHeartbeatBytes, n)
		c.Writer.Flush()
		before := OpenAICompactKeepaliveAdjustedWrittenSize(c)
		svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + earlyUpstreamFailed + "\n\n"))}
		account := &Account{ID: 820, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
		if passthrough {
			_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
		} else {
			_, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
		}
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover)
		require.Equal(t, before, OpenAICompactKeepaliveAdjustedWrittenSize(c))
		require.Equal(t, heartbeat, recorder.Body.String())
	}
}
