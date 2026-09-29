//go:build unit

package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAutoModelPriorityHTTPFailuresReachGLMAndRemainingModels(t *testing.T) {
	for _, failure := range []struct{ name, body string }{
		{"tool_choice", `{"error":{"message":"\"auto\" tool choice requires --enable-auto-tool-choice and --tool-call-parser to be set"}}`},
		{"unsupported_parameter", `{"error":{"code":"unsupported_parameter","message":"VibeX does not support parameter: max_completion_tokens","type":"upstream_error"}}`},
	} {
		for _, endpoint := range []string{"responses", "chat/completions"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream_%t", failure.name, endpoint, stream), func(t *testing.T) {
					accounts := autoModelTestAccounts()[:1]
					account := &accounts[0]
					account.Type, account.Concurrency = service.AccountTypeAPIKey, 1
					account.Extra = map[string]any{"openai_passthrough": true}
					account.Credentials = map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{
						"deepseek-v4.1-flash": "deepseek-v4.1-flash", "glm-5.3": "glm-5.3", "qwen3.8-max": "qwen3.8-max", "gpt-5.5": "gpt-5.5",
					}}
					second := *account
					second.ID = 2
					accounts = append(accounts, second)
					cfg := &config.Config{RunMode: config.RunModeSimple}
					billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
					defer billing.Stop()
					upstream := &fallbackTestUpstream{status: http.StatusBadRequest,
						errorResponse: failure.body}
					gateway := service.NewOpenAIGatewayService(&grokCredentialHandlerRepo{accounts: accounts}, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
						service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
					concurrency := service.NewConcurrencyService(&concurrencyCacheMock{
						acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
						acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
					})
					openAI := NewOpenAIGatewayHandler(gateway, concurrency, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
					h := newAutoModelTestHandler(accounts)
					group := &service.Group{ID: 71, Platform: service.PlatformOpenAI, Status: service.StatusActive}
					key := &service.APIKey{ID: 2, GroupID: &group.ID, Group: group, User: &service.User{ID: 3, Status: service.StatusActive}}
					router := gin.New()
					router.Use(func(c *gin.Context) {
						c.Set(string(middleware.ContextKeyAPIKey), key)
						c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
					}, h.AutoModelMiddleware(nil))
					router.POST("/v1/responses", openAI.Responses)
					router.POST("/v1/chat/completions", openAI.ChatCompletions)
					recorder := httptest.NewRecorder()
					body := fmt.Sprintf(`{"model":"auto","input":"use shell","messages":[{"role":"user","content":"use shell"}],"stream":%t,"tools":[{"type":"function","name":"shell","function":{"name":"shell","parameters":{"type":"object"}},"parameters":{"type":"object"}}]}`, stream)
					router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(body)))
					require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
					require.Equal(t, []string{"deepseek-v4.1-flash", "deepseek-v4.1-flash", "glm-5.3", "glm-5.3", "qwen3.8-max", "qwen3.8-max", "gpt-5.5"}, upstream.models)
					require.Equal(t, "deepseek-v4.1-flash", recorder.Header().Get("X-Sub2API-Selected-Model"))
					require.Equal(t, "gpt-5.5", recorder.Header().Get("X-Sub2api-Fallback-Model"))
				})
			}
		}
	}
}

// 331 个候选必须走到最后一个，且全部失败后立即终止，覆盖超过普通档位策略的 256 模型限制。
func TestAutoModelHTTPTraverses331CandidatesAndTerminates(t *testing.T) {
	for _, endpoint := range []string{"responses", "chat/completions"} {
		for _, stream := range []bool{false, true} {
			for _, succeeds := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream_%t/final_success_%t", endpoint, stream, succeeds), func(t *testing.T) {
					models := make([]string, 331)
					mapping := make(map[string]any, len(models))
					for i := range models {
						models[i] = fmt.Sprintf("aaa-model-%03d", i)
					}
					if succeeds {
						models[len(models)-1] = "gpt-5.5"
					}
					for _, model := range models {
						mapping[model] = model
					}
					accounts := autoModelTestAccounts()[:1]
					accounts[0].Type, accounts[0].Concurrency = service.AccountTypeAPIKey, 1
					accounts[0].Extra = map[string]any{"openai_passthrough": true}
					accounts[0].Credentials = map[string]any{
						"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": mapping,
					}
					cfg := &config.Config{RunMode: config.RunModeSimple}
					billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
					t.Cleanup(billing.Stop)
					upstream := &fallbackTestUpstream{status: http.StatusBadRequest,
						errorResponse: `{"error":{"message":"\"auto\" tool choice requires --enable-auto-tool-choice and --tool-call-parser to be set"}}`}
					gateway := service.NewOpenAIGatewayService(&grokCredentialHandlerRepo{accounts: accounts}, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
						service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
					concurrency := service.NewConcurrencyService(&concurrencyCacheMock{
						acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
						acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
					})
					openAI := NewOpenAIGatewayHandler(gateway, concurrency, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
					auto := newAutoModelTestHandler(accounts)
					group := &service.Group{ID: 71, Platform: service.PlatformOpenAI, Status: service.StatusActive}
					key := &service.APIKey{ID: 2, GroupID: &group.ID, Group: group, User: &service.User{ID: 3, Status: service.StatusActive}}
					router := gin.New()
					router.Use(func(c *gin.Context) {
						c.Set(string(middleware.ContextKeyAPIKey), key)
						c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
					}, auto.AutoModelMiddleware(nil))
					router.POST("/v1/responses", openAI.Responses)
					router.POST("/v1/chat/completions", openAI.ChatCompletions)
					recorder := httptest.NewRecorder()
					body := fmt.Sprintf(`{"model":"auto","input":"use shell","messages":[{"role":"user","content":"use shell"}],"stream":%t,"tools":[{"type":"function","name":"shell","function":{"name":"shell","parameters":{"type":"object"}},"parameters":{"type":"object"}}]}`, stream)
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					request := httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(body)).WithContext(ctx)
					router.ServeHTTP(recorder, request)
					require.NoError(t, ctx.Err(), "candidate exhaustion must terminate before the request timeout")
					require.Equal(t, models, upstream.models, "each candidate must be attempted exactly once, in order")
					require.Equal(t, models[0], recorder.Header().Get("X-Sub2API-Selected-Model"))
					require.Equal(t, models[len(models)-1], recorder.Header().Get("X-Sub2api-Fallback-Model"))
					if succeeds {
						require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
						require.Contains(t, recorder.Body.String(), "gpt-5.5")
					} else {
						require.GreaterOrEqual(t, recorder.Code, http.StatusBadRequest, recorder.Body.String())
						require.Contains(t, recorder.Body.String(), "error")
					}
				})
			}
		}
	}
}
