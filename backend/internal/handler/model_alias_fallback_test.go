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

// 原模型的两个账号都失败后，带供应商前缀的账号必须参与同模型切换或同档降级。
func TestProviderAliasSameModelAndTierFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []struct {
		endpoint string
		stream   bool
	}{{"responses", false}, {"responses", true}, {"chat/completions", false}, {"messages", false}} {
		for _, target := range []string{"deepseek-v4-flash", "deepseek-v4.1-flash"} {
			t.Run(fmt.Sprintf("%s/stream_%t/%s", protocol.endpoint, protocol.stream, target), func(t *testing.T) {
				cfg := &config.Config{RunMode: config.RunModeSimple}
				repo := &grokCredentialHandlerRepo{}
				models := []string{"deepseek-v4-flash", "DeepSeek-V4-Flash", "deepseek/" + target}
				ids := []int64{837, 848, 850}
				for i, model := range models {
					repo.accounts = append(repo.accounts, service.Account{
						ID: ids[i], Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
						Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: i + 1,
						Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{model: model}},
						Extra:       map[string]any{"openai_passthrough": true},
					})
				}
				aliases := service.ModelAliasPolicy{Groups: []service.ModelAliasGroup{
					{Canonical: "deepseek-v4-flash", Aliases: []string{"DeepSeek-V4-Flash", "deepseek/deepseek-v4-flash"}},
					{Canonical: "deepseek-v4.1-flash", Aliases: []string{"deepseek/deepseek-v4.1-flash"}},
				}}
				aliasJSON, err := json.Marshal(aliases)
				require.NoError(t, err)
				settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{
					service.SettingKeyModelAliases:        string(aliasJSON),
					service.SettingKeyModelFallbackPolicy: `{"enabled":true,"tiers":[{"name":"same","models":["deepseek-v4-flash","deepseek-v4.1-flash"]}]}`,
				}}, cfg)
				billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billing.Stop)
				upstream := &rateLimitChainUpstream{healthyID: 850, sse: protocol.stream}
				gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
				cache := &concurrencyCacheMock{
					acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
					acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
				}
				h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
				key := &service.APIKey{ID: 2, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowMessagesDispatch: true}}
				router := gin.New()
				var events []*service.OpsUpstreamErrorEvent
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
					c.Next()
					value, _ := c.Get(service.OpsUpstreamErrorsKey)
					events, _ = value.([]*service.OpsUpstreamErrorEvent)
				})
				router.Use(middleware.GlobalModelAliases(settings))
				router.POST("/v1/responses", h.Responses)
				router.POST("/v1/chat/completions", h.ChatCompletions)
				router.POST("/v1/messages", h.Messages)
				body := fmt.Sprintf(`{"model":"deepseek-v4-flash","input":"hi","messages":[{"role":"user","content":"hi"}],"max_tokens":32,"stream":%t}`, protocol.stream)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/"+protocol.endpoint, strings.NewReader(body)))
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Equal(t, ids, upstream.ids)
				require.Equal(t, models, upstream.models)
				require.Contains(t, response.Body.String(), "ok")
				require.NotContains(t, repo.accounts[2].GetModelMapping(), target)
				if target == "deepseek-v4.1-flash" {
					require.Equal(t, target, response.Header().Get("X-Sub2api-Fallback-Model"))
					require.Len(t, events, 3)
					require.Equal(t, "model_fallback", events[2].Kind)
					require.Equal(t, target, events[2].Model)
				} else {
					require.Empty(t, response.Header().Get("X-Sub2api-Fallback-Model"))
					require.Len(t, events, 2)
				}
			})
		}
	}
}
