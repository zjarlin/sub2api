//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelTierFallbackEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "chat/completions", "messages"} {
		for _, healthy := range []int64{3, 4, 5, 0} {
			for _, stream := range []bool{false, true} {
				// 通用 SSE 桩只生成 Responses 帧；另外两种协议通过非流式验证真实转发。
				if stream && endpoint != "responses" {
					continue
				}
				t.Run(fmt.Sprintf("%s/healthy_%d/stream_%t", endpoint, healthy, stream), func(t *testing.T) {
					models := []string{"gpt-6-astra", "gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-5.5"}
					repo := &grokCredentialHandlerRepo{}
					for i, model := range models {
						repo.accounts = append(repo.accounts, service.Account{
							ID: int64(i + 1), Name: fmt.Sprintf("account-%d", i+1), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
							Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: i + 1,
							Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{model: model}},
							Extra:       map[string]any{"openai_passthrough": true},
						})
					}
					policy := service.ModelFallbackPolicy{Enabled: true, Tiers: []service.ModelCapabilityTier{
						{Name: "same", Models: []string{"gpt-5.6-sol", "gpt-6-astra"}},
						{Name: "lower", Models: []string{"gpt-5.6-luna"}},
						{Name: "lowest", Models: []string{"gpt-5.5"}},
					}}
					data, err := json.Marshal(policy)
					require.NoError(t, err)
					cfg := &config.Config{RunMode: config.RunModeSimple}
					cfg.Gateway.MaxAccountSwitches = 0
					settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{service.SettingKeyModelFallbackPolicy: string(data)}}, cfg)
					billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
					t.Cleanup(billing.Stop)
					upstream := &rateLimitChainUpstream{healthyID: healthy, sse: stream}
					gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
					cache := &concurrencyCacheMock{acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }, acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }}
					h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
					key := &service.APIKey{ID: 2, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowMessagesDispatch: true}}
					var events []*service.OpsUpstreamErrorEvent
					router := gin.New()
					router.Use(func(c *gin.Context) {
						c.Set(string(middleware.ContextKeyAPIKey), key)
						c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
						c.Next()
						v, _ := c.Get(service.OpsUpstreamErrorsKey)
						events, _ = v.([]*service.OpsUpstreamErrorEvent)
					})
					router.POST("/responses", h.Responses)
					router.POST("/chat/completions", h.ChatCompletions)
					router.POST("/messages", h.Messages)
					response := httptest.NewRecorder()
					body := fmt.Sprintf(`{"model":"gpt-6-astra","input":"hi","messages":[{"role":"user","content":"hi"}],"max_tokens":20,"stream":%t}`, stream)
					router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/"+endpoint, strings.NewReader(body)))
					count := int(healthy)
					if count == 0 {
						count = 5
						require.Equal(t, http.StatusTooManyRequests, response.Code, response.Body.String())
					} else {
						require.Equal(t, http.StatusOK, response.Code, response.Body.String())
						require.Contains(t, response.Body.String(), "ok")
						require.NotContains(t, response.Body.String(), "rate_limit")
						require.NotContains(t, response.Body.String(), "failed_attempt")
					}
					require.Equal(t, models[:count], upstream.models)
					for i, id := range upstream.ids {
						require.Equal(t, int64(i+1), id)
					}
					require.Equal(t, "gpt-6-astra", response.Header().Get("X-Sub2api-Requested-Model"))
					require.Equal(t, models[count-1], response.Header().Get("X-Sub2api-Fallback-Model"))
					var transitions []string
					for _, event := range events {
						if event.Kind == "model_fallback" {
							transitions = append(transitions, event.Model)
							continue
						}
						require.NotEmpty(t, event.Model)
					}
					require.Equal(t, models[2:count], transitions)
				})
			}
		}
	}
}

func TestModelFallbackRejectsHostedTools(t *testing.T) {
	for _, body := range []string{`{"tools":[{"type":"web_search"}]}`, `{"tools":[{"type":"namespace","tools":[{"type":"computer"}]}]}`} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/responses", nil)
		h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}
		_, ok := h.nextModelFallback(c, &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}, "gpt-6-astra", []byte(body), false)
		require.False(t, ok)
	}
}

func TestModelFallbackRechecksSelectedAccountAndReleasesSlot(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(modelFallbackStateKey, &modelFallbackState{})
	account := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}
	account.SetUpstreamModelMetadataSnapshot(service.UpstreamModelMetadataSnapshot{Models: map[string]service.UpstreamModelMetadata{"target": {ID: "target", MaxContextWindow: 20}}})
	released := 0
	selection := &service.AccountSelectionResult{Account: account, ReleaseFunc: func() { released++ }}
	require.True(t, rejectIncompatibleModelFallbackAccount(c, selection, "target", []byte(`{"input":"a complete conversation cannot fit"}`)))
	require.Equal(t, 1, released)
}
