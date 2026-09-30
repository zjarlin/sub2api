//go:build unit

package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var ccPreambleTestPaths = []string{"/v1/responses", "/v1/messages", "/v1/chat/completions"}

func ccPreambleTestChunk(delta string) string {
	return `data: {"id":"chatcmpl_prelude","model":"glm-5","choices":[{"index":0,"delta":` + delta + `,"finish_reason":null}]}` + "\n\n"
}

func TestCCStreamPreambleErrorFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	preambles := map[string]string{
		"role":            ccPreambleTestChunk(`{"role":"assistant"}`),
		"empty content":   ccPreambleTestChunk(`{"role":"assistant","content":""}`),
		"empty reasoning": ccPreambleTestChunk(`{"reasoning_content":"","reasoning":""}`),
		"empty delta":     ccPreambleTestChunk(`{}`),
		"usage":           "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":0}}\n\n",
		"SSE metadata":    ": keepalive\nid: prelude\nretry: 1000\nevent: message\ndata:\n\n",
		"tool index":      ccPreambleTestChunk(`{"tool_calls":[{"index":0}]}`),
		"tool empty args": ccPreambleTestChunk(`{"tool_calls":[{"index":0,"id":"","type":"function","function":{"name":"","arguments":""}}]}`),
	}
	for _, path := range ccPreambleTestPaths {
		for name, preamble := range preambles {
			for _, streamError := range []string{traeQuotaErrorStream, "event: error\ndata: {\"error\":{\"code\":\"rate_limit_exceeded\",\"type\":\"rate_limit_error\",\"message\":\"Too many requests\"}}\n\n"} {
				t.Run(path+"/"+name+"/"+streamError[:12], func(t *testing.T) {
					result, err, c, rec := forwardCCErrorTest(t, path, preamble+streamError)
					var failover *UpstreamFailoverError
					require.ErrorAs(t, err, &failover)
					require.Equal(t, http.StatusTooManyRequests, failover.StatusCode)
					require.Nil(t, result)
					require.False(t, c.Writer.Written())
					require.Empty(t, rec.Body.String())
					require.Equal(t, http.StatusTooManyRequests, c.GetInt(OpsUpstreamStatusCodeKey))
				})
			}
		}
	}
}

func TestCCStreamPreamblePreservesNonRetryableError(t *testing.T) {
	for _, path := range ccPreambleTestPaths {
		t.Run(path, func(t *testing.T) {
			preamble := ccPreambleTestChunk(`{"role":"assistant","content":""}`)
			stream := preamble + "event: error\ndata: {\"error\":{\"code\":\"invalid_roles\",\"type\":\"invalid_request_error\",\"message\":\"invalid roles\"}}\n\n"
			result, err, c, rec := forwardCCErrorTest(t, path, stream)
			var failover *UpstreamFailoverError
			require.Error(t, err)
			require.NotErrorAs(t, err, &failover)
			require.Nil(t, result)
			require.True(t, IsResponseCommitted(c))
			require.GreaterOrEqual(t, rec.Code, 400)
			require.Contains(t, rec.Body.String(), "invalid roles")
			require.NotContains(t, rec.Body.String(), "chatcmpl_prelude")
		})
	}
}

