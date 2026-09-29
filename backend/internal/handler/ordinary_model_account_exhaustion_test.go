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

type ordinaryModelFailureUpstream struct {
	service.HTTPUpstream
	ids           []int64
	models        []string
	healthyID     int64
	streamFailure bool
}

func (u *ordinaryModelFailureUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	u.ids = append(u.ids, id)
	u.models = append(u.models, gjson.GetBytes(body, "model").String())
	if id == u.healthyID {
		req.Body = io.NopCloser(strings.NewReader(string(body)))
		return (&fallbackTestUpstream{}).Do(req, "", id, 1)
	}
	status := http.StatusServiceUnavailable
	contentType := "application/json"
	response := `{"error":{"type":"upstream_error","message":"provider unavailable"}}`
	if u.streamFailure {
		status = http.StatusOK
		contentType = "text/event-stream"
		response = "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"provider unavailable\"}}}\n\n"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

func TestOrdinaryFailureExhaustsCanonicalModelAccountsBeforeFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		endpoint      string
		stream        bool
		streamFailure bool
		healthyID     int64
		incompatible  bool
		mixedProvider string
		advanced      bool
	}{
		{endpoint: "responses", healthyID: 3},
		{endpoint: "responses", healthyID: 4},
		{endpoint: "responses", healthyID: 3, incompatible: true},
		{endpoint: "responses", healthyID: 3, mixedProvider: service.PlatformDeepseek},
		{endpoint: "responses", stream: true, healthyID: 3},
		{endpoint: "responses", stream: true, streamFailure: true, healthyID: 4},
		{endpoint: "chat/completions", healthyID: 3},
		{endpoint: "chat/completions", stream: true, healthyID: 4},
		{endpoint: "messages", healthyID: 3},
		{endpoint: "messages", healthyID: 4},
		{endpoint: "responses", healthyID: 4, advanced: true},
		{endpoint: "responses", healthyID: 4, stream: true, streamFailure: true, advanced: true},
		{endpoint: "chat/completions", healthyID: 4, advanced: true},
		{endpoint: "messages", healthyID: 4, advanced: true},
	} {
		t.Run(fmt.Sprintf("%s/stream_%t/sse_failure_%t/healthy_%d/incompatible_%t/provider_%s/advanced_%t", tc.endpoint, tc.stream, tc.streamFailure, tc.healthyID, tc.incompatible, tc.mixedProvider, tc.advanced), func(t *testing.T) {
			const canonical = "deepseek-v4.1-flash"
			const alias = "deepseek/deepseek-v4.1-flash"
			models := []string{canonical, alias, canonical, "gpt-5.5"}
			repo := &grokCredentialHandlerRepo{}
			for i, model := range models {
				account := service.Account{
					ID: int64(i + 1), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: i + 1,
					Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{model: model}},
					Extra:       map[string]any{"openai_passthrough": true},
				}
				if i == 1 && tc.incompatible {
					account.SetUpstreamModelMetadataSnapshot(service.UpstreamModelMetadataSnapshot{Models: map[string]service.UpstreamModelMetadata{
						alias: {ID: alias, MaxContextWindow: 20},
					}})
				}
				if i == 1 && tc.mixedProvider != "" {
					account.Platform = tc.mixedProvider
					account.Extra["mixed_scheduling"] = true
				}
				repo.accounts = append(repo.accounts, account)
			}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Gateway.MaxAccountSwitches = 0
			aliases, err := json.Marshal(service.ModelAliasPolicy{Groups: []service.ModelAliasGroup{{Canonical: canonical, Aliases: []string{alias}}}})
			require.NoError(t, err)
			settingsRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
				service.SettingKeyModelAliases:        string(aliases),
				service.SettingKeyModelFallbackPolicy: `{"enabled":true,"tiers":[{"name":"top","models":["deepseek-v4.1-flash"]},{"name":"base","models":["gpt-5.5"]}]}`,
			}}
			settings := service.NewSettingService(settingsRepo, cfg)
			var rateLimits *service.RateLimitService
			if tc.advanced {
				configureOrdinaryModelTestScheduler(t, settings)
				rateLimits = service.NewRateLimitService(repo, nil, cfg, nil, nil)
				rateLimits.SetSettingService(settings)
			}
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			upstream := &ordinaryModelFailureUpstream{healthyID: tc.healthyID, streamFailure: tc.streamFailure}
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), rateLimits, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
			cache := &concurrencyCacheMock{
				acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
				acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
			}
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
			groupID := int64(77)
			key := &service.APIKey{ID: 2, GroupID: &groupID, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowMessagesDispatch: true}}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
			})
			router.Use(middleware.GlobalModelAliases(settings))
			router.POST("/v1/responses", h.Responses)
			router.POST("/v1/chat/completions", h.ChatCompletions)
			router.POST("/v1/messages", h.Messages)
			serve := func(endpoint string, stream bool) *httptest.ResponseRecorder {
				body := fmt.Sprintf(`{"model":"%s","input":"hi","messages":[{"role":"user","content":"hi"}],"max_tokens":32,"stream":%t}`, canonical, stream)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(body)))
				return response
			}
			response := serve(tc.endpoint, tc.stream)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			if tc.endpoint == "chat/completions" && tc.stream {
				require.Contains(t, response.Body.String(), "[DONE]")
			} else {
				require.Contains(t, response.Body.String(), "ok")
			}
			count := int(tc.healthyID)
			expectedIDs := []int64{1, 2, 3, 4}[:count]
			expectedModels := models[:count]
			if tc.incompatible {
				expectedIDs = []int64{1, 3}
				expectedModels = []string{canonical, canonical}
			}
			require.Equal(t, expectedIDs, upstream.ids)
			require.Equal(t, expectedModels, upstream.models)
			if tc.healthyID == 3 {
				require.Empty(t, response.Header().Get("X-Sub2api-Fallback-Model"))
			} else {
				require.Equal(t, "gpt-5.5", response.Header().Get("X-Sub2api-Fallback-Model"))
			}
			if !tc.advanced {
				return
			}
			protocol := map[string]string{
				"responses": service.APIProtocolResponses, "chat/completions": service.APIProtocolChatCompletions, "messages": service.APIProtocolAnthropic,
			}[tc.endpoint]
			recoveryPermit, allowed := gateway.AcquireOpenAIModelPool(context.Background(), &groupID, protocol, canonical)
			require.True(t, allowed, "首次完整池失败仍允许下一轮")
			t.Cleanup(func() { recoveryPermit.Release(context.Background()) })
			upstream.ids, upstream.models = nil, nil
			response = serve(tc.endpoint, tc.stream)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, []int64{1, 2, 3, 4}, upstream.ids, "第二轮实际穷尽原模型池后降级")
			_, allowed = gateway.AcquireOpenAIModelPool(context.Background(), &groupID, protocol, canonical)
			require.False(t, allowed, "两轮真实失败触发池级熔断")
			upstream.ids, upstream.models = nil, nil
			response = serve(tc.endpoint, false)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, []int64{4}, upstream.ids, "冷却期间直接切换档位")
			otherEndpoint := "responses"
			if tc.endpoint == "responses" {
				otherEndpoint = "chat/completions"
			}
			otherProtocol := service.APIProtocolResponses
			if otherEndpoint == "chat/completions" {
				otherProtocol = service.APIProtocolChatCompletions
			}
			otherPermit, allowed := gateway.AcquireOpenAIModelPool(context.Background(), &groupID, otherProtocol, canonical)
			require.True(t, allowed, "其他协议池不受冷却影响")
			otherPermit.Release(context.Background())
			upstream.ids, upstream.models = nil, nil
			response = serve(otherEndpoint, false)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			fallbackPolicy := settingsRepo.values[service.SettingKeyModelFallbackPolicy]
			settingsRepo.values[service.SettingKeyModelFallbackPolicy] = `{"enabled":false,"tiers":[]}`
			upstream.ids, upstream.models = nil, nil
			response = serve(tc.endpoint, false)
			require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			require.Empty(t, upstream.ids)
			if tc.endpoint == "messages" {
				require.Equal(t, "error", gjson.Get(response.Body.String(), "type").String())
			}
			settingsRepo.values[service.SettingKeyModelFallbackPolicy] = fallbackPolicy
			recoveryPermit.Success(context.Background())
			recoveredPermit, allowed := gateway.AcquireOpenAIModelPool(context.Background(), &groupID, protocol, canonical)
			require.True(t, allowed, "成功恢复原模型池许可，账号自己的冷却独立处理")
			recoveredPermit.Release(context.Background())
		})
	}
}

