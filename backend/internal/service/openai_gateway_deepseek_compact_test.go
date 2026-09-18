package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildDeepSeekCompactRequestBody(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-flash","input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}],"tools":[{"type":"function","name":"shell"}],"tool_choice":"auto","stream":true}`)

	patched, err := buildDeepSeekCompactRequestBody(body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "stream").Bool())
	require.False(t, gjson.GetBytes(patched, "store").Bool())
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())
	require.Equal(t, 2, len(gjson.GetBytes(patched, "input").Array()))
	require.Equal(t, "message", gjson.GetBytes(patched, "input.1.type").String())
	require.Contains(t, gjson.GetBytes(patched, "input.1.content.0.text").String(), "Summarize the conversation")
}

func TestConvertDeepSeekResponseToOpenAICompact(t *testing.T) {
	body := []byte(`{
		"id":"resp_deepseek_1",
		"object":"response",
		"status":"completed",
		"model":"deepseek-v4-flash",
		"output":[
			{"id":"reasoning_1","type":"reasoning","encrypted_content":"deepseek-state"},
			{"id":"message_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"summary text"}]}
		],
		"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}
	}`)

	service := newDeepSeekCompactTestService()
	converted, err := service.convertDeepSeekResponseToOpenAICompact(body)
	require.NoError(t, err)
	require.Equal(t, "resp_deepseek_1", gjson.GetBytes(converted, "id").String())
	require.Len(t, gjson.GetBytes(converted, "output").Array(), 1)
	require.Equal(t, "compaction", gjson.GetBytes(converted, "output.0.type").String())
	token := gjson.GetBytes(converted, "output.0.encrypted_content").String()
	require.True(t, strings.HasPrefix(token, deepSeekCompactTokenPrefix))
	replayed, err := service.restoreDeepSeekCompaction([]byte(fmt.Sprintf(`{"input":[{"type":"compaction","encrypted_content":%q}]}`, token)))
	require.NoError(t, err)
	require.Contains(t, gjson.GetBytes(replayed, "input.0.content.0.text").String(), "summary text")
	require.Equal(t, int64(14), gjson.GetBytes(converted, "usage.total_tokens").Int())
}

func TestNormalizeDeepSeekResponsesRequestBodyRestoresCompactSummary(t *testing.T) {
	account := &Account{
		Platform: PlatformDeepseek,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_protocol": APIProtocolResponses,
		},
	}
	body := []byte(`{"model":"deepseek-v4-flash","input":[{"type":"compaction","encrypted_content":"deepseek-state","summary":[{"type":"summary_text","text":"summary text"}]},{"type":"message","role":"user","content":"continue"}]}`)

	normalized := normalizeDeepSeekResponsesRequestBody(account, body)
	require.Equal(t, "message", gjson.GetBytes(normalized, "input.0.type").String())
	require.Contains(t, gjson.GetBytes(normalized, "input.0.content.0.text").String(), "summary text")
	require.Equal(t, "continue", gjson.GetBytes(normalized, "input.1.content").String())
	require.False(t, gjson.GetBytes(normalized, "store").Bool())
}

func TestHandleNonStreamingResponseDeepSeekNativeCompactionBridgesToSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	MarkOpenAINativeCompactionV2(c)
	c.Set(deepSeekCompactContextKey, true)
	MarkOpenAICompactClientStream(c)

	service := newDeepSeekCompactTestService()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_deepseek_2",
			"object":"response",
			"status":"completed",
			"output":[
				{"type":"reasoning","encrypted_content":"deepseek-state"},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary text"}]}
			],
			"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}
		}`)),
	}

	result, err := service.handleNonStreamingResponse(context.Background(), resp, c, &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey}, "deepseek-v4-flash", "deepseek-v4-flash")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	events := parseCompactBridgeSSE(t, recorder.Body.String())
	require.Len(t, events, 2)
	require.Equal(t, "response.output_item.done", events[0][0])
	require.Equal(t, "compaction", gjson.Get(events[0][1], "item.type").String())
	require.True(t, strings.HasPrefix(gjson.Get(events[0][1], "item.encrypted_content").String(), deepSeekCompactTokenPrefix))
	require.Equal(t, "response.completed", events[1][0])
}

func newDeepSeekCompactTestService() *OpenAIGatewayService {
	s := newCompactBridgeTestService()
	s.cfg.JWT.Secret = "compact-regression-test-secret"
	return s
}

