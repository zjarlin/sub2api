package handler

import (
	"context"
	"encoding/json"
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

type autoInventoryRepo struct {
	autoModelAccountRepoStub
	reads int
}

func TestAutoModelPriorityHTTPFailuresReachGLMAndRemainingModels(t *testing.T) {
	for _, endpoint := range []string{"responses", "chat/completions"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream_%t", endpoint, stream), func(t *testing.T) {
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
					errorResponse: `{"error":{"message":"\"auto\" tool choice requires --enable-auto-tool-choice and --tool-call-parser to be set"}}`}
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

func (r *autoInventoryRepo) ListByGroup(ctx context.Context, groupID int64) ([]service.Account, error) {
	r.reads++
	return r.autoModelAccountRepoStub.ListByGroup(ctx, groupID)
}

func TestAutoModelPlanIncludesEveryConfiguredCandidateWithoutHealthHistory(t *testing.T) {
	mapping := map[string]any{}
	for i := 0; i < 700; i++ {
		model := fmt.Sprintf("vendor/unrated-%03d", i)
		mapping[model] = model
	}
	for _, model := range []string{"deepseek-v4.1-flash", "glm-5.3", "qwen3.8-max", "gpt-5.5", "gpt-6-astra", "doubao-pro", "nvidia/riva-translate-4b-instruct-v2", "vendor/no-tools"} {
		mapping[model] = model
	}
	accounts := []service.Account{autoModelTestAccounts()[0], autoModelTestAccounts()[0]}
	accounts[0].Credentials = map[string]any{"model_mapping": mapping}
	accounts[0].SetUpstreamModelMetadataSnapshot(service.UpstreamModelMetadataSnapshot{Models: map[string]service.UpstreamModelMetadata{
		"vendor/no-tools": {ID: "vendor/no-tools", CodexToolCapabilities: map[string]json.RawMessage{"supports_function_calling": json.RawMessage("false")}},
	}})
	accounts[1].ID = 9
	accounts[1].Schedulable = false
	accounts[1].Credentials = map[string]any{"model_mapping": map[string]any{"offline-model": "offline-model"}}
	repo := &autoInventoryRepo{autoModelAccountRepoStub: autoModelAccountRepoStub{gatewayModelsAccountRepoStub{
		byGroup: map[int64][]service.Account{71: accounts, 72: {{ID: 99, Credentials: map[string]any{"model_mapping": map[string]any{"private-model": "private-model"}}}}},
	}}}
	h := newGatewayModelsHandlerForTest(repo)
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, 71)
	require.NoError(t, err)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	body := []byte(`{"model":"auto","input":"use shell","tools":[{"type":"function","name":"shell"}]}`)
	routes, plan, err := h.autoModelPlan(ctx, group, nil, "/v1/responses", body, models)
	require.NoError(t, err)
	require.Equal(t, 1, repo.reads)
	require.Len(t, plan, 709)
	require.Len(t, routes, 704)
	require.Equal(t, "deepseek-v4.1-flash", routes[0].model)
	require.Equal(t, "glm-5.3", routes[1].model)
	require.Equal(t, "qwen3.8-max", routes[2].model)
	excluded := map[string]string{}
	for _, candidate := range plan {
		require.NotEqual(t, "private-model", candidate.Model)
		if !candidate.Eligible {
			excluded[candidate.Model] = candidate.Reason
		}
	}
	require.Equal(t, map[string]string{
		"gpt-6-astra": "auto_policy_excluded", "doubao-pro": "auto_policy_excluded",
		"nvidia/riva-translate-4b-instruct-v2": "not_text_generation",
		"vendor/no-tools":                      "no_compatible_account", "offline-model": "no_compatible_account",
	}, excluded)
	_, textPlan, err := h.autoModelPlan(ctx, group, nil, "/v1/responses", []byte(`{"input":"hello"}`), models)
	require.NoError(t, err)
	for _, candidate := range textPlan {
		if candidate.Model == "vendor/no-tools" {
			require.True(t, candidate.Eligible)
		}
	}
}

func TestAutoModelPlanKeepsProviderAlternativesAndExhaustsFiniteChain(t *testing.T) {
	accounts := []service.Account{autoModelTestAccounts()[0], autoModelTestAccounts()[1]}
	accounts[0].Credentials = map[string]any{"model_mapping": map[string]any{
		"deepseek-v4.1-flash": "deepseek-v4.1-flash", "glm-5.3": "glm-5.3", "unknown-instruct": "unknown-instruct",
	}}
	accounts[1].Credentials = map[string]any{"model_mapping": map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"}}
	h := newAutoModelTestHandler(accounts)
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, 71)
	require.NoError(t, err)
	group := &service.Group{ID: 71, Platform: service.PlatformComposite}
	body := []byte(`{"model":"auto","input":"hello"}`)
	routes, _, err := h.autoModelPlan(ctx, group, nil, "/v1/responses", body, models)
	require.NoError(t, err)
	require.Len(t, routes, 4)
	require.Equal(t, "deepseek-v4.1-flash", routes[0].model)
	require.Equal(t, "deepseek-v4.1-flash", routes[1].model)
	require.NotEqual(t, routes[0].targetPlatform, routes[1].targetPlatform)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(service.WithResolvedTargetPlatform(ctx, routes[0].targetPlatform))
	seedAutoModelFallback(c, routes[0].model, routes)
	openAI := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}
	key := &service.APIKey{Group: group, GroupID: &group.ID}
	for _, expected := range routes[1:] {
		attempt, ok := openAI.nextModelFallback(c, key, routes[0].model, body, false)
		require.True(t, ok)
		require.Equal(t, expected.upstreamModel, attempt.Model)
		platform, found := service.ResolvedTargetPlatformFromContext(c.Request.Context())
		require.True(t, found)
		require.Equal(t, expected.targetPlatform, platform)
	}
	_, ok := openAI.nextModelFallback(c, key, routes[0].model, body, false)
	require.False(t, ok)
	seedAutoModelFallback(c, routes[0].model, routes)
	_, ok = openAI.nextModelFallback(c, key, routes[0].model, []byte(`{"previous_response_id":"resp_state","input":"next"}`), false)
	require.False(t, ok)
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	_, ok = openAI.nextModelFallback(c, key, routes[0].model, body, false)
	require.False(t, ok)
}

func TestAutoModelInventoryUsesSnapshotAndKeepsAutoListedWithoutSuccessfulModels(t *testing.T) {
	accounts := autoModelTestAccounts()[:1]
	accounts[0].Credentials = map[string]any{}
	accounts[0].Type = service.AccountTypeAPIKey
	accounts[0].SetUpstreamSupportedModelsSnapshot(service.UpstreamSupportedModelsSnapshot{
		Source: "test", SyncedAt: time.Now().UTC().Format(time.RFC3339),
		Models: []string{"deepseek-v4.1-flash", "glm-5.3", "vendor/new-model"},
	})
	h := newAutoModelTestHandler(accounts)
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, 71)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"deepseek-v4.1-flash", "glm-5.3", "vendor/new-model"}, models)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	require.True(t, h.autoModelAvailable(ctx, group, nil))
	routes, _, err := h.autoModelPlan(ctx, group, nil, "/v1/responses", []byte(`{"input":"hello"}`), models)
	require.NoError(t, err)
	require.Len(t, routes, 3)
	require.Equal(t, "deepseek-v4.1-flash", routes[0].model)
}
