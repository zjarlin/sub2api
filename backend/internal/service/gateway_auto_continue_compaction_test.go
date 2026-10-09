//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const autoCompactTestSummary = "原始目标：修复404并验证。已完成备份，不要重放。部署必须另行批准。剩余：检查修复并验证。"

func autoCompactTestInput(t *testing.T) []byte {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(autoContinueTestInput), &payload))
	payload["instructions"] = "生产部署必须另行批准，已成功的外部操作不得重放。"
	payload["input"] = []any{
		map[string]any{"role": "system", "content": "不要泄露密钥"},
		map[string]any{"role": "developer", "content": "保留已有脏改动"},
		map[string]any{"role": "user", "content": "请修复404并验证，先核对之前的操作。"},
		map[string]any{"type": "function_call", "name": "exec_command", "call_id": "done-backup", "arguments": `{"cmd":"backup"}`},
		map[string]any{"type": "function_call_output", "call_id": "done-backup", "output": "备份已成功。" + strings.Repeat("历史诊断输出。", 1800)},
		map[string]any{"role": "user", "content": "从中断处继续；不要重放已完成的命令、修改或外部操作。"},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return body
}

func autoCompactTestError(message string) *http.Response {
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"code": "prompt_too_long", "type": "invalid_request_error", "message": message}})
	return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}
}

func autoCompactTestReply(native bool, text string) *http.Response {
	if !native {
		return visionTestResponse("text-model", text)
	}
	body, _ := json.Marshal(map[string]any{"id": "resp_compact_test", "object": "response", "model": "text-model", "status": "completed",
		"output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}},
		"usage":  map[string]any{"input_tokens": 11, "output_tokens": 7, "total_tokens": 18}})
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}
}

func TestAutoContinueCompactionRecoversOverflowAndKeepsAuthorization(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat_completions", true: "responses"}[native], func(t *testing.T) {
			body := autoCompactTestInput(t)
			calls, summaries := 0, 0
			svc, account, store, decisions := autoContinueTestService(t, "repair", func(request []byte, _ int) (*http.Response, error) {
				calls++
				if calls == 1 {
					return autoCompactTestError(`{"code":11115,"msg":"prompt is too long: 1185446 tokens > 1048576 maximum","requestId":"overflow-original"}`), nil
				}
				if bytes.Contains(request, []byte(autoContinueSummaryMarker)) {
					summaries++
					require.False(t, gjson.GetBytes(request, "tools").Exists())
					content := gjson.GetBytes(request, "input.1.content")
					data := content.String()
					if content.IsArray() {
						data = content.Get("0.text").String()
					}
					if !native {
						data = gjson.GetBytes(request, "messages.1.content").String()
					}
					require.LessOrEqual(t, len(gjson.Get(data, "history_chunk").String()), 16<<10)
					if summaries > 1 {
						require.Equal(t, autoCompactTestSummary, gjson.Get(data, "previous_summary").String())
					}
					return autoCompactTestReply(native, autoCompactTestSummary), nil
				}
				require.Less(t, len(request), len(body)/2)
				require.Contains(t, string(request), "生产部署必须另行批准")
				require.Contains(t, string(request), "不要泄露密钥")
				require.Contains(t, string(request), "保留已有脏改动")
				require.Contains(t, string(request), "从中断处继续；不要重放已完成的命令、修改或外部操作。")
				require.Contains(t, string(request), autoCompactTestSummary)
				require.True(t, gjson.GetBytes(request, "tools").Exists())
				require.NotContains(t, string(request), `"call_id":"done-backup"`)
				return autoCompactTestReply(native, "已核对并完成修复验证"), nil
			})
			svc.cfg.Gateway.AutoContinue.CompactionChunkBytes = 16 << 10
			if native {
				account.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceResponses)
			}
			c, recorder := autoContinueTestContext(body, 9, 7)
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Greater(t, summaries, 1)
			require.Equal(t, summaries+2, calls)
			require.Zero(t, *decisions, "known context recovery does not need a business decision")
			require.Len(t, store.routes, 1)
			require.Equal(t, "compaction", store.routes[0].Operation.Decision.Source)
			require.Equal(t, "text-model", store.routes[0].Operation.Decision.Model)
			require.Equal(t, "compacted", recorder.Header().Get("X-Sub2API-Auto-Compaction"))
			require.Contains(t, recorder.Body.String(), "已核对并完成修复验证")
			require.Contains(t, recorder.Body.String(), "自动上下文压缩")
			require.NotContains(t, recorder.Body.String(), "overflow-original")
			require.Len(t, TakeVisionFallbackUsage(c), summaries)
		})
	}
}

