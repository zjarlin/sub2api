package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const staleReasoningRecoveryInput = `{"model":"gpt-5","store":true,"input":[{"type":"message","role":"user","content":"Continue the existing task"},{"type":"reasoning","id":"rs_old","encrypted_content":"old-ciphertext","summary":[{"type":"summary_text","text":"old reasoning"}]},{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"app.go\"}"},{"type":"function_call_output","call_id":"call_1","output":"saved file contents"}]}`

const staleReasoningRecoveryError = `{"code":"invalid_encrypted_content","type":"invalid_request_error","message":"The encrypted content could not be decrypted or parsed."}`

const staleReasoningRecoverySuccess = `{"id":"resp_recovered","object":"response","model":"gpt-5","status":"completed","output":[{"type":"message","id":"msg_recovered","role":"assistant","content":[{"type":"output_text","text":"Continued in the same conversation"}]}],"usage":{"input_tokens":12,"output_tokens":8}}`

func staleReasoningRecoveryResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func staleReasoningRecoveryContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	c.Request.Header.Set("Session_id", "dsh-existing-session")
	c.Request.Header.Set(openAICodexTurnStateHeader, "stale-turn-state")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	return c, rec
}

func staleReasoningRecoveryAccount(passthrough bool) *Account {
	return &Account{
		ID: 23, Name: "recovery-test", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://example.com"},
		Extra:       map[string]any{"use_responses_api": true, "openai_passthrough": passthrough},
	}
}

// 同一份会话输入在普通、透传及不同上游报错载体下均应自动恢复。
func TestOpenAIStaleReasoningRecovery_Forward(t *testing.T) {
	gin.SetMode(gin.TestMode)
	failed := `{"type":"response.failed","response":{"id":"resp_rejected","status":"failed","error":` + staleReasoningRecoveryError + `}}`
	cases := []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{"http400", http.StatusBadRequest, "application/json", `{"error":` + staleReasoningRecoveryError + `}`},
		{"sse_failed", http.StatusOK, "text/event-stream", "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_rejected\"}}\n\nevent: response.failed\ndata: " + failed + "\n\n"},
		{"sse_error", http.StatusOK, "text/event-stream", "event: error\ndata: {\"type\":\"error\",\"error\":" + staleReasoningRecoveryError + "}\n\n"},
		{"sse_flat_error", http.StatusOK, "text/event-stream", "event: error\ndata: {\"type\":\"error\",\"code\":\"invalid_encrypted_content\",\"message\":\"The encrypted content could not be decrypted or parsed.\"}\n\n"},
		{"json_failed", http.StatusOK, "application/json", `{"id":"resp_rejected","status":"failed","error":` + staleReasoningRecoveryError + `}`},
	}
	for _, passthrough := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, tc := range cases {
				// stream=true 的 JSON 错误由 HTTP 状态码路径覆盖。
				if stream && tc.name == "json_failed" {
					continue
				}
				t.Run(fmt.Sprintf("passthrough=%v/stream=%v/%s", passthrough, stream, tc.name), func(t *testing.T) {
					body := strings.TrimSuffix(staleReasoningRecoveryInput, "}") + fmt.Sprintf(`,"stream":%v}`, stream)
					c, rec := staleReasoningRecoveryContext(body)
					successType, successBody := "application/json", staleReasoningRecoverySuccess
					if stream {
						successType = "text/event-stream"
						successBody = "data: {\"type\":\"response.completed\",\"response\":" + staleReasoningRecoverySuccess + "}\n\n"
					}
					upstream := &httpUpstreamRecorder{responses: []*http.Response{
						staleReasoningRecoveryResponse(tc.status, tc.contentType, tc.body),
						staleReasoningRecoveryResponse(http.StatusOK, successType, successBody),
					}}
					svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
					result, err := svc.Forward(context.Background(), c, staleReasoningRecoveryAccount(passthrough), []byte(body))

					require.NoError(t, err)
					require.NotNil(t, result)
					require.Len(t, upstream.bodies, 2)
					require.Contains(t, string(upstream.bodies[0]), "old-ciphertext")
					require.NotContains(t, string(upstream.bodies[1]), "old-ciphertext")
					require.Equal(t, int64(3), gjson.GetBytes(upstream.bodies[1], "input.#").Int())
					require.Equal(t, "Continue the existing task", gjson.GetBytes(upstream.bodies[1], "input.0.content").String())
					require.Equal(t, "call_1", gjson.GetBytes(upstream.bodies[1], "input.1.call_id").String())
					require.Equal(t, "call_1", gjson.GetBytes(upstream.bodies[1], "input.2.call_id").String())
					require.Equal(t, "saved file contents", gjson.GetBytes(upstream.bodies[1], "input.2.output").String())
					require.Contains(t, rec.Body.String(), "Continued in the same conversation")
					require.Empty(t, c.GetHeader(openAICodexTurnStateHeader))
					require.NotContains(t, rec.Body.String(), "resp_rejected")
					require.NotContains(t, rec.Body.String(), "invalid_encrypted_content")
				})
			}
		}
	}
}

