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

// 显式请求未评级/档位耗尽的 guard 模型时，降级链末尾的 auto 兜底应让请求落到
// 可用的通用文本模型，而不是把原始上游 5xx 直接透传给客户端。
func TestModelFallbackAutoSentinelReachesGeneralTextModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 账号 1 只支持 guard 模型且持续 502，账号 2 支持通用文本模型且健康。
	models := map[int64]string{1: "meta/llama-guard-4-12b", 2: "gpt-5.6-luna"}
	repo := &grokCredentialHandlerRepo{}
	for id, model := range models {
		repo.accounts = append(repo.accounts, service.Account{
			ID: id, Name: fmt.Sprintf("account-%d", id), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: int(id),
			Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{model: model}},
			Extra:       map[string]any{"openai_passthrough": true},
		})
	}
	// guard 档只有该模型自身，通用文本模型不在任何档位；因此只有末尾 auto 兜底
	// 才能把请求带到 gpt-5.6-luna，档位机制本身找不到它。
	policy := service.ModelFallbackPolicy{Enabled: true, AutoFallback: true, Tiers: []service.ModelCapabilityTier{
		{Name: "guard", Models: []string{"meta/llama-guard-4-12b"}},
	}}
	data, err := json.Marshal(policy)
	require.NoError(t, err)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.MaxAccountSwitches = 0
	settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{service.SettingKeyModelFallbackPolicy: string(data)}}, cfg)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	// 账号 1 返回 502，账号 2 健康。
	upstream := &rateLimitChainUpstream{healthyID: 2, failureStatus: http.StatusBadGateway}
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
	cache := &concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
	key := &service.APIKey{ID: 2, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{Platform: service.PlatformOpenAI, Status: service.StatusActive}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
		c.Next()
	})
	router.POST("/responses", h.Responses)
	response := httptest.NewRecorder()
	body := `{"model":"meta/llama-guard-4-12b","input":"hi","stream":false}`
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, upstream.models, "gpt-5.6-luna")
}
