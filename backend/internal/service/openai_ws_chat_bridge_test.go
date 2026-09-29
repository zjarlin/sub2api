package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func vibexBridgeTestAccount() *Account {
	return &Account{
		ID: 858, Platform: PlatformVibex, Type: AccountTypeAPIKey, Concurrency: 1, Status: StatusActive,
		Credentials: map[string]any{"base_url": "https://vibex-adapter.example", "api_key": "adapter-key", "api_protocol": APIProtocolChatCompletions, "model_mapping": map[string]any{"vibex-alias": "free-qwen-3.8-max"}},
	}
}

func vibexBridgeTestResponse(id, text string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"upstream-" + id}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"id":"` + id + `","model":"free-qwen-3.8-max","choices":[{"index":0,"delta":{"role":"assistant","content":"` + text + `"},"finish_reason":null}]}`,
			"",
			`data: {"id":"` + id + `","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}`,
			"",
			"data: [DONE]\n\n",
		}, "\n"))),
	}
}

func TestVibexResponsesWebSocketBridgeReplaysAssistantAndUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		vibexBridgeTestResponse("chatcmpl-vibex-1", "remember violet"),
		vibexBridgeTestResponse("chatcmpl-vibex-2", "violet"),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	errCh := make(chan error, 1)
	var results []*OpenAIForwardResult
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer conn.CloseNow()
		_, first, err := conn.Read(r.Context())
		if err != nil {
			errCh <- err
			return
		}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = r.Clone(r.Context())
		c.Set("api_key", &APIKey{ID: 85801})
		err = svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, vibexBridgeTestAccount(), "adapter-key", first, &OpenAIWSIngressHooks{
			AfterTurn: func(_ int, result *OpenAIForwardResult, turnErr error) {
				if turnErr == nil {
					results = append(results, result)
				}
			},
		})
		if recorder.Body.Len() != 0 {
			err = errors.New("bridge wrote HTTP bytes after websocket upgrade")
		}
		errCh <- err
	}))
	defer wsServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.CloseNow()

	responseID := ""
	for turn := 0; turn < 2; turn++ {
		payload := `{"type":"response.create","model":"vibex-alias","instructions":"Reply briefly","reasoning":{"effort":"high"},"input":"remember violet","store":false}`
		if turn == 1 {
			payload = `{"type":"response.create","previous_response_id":"` + responseID + `","input":"what color?","store":false}`
		}
		require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(payload)))
		text := ""
		for {
			_, event, err := conn.Read(ctx)
			require.NoError(t, err)
			require.True(t, gjson.GetBytes(event, "sequence_number").Exists())
			if gjson.GetBytes(event, "type").String() == "response.output_text.delta" {
				text += gjson.GetBytes(event, "delta").String()
			}
			if gjson.GetBytes(event, "type").String() == "response.completed" {
				responseID = gjson.GetBytes(event, "response.id").String()
				require.True(t, strings.HasPrefix(responseID, "resp_"))
				require.Equal(t, "vibex-alias", gjson.GetBytes(event, "response.model").String())
				require.Equal(t, int64(8), gjson.GetBytes(event, "response.usage.input_tokens").Int())
				require.Equal(t, int64(2), gjson.GetBytes(event, "response.usage.output_tokens").Int())
				break
			}
		}
		require.NotEmpty(t, text)
	}
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","previous_response_id":"resp_unknown","input":"continue"}`)))
	_, invalidPreviousEvent, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, "error", gjson.GetBytes(invalidPreviousEvent, "type").String())
	require.Equal(t, int64(400), gjson.GetBytes(invalidPreviousEvent, "status").Int())
	require.Contains(t, gjson.GetBytes(invalidPreviousEvent, "error.message").String(), "previous_response_id")
	require.Error(t, <-errCh)
	require.Len(t, results, 2)
	require.Equal(t, responseID, results[1].RequestID)
	require.True(t, results[1].OpenAIWSMode)
	require.Equal(t, 8, results[1].Usage.InputTokens)
	require.Len(t, upstream.requests, 2)
	for _, request := range upstream.requests {
		require.Equal(t, "/v1/chat/completions", request.URL.Path)
		require.Equal(t, "Bearer adapter-key", request.Header.Get("Authorization"))
	}
	for _, body := range upstream.bodies {
		require.Equal(t, "free-qwen-3.8-max", gjson.GetBytes(body, "model").String())
		require.True(t, gjson.GetBytes(body, "stream_options.include_usage").Bool())
		require.False(t, gjson.GetBytes(body, "reasoning_effort").Exists())
		require.False(t, gjson.GetBytes(body, "previous_response_id").Exists())
	}
	require.Equal(t, "Reply briefly", gjson.GetBytes(upstream.bodies[0], "messages.0.content").String())
	require.Equal(t, "remember violet", gjson.GetBytes(upstream.bodies[1], "messages.0.content").String())
	require.Equal(t, "assistant", gjson.GetBytes(upstream.bodies[1], "messages.1.role").String())
	require.Equal(t, "remember violet", gjson.GetBytes(upstream.bodies[1], "messages.1.content").String())
	require.Equal(t, "what color?", gjson.GetBytes(upstream.bodies[1], "messages.2.content").String())
}