func configureOrdinaryModelTestScheduler(t *testing.T, settings *service.SettingService) {
	t.Helper()
	baseline, err := settings.GetAllSettings(context.Background())
	require.NoError(t, err)
	updated := *baseline
	updated.OpenAIAdvancedSchedulerEnabled = true
	updated.OpenAIAdvancedSchedulerStickyWeightedEnabled = false
	updated.OpenAIAdvancedSchedulerSubscriptionPriorityEnabled = false
	require.NoError(t, settings.UpdateSettings(context.Background(), &updated))
	t.Cleanup(func() { require.NoError(t, settings.UpdateSettings(context.Background(), baseline)) })
}

func TestModelPoolCircuitUsesChannelMappedSelectionModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "chat/completions", "messages"} {
		t.Run(endpoint, func(t *testing.T) {
			const requested = "public-model"
			const mapped = "gpt-6-astra"
			const healthy = "gpt-5.5"
			groupID := int64(78)
			repo := &grokCredentialHandlerRepo{}
			for i, model := range []string{mapped, mapped, healthy} {
				repo.accounts = append(repo.accounts, service.Account{
					ID: int64(i + 1), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: i + 1,
					Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{model: model}},
					Extra:       map[string]any{"openai_passthrough": true},
				})
			}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{
				service.SettingKeyModelFallbackPolicy: `{"enabled":false,"tiers":[]}`,
			}}, cfg)
			configureOrdinaryModelTestScheduler(t, settings)
			rateLimits := service.NewRateLimitService(repo, nil, cfg, nil, nil)
			rateLimits.SetSettingService(settings)
			channels := service.NewChannelService(&openAIWSUsageHandlerChannelRepoStub{
				channels: []service.Channel{{
					ID: 1, Status: service.StatusActive, GroupIDs: []int64{groupID},
					ModelMapping: map[string]map[string]string{service.PlatformOpenAI: {requested: mapped}},
				}},
				groupPlatforms: map[int64]string{groupID: service.PlatformOpenAI},
			}, nil, nil, nil, nil)
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			upstream := &ordinaryModelFailureUpstream{healthyID: 3}
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), rateLimits, billing, upstream, &service.DeferredService{}, nil, nil, nil, channels, nil, settings, nil)
			cache := &concurrencyCacheMock{
				acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
				acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
			}
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
			key := &service.APIKey{ID: 2, GroupID: &groupID, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowMessagesDispatch: true}}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
			})
			router.POST("/v1/responses", h.Responses)
			router.POST("/v1/chat/completions", h.ChatCompletions)
			router.POST("/v1/messages", h.Messages)
			serve := func(model string) *httptest.ResponseRecorder {
				upstream.ids, upstream.models = nil, nil
				body := fmt.Sprintf(`{"model":%q,"input":"hi","messages":[{"role":"user","content":"hi"}],"max_tokens":32}`, model)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(body)))
				return response
			}
			for range 2 {
				response := serve(requested)
				require.Equal(t, http.StatusBadGateway, response.Code, response.Body.String())
				require.Equal(t, []int64{1, 2}, upstream.ids)
				require.Equal(t, []string{mapped, mapped}, upstream.models)
			}
			protocol := map[string]string{"responses": service.APIProtocolResponses, "chat/completions": service.APIProtocolChatCompletions, "messages": service.APIProtocolAnthropic}[endpoint]
			_, allowed := gateway.AcquireOpenAIModelPool(context.Background(), &groupID, protocol, mapped)
			require.False(t, allowed, "渠道映射后的实际选号模型进入冷却")
			permit, allowed := gateway.AcquireOpenAIModelPool(context.Background(), &groupID, protocol, requested)
			require.True(t, allowed, "公开请求名不污染独立池")
			permit.Release(context.Background())
			response := serve(mapped)
			require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			require.Empty(t, upstream.ids, "直接请求相同选号模型也命中冷却")
			_, err := channels.Update(context.Background(), 1, &service.UpdateChannelInput{
				ModelMapping: map[string]map[string]string{service.PlatformOpenAI: {requested: healthy}},
			})
			require.NoError(t, err)
			response = serve(requested)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, []int64{3}, upstream.ids, "修改映射后不继承旧池冷却")
			require.Equal(t, []string{healthy}, upstream.models)
			require.Empty(t, response.Header().Get("X-Sub2api-Fallback-Model"))
		})
	}
}

