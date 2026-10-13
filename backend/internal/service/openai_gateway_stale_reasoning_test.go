package service

import (
	"context"
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

// TestDropOpenAIStaleReasoningInputItems_SummaryOnlySkeleton 覆盖 #3138 的核心
// 场景：历史 reasoning 项只剩 summary/元数据、已不带 encrypted_content，上游仍以
// invalid_encrypted_content 拒绝；宽剥离必须能删掉它并报告改动，否则恢复被跳过、
// 400 直接透出。
func TestDropOpenAIStaleReasoningInputItems_SummaryOnlySkeleton(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},` +
		`{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"thinking"}]},` +
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}` +
		`]}`)

	sanitized, changed, err := DropOpenAIStaleReasoningInputItems(body)
	require.NoError(t, err)
	require.True(t, changed, "summary-only reasoning item must be recoverable")

	require.Equal(t, int64(0), gjson.GetBytes(sanitized, `input.#(type=="reasoning")`).Int(),
		"stale reasoning item must be gone")
	require.Equal(t, int64(2), gjson.GetBytes(sanitized, "input.#").Int(),
		"non-reasoning history must be preserved")
}

// TestDropOpenAIStaleReasoningInputItems_EncryptedStillDropped 确认带密文的项同样
// 被整块删除（宽剥离是窄剥离的超集）。
func TestDropOpenAIStaleReasoningInputItems_EncryptedStillDropped(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[` +
		`{"type":"reasoning","id":"rs_2","encrypted_content":"gAAA","summary":[{"type":"summary_text","text":"t"}]},` +
		`{"type":"message","role":"user","content":"hi"}` +
		`]}`)

	sanitized, changed, err := DropOpenAIStaleReasoningInputItems(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotContains(t, string(sanitized), "gAAA")
	require.Equal(t, int64(1), gjson.GetBytes(sanitized, "input.#").Int())
}

// TestDropOpenAIStaleReasoningInputItems_CompactionDropped 覆盖 compaction /
// compaction_summary：这类项本身即压缩产物，同样只能由签发账号解开。
func TestDropOpenAIStaleReasoningInputItems_CompactionDropped(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[` +
		`{"type":"compaction","id":"cmp_1","encrypted_content":"CMP"},` +
		`{"type":"compaction_summary","id":"cmp_2","encrypted_content":"CMP2"},` +
		`{"type":"message","role":"user","content":"hi"}` +
		`]}`)

	sanitized, changed, err := DropOpenAIStaleReasoningInputItems(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotContains(t, string(sanitized), "CMP")
	require.Equal(t, int64(1), gjson.GetBytes(sanitized, "input.#").Int())
	require.Equal(t, "message", gjson.GetBytes(sanitized, "input.0.type").String())
}

// TestDropOpenAIStaleReasoningInputItems_NoReasoningIsNoop 确认常态请求不被改写：
// 没有推理/压缩项时必须报告无改动，避免每轮请求损失推理连续性。
func TestDropOpenAIStaleReasoningInputItems_NoReasoningIsNoop(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":"hi"}]}`)

	sanitized, changed, err := DropOpenAIStaleReasoningInputItems(body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(sanitized), "no-op must return the original bytes")
}

// TestDropOpenAIStaleReasoningInputItems_NoInputIsNoop 覆盖无 input 字段的请求。
func TestDropOpenAIStaleReasoningInputItems_NoInputIsNoop(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","instructions":"x"}`)
	sanitized, changed, err := DropOpenAIStaleReasoningInputItems(body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(sanitized))
}

// TestDropOpenAIStaleReasoningInputItems_PreservesLargeIntegers 确认 64 位整型
// 不发生浮点精度损失（与既有剥离逻辑保持同一不变式）。
func TestDropOpenAIStaleReasoningInputItems_PreservesLargeIntegers(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[` +
		`{"type":"reasoning","summary":[{"type":"summary_text","text":"t"}]},` +
		`{"type":"message","content":[{"type":"input_text","text":"hi","nonce":9007199254740993}]}` +
		`]}`)

	sanitized, changed, err := DropOpenAIStaleReasoningInputItems(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, string(sanitized), "9007199254740993", "large integers must not lose precision")
}

// TestDropOpenAIStaleReasoningInputItems_SingleObjectInput 覆盖 input 为单个对象
// 而非数组的形态。
func TestDropOpenAIStaleReasoningInputItems_SingleObjectInput(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":{"type":"reasoning","id":"rs_9","summary":[{"type":"summary_text","text":"t"}]}}`)
	sanitized, changed, err := DropOpenAIStaleReasoningInputItems(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(sanitized, "input").Exists())
}

// 验证实际转发链路能恢复仅剩摘要的历史，且再次被拒绝时不循环重试。
func TestOpenAIGatewayService_Forward_StaleReasoningHTTPRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, secondStatus := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(http.StatusText(secondStatus), func(t *testing.T) {
			errorBody := `{"error":{"code":"invalid_encrypted_content","type":"invalid_request_error","message":"bad encrypted content"}}`
			secondBody := errorBody
			if secondStatus == http.StatusOK {
				secondBody = `{"usage":{"input_tokens":1,"output_tokens":2}}`
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(errorBody))},
				{StatusCode: secondStatus, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(secondBody))},
			}}
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.Enabled = false
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
			account := &Account{ID: 10, Name: "openai-apikey", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://example.com"},
				Extra:       map[string]any{"use_responses_api": true}}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			body := []byte(`{"model":"gpt-5","stream":false,"input":[{"type":"reasoning","id":"rs_stale","summary":[{"type":"summary_text","text":"old"}]},{"type":"message","role":"user","content":"hi"}]}`)
			result, err := svc.Forward(context.Background(), c, account, body)
			if secondStatus == http.StatusOK {
				require.NoError(t, err)
				require.NotNil(t, result)
			} else {
				require.Error(t, err)
			}
			require.Len(t, upstream.bodies, 2)
			require.Contains(t, string(upstream.bodies[0]), "rs_stale")
			require.Equal(t, "message", gjson.GetBytes(upstream.bodies[1], "input.0.type").String())
			require.Equal(t, int64(1), gjson.GetBytes(upstream.bodies[1], "input.#").Int())
		})
	}
}
