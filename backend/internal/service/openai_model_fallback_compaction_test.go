//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIFailoverCompactionPreservesTaskAndLiveToolResults(t *testing.T) {
	body := autoCompactTestInput(t)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	payload["context_management"] = []any{map[string]any{"type": "compaction", "compact_threshold": 100000}}
	payload["input"] = append(payload["input"].([]any),
		map[string]any{"type": "reasoning", "encrypted_content": "old-ciphertext"},
		map[string]any{"type": "function_call", "name": "exec_command", "call_id": "current-check", "arguments": `{"cmd":"git status"}`},
		map[string]any{"type": "function_call_output", "call_id": "current-check", "output": "当前修复尚未提交"})
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	calls := 0
	svc, account, _, _ := autoContinueTestService(t, "repair", func(request []byte, _ int) (*http.Response, error) {
		calls++
		require.Contains(t, string(request), autoContinueSummaryMarker)
		require.NotContains(t, string(request), "old-ciphertext")
		require.False(t, gjson.GetBytes(request, "tools").Exists())
		require.False(t, gjson.GetBytes(request, "context_management").Exists())
		require.False(t, gjson.GetBytes(request, "store").Bool())
		return autoCompactTestReply(true, autoCompactTestSummary), nil
	})
	account.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceResponses)
	c, rec := autoContinueTestContext(body, 9, 7)
	c.Request.Header.Set("session_id", "same-dsh-session")
	next, compacted, err := svc.CompactOpenAIFailoverHistory(context.Background(), c, account, body)
	require.NoError(t, err)
	require.True(t, compacted)
	require.Equal(t, 1, calls)
	require.Empty(t, rec.Body.String(), "摘要不向客户端输出")
	require.Equal(t, "same-dsh-session", c.GetHeader("session_id"))
	require.NotContains(t, string(next), "old-ciphertext")
	require.False(t, gjson.GetBytes(next, "context_management").Exists())
	for _, field := range []string{"instructions", "tools", "stream"} {
		require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(next, field).Raw)
	}
	require.Contains(t, string(next), "不要泄露密钥")
	require.Contains(t, string(next), "保留已有脏改动")
	require.Contains(t, string(next), autoCompactTestSummary)
	require.Contains(t, string(next), "从中断处继续；不要重放已完成的命令、修改或外部操作。")
	require.NotContains(t, string(next), `"call_id":"done-backup"`)
	items := gjson.GetBytes(next, "input").Array()
	require.Equal(t, "current-check", items[len(items)-2].Get("call_id").String())
	require.Equal(t, "当前修复尚未提交", items[len(items)-1].Get("output").String())
	require.Len(t, TakeVisionFallbackUsage(c), 1)
}

func TestOpenAIFailoverCompactionRestoresGatewayHistory(t *testing.T) {
	svc, account, _, _ := autoContinueTestService(t, "repair", func(request []byte, _ int) (*http.Response, error) {
		require.Contains(t, string(request), "保留的早期目标")
		require.NotContains(t, string(request), deepSeekCompactTokenPrefix)
		return autoCompactTestReply(true, "保留的早期目标；已经完成备份。"), nil
	})
	account.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceResponses)
	token, err := svc.deepSeekCompactionCipher().Encrypt("保留的早期目标。" + strings.Repeat("已经完成备份。", 400))
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{"model": "text-model", "input": []any{
		map[string]any{"type": "compaction", "encrypted_content": deepSeekCompactTokenPrefix + token},
		map[string]any{"role": "user", "content": "接着做"},
	}})
	require.NoError(t, err)
	c, _ := autoContinueTestContext(body, 9, 7)
	next, compacted, err := svc.CompactOpenAIFailoverHistory(context.Background(), c, account, body)
	require.NoError(t, err)
	require.True(t, compacted)
	require.Contains(t, string(next), "保留的早期目标")
	require.NotContains(t, string(next), deepSeekCompactTokenPrefix)
}