func TestCCRawStreamExtensionOutputPreventsReplay(t *testing.T) {
	for name, delta := range map[string]string{
		"legacy function": `{"function_call":{"name":"lookup","arguments":"{}"}}`,
		"audio":           `{"audio":{"id":"audio_1","data":"audio data"}}`,
		"refusal":         `{"refusal":"declined"}`,
		"custom tool":     `{"tool_calls":[{"index":0,"custom":{"name":"exec","input":"x"}}]}`,
		"function extra":  `{"tool_calls":[{"index":0,"type":"function","function":{"arguments":"","extra":"run"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			preamble := ccPreambleTestChunk(`{"role":"assistant","content":""}`)
			_, err, c, rec := forwardCCErrorTest(t, "/v1/chat/completions", preamble+ccPreambleTestChunk(delta)+traeQuotaErrorStream)
			var failover *UpstreamFailoverError
			require.Error(t, err)
			require.NotErrorAs(t, err, &failover)
			require.True(t, IsResponseCommitted(c))
			require.Contains(t, rec.Body.String(), delta)
			require.Contains(t, rec.Body.String(), "Your requests have exceeded the quota.")
		})
	}
}

func TestCCStreamEmptyPreambleEOFDoesNotBecomeSuccess(t *testing.T) {
	for _, path := range ccPreambleTestPaths {
		for _, preamble := range []string{"", ccPreambleTestChunk(`{"role":"assistant"}`), ccPreambleTestChunk(`{"content":"","reasoning_content":""}`)} {
			t.Run(path+"/"+preamble, func(t *testing.T) {
				result, err, c, rec := forwardCCErrorTest(t, path, preamble)
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.Nil(t, result)
				require.False(t, c.Writer.Written())
				require.Empty(t, rec.Body.String())
			})
		}
	}
}

func TestCCStreamPreambleRealOutputBlocksReplay(t *testing.T) {
	for _, path := range ccPreambleTestPaths {
		for name, delta := range map[string]string{
			"text":            `{"content":"partial"}`,
			"whitespace":      `{"content":" "}`,
			"reasoning":       `{"reasoning_content":"thinking"}`,
			"reasoning alias": `{"reasoning":"thinking"}`,
			"tool identity":   `{"tool_calls":[{"index":0,"id":"call_once","type":"function","function":{"name":"lookup","arguments":""}}]}`,
			"tool id":         `{"tool_calls":[{"index":0,"id":"call_once","type":"function","function":{"arguments":""}}]}`,
			"tool name":       `{"tool_calls":[{"index":0,"function":{"name":"lookup","arguments":""}}]}`,
			"tool arguments":  `{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}`,
		} {
			t.Run(path+"/"+name, func(t *testing.T) {
				preamble := ccPreambleTestChunk(`{"role":"assistant","content":""}`)
				_, err, c, rec := forwardCCErrorTest(t, path, preamble+ccPreambleTestChunk(delta)+traeQuotaErrorStream)
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover))
				require.True(t, c.Writer.Written())
				require.True(t, IsResponseCommitted(c))
				require.Contains(t, rec.Body.String(), "Your requests have exceeded the quota.")
				require.NotContains(t, rec.Body.String(), "response.completed")
				require.NotContains(t, rec.Body.String(), "message_stop")
				require.NotContains(t, rec.Body.String(), "[DONE]")
			})
		}
	}
}

func TestCCStreamPreambleSuccessPreservesOrderAndUsage(t *testing.T) {
	for _, path := range ccPreambleTestPaths {
		for _, delta := range []string{`{"content":"answer"}`, `{}`} {
			t.Run(path+"/"+delta, func(t *testing.T) {
				preamble := ccPreambleTestChunk(`{"role":"assistant","content":"","reasoning_content":""}`)
				stream := preamble + ccPreambleTestChunk(delta) +
					"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
					"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":6,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n"
				result, err, _, rec := forwardCCErrorTest(t, path, stream)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 6, result.Usage.InputTokens)
				require.Equal(t, 2, result.Usage.OutputTokens)
				body := rec.Body.String()
				start, finish := `"role":"assistant"`, "[DONE]"
				switch path {
				case "/v1/responses":
					start, finish = "event: response.created", "event: response.completed"
				case "/v1/messages":
					start, finish = "event: message_start", "event: message_stop"
				}
				require.Equal(t, 1, strings.Count(body, start))
				require.Contains(t, body, finish)
				require.Less(t, strings.Index(body, start), strings.Index(body, finish))
				if delta == `{}` {
					require.Nil(t, result.FirstTokenMs)
					return
				}
				require.NotNil(t, result.FirstTokenMs)
				require.Less(t, strings.Index(body, start), strings.Index(body, "answer"))
				require.Less(t, strings.Index(body, "answer"), strings.Index(body, finish))
			})
		}
	}
}

func TestCCStreamPreambleLimitFailsOverWithoutCommitting(t *testing.T) {
	for _, path := range ccPreambleTestPaths {
		for name, preamble := range map[string]string{
			"bytes": `data: {"choices":[],"system_fingerprint":"` + strings.Repeat("x", ccStreamPreambleMaxBytes) + "\"}\n\n",
			"items": strings.Repeat("data: {}\n\n", ccStreamPreambleMaxItems+1),
		} {
			t.Run(path+"/"+name, func(t *testing.T) {
				result, err, c, rec := forwardCCErrorTest(t, path, preamble)
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.Contains(t, string(failover.ResponseBody), "preamble exceeded buffer limit")
				require.Nil(t, result)
				require.False(t, c.Writer.Written())
				require.Empty(t, rec.Body.String())
			})
		}
	}
}

func TestCCStreamPreambleFlushesRealOutputBeforeEOF(t *testing.T) {
	for _, path := range ccPreambleTestPaths {
		for name, delta := range map[string]string{
			"text":      `{"content":"answer"}`,
			"reasoning": `{"reasoning_content":"thinking"}`,
			"tool":      `{"tool_calls":[{"index":0,"id":"call_once","type":"function","function":{"name":"lookup","arguments":""}}]}`,
		} {
			t.Run(path+"/"+name, func(t *testing.T) {
				reader, writer := io.Pipe()
				t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
				recorder := newOpenAIResponseFlushRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, path, nil)
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
				resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: reader}
				account := forceChatResponsesFallbackAccount()
				done := make(chan error, 1)
				go func() {
					var err error
					switch path {
					case "/v1/responses":
						_, err = svc.streamChatCompletionsAsResponses(c, resp, account, "glm-5", nil, nil, false, nil, "glm-5", "glm-5", nil, nil, time.Now(), nil, "", "", nil)
					case "/v1/messages":
						_, err = svc.streamChatCompletionsAsAnthropic(c, resp, account, "glm-5", "glm-5", "glm-5", nil, nil, time.Now())
					case "/v1/chat/completions":
						_, err = svc.streamRawChatCompletions(c, resp, account, "glm-5", "glm-5", "glm-5", nil, nil, time.Now(), 20)
					}
					done <- err
				}()
				_, err := fmt.Fprint(writer, ccPreambleTestChunk(`{"role":"assistant","content":""}`))
				require.NoError(t, err)
				_, err = fmt.Fprint(writer, ccPreambleTestChunk(delta))
				require.NoError(t, err)
				select {
				case <-recorder.flushEvents:
				case <-time.After(time.Second):
					t.Fatal("真实输出必须在 EOF 前提交")
				}
				_, err = fmt.Fprint(writer, traeQuotaErrorStream)
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				select {
				case err := <-done:
					require.Error(t, err)
					var failover *UpstreamFailoverError
					require.NotErrorAs(t, err, &failover)
				case <-time.After(time.Second):
					t.Fatal("错误流未结束")
				}
			})
		}
	}
}
