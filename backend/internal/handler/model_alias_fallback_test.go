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
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 原模型的两个账号都失败后，带供应商前缀的账号必须参与同模型切换或同档降级。
func TestProviderAliasSameModelAndTierFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []struct {
		endpoint string
		stream   bool
	}{{"responses", false}, {"responses", true}, {"chat/completions", false}, {"messages", false}} {
		for _, target := range []string{"deepseek-v4-flash", "deepseek-v4.1-flash", "deepseek-v4-pro"} {
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
					{Canonical: "deepseek-v4-pro", Aliases: []string{"deepseek/deepseek-v4-pro"}},
				}}
				aliasJSON, err := json.Marshal(aliases)
				require.NoError(t, err)
				settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{
					service.SettingKeyModelAliases:        string(aliasJSON),
					service.SettingKeyModelFallbackPolicy: `{"enabled":true,"tiers":[{"name":"same","models":["deepseek-v4-flash","deepseek-v4.1-flash","deepseek-v4-pro"]}]}`,
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
				if target == "deepseek-v4-pro" && protocol.endpoint == "responses" {
					body = fmt.Sprintf(`{"model":"deepseek-v4-flash","input":"hi","tools":[{"type":"custom","name":"apply_patch"},{"type":"tool_search","execution":"client"}],"stream":%t}`, protocol.stream)
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/"+protocol.endpoint, strings.NewReader(body)))
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Equal(t, ids, upstream.ids)
				require.Equal(t, models, upstream.models)
				require.Contains(t, response.Body.String(), "ok")
				require.NotContains(t, repo.accounts[2].GetModelMapping(), target)
				if target != "deepseek-v4-flash" {
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

// 每次重新调度必须重新派生叶子，不能把一次 429 扩散为整个账号的失败。
type modelAliasLeafUpstream struct {
	service.HTTPUpstream
	models    []string
	ids       []int64
	exhausted bool
}

func (u *modelAliasLeafUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	model := gjson.GetBytes(body, "model").String()
	u.models = append(u.models, model)
	u.ids = append(u.ids, id)
	req.Body = io.NopCloser(strings.NewReader(string(body)))
	if model == "deepseek/deepseek-v4.1-flash-1017" || u.exhausted {
		return &http.Response{StatusCode: 429, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"model rate limited"}}`))}, nil
	}
	return (&fallbackTestUpstream{}).Do(req, "", id, 1)
}
func TestModelAliasTreeSameAccountHTTPRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "chat/completions", "messages"} {
		for _, exhausted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/exhausted_%t", endpoint, exhausted), func(t *testing.T) {
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cfg.Gateway.MaxAccountSwitches = 1
				first, second := "deepseek/deepseek-v4.1-flash-1017", "deepseek-v4.1-flash"
				repo := &grokCredentialHandlerRepo{accounts: []service.Account{{ID: 872, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{first: first, second: second}}, Extra: map[string]any{"openai_passthrough": true}}}}
				aliases := &service.ModelAliasPolicy{Groups: []service.ModelAliasGroup{{Canonical: "ds"}, {Canonical: "deepseek-flash", Parent: "ds", Aliases: []string{first, second}, Routes: []service.ModelAliasRoute{{Model: first, Priority: 0}, {Model: second, Priority: 1}}}}}
				raw, err := json.Marshal(aliases)
				require.NoError(t, err)
				settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{service.SettingKeyModelAliases: string(raw)}}, cfg)
				billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billing.Stop)
				upstream := &modelAliasLeafUpstream{exhausted: exhausted}
				gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
				cache := &concurrencyCacheMock{acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }, acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }}
				h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
				router := gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowMessagesDispatch: true}})
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
				})
				router.Use(middleware.GlobalModelAliases(settings))
				router.POST("/responses", h.Responses)
				router.POST("/chat/completions", h.ChatCompletions)
				router.POST("/messages", h.Messages)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/"+endpoint, strings.NewReader(`{"model":"ds","input":"hi","messages":[{"role":"user","content":"hi"}],"max_tokens":32,"stream":false}`)))
				require.Equal(t, []string{first, second}, upstream.models)
				require.Equal(t, []int64{872, 872}, upstream.ids)
				if exhausted {
					require.Equal(t, 429, response.Code, response.Body.String())
				} else {
					require.Equal(t, 200, response.Code, response.Body.String())
					require.Contains(t, response.Body.String(), "ok")
				}
				require.True(t, repo.accounts[0].Schedulable)
				require.Nil(t, repo.accounts[0].RateLimitResetAt)
			})
		}
	}
}

func TestModelAliasTreePublicCatalogAndRetrieve(t *testing.T) {
	gin.SetMode(gin.TestMode)
	policy := &service.ModelAliasPolicy{Groups: []service.ModelAliasGroup{{Canonical: "ds"}, {Canonical: "deepseek-flash", Parent: "ds", Aliases: []string{"deepseek-v4.1-flash"}}}}
	router := gin.New()
	write := func(c *gin.Context) {
		c.Request = c.Request.WithContext(service.WithModelAliases(c.Request.Context(), policy))
		c.Set("model_alias_if_none_match", c.GetHeader("If-None-Match"))
		writeOpenAIModelsResponse(c, &service.OpenAIModelsResponse{Body: []byte(`{"data":[{"id":"deepseek-flash"},{"id":"deepseek-v4.1-flash"}]}`)})
	}
	router.GET("/models", write)
	router.GET("/models/:model", write)
	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest("GET", "/models", nil))
	require.Equal(t, 200, first.Code)
	require.Equal(t, `[{"id":"ds"}]`, gjson.Get(first.Body.String(), "data").Raw)
	for _, id := range []string{"ds", "deepseek-flash", "deepseek-v4.1-flash"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest("GET", "/models/"+id, nil))
		require.Equal(t, 200, recorder.Code)
		require.Equal(t, "ds", gjson.Get(recorder.Body.String(), "id").String())
	}
	request := httptest.NewRequest("GET", "/models", nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	cached := httptest.NewRecorder()
	router.ServeHTTP(cached, request)
	require.Equal(t, 304, cached.Code)
}
