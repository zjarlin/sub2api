//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestDeepSeekReasoningReplayForward(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%t/stream=%t", passthrough, streaming), func(t *testing.T) {
				response := `{"id":"resp_ds","status":"completed","output":[{"type":"reasoning","id":"rs_0","content":[{"type":"reasoning_text","text":"provider original"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`
				upstreamResponse := newOpenAIRejectedFieldTestResponse(200, response)
				if streaming {
					upstreamResponse.Header.Set("Content-Type", "text/event-stream")
					upstreamResponse.Body = io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"))
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{upstreamResponse, newOpenAIRejectedFieldTestResponse(200, response)}}
				svc := newOpenAIRejectedFieldTestService(upstream)
				cache := &reasoningRecordingCache{}
				svc.cache = cache
				account := newOpenAIRejectedFieldTestAccount()
				account.Credentials["base_url"] = "https://api.deepseek.com"
				if passthrough {
					account.Extra["openai_passthrough"] = true
				} else {
					account.Platform = PlatformDeepseek
					account.Credentials["api_protocol"] = APIProtocolResponses
				}
				body := []byte(fmt.Sprintf(`{"model":"deepseek-v4-pro","input":"hello","stream":%t}`, streaming))
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
				c.Set("api_key", &APIKey{ID: 17})
				_, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				data := rec.Body.Bytes()
				if streaming {
					data, _ = extractCodexFinalResponse(string(data))
				}
				token := gjson.GetBytes(data, "output.0.encrypted_content").String()
				require.True(t, strings.HasPrefix(token, reasoningReplayPrefix), string(data))
				cache.getResp = cache.snapshotSets()
				replay := []byte(fmt.Sprintf(`{"model":"deepseek-v4-pro","reasoning":{"effort":"high"},"input":[{"type":"reasoning","encrypted_content":%q},{"role":"assistant","content":"answer"},{"role":"user","content":"continue"}],"stream":false}`, token))
				c = newOpenAIRejectedFieldTestContext(replay)
				c.Set("api_key", &APIKey{ID: 17})
				_, err = svc.Forward(context.Background(), c, account, replay)
				require.NoError(t, err)
				require.Equal(t, "provider original", gjson.GetBytes(upstream.lastBody, "input.0.content.0.text").String())
				require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
			})
		}
	}
}

const missingReasoningError = "{\"error\":{\"code\":\"invalid_request_error\",\"message\":\"The `reasoning_text` in the thinking mode must be passed back to the API.\"}}"

func TestDeepSeekReasoningReplayRoundTrip(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		body := []byte(`{"model":"deepseek-v4-pro","input":"hello","stream":false}`)
		c := newOpenAIRejectedFieldTestContext(body)
		c.Set("api_key", &APIKey{ID: 17})
		cache := &reasoningRecordingCache{}
		svc := &OpenAIGatewayService{cache: cache}
		account := newOpenAIRejectedFieldTestAccount()
		account.Credentials["base_url"] = "https://api.deepseek.com"
		item := `{"type":"reasoning","id":"rs_0","content":[{"type":"reasoning_text","text":"exact provider text\nwith spacing "}],"summary":[]}`
		data := []byte(`{"output":[` + item + `]}`)
		path := "output.0"
		if streaming {
			data = []byte(`{"type":"response.output_item.done","item":` + item + `}`)
			path = "item"
		}
		preserved := svc.preserveDeepSeekReasoning(c, account, data)
		token := gjson.GetBytes(preserved, path+".encrypted_content").String()
		require.True(t, strings.HasPrefix(token, reasoningReplayPrefix))
		cache.getResp = cache.snapshotSets()
		replay := []byte(`{"input":[` + gjson.GetBytes(preserved, path).Raw + `],"reasoning":{"effort":"high"}}`)
		replay, _ = sjson.DeleteBytes(replay, "input.0.content")
		restored := svc.restoreDeepSeekReasoning(c, account, replay)
		require.Equal(t, "exact provider text\nwith spacing ", gjson.GetBytes(restored, "input.0.content.0.text").String())
		require.False(t, gjson.GetBytes(restored, "input.0.encrypted_content").Exists())
		require.Equal(t, "high", gjson.GetBytes(restored, "reasoning.effort").String())
		c.Set("api_key", &APIKey{ID: 18})
		other := svc.restoreDeepSeekReasoning(c, account, replay)
		require.False(t, gjson.GetBytes(other, "input.0.content").Exists())
		account.Credentials["base_url"] = "https://api.deepseek.com.attacker.example"
		require.Equal(t, data, svc.preserveDeepSeekReasoning(c, account, data))
	}
}

func TestDeepSeekMissingReasoningRetriesOnceWithoutThinking(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		body := []byte(`{"model":"deepseek-v4-pro","input":[{"role":"assistant","content":"prior answer"},{"role":"user","content":"continue"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"reasoning":{"effort":"high"},"stream":false}`)
		upstream := &httpUpstreamRecorder{responses: []*http.Response{
			newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, missingReasoningError),
			newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_ok","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`),
		}}
		account := newOpenAIRejectedFieldTestAccount()
		account.Credentials["base_url"] = "https://api.deepseek.com"
		if passthrough {
			account.Extra = map[string]any{"openai_passthrough": true}
		}
		c := newOpenAIRejectedFieldTestContext(body)
		result, err := newOpenAIRejectedFieldTestService(upstream).Forward(context.Background(), c, account, body)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, upstream.bodies, 2)
		require.Equal(t, "none", gjson.GetBytes(upstream.bodies[1], "reasoning.effort").String())
		require.Equal(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(upstream.bodies[1], "input").Raw)
		require.Equal(t, "none", c.Writer.Header().Get("X-Sub2api-Reasoning-Fallback"))
		_, retry := deepSeekReasoningReplayRetry(c, account, 400, upstream.bodies[1], []byte(missingReasoningError))
		require.False(t, retry)
		_, retry = deepSeekReasoningReplayRetry(c, account, 400, body, []byte(`{"error":{"message":"invalid tool arguments"}}`))
		require.False(t, retry)
		account.Credentials["base_url"] = "https://api.openai.com"
		_, retry = deepSeekReasoningReplayRetry(c, account, 400, body, []byte(missingReasoningError))
		require.False(t, retry)
	}
}