func TestDeepSeekCompactionRejectsMissingOrIncompleteSummary(t *testing.T) {
	service := newDeepSeekCompactTestService()
	for _, body := range []string{
		`{"status":"completed","output":[{"type":"reasoning","encrypted_content":"opaque"}]}`,
		`{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"partial summary"}]}]}`,
	} {
		_, err := service.convertDeepSeekResponseToOpenAICompact([]byte(body))
		require.Error(t, err)
	}
}

func TestDeepSeekCompactionRejectsTamperedToken(t *testing.T) {
	_, err := newDeepSeekCompactTestService().restoreDeepSeekCompaction([]byte(`{"input":[{"type":"compaction","encrypted_content":"sub2api_compact_v1:invalid"}]}`))
	require.ErrorContains(t, err, "decrypt DeepSeek compaction")
}

// 复现 openai 平台第三方账号的完整压缩与下一轮回放，而非只测试转换函数。
func TestForwardDeepSeekCompatibleCompactionAndReplay(t *testing.T) {
	for _, mode := range []string{"json", "sse", "sse_output_in_done"} {
		t.Run(mode, func(t *testing.T) {
			body := []byte(`{"model":"deepseek-v4-flash","stream":true,"input":[{"role":"user","content":"remember COMPACT-728"},{"type":"compaction_trigger"}],"tools":[{"type":"function","name":"shell","parameters":{"type":"object"}}],"tool_choice":"auto"}`)
			response := `{"id":"resp_summary","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The marker is COMPACT-728"}]}],"usage":{"input_tokens":10,"output_tokens":8,"total_tokens":18}}`
			resp := newOpenAIRejectedFieldTestResponse(http.StatusOK, response)
			if mode != "json" {
				resp.Header.Set("Content-Type", "text/event-stream")
				streamBody := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
				if mode == "sse_output_in_done" {
					item := gjson.Get(response, "output.0").Raw
					output := gjson.Get(response, "output").Raw
					terminal := strings.Replace(response, output, "[]", 1)
					streamBody = "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":" + item + "}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + terminal + "}\n\n"
				}
				resp.Body = io.NopCloser(strings.NewReader(streamBody))
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{resp, newOpenAIRejectedFieldTestResponse(http.StatusOK, response)}}
			service := newOpenAIRejectedFieldTestService(upstream)
			service.cfg.JWT.Secret = "compact-regression-test-secret"
			account := newOpenAIRejectedFieldTestAccount()
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			MarkOpenAINativeCompactionV2(c)
			_, err := service.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(upstream.bodies[0], "stream").Bool())
			require.False(t, gjson.GetBytes(upstream.bodies[0], "tools").Exists())
			require.NotContains(t, string(upstream.bodies[0]), "compaction_trigger")
			require.Contains(t, string(upstream.bodies[0]), deepSeekCompactSummaryPrompt)
			events := parseCompactBridgeSSE(t, recorder.Body.String())
			require.Len(t, events, 2)
			require.Equal(t, "compaction", gjson.Get(events[0][1], "item.type").String())
			token := gjson.Get(events[0][1], "item.encrypted_content").String()
			// 模拟 Codex 仅持久化标准压缩字段，摘要不得依赖自定义 summary 字段。
			replay, err := json.Marshal(map[string]any{"model": "deepseek-v4-flash", "stream": false, "input": []any{
				map[string]any{"type": "compaction", "encrypted_content": token},
				map[string]any{"role": "user", "content": "what was the marker?"},
			}})
			require.NoError(t, err)
			_, err = service.Forward(context.Background(), newOpenAIRejectedFieldTestContext(replay), account, replay)
			require.NoError(t, err)
			require.Contains(t, string(upstream.bodies[1]), "COMPACT-728")
			require.NotContains(t, string(upstream.bodies[1]), deepSeekCompactTokenPrefix)
		})
	}
}

func TestDeepSeekCompactBridgeUsesUpstreamModel(t *testing.T) {
	c := newOpenAIRejectedFieldTestContext(nil)
	MarkOpenAINativeCompactionV2(c)
	account := newOpenAIRejectedFieldTestAccount()
	account.Credentials["model_mapping"] = map[string]any{"alias": "deepseek-v4-flash", "deepseek-label": "gpt-5.5"}
	require.True(t, shouldBridgeDeepSeekCompaction(c, account, []byte(`{"model":"alias"}`)))
	require.False(t, shouldBridgeDeepSeekCompaction(c, account, []byte(`{"model":"deepseek-label"}`)))
	require.False(t, shouldBridgeDeepSeekCompaction(c, account, []byte(`{"model":"gpt-5.5"}`)))
	account.Extra["openai_passthrough"] = true
	require.False(t, shouldBridgeDeepSeekCompaction(c, account, []byte(`{"model":"alias"}`)))
}