func TestVibexResponsesWebSocketBridgeReportsUnsupportedTools(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"tools is not supported"}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	var messages [][]byte
	_, err := svc.proxyResponsesViaChatWebSocketTurn(context.Background(), c, vibexBridgeTestAccount(),
		[]byte(`{"model":"free-qwen-3.8-max","input":"hi","stream":true,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`),
		"free-qwen-3.8-max",
		func(message []byte) error { messages = append(messages, message); return nil })
	require.Error(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "error", gjson.GetBytes(messages[0], "type").String())
	require.Equal(t, int64(400), gjson.GetBytes(messages[0], "status").Int())
	require.Contains(t, gjson.GetBytes(messages[0], "error.message").String(), "tools")
	require.Equal(t, "lookup", gjson.GetBytes(upstream.lastBody, "tools.0.function.name").String())
	require.Zero(t, recorder.Body.Len())
}

func TestVibexResponsesWebSocketBridgePreservesToolsImagesAndReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(
			"data: {\"id\":\"chatcmpl-tool\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_client\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"city\\\":\\\"Tianjin\\\"}\"}}]},\"finish_reason\":null}]}\n\n" +
				"data: {\"id\":\"chatcmpl-tool\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2,\"total_tokens\":10}}\n\ndata: [DONE]\n\n"))},
		vibexBridgeTestResponse("chatcmpl-result", "verified 17"),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	first := []byte(`{"model":"free-qwen-3.8-max","input":[{"role":"user","content":[{"type":"input_text","text":"inspect and lookup"},{"type":"input_image","image_url":"data:image/png;base64,aGVsbG8="}]}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}],"tool_choice":{"type":"function","name":"lookup"},"parallel_tool_calls":false,"stream":true}`)
	var events [][]byte
	result, err := svc.proxyResponsesViaChatWebSocketTurn(context.Background(), c, vibexBridgeTestAccount(), first, "free-qwen-3.8-max", func(event []byte) error { events = append(events, event); return nil })
	require.NoError(t, err)
	require.True(t, result.wsReplayInputExists)
	require.Len(t, result.wsReplayInput, 1)
	require.Equal(t, "lookup", gjson.GetBytes(upstream.bodies[0], "tools.0.function.name").String())
	require.Equal(t, "lookup", gjson.GetBytes(upstream.bodies[0], "tool_choice.function.name").String())
	require.Equal(t, "image_url", gjson.GetBytes(upstream.bodies[0], "messages.0.content.1.type").String())
	require.Equal(t, "data:image/png;base64,aGVsbG8=", gjson.GetBytes(upstream.bodies[0], "messages.0.content.1.image_url.url").String())
	last := events[len(events)-1]
	require.Equal(t, "response.completed", gjson.GetBytes(last, "type").String())
	require.Equal(t, "function_call", gjson.GetBytes(last, "response.output.0.type").String())
	require.Equal(t, "call_client", gjson.GetBytes(last, "response.output.0.call_id").String())
	input := []json.RawMessage{json.RawMessage(`{"role":"user","content":"inspect and lookup"}`)}
	input = append(input, result.wsReplayInput...)
	input = append(input, json.RawMessage(`{"type":"function_call_output","call_id":"call_client","output":"17"}`))
	body, err := json.Marshal(map[string]any{"model": "free-qwen-3.8-max", "input": input, "stream": true})
	require.NoError(t, err)
	_, err = svc.proxyResponsesViaChatWebSocketTurn(context.Background(), c, vibexBridgeTestAccount(), body, "free-qwen-3.8-max", func([]byte) error { return nil })
	require.NoError(t, err)
	require.Equal(t, "call_client", gjson.GetBytes(upstream.bodies[1], "messages.1.tool_calls.0.id").String())
	require.Equal(t, "tool", gjson.GetBytes(upstream.bodies[1], "messages.2.role").String())
	require.Equal(t, "17", gjson.GetBytes(upstream.bodies[1], "messages.2.content").String())
	require.Len(t, gjson.GetBytes(upstream.bodies[1], "messages").Array(), 3)
}

func TestVibexResponsesWebSocketBridgeCancellationDoesNotCompletePartialOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reader, pipeWriter := io.Pipe()
	defer pipeWriter.Close()
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: reader}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = io.WriteString(pipeWriter, "data: {\"id\":\"chatcmpl-partial\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}()
	done := make(chan error, 1)
	var completed bool
	go func() {
		_, err := svc.proxyResponsesViaChatWebSocketTurn(ctx, c, vibexBridgeTestAccount(), []byte(`{"model":"free-qwen-3.8-max","input":"hi","stream":true}`), "free-qwen-3.8-max", func(message []byte) error {
			if gjson.GetBytes(message, "type").String() == "response.output_text.delta" {
				cancel()
			}
			if gjson.GetBytes(message, "type").String() == "response.completed" {
				completed = true
			}
			return nil
		})
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err)
		require.False(t, completed)
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("canceled bridge did not close upstream body")
	}
}
