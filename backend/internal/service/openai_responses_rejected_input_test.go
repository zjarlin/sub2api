package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const rejectedResponsesInputError = `{"error":{"message":"Invalid input","param":"input","type":"invalid_request_error"}}`

func TestResponsesRejectedInputCompatibility(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "agent task preserves envelope and payload",
			body: `{"input":[{"type":"agent_message","id":"amsg_1","author":"/root","recipient":"/root/task","content":[{"type":"input_text","text":"Task:\n"},{"type":"encrypted_content","encrypted_content":"Reply OK."}]}]}`,
			want: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Task:\nReply OK."}]}]}`,
		},
		{
			name: "additional tools retain schemas and order",
			body: `{"tools":[{"type":"function","name":"first"}],"input":[{"type":"additional_tools","tools":[{"type":"function","name":"second","parameters":{"type":"object","properties":{"n":{"const":9007199254740993}}}}]},{"role":"user","content":"go"}]}`,
			want: `{"tools":[{"type":"function","name":"first"},{"type":"function","name":"second","parameters":{"type":"object","properties":{"n":{"const":9007199254740993}}}}],"input":[{"role":"user","content":"go"}]}`,
		},
		{
			name: "refusal history remains visible",
			body: `{"input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Before"},{"type":"refusal","refusal":"Unable to proceed."},{"type":"output_text","text":"After"}]}]}`,
			want: `{"input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Before"},{"type":"output_text","text":"Unable to proceed."},{"type":"output_text","text":"After"}]}]}`,
		},
		{
			name: "null content becomes empty text",
			body: `{"input":[{"type":"message","role":"assistant","content":null},{"role":"user","content":"continue"}]}`,
			want: `{"input":[{"type":"message","role":"assistant","content":""},{"role":"user","content":"continue"}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			retry, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, []byte(tc.body), []byte(rejectedResponsesInputError))
			require.NoError(t, err)
			require.True(t, changed)
			require.JSONEq(t, tc.want, string(retry))
			_, _, changed, err = normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, retry, []byte(rejectedResponsesInputError))
			require.NoError(t, err)
			require.False(t, changed, "同一输入最多转换一次")
		})
	}
}

func TestResponsesRejectedInputDoesNotGuessUnsupportedContent(t *testing.T) {
	for _, body := range []string{
		`{"input":"hello"}`,
		`{"input":42}`,
		`{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`,
		`{"input":[{"type":"compaction","encrypted_content":"opaque"}]}`,
		`{"input":[{"type":"item_reference","id":"msg_previous"}]}`,
		`{"input":[{"type":"agent_message","content":[{"type":"input_text","text":"keep"},{"type":"input_image","image_url":"https://example.com/image.png"}]}]}`,
		`{"input":[{"type":"agent_message","content":[{"type":"encrypted_content","encrypted_content":{"unknown":true}}]}]}`,
		`{"input":[{"type":"agent_message","content":[]}]}`,
		`{"tools":{"invalid":true},"input":[{"type":"additional_tools","tools":[]}]}`,
		`{"input":[{"type":"additional_tools","tools":"invalid"}]}`,
		`{"input":[{"type":"function_call_output","call_id":"call_1","content":null,"output":"keep"}]}`,
		`{"input":[{"type":"unknown","role":"assistant","content":null}]}`,
	} {
		retry, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, []byte(body), []byte(rejectedResponsesInputError))
		require.NoError(t, err, body)
		require.False(t, changed, body)
		require.Nil(t, retry, body)
	}
	for _, response := range []string{
		`{"error":{"message":"Invalid input","param":"tools","type":"invalid_request_error"}}`,
		`{"error":{"message":"Invalid input","param":"input[0].content","type":"invalid_request_error"}}`,
		`{"error":{"message":"Input exceeds context window","param":"input","type":"invalid_request_error"}}`,
		`{"error":{"message":"Invalid input","param":"input","type":"invalid_request_error","code":"content_policy_violation"}}`,
		`{"error":{"message":"Invalid input","param":"input","type":"upstream_error"}}`,
	} {
		_, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, []byte(`{"input":[{"role":"assistant","content":null}]}`), []byte(response))
		require.NoError(t, err)
		require.False(t, changed, response)
	}
}

func TestResponsesRejectedInputRetryPreservesToolHistoryAndStops(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, succeeds := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough_%t/success_%t", passthrough, succeeds), func(t *testing.T) {
				body := []byte(`{"model":"deepseek/deepseek-v4.1-flash","stream":false,"input":[{"type":"function_call","call_id":"call_1","name":"probe_ping","arguments":"{\"n\":9007199254740993}"},{"type":"function_call_output","call_id":"call_1","output":"original tool output"},{"type":"agent_message","content":[{"type":"input_text","text":"Reply OK."}]}]}`)
				terminal := newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, rejectedResponsesInputError)
				if succeeds {
					terminal = newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_ok","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":3,"output_tokens":1}}`)
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, rejectedResponsesInputError), terminal,
				}}
				account := newOpenAIRejectedFieldTestAccount()
				account.Extra["openai_passthrough"] = passthrough
				c := newOpenAIRejectedFieldTestContext(body)
				result, err := newOpenAIRejectedFieldTestService(upstream).Forward(context.Background(), c, account, body)
				if succeeds {
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, 3, result.Usage.InputTokens)
				} else {
					require.Error(t, err)
					require.Equal(t, http.StatusBadRequest, c.Writer.Status())
				}
				require.Len(t, upstream.bodies, 2)
				for _, sent := range upstream.bodies {
					require.Equal(t, "deepseek/deepseek-v4.1-flash", gjson.GetBytes(sent, "model").String())
					require.Equal(t, `{"n":9007199254740993}`, gjson.GetBytes(sent, "input.0.arguments").String())
					require.Equal(t, "call_1", gjson.GetBytes(sent, "input.1.call_id").String())
					require.Equal(t, "original tool output", gjson.GetBytes(sent, "input.1.output").String())
				}
				require.Equal(t, "agent_message", gjson.GetBytes(upstream.bodies[0], "input.2.type").String())
				require.Equal(t, "Reply OK.", gjson.GetBytes(upstream.bodies[1], "input.2.content.0.text").String())
			})
		}
	}
}
