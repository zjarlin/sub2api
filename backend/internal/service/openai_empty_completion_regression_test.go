//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNonStreamingEmptyPlaceholderStreamingTerminal(t *testing.T) {
	acc := apicompat.NewBufferedResponseAccumulator()
	acc.ProcessEvent(&apicompat.ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "OK"})
	done := newResponsesStreamOutputItems()
	done.Observe([]byte(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","content":[{"type":"output_text"}]}}`))
	raw := []byte(`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","id":"msg_actual","status":"completed","content":[{"type":"output_text"}]}],"usage":{"input_tokens":34,"output_tokens":14}}}`)
	normalized, changed := normalizeResponsesStreamingTerminalOutput(raw, acc, done, nil)
	require.True(t, changed)
	require.Equal(t, "OK", gjson.GetBytes(normalized, "response.output.0.content.0.text").String())
	require.Equal(t, "msg_actual", gjson.GetBytes(normalized, "response.output.0.id").String())
	require.Equal(t, int64(14), gjson.GetBytes(normalized, "response.usage.output_tokens").Int())
}

func TestNonStreamingEmptyPlaceholderPreservesTextDelta(t *testing.T) {
	for _, usage := range []string{`{"input_tokens":0,"output_tokens":0}`, `{"input_tokens":34,"output_tokens":14}`} {
		for _, passthrough := range []bool{false, true} {
			c, rec := newNonStreamingFailoverContext(t)
			svc := newNonStreamingFailoverService()
			body := []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n" +
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\"}]}}\n\n" +
				string(sseTerminalBody("response.completed", `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text"}]}],"usage":`+usage+`}}`)))
			var err error
			if passthrough {
				_, err = svc.handlePassthroughSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(), body, "model", "model")
			} else {
				_, err = svc.handleSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(), body, "model", "model")
			}
			require.NoError(t, err)
			require.Equal(t, "OK", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
		}
	}
}

func TestNonStreamingJSONEmptyCompletedFailsOver(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		c, rec := newNonStreamingFailoverContext(t)
		svc := newNonStreamingFailoverService()
		resp := newNonStreamingSSEResponse()
		resp.Header.Set("Content-Type", "application/json")
		resp.Body = io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":""}]}],"usage":{"input_tokens":0,"output_tokens":0}}`))
		var err error
		if passthrough {
			_, err = svc.handleNonStreamingResponsePassthrough(context.Background(), resp, c, newNonStreamingFailoverAccount(), "glm-5.3", "glm-5.3")
		} else {
			_, err = svc.handleNonStreamingResponse(context.Background(), resp, c, newNonStreamingFailoverAccount(), "glm-5.3", "glm-5.3")
		}
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover)
		require.False(t, c.Writer.Written())
		require.Empty(t, rec.Body.String())
	}
}