// 已输出正文后重放可能重复执行工具；清理后再次失败也必须结束重试。
func TestOpenAIStaleReasoningRecovery_RetryBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	failed := "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":" + staleReasoningRecoveryError + "}}\n\n"
	for _, passthrough := range []bool{false, true} {
		for _, outputStarted := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%v/outputStarted=%v", passthrough, outputStarted), func(t *testing.T) {
				body := strings.TrimSuffix(staleReasoningRecoveryInput, "}") + `,"stream":true}`
				c, rec := staleReasoningRecoveryContext(body)
				first := failed
				expectedAttempts := 2
				if outputStarted {
					first = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Already started\"}\n\n" + failed
					expectedAttempts = 1
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					staleReasoningRecoveryResponse(http.StatusOK, "text/event-stream", first),
					staleReasoningRecoveryResponse(http.StatusOK, "text/event-stream", failed),
				}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				_, err := svc.Forward(context.Background(), c, staleReasoningRecoveryAccount(passthrough), []byte(body))
				require.Error(t, err)
				require.Len(t, upstream.bodies, expectedAttempts)
				require.Contains(t, rec.Body.String(), "invalid_encrypted_content")
				if outputStarted {
					require.Contains(t, rec.Body.String(), "Already started")
				}
			})
		}
	}
}

// OAuth 与 compact 使用同一恢复入口；摘要骨架和压缩项不能漏清。
func TestOpenAIStaleReasoningRecovery_CompactAndOAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, compact := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%v/compact=%v", passthrough, compact), func(t *testing.T) {
				body := strings.Replace(staleReasoningRecoveryInput, `"type":"reasoning"`, `"type":"compaction"`, 1)
				c, rec := staleReasoningRecoveryContext(body)
				if compact {
					c.Request.URL.Path = "/v1/responses/compact"
				}
				account := staleReasoningRecoveryAccount(passthrough)
				account.Type = AccountTypeOAuth
				account.Credentials = map[string]any{"access_token": "oauth-test", "chatgpt_account_id": "account-test"}
				successType := "text/event-stream"
				success := "data: {\"type\":\"response.completed\",\"response\":" + staleReasoningRecoverySuccess + "}\n\n"
				if compact {
					successType = "application/json"
					success = `{"id":"cmp_new","object":"response.compaction","output":[{"type":"compaction","encrypted_content":"fresh-ciphertext"}],"usage":{"input_tokens":12,"output_tokens":8}}`
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					staleReasoningRecoveryResponse(http.StatusBadRequest, "application/json", `{"error":`+staleReasoningRecoveryError+`}`),
					staleReasoningRecoveryResponse(http.StatusOK, successType, success),
				}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				result, err := svc.Forward(context.Background(), c, account, []byte(body))
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.bodies, 2)
				require.NotContains(t, string(upstream.bodies[1]), "old-ciphertext")
				require.Contains(t, string(upstream.bodies[1]), "saved file contents")
				require.Contains(t, string(upstream.bodies[1]), "old reasoning", "可读压缩摘要必须保留")
				require.NotContains(t, rec.Body.String(), "invalid_encrypted_content")
				if compact {
					require.Contains(t, rec.Body.String(), "fresh-ciphertext")
				}
			})
		}
	}
}

// 正常密文不预先删除；没有可恢复历史或无关错误时不发送多余请求。
func TestOpenAIStaleReasoningRecovery_LeavesOtherRequestsAlone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name     string
		body     string
		status   int
		response string
	}{
		{"healthy_history", staleReasoningRecoveryInput, http.StatusOK, staleReasoningRecoverySuccess},
		{"no_reasoning", `{"model":"gpt-5","input":"continue"}`, http.StatusBadRequest, `{"error":` + staleReasoningRecoveryError + `}`},
		{"opaque_compaction", `{"model":"gpt-5","input":[{"type":"compaction","encrypted_content":"unreadable"},{"role":"user","content":"continue"}]}`, http.StatusBadRequest, `{"error":` + staleReasoningRecoveryError + `}`},
		{"unrelated_error", staleReasoningRecoveryInput, http.StatusBadRequest, `{"error":{"code":"invalid_request_error","message":"invalid temperature"}}`},
	}
	for _, passthrough := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("passthrough=%v/%s", passthrough, tc.name), func(t *testing.T) {
				c, _ := staleReasoningRecoveryContext(tc.body)
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					staleReasoningRecoveryResponse(tc.status, "application/json", tc.response),
				}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				_, err := svc.Forward(context.Background(), c, staleReasoningRecoveryAccount(passthrough), []byte(tc.body))
				if tc.status == http.StatusOK {
					require.NoError(t, err)
					require.Contains(t, string(upstream.bodies[0]), "old-ciphertext")
				} else {
					require.Error(t, err)
				}
				require.Len(t, upstream.bodies, 1)
			})
		}
	}
}