func TestAutoContinueCompactionHandlesAssistantPause(t *testing.T) {
	body := autoCompactTestInput(t)
	calls, summaries := 0, 0
	svc, account, store, decisions := autoContinueTestService(t, "repair", func(request []byte, _ int) (*http.Response, error) {
		calls++
		if calls == 1 {
			return visionTestResponse("text-model", "上下文已满，需要先压缩历史，才能继续修复。"), nil
		}
		if bytes.Contains(request, []byte(autoContinueSummaryMarker)) {
			summaries++
			return visionTestResponse("text-model", autoCompactTestSummary), nil
		}
		return visionTestResponse("text-model", "已完成验证"), nil
	})
	c, recorder := autoContinueTestContext(body, 9, 7)
	_, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.Equal(t, "compacted", recorder.Header().Get("X-Sub2API-Auto-Compaction"))
	require.Zero(t, *decisions)
	require.Len(t, store.routes, 1)
	require.Len(t, TakeVisionFallbackUsage(c), summaries+1)
	require.Contains(t, recorder.Body.String(), "已完成验证")
}

func TestAutoContinueCompactionAfterDecisionKeepsEarlierUsage(t *testing.T) {
	body := autoCompactTestInput(t)
	calls := 0
	svc, account, store, _ := autoContinueTestService(t, "repair", func(request []byte, _ int) (*http.Response, error) {
		calls++
		if calls == 1 {
			return visionTestResponse("text-model", "已定位缺陷，尚未修复。"), nil
		}
		if calls == 2 {
			return autoCompactTestError("prompt is too long"), nil
		}
		if bytes.Contains(request, []byte(autoContinueSummaryMarker)) {
			return visionTestResponse("text-model", autoCompactTestSummary), nil
		}
		return visionTestResponse("text-model", "已完成修复验证"), nil
	})
	c, recorder := autoContinueTestContext(body, 9, 7)
	_, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.Equal(t, 4, calls)
	require.Len(t, store.routes, 2)
	require.Len(t, TakeVisionFallbackUsage(c), 2)
	require.Contains(t, recorder.Body.String(), "已完成修复验证")
}

func TestAutoContinueCompactionFailurePreservesOriginalErrorAndBoundsRetry(t *testing.T) {
	for _, failure := range []string{"summary", "persistence", "retry", "budget"} {
		t.Run(failure, func(t *testing.T) {
			body := autoCompactTestInput(t)
			calls, retries := 0, 0
			svc, account, store, _ := autoContinueTestService(t, "repair", func(request []byte, _ int) (*http.Response, error) {
				calls++
				if calls == 1 {
					return autoCompactTestError("prompt is too long: 1185446 tokens > 1048576 maximum; original-error-id"), nil
				}
				if bytes.Contains(request, []byte(autoContinueSummaryMarker)) {
					if failure == "summary" {
						return visionTestResponse("text-model", ""), nil
					}
					return visionTestResponse("text-model", autoCompactTestSummary), nil
				}
				retries++
				return autoCompactTestError("prompt is too long; replacement-error-id"), nil
			})
			if failure == "persistence" {
				store.err = errors.New("audit unavailable")
			}
			if failure == "budget" {
				svc.cfg.Gateway.AutoContinue.CompactionChunkBytes = 4 << 10
				svc.cfg.Gateway.AutoContinue.CompactionMaxChunks = 1
			}
			c, recorder := autoContinueTestContext(body, 9, 7)
			_, err := svc.Forward(context.Background(), c, account, body)
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "original-error-id")
			require.NotContains(t, recorder.Body.String(), "replacement-error-id")
			if failure == "retry" {
				require.Equal(t, 1, retries)
				require.Equal(t, 3, calls)
				require.Equal(t, "retry_failed", recorder.Header().Get("X-Sub2API-Auto-Compaction"))
			} else {
				require.Zero(t, retries)
				require.Empty(t, store.routes)
				if failure == "budget" {
					require.Equal(t, 1, calls)
				}
			}
		})
	}
}

