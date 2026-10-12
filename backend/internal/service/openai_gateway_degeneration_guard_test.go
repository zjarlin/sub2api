//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// degenerateTailRunes 是 2026-10-12 事故样本的重复单元（36 个 rune）：
// cline-pass 转发的 deepseek-v4.1-flash 卡在这个周期上复读，直到 65536 输出上限（515 秒）。
const degenerateTailRunes = "。\n\n（写。）\n\n我输出。\n\n写。\n\n（写。）\n\n我输出工具调用。\n\n写"

func degenerationGuardTestConfig() *config.Config {
	cfg := rawChatCompletionsTestConfig()
	cfg.Gateway.Degeneration = config.GatewayDegenerationConfig{Enabled: true}
	return cfg
}

func TestChatDegenerationGuard_ConfigGating(t *testing.T) {
	require.Nil(t, (&OpenAIGatewayService{}).chatDegenerationGuard(), "无配置时不启用")
	require.Nil(t, (&OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}).chatDegenerationGuard(), "默认关闭时不启用")

	svc := &OpenAIGatewayService{cfg: degenerationGuardTestConfig()}
	guard := svc.chatDegenerationGuard()
	require.NotNil(t, guard)
	require.Equal(t, apicompat.DefaultChatDegenerationGuard(), *guard, "空阈值必须归一化为默认值")

	svc.cfg.Gateway.Degeneration = config.GatewayDegenerationConfig{
		Enabled:     true,
		MinRepeats:  8,
		WindowRunes: 16,
	}
	guard = svc.chatDegenerationGuard()
	require.NotNil(t, guard)
	require.GreaterOrEqual(t, guard.WindowRunes, guard.MaxPeriod*guard.MinRepeats, "窗口必须容得下 MaxPeriod*MinRepeats")
}

func TestClampDegenerationProneUpstreamMaxTokens(t *testing.T) {
	cfg := degenerationGuardTestConfig()
	cfg.Gateway.Degeneration.MaxOutputTokensModels = []string{"deepseek-v4.1-flash"}
	cfg.Gateway.Degeneration.MaxOutputTokensCap = 32768
	svc := &OpenAIGatewayService{cfg: cfg}

	cases := []struct {
		name     string
		models   []string
		body     string
		wantCap  int64
		wantBody string
	}{
		{
			name:     "命中 flash 模型时钳制到上限",
			models:   []string{"deepseek-v4.1-flash"},
			body:     `{"model":"deepseek-v4.1-flash","max_completion_tokens":65536}`,
			wantCap:  32768,
			wantBody: `32768`,
		},
		{
			name:     "relay 前缀的上游模型名同样命中",
			models:   []string{"deepseek-v4.1-flash", "cline-pass/deepseek-v4.1-flash"},
			body:     `{"model":"cline-pass/deepseek-v4.1-flash","max_completion_tokens":65536,"max_tokens":50000}`,
			wantCap:  32768,
			wantBody: `32768`,
		},
		{
			name:     "非目标模型不钳制",
			models:   []string{"gpt-6-sol"},
			body:     `{"model":"gpt-6-sol","max_completion_tokens":65536}`,
			wantCap:  0,
			wantBody: `65536`,
		},
		{
			name:     "低于上限不动",
			models:   []string{"deepseek-v4.1-flash"},
			body:     `{"model":"deepseek-v4.1-flash","max_completion_tokens":8192}`,
			wantCap:  0,
			wantBody: `8192`,
		},
		{
			name:     "没有上限字段不动",
			models:   []string{"deepseek-v4.1-flash"},
			body:     `{"model":"deepseek-v4.1-flash"}`,
			wantCap:  0,
			wantBody: ``,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			out, cap := svc.clampDegenerationProneUpstreamMaxTokens(tt.models, []byte(tt.body))
			require.Equal(t, tt.wantCap, cap)
			require.True(t, json.Valid(out))
			if tt.wantCap > 0 {
				require.Equal(t, tt.wantCap, gjson.GetBytes(out, "max_completion_tokens").Int())
			} else {
				require.Equal(t, tt.wantBody, gjson.GetBytes(out, "max_completion_tokens").Raw)
			}
		})
	}

	svc.cfg.Gateway.Degeneration.MaxOutputTokensCap = 0
	_, cap := svc.clampDegenerationProneUpstreamMaxTokens([]string{"deepseek-v4.1-flash"}, []byte(`{"max_completion_tokens":65536}`))
	require.Zero(t, cap, "cap<=0 表示不钳制")
}

// TestForwardResponses_AbortsDegenerateUpstreamStream 覆盖主防线：上游复读流必须被
// 中转为 response.failed（而不是跑满输出上限、被 reasoning 兜底成正文）。
func TestForwardResponses_AbortsDegenerateUpstreamStream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-v4.1-flash","input":"hello","stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	lines := []string{
		`data: {"id":"chatcmpl_degen","object":"chat.completion.chunk","model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":""},"finish_reason":null}]}`,
		"",
	}
	for i := 0; i < 400; i++ {
		payload, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"reasoning_content": degenerateTailRunes}}},
		})
		require.NoError(t, err)
		lines = append(lines, "data: "+string(payload), "")
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_degenerate"}},
		Body:       io.NopCloser(strings.NewReader(strings.Join(lines, "\n"))),
	}}
	cfg := degenerationGuardTestConfig()
	cfg.Gateway.Degeneration.MinTotalRunes = 400
	cfg.Gateway.Degeneration.MinRepeats = 8
	cfg.Gateway.Degeneration.WindowRunes = 384
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}

	result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "upstream_degenerate_repetition")
	require.NotNil(t, result)
	require.True(t, result.Stream)

	out := rec.Body.String()
	require.Contains(t, out, "event: response.failed")
	require.Contains(t, out, `"code":"upstream_degenerate_repetition"`)
	require.NotContains(t, out, "event: response.completed")
	require.NotContains(t, out, "event: response.output_text.delta", "退化的 reasoning 不得兜底成正文")
}

// TestForwardResponses_ClampsDegenerationProneMaxTokens 固定兜底钳制：易退化模型的
// 出站输出上限被改写，缩小一次退化能烧掉的 token。
func TestForwardResponses_ClampsDegenerationProneMaxTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-v4.1-flash","input":"hello","stream":true,"max_output_tokens":65536}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_clamp"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"id":"chatcmpl_clamp","object":"chat.completion.chunk","model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
			"",
			`data: {"id":"chatcmpl_clamp","object":"chat.completion.chunk","model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			"",
			"data: [DONE]",
			"",
		}, "\n"))),
	}}
	cfg := degenerationGuardTestConfig()
	cfg.Gateway.Degeneration.MaxOutputTokensModels = []string{"deepseek-v4.1-flash"}
	cfg.Gateway.Degeneration.MaxOutputTokensCap = 32768
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}

	_, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
	require.NoError(t, err)
	require.Equal(t, int64(32768), gjson.GetBytes(upstream.lastBody, "max_completion_tokens").Int())
}
