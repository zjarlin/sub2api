//go:build unit

package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type rateLimitChainUpstream struct {
	service.HTTPUpstream
	ids       []int64
	models    []string
	healthyID int64
	sse       bool
}

func (u *rateLimitChainUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.ids = append(u.ids, id)
	body, _ := io.ReadAll(req.Body)
	u.models = append(u.models, gjson.GetBytes(body, "model").String())
	req.Body = io.NopCloser(strings.NewReader(string(body)))
	if id == u.healthyID {
		return (&fallbackTestUpstream{}).Do(req, "", id, 1)
	}
	status := http.StatusTooManyRequests
	contentType := "application/json"
	response := `{"error":{"type":"new_api_error","message":"此 Key 已达到 RPM 限制：每分钟最多15次"}}`
	if u.sse {
		status = http.StatusOK
		contentType = "text/event-stream"
		response = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"failed_attempt\"}}\n\nevent: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"Upstream rate limit exceeded, please retry later\"}}}\n\n"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

func TestOpenAIRateLimitTriesRemainingSameModelAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, scenario := range []struct {
		endpoint  string
		sse       bool
		healthyID int64
	}{
		{"responses", false, 6}, {"responses", false, 0},
		{"responses", true, 6}, {"responses", true, 0},
		{"chat/completions", false, 6}, {"chat/completions", false, 0},
	} {
		endpoint, sse, healthyID := scenario.endpoint, scenario.sse, scenario.healthyID
		t.Run(fmt.Sprintf("%s/sse_%t/healthy_%d", endpoint, sse, healthyID), func(t *testing.T) {
			repo := &grokCredentialHandlerRepo{}
			for id := int64(1); id <= 6; id++ {
				model := "deepseek-v4.1-flash"
				if id == 1 {
					model = "different-model"
				}
				repo.accounts = append(repo.accounts, service.Account{
					ID: id, Name: fmt.Sprintf("account-%d", id), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: int(id),
					Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{model: model},
						"pool_mode": true, "pool_mode_retry_count": float64(3), "pool_mode_retry_status_codes": []any{float64(429)}},
					Extra: map[string]any{"openai_passthrough": true},
				})
			}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Gateway.MaxAccountSwitches = 1
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			upstream := &rateLimitChainUpstream{healthyID: healthyID, sse: sse}
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			cache := &concurrencyCacheMock{acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }, acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }}
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
			key := &service.APIKey{ID: 2, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{Platform: service.PlatformOpenAI, Status: service.StatusActive}}
			var events []*service.OpsUpstreamErrorEvent
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
				c.Next()
				value, _ := c.Get(service.OpsUpstreamErrorsKey)
				events, _ = value.([]*service.OpsUpstreamErrorEvent)
			})
			router.POST("/responses", h.Responses)
			router.POST("/chat/completions", h.ChatCompletions)
			response := httptest.NewRecorder()
			requestBody := fmt.Sprintf(`{"model":"deepseek-v4.1-flash","input":"hi","messages":[{"role":"user","content":"hi"}],"stream":%t}`, sse)
			router.ServeHTTP(response, httptest.NewRequest("POST", "/"+endpoint, strings.NewReader(requestBody)))
			require.Equal(t, []int64{2, 3, 4, 5, 6}, upstream.ids)
			for _, model := range upstream.models {
				require.Equal(t, "deepseek-v4.1-flash", model)
			}
			failedCount := 5
			if healthyID > 0 {
				failedCount = 4
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Contains(t, response.Body.String(), "ok")
				require.NotContains(t, response.Body.String(), "rate_limit")
				require.NotContains(t, response.Body.String(), "failed_attempt")
			} else {
				require.Equal(t, http.StatusTooManyRequests, response.Code)
			}
			require.Len(t, events, failedCount)
			for index, event := range events {
				require.Equal(t, int64(index+2), event.AccountID)
				require.Equal(t, fmt.Sprintf("account-%d", index+2), event.AccountName)
				require.Equal(t, 429, event.UpstreamStatusCode)
			}
			require.Empty(t, response.Header().Get("X-Sub2api-Fallback-Model"))
		})
	}
}

type localCapacityRecoveryCache struct {
	concurrencyCacheMock
	available bool
}

func (c *localCapacityRecoveryCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return c.available, nil
}

func (c *localCapacityRecoveryCache) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	// 首轮队列已满，下一轮调度前模拟已有请求释放槽位。
	c.available = true
	return false, nil
}

func TestOpenAILocalCapacityWaitRecoversWithoutClientError(t *testing.T) {
	for _, endpoint := range []string{"responses", "chat/completions"} {
		t.Run(endpoint, func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 2)
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Gateway.MaxAccountSwitches = 1
			repo := &grokCredentialHandlerRepo{accounts: []service.Account{{
				ID: 832, Name: "r4", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"}},
				Extra:       map[string]any{"openai_passthrough": true},
			}}}
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			cache := &localCapacityRecoveryCache{}
			concurrency := service.NewConcurrencyService(cache)
			upstream := &fallbackTestUpstream{}
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, concurrency, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			h := NewOpenAIGatewayHandler(gateway, concurrency, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
			ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{Platform: service.PlatformOpenAI, Status: service.StatusActive}})
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3})
			})
			router.POST("/responses", h.Responses)
			router.POST("/chat/completions", h.ChatCompletions)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("POST", "/"+endpoint, strings.NewReader(`{"model":"deepseek-v4.1-flash","input":"hi","messages":[{"role":"user","content":"hi"}],"stream":false}`)))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "ok")
			require.NotContains(t, response.Body.String(), "error")
			require.Equal(t, []string{"deepseek-v4.1-flash"}, upstream.models)
			require.Len(t, opsErrorLogQueue, 1)
			entry := (<-opsErrorLogQueue).entry
			require.Equal(t, http.StatusOK, entry.StatusCode)
			require.Equal(t, "routing", entry.ErrorPhase)
			require.Equal(t, "platform", entry.ErrorOwner)
			require.Zero(t, *entry.UpstreamStatusCode)
			events, err := service.ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
			require.NoError(t, err)
			require.Len(t, events, 1)
			require.Equal(t, "r4", events[0].AccountName)
			require.Equal(t, "account_wait_queue_full", events[0].Reason)
		})
	}
}