func TestAutoContinueCompactionRecognizesRealErrorsOnly(t *testing.T) {
	for _, body := range []string{
		`{"code":11115,"msg":"prompt is too long"}`,
		`{"error":{"code":"prompt_too_long","message":"request too large"}}`,
		`{"error":{"code":"invalid_request_error","message":"prompt is too long: 1185446 tokens > 1048576 maximum"}}`,
		`{"error":{"code":"context_length_exceeded"}}`,
	} {
		require.True(t, isOpenAIContextWindowError("", []byte(body)))
		require.False(t, (&OpenAIGatewayService{}).shouldFailoverOpenAIUpstreamResponse(nil, 400, "", []byte(body)))
		require.False(t, isOpenAIInstructionsRequiredError(400, "", []byte(body)))
	}
	for _, body := range []string{
		`{"error":{"message":"invalid schema"},"echo":{"code":11115,"msg":"prompt is too long"}}`,
		`{"error":{"message":"invalid schema"},"request":{"error":{"code":"prompt_too_long"}}}`,
	} {
		require.False(t, isOpenAIContextWindowError("", []byte(body)))
	}
	for _, text := range []string{"任务已完成", "压缩接口可用于继续任务", "上下文已满，但无需压缩或继续，任务已经完成。"} {
		require.False(t, autoContinueNeedsCompaction(text))
	}
}

func TestAutoContinueCompactionChunksKeepUTF8AndRejectExcessBudget(t *testing.T) {
	text := strings.Repeat("中文abc", 30)
	chunks, err := autoContinueSummaryChunks(text, 17, 30)
	require.NoError(t, err)
	require.Equal(t, text, strings.Join(chunks, ""))
	for _, chunk := range chunks {
		require.True(t, utf8.ValidString(chunk))
		require.LessOrEqual(t, len(chunk), 17)
	}
	_, err = autoContinueSummaryChunks(text, 17, 1)
	require.Error(t, err)
}

func TestAutoContinueCompactionKeepsDelayedErrorsBuffered(t *testing.T) {
	c, _ := autoContinueTestContext(nil, 9, 7)
	writer := newAutoContinueWriter(c.Writer)
	writer.startedAt = time.Now().Add(-time.Minute)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusBadRequest)
	_, err := writer.WriteString(`{"error":{"code":"prompt_too_long"}}`)
	require.NoError(t, err)
	require.False(t, writer.live)
	require.True(t, autoContinueContextError(writer))
}

func TestAutoContinueCompactionKeepsDelayedSSEErrorBeforeOutput(t *testing.T) {
	c, recorder := autoContinueTestContext(nil, 9, 7)
	writer := newAutoContinueWriter(c.Writer)
	writer.startedAt = time.Now().Add(-time.Minute)
	writer.Header().Set("Content-Type", "text/event-stream")
	_, err := writer.WriteString("event: response.created\ndata: {\"type\":\"response.created\"}\n\n")
	require.NoError(t, err)
	_, err = writer.WriteString("event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"context_length_exceeded\"}}}\n\n")
	require.NoError(t, err)
	require.False(t, writer.live)
	require.Empty(t, recorder.Body.String())
	require.True(t, autoContinueContextError(writer))
	// 同样的等待时长，真正开始输出内容时应恢复普通流式输出。
	writer = newAutoContinueWriter(c.Writer)
	writer.startedAt = time.Now().Add(-time.Minute)
	writer.Header().Set("Content-Type", "text/event-stream")
	_, err = writer.WriteString("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"working\"}\n\n")
	require.NoError(t, err)
	require.True(t, writer.live)
	require.Contains(t, recorder.Body.String(), "working")
}

func TestAutoContinueCompactionSummaryDoesNotReuseCallerTurnState(t *testing.T) {
	svc, account, _, _ := autoContinueTestService(t, "repair", func(_ []byte, _ int) (*http.Response, error) {
		return autoCompactTestReply(true, autoCompactTestSummary), nil
	})
	account.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceResponses)
	upstream := svc.httpUpstream.(*codexModelsHTTPUpstreamStub)
	forward := upstream.do
	upstream.do = func(r *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
		require.Empty(t, r.Header.Get(openAICodexTurnStateHeader))
		require.Empty(t, r.Header.Get("x-codex-turn-metadata"))
		require.NotEqual(t, "caller-session", r.Header.Get("session_id"))
		return forward(r, proxy, accountID, concurrency)
	}
	c, _ := autoContinueTestContext(nil, 9, 7)
	c.Request.Header.Set("session_id", "caller-session")
	c.Request.Header.Set(openAICodexTurnStateHeader, "caller-state")
	c.Request.Header.Set("x-codex-turn-metadata", "caller-metadata")
	summary, _, err := svc.callAutoContinueSummary(context.Background(), c, account, "text-model", "", "history")
	require.NoError(t, err)
	require.Equal(t, autoCompactTestSummary, summary)
	require.Equal(t, "caller-session", c.GetHeader("session_id"))
	require.Equal(t, "caller-state", c.GetHeader(openAICodexTurnStateHeader))
	require.Equal(t, "caller-metadata", c.GetHeader("x-codex-turn-metadata"))
}