func TestNonStreamingClineEnvelopePreservesTextAndUsage(t *testing.T) {
	for _, path := range ccPreambleTestPaths {
		t.Run(path, func(t *testing.T) {
			c, rec := newNonStreamingFailoverContext(t)
			svc := newNonStreamingFailoverService()
			resp := newNonStreamingSSEResponse()
			resp.Body = io.NopCloser(strings.NewReader(`{"success":true,"data":{"id":"cc_ok","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":34,"completion_tokens":14}}}`))
			account := newNonStreamingFailoverAccount()
			var result *OpenAIForwardResult
			var err error
			switch path {
			case "/v1/responses":
				result, err = svc.bufferChatCompletionsAsResponses(c, resp, account, "deepseek-v4.1-flash", nil, nil, false, nil, "deepseek-v4.1-flash", "cline-pass/deepseek-v4.1-flash", nil, nil, time.Now())
				require.Equal(t, "OK", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
				require.Equal(t, int64(14), gjson.Get(rec.Body.String(), "usage.output_tokens").Int())
			case "/v1/messages":
				result, err = svc.bufferChatCompletionsAsAnthropic(c, resp, account, "deepseek-v4.1-flash", "deepseek-v4.1-flash", "cline-pass/deepseek-v4.1-flash", nil, nil, time.Now())
				require.Equal(t, "OK", gjson.Get(rec.Body.String(), "content.0.text").String())
				require.Equal(t, int64(14), gjson.Get(rec.Body.String(), "usage.output_tokens").Int())
			default:
				result, err = svc.bufferRawChatCompletions(c, resp, account, "deepseek-v4.1-flash", "deepseek-v4.1-flash", "cline-pass/deepseek-v4.1-flash", nil, nil, time.Now())
				require.Equal(t, "OK", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
				require.Equal(t, int64(14), gjson.Get(rec.Body.String(), "usage.completion_tokens").Int())
			}
			require.NoError(t, err)
			require.Equal(t, 34, result.Usage.InputTokens)
			require.Equal(t, 14, result.Usage.OutputTokens)
		})
	}
}

func TestNonStreamingClineEnvelopeKeepsErrorsAndRootChoices(t *testing.T) {
	for _, body := range []string{
		`{"success":false,"data":{"choices":[]}}`,
		`{"error":{"message":"failure"},"data":{"choices":[]}}`,
		`{"data":{"error":{"message":"failure"},"choices":[]}}`,
		`{"choices":[],"data":{"choices":[{"message":{"content":"echoed request"}}]}}`,
	} {
		require.Equal(t, body, string(unwrapOpenAIChatCompletionEnvelope([]byte(body))))
	}
}

func TestNonStreamingClineErrorEnvelopeFailsOver(t *testing.T) {
	for _, raw := range []bool{false, true} {
		c, rec := newNonStreamingFailoverContext(t)
		svc := newNonStreamingFailoverService()
		resp := newNonStreamingSSEResponse()
		resp.Body = io.NopCloser(strings.NewReader(`{"success":false,"data":{"error":{"message":"rate limited"}}}`))
		var err error
		if raw {
			_, err = svc.bufferRawChatCompletions(c, resp, newNonStreamingFailoverAccount(), "model", "model", "model", nil, nil, time.Now())
		} else {
			_, _, err = svc.readCCUpstreamJSONResponse(c, resp, newNonStreamingFailoverAccount(), writeOpenAIResponsesFallbackError)
		}
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover)
		require.Equal(t, GatewayFailureReason("invalid_chat_completion_response"), failover.Reason)
		require.Empty(t, rec.Body.String())
		require.False(t, c.Writer.Written())
	}
}

func TestNonStreamingEmptyCompleted(t *testing.T) {
	cases := []struct {
		name     string
		response string
		failover bool
	}{
		{"empty text zero usage", `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":""}]}],"usage":{"input_tokens":0,"output_tokens":0}}`, true},
		{"text", `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`, false},
		{"function call", `{"status":"completed","output":[{"type":"function_call","name":"echo","arguments":"{}","call_id":"call_1"}]}`, false},
		{"reasoning", `{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}]}]}`, false},
		{"image", `{"status":"completed","output":[{"type":"image_generation_call","result":"image"}]}`, false},
		{"nonzero usage", `{"status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":0}}`, false},
		{"reasoning usage", `{"status":"completed","output":[],"usage":{"output_tokens_details":{"reasoning_tokens":1}}}`, false},
		{"refusal", `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"Denied"}]}]}`, false},
	}
	for _, tc := range cases {
		for _, passthrough := range []bool{false, true} {
			t.Run(tc.name+"/"+map[bool]string{false: "converted", true: "passthrough"}[passthrough], func(t *testing.T) {
				c, rec := newNonStreamingFailoverContext(t)
				svc := newNonStreamingFailoverService()
				body := sseTerminalBody("response.completed", `{"type":"response.completed","response":`+tc.response+`}`)
				var err error
				if passthrough {
					_, err = svc.handlePassthroughSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(), body, "model", "model")
				} else {
					_, err = svc.handleSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(), body, "model", "model")
				}
				if tc.failover {
					var failover *UpstreamFailoverError
					require.ErrorAs(t, err, &failover)
					require.False(t, c.Writer.Written())
					require.Empty(t, rec.Body.String())
					return
				}
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, rec.Code)
			})
		}
	}
}

func TestNonStreamingEmptyChatCompletion(t *testing.T) {
	for _, path := range ccPreambleTestPaths {
		t.Run(path, func(t *testing.T) {
			c, rec := newNonStreamingFailoverContext(t)
			svc := newNonStreamingFailoverService()
			resp := newNonStreamingSSEResponse()
			resp.Body = io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0}}`))
			account := newNonStreamingFailoverAccount()
			var err error
			switch path {
			case "/v1/responses":
				_, err = svc.bufferChatCompletionsAsResponses(c, resp, account, "glm-5.3", nil, nil, false, nil, "glm-5.3", "glm-5.3", nil, nil, time.Now())
			case "/v1/messages":
				_, err = svc.bufferChatCompletionsAsAnthropic(c, resp, account, "glm-5.3", "glm-5.3", "glm-5.3", nil, nil, time.Now())
			default:
				_, err = svc.bufferRawChatCompletions(c, resp, account, "glm-5.3", "glm-5.3", "glm-5.3", nil, nil, time.Now())
			}
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.False(t, c.Writer.Written())
			require.Empty(t, rec.Body.String())
		})
	}
}

func TestCCStreamEmptyCompletedFailsOver(t *testing.T) {
	stream := ccPreambleTestChunk(`{"role":"assistant","content":""}`) +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0}}\n\ndata: [DONE]\n\n"
	for _, path := range ccPreambleTestPaths {
		t.Run(path, func(t *testing.T) {
			result, err, c, rec := forwardCCErrorTest(t, path, stream)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Nil(t, result)
			require.False(t, c.Writer.Written())
			require.Empty(t, rec.Body.String())
		})
	}
}

func TestEmptyChatCompletionPreservesOutput(t *testing.T) {
	for _, message := range []string{`{"content":"OK"}`, `{"tool_calls":[{"id":"c","function":{"name":"echo","arguments":"{}"}}]}`, `{"reasoning_content":"thinking"}`, `{"refusal":"Denied"}`, `{"audio":{"data":"audio"}}`} {
		require.False(t, openAIChatResponseIsEmpty([]byte(`{"choices":[{"message":`+message+`,"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0}}`), &OpenAIUsage{}))
	}
}
