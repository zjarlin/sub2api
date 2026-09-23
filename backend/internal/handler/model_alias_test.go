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

func TestGlobalModelAliasHTTPForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "chat/completions", "messages"} {
		for _, original := range []string{"cn:deepseek-v4-flash", "DeepSeek-V4-Flash", "deepseek-v4-flash"} {
			t.Run(endpoint+"/"+original, func(t *testing.T) {
				repo := &grokCredentialHandlerRepo{accounts: []service.Account{{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{"cn:deepseek-v4-flash": "cn:deepseek-v4-flash"}}, Extra: map[string]any{"openai_passthrough": true}}}}
				cfg := &config.Config{RunMode: config.RunModeSimple}
				policy := service.ModelAliasPolicy{Groups: []service.ModelAliasGroup{{Canonical: "deepseek-v4-flash", Aliases: []string{"DeepSeek-V4-Flash", "cn:deepseek-v4-flash"}}}}
				data, _ := json.Marshal(policy)
				settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{service.SettingKeyModelAliases: string(data)}}, cfg)
				billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billing.Stop)
				upstream := &rateLimitChainUpstream{healthyID: 1}
				gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
				cache := &concurrencyCacheMock{acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }, acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }}
				h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
				key := &service.APIKey{ID: 2, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowMessagesDispatch: true}}
				router := gin.New()
				var requested string
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
					c.Next()
					requested = clientRequestedModel(c, "deepseek-v4-flash")
				})
				router.Use(middleware.GlobalModelAliases(settings))
				router.POST("/v1/responses", h.Responses)
				router.POST("/v1/chat/completions", h.ChatCompletions)
				router.POST("/v1/messages", h.Messages)
				response := httptest.NewRecorder()
				body := fmt.Sprintf(`{"model":%q,"input":"hi","messages":[{"role":"user","content":"hi"}],"max_tokens":32,"stream":false}`, original)
				router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(body)))
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Equal(t, []string{"cn:deepseek-v4-flash"}, upstream.models)
				require.Equal(t, original, requested)
				if original != "deepseek-v4-flash" {
					require.Equal(t, "deepseek-v4-flash", response.Header().Get("X-Sub2api-Canonical-Model"))
				}
				require.NotContains(t, repo.accounts[0].GetModelMapping(), "deepseek-v4-flash")
			})
		}
	}
}
