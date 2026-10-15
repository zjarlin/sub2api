//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type implicitCompactUpstream struct {
	service.HTTPUpstream
	forward func(*http.Request, []byte) (*http.Response, error)
}

func (u *implicitCompactUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	return u.forward(request, body)
}

func implicitCompactReply(model, text string, stream bool) *http.Response {
	body := fmt.Sprintf(`{"id":"resp_fallback","object":"response","status":"completed","model":%q,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, model, text)
	contentType := "application/json"
	if stream {
		contentType = "text/event-stream"
		body = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + body + "}\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

func implicitCompactUnavailable() *http.Response {
	return &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"provider unavailable","type":"upstream_error"}}`))}
}

func implicitCompactHandler(t *testing.T, upstream service.HTTPUpstream) (*OpenAIGatewayHandler, *service.APIKey, *concurrencyCacheMock) {
	t.Helper()
	repo := &grokCredentialHandlerRepo{accounts: []service.Account{{
		ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{
			"gpt-6-astra": "gpt-6-astra", "gpt-5.6-sol": "gpt-5.6-sol", "gpt-5.5": "gpt-5.5",
		}},
		Extra: map[string]any{"openai_passthrough": true, openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceResponses)},
	}}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	policy, err := json.Marshal(&service.ModelFallbackPolicy{Enabled: true, Tiers: []service.ModelCapabilityTier{
		{Name: "top", Models: []string{"gpt-6-astra"}},
		{Name: "middle", Models: []string{"gpt-5.6-sol"}},
		{Name: "base", Models: []string{"gpt-5.5"}},
	}})
	require.NoError(t, err)
	settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{service.SettingKeyModelFallbackPolicy: string(policy)}}, cfg)
	cache := &concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	concurrency := service.NewConcurrencyService(cache)
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, concurrency,
		service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
	h := NewOpenAIGatewayHandler(gateway, concurrency, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
	h.maxAccountSwitches = 0
	groupID := int64(7)
	key := &service.APIKey{ID: 2, GroupID: &groupID, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive}}
	return h, key, cache
}

func implicitCompactInput(t *testing.T, stream bool) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model": "gpt-6-astra", "stream": stream, "instructions": "保留用户授权边界",
		"context_management": []any{map[string]any{"type": "compaction", "compact_threshold": 100000}},
		"tools":              []any{map[string]any{"type": "function", "name": "read_file", "parameters": map[string]any{"type": "object"}}},
		"input": []any{
			map[string]any{"role": "user", "content": "修复任务；生产部署须另行批准"},
			map[string]any{"type": "reasoning", "encrypted_content": "old-ciphertext"},
			map[string]any{"role": "assistant", "content": strings.Repeat("备份已完成；修复尚未执行。", 800)},
			map[string]any{"role": "user", "content": "从中断处继续，不要重放已完成的操作"},
		},
	})
	require.NoError(t, err)
	return body
}

func TestResponsesModelFallbackImplicitCompaction(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, summaryFailure := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/summaryFailure=%v", stream, summaryFailure), func(t *testing.T) {
				summaries := 0
				var executions []string
				var retainedInputs []string
				upstream := &implicitCompactUpstream{forward: func(request *http.Request, body []byte) (*http.Response, error) {
					model := gjson.GetBytes(body, "model").String()
					if strings.Contains(string(body), "[sub2api:context-summary]") {
						summaries++
						require.NotContains(t, string(body), "old-ciphertext")
						require.False(t, gjson.GetBytes(body, "tools").Exists())
						require.Empty(t, request.Header.Get("session_id"))
						require.Empty(t, request.Header.Get("x-codex-turn-state"))
						if summaryFailure && model == "gpt-5.6-sol" {
							return implicitCompactUnavailable(), nil
						}
						return implicitCompactReply(model, "历史摘要：备份已完成，不得重放；生产部署须另行批准。", false), nil
					}
					executions = append(executions, model)
					if model == "gpt-6-astra" {
						require.Contains(t, string(body), "old-ciphertext")
						return implicitCompactUnavailable(), nil
					}
					require.NotContains(t, string(body), "old-ciphertext")
					require.Contains(t, string(body), "历史摘要：备份已完成")
					require.Contains(t, string(body), "从中断处继续，不要重放已完成的操作")
					require.Equal(t, "保留用户授权边界", gjson.GetBytes(body, "instructions").String())
					require.True(t, gjson.GetBytes(body, "tools").Exists())
					require.False(t, gjson.GetBytes(body, "context_management").Exists())
					require.Equal(t, "same-dsh-session", request.Header.Get("session_id"))
					require.Empty(t, request.Header.Get("x-codex-turn-state"))
					require.Empty(t, request.Header.Get("x-codex-turn-metadata"))
					retainedInputs = append(retainedInputs, gjson.GetBytes(body, "input").Raw)
					if model == "gpt-5.6-sol" {
						return implicitCompactUnavailable(), nil
					}
					return implicitCompactReply(model, "继续完成原任务", stream), nil
				}}
				h, key, cache := implicitCompactHandler(t, upstream)
				router := gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
				})
				router.POST("/responses", h.Responses)
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(string(implicitCompactInput(t, stream))))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("session_id", "same-dsh-session")
				request.Header.Set("x-codex-turn-state", "old-state")
				request.Header.Set("x-codex-turn-metadata", "old-metadata")
				router.ServeHTTP(response, request)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Contains(t, response.Body.String(), "继续完成原任务")
				require.NotContains(t, response.Body.String(), "历史摘要")
				require.NotContains(t, response.Body.String(), "provider unavailable")
				require.Equal(t, "gpt-5.5", response.Header().Get("X-Sub2api-Fallback-Model"))
				require.Equal(t, int32(3), atomic.LoadInt32(&cache.releaseAccountCalled))
				if summaryFailure {
					require.Equal(t, 2, summaries)
					require.Equal(t, []string{"gpt-6-astra", "gpt-5.5"}, executions)
				} else {
					require.Equal(t, 1, summaries, "连续降级必须复用同一个摘要")
					require.Equal(t, []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.5"}, executions)
					require.Equal(t, retainedInputs[0], retainedInputs[1])
				}
			})
		}
	}
}

func TestResponsesAccountFailoverImplicitCompaction(t *testing.T) {
	summaries := 0
	upstream := &implicitCompactUpstream{forward: func(_ *http.Request, body []byte) (*http.Response, error) {
		summaries++
		require.Contains(t, string(body), "[sub2api:context-summary]")
		require.NotContains(t, string(body), "old-ciphertext")
		return implicitCompactReply("gpt-6-astra", "目标不变；备份已完成，不得重放。", false), nil
	}}
	h, key, _ := implicitCompactHandler(t, upstream)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/responses", nil)
	c.Set("api_key", key)
	state := &openAIPassthroughFailoverState{reasoningSourceAccountID: 1}
	account := &service.Account{ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example"}, Extra: map[string]any{"openai_passthrough": true, openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceResponses)}}
	next, err := h.prepareOpenAIForwardAttemptBody(context.Background(), c, nil, implicitCompactInput(t, false), account, state)
	require.NoError(t, err)
	require.Equal(t, 1, summaries)
	require.Contains(t, string(next), "目标不变；备份已完成")
	require.NotContains(t, string(next), "old-ciphertext")
}