func TestOpenAIFailoverCompactionKeepsImagesAndCustomToolPair(t *testing.T) {
	input := []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": "https://example.com/diagram.png"}}},
		map[string]any{"role": "assistant", "content": strings.Repeat("old findings ", 1000)},
		map[string]any{"role": "user", "content": "继续"},
		map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "patch", "input": "patch body"},
		map[string]any{"type": "custom_tool_call_output", "call_id": "patch", "output": "已修改"},
	}
	body, err := json.Marshal(map[string]any{"model": "text-model", "input": input})
	require.NoError(t, err)
	svc, account, _, _ := autoContinueTestService(t, "repair", func(request []byte, _ int) (*http.Response, error) {
		require.NotContains(t, string(request), "diagram.png")
		require.NotContains(t, string(request), "patch body")
		return autoCompactTestReply(true, "早期发现的摘要"), nil
	})
	account.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceResponses)
	c, _ := autoContinueTestContext(body, 9, 7)
	next, compacted, err := svc.CompactOpenAIFailoverHistory(context.Background(), c, account, body)
	require.NoError(t, err)
	require.True(t, compacted)
	require.Contains(t, string(next), "diagram.png")
	require.Contains(t, string(next), "patch body")
	require.Contains(t, string(next), "已修改")
}

func TestOpenAIFailoverCompactionSkipsTrivialAndExplicitCompactRequests(t *testing.T) {
	for _, tc := range []struct{ name, body, path string }{
		{"single_user", `{"model":"text-model","input":[{"role":"user","content":"hi"}]}`, "/v1/responses"},
		{"tool_pair", `{"model":"text-model","input":[{"role":"user","content":"hi"},{"type":"function_call","call_id":"c","name":"read_file","arguments":"{}"},{"type":"function_call_output","call_id":"c","output":"done"}]}`, "/v1/responses"},
		{"explicit_compact", string(autoCompactTestInput(t)), "/v1/responses/compact"},
		{"native_compact", string(autoCompactTestInput(t)), "/responses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, account, _, _ := autoContinueTestService(t, "repair", func([]byte, int) (*http.Response, error) {
				t.Fatal("不能为无需摘要的请求增加上游回合")
				return nil, nil
			})
			c, _ := autoContinueTestContext([]byte(tc.body), 9, 7)
			c.Request.URL.Path = tc.path
			if tc.name == "native_compact" {
				MarkOpenAINativeCompactionV2(c)
			}
			next, compacted, err := svc.CompactOpenAIFailoverHistory(context.Background(), c, account, []byte(tc.body))
			require.NoError(t, err)
			require.False(t, compacted)
			require.JSONEq(t, tc.body, string(next))
		})
	}
}

func TestOpenAIFailoverCompactionAllowsAuthenticatedUngroupedKey(t *testing.T) {
	svc, account, _, _ := autoContinueTestService(t, "repair", func([]byte, int) (*http.Response, error) {
		return autoCompactTestReply(true, autoCompactTestSummary), nil
	})
	account.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceResponses)
	body := autoCompactTestInput(t)
	c, _ := autoContinueTestContext(body, 9, 7)
	value, _ := c.Get("api_key")
	value.(*APIKey).GroupID = nil
	_, compacted, err := svc.CompactOpenAIFailoverHistory(context.Background(), c, account, body)
	require.NoError(t, err)
	require.True(t, compacted)
}

func TestOpenAIFailoverCompactionRejectsUnreadableHistoryAndFailedSummary(t *testing.T) {
	for _, tc := range []string{"opaque_compaction", "summary_failure"} {
		t.Run(tc, func(t *testing.T) {
			calls := 0
			svc, account, _, _ := autoContinueTestService(t, "repair", func([]byte, int) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"unavailable"}}`))}, nil
			})
			account.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceResponses)
			body := autoCompactTestInput(t)
			if tc == "opaque_compaction" {
				body = []byte(`{"model":"text-model","input":[{"type":"compaction","encrypted_content":"unknown"},{"role":"user","content":"continue"}]}`)
			}
			c, recorder := autoContinueTestContext(body, 9, 7)
			_, _, err := svc.CompactOpenAIFailoverHistory(context.Background(), c, account, body)
			require.Error(t, err)
			require.Empty(t, recorder.Body.String())
			if tc == "opaque_compaction" {
				require.Zero(t, calls)
				require.ErrorContains(t, err, "opaque compaction")
			} else {
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.Equal(t, 503, failover.StatusCode)
			}
		})
	}
}
