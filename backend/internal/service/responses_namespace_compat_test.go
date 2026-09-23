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

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAgnesNamespaceOnlyToolsRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%t/stream=%t", passthrough, stream), func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":"agnes-2.0-flash","stream":%t,
					"tools":[
						{"type":"namespace","name":"alpha","tools":[{"type":"function","name":"run","parameters":{"type":"object","properties":{}}}]},
						{"type":"namespace","name":"beta","tools":[{"type":"function","name":"run","parameters":{"type":"object","properties":{}}}]}
					],
					"tool_choice":{"type":"function","namespace":"beta","name":"run"},
					"input":[{"type":"function_call","call_id":"old","namespace":"alpha","name":"run","arguments":"{}"},{"type":"function_call_output","call_id":"old","output":"done"},{"type":"message","id":"msg_old","status":"completed","role":"assistant","content":[{"type":"output_text","text":"done"}]},{"role":"user","content":"run beta"}]}`, stream))
				reply := `{"id":"resp_ns","status":"completed","output":[{"type":"function_call","id":"fc_new","call_id":"new","name":"beta__run","arguments":"{}"}],"usage":{"input_tokens":1,"output_tokens":1}}`
				contentType := "application/json"
				if stream {
					reply = "data: " + `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_new","call_id":"new","name":"beta__run","arguments":""}}` + "\n\n" +
						"data: " + `{"type":"response.function_call_arguments.done","item_id":"fc_new","name":"beta__run","arguments":"{}"}` + "\n\n" +
						"data: " + `{"type":"response.completed","response":` + reply + "}\n\n"
					contentType = "text/event-stream"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(reply))}}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
				account := &Account{ID: 287, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Credentials: map[string]any{"api_key": "test-key", "base_url": "https://relay.example"},
					Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true, "openai_passthrough": passthrough}}
				_, err := openAIClientToolsTestService(upstream).Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				require.Equal(t, "function", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
				require.Equal(t, "alpha__run", gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
				require.Equal(t, "beta__run", gjson.GetBytes(upstream.lastBody, "tools.1.name").String())
				require.Equal(t, "beta__run", gjson.GetBytes(upstream.lastBody, "tool_choice.name").String())
				require.Equal(t, "alpha__run", gjson.GetBytes(upstream.lastBody, "input.0.name").String())
				require.False(t, gjson.GetBytes(upstream.lastBody, "input.0.namespace").Exists())
				require.Equal(t, "done", gjson.GetBytes(upstream.lastBody, "input.2.content").String())
				require.False(t, gjson.GetBytes(upstream.lastBody, "input.2.status").Exists())
				if stream {
					require.Contains(t, recorder.Body.String(), `"namespace":"beta"`)
					require.Contains(t, recorder.Body.String(), `"name":"run"`)
					require.NotContains(t, recorder.Body.String(), "beta__run")
				} else {
					require.Equal(t, "beta", gjson.Get(recorder.Body.String(), "output.0.namespace").String())
					require.Equal(t, "run", gjson.Get(recorder.Body.String(), "output.0.name").String())
					require.Equal(t, "new", gjson.Get(recorder.Body.String(), "output.0.call_id").String())
				}
			})
		}
	}
}