func TestOrdinaryFailureSwitchBudgetBoundaries(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformDeepseek, service.PlatformMiniMax} {
		account := &service.Account{Platform: platform}
		budget := openAIAccountSwitchBudget{limit: 0, replayable: true}
		require.False(t, budget.exhausted(account, &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}), platform)
		require.True(t, budget.requireCompatible, platform)
	}
	for _, failure := range []*service.UpstreamFailoverError{
		{StatusCode: http.StatusBadRequest},
		{StatusCode: http.StatusTooManyRequests, RequestScopedTransient: true},
		{StatusCode: http.StatusServiceUnavailable, Scope: service.GatewayFailureScopeRequest},
		{StatusCode: http.StatusServiceUnavailable, Stage: service.GatewayFailureStageAccountAuth},
		{StatusCode: http.StatusServiceUnavailable, NextAccountAction: service.NextAccountStop},
	} {
		budget := openAIAccountSwitchBudget{limit: 0, replayable: true}
		require.True(t, budget.exhausted(&service.Account{Platform: service.PlatformOpenAI}, failure))
		require.False(t, budget.requireCompatible)
	}
	for _, status := range []int{0, http.StatusRequestTimeout, http.StatusBadGateway} {
		require.True(t, ordinaryOpenAIAccountFailure(&service.Account{Platform: service.PlatformOpenAI}, &service.UpstreamFailoverError{StatusCode: status}))
	}
}

func TestModelFallbackReplayableRequestBoundaries(t *testing.T) {
	for _, tc := range []struct {
		body       string
		replayable bool
	}{
		{body: `{"model":"gpt-6-astra","input":"hi"}`, replayable: true},
		{body: `{"model":"gpt-6-astra","previous_response_id":"resp_old"}`},
		{body: `{"model":"gpt-6-astra","conversation":{"id":"conv_old"}}`},
		{body: `{"model":"gpt-6-astra","input":[{"type":"input_image","file_id":"file_old"}]}`},
		{body: `{"model":"gpt-6-astra","messages":[{"role":"assistant","content":[{"type":"thinking","signature":"ciphertext"}]}]}`},
		{body: `{"model":"gpt-6-astra","messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"ciphertext"}]}]}`},
		{body: `{"model":"gpt-6-astra","tools":[{"type":"web_search"}]}`, replayable: true},
		{body: `{"model":"gpt-6-astra","tools":[{"type":"web_search"}],"input":[{"type":"web_search_call","id":"search_old","status":"completed"}]}`},
		{body: `{"model":"gpt-6-astra","input":[{"type":"input_file","file_id":"file_old"}]}`},
		{body: `{"model":"gpt-6-astra","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"abc","format":"wav"}}]}]}`},
		{body: `{"model":"gpt-6-astra","messages":[{"role":"assistant","audio":{"id":"audio_old"}}]}`},
		{body: `{"model":"gpt-6-astra","input":[{"type":"video","video_url":"https://example.com/a.mp4"}]}`},
		{body: `{"model":"gpt-6-astra","input":[{"type":"video_url","video_url":{"url":"https://example.com/a.mp4"}}]}`},
		{body: `{"model":"gpt-6-astra","input":[{"type":"input_video","video_url":"https://example.com/a.mp4"}]}`},
		{body: `{"model":"gpt-6-astra","input":[{"type":"item_reference","id":"item_old"}]}`},
		{body: `{"model":"gpt-6-astra","input":[{"type":"reasoning","encrypted_content":"opaque"}]}`},
		{body: `{"model":"gpt-6-astra","tools":[{"type":"function","name":"lookup"}]}`, replayable: true},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		key := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}
		require.Equal(t, tc.replayable, modelFallbackReplayableRequest(c, key, "gpt-6-astra", []byte(tc.body)), tc.body)
	}
}
