package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type autoInventoryRepo struct {
	autoModelAccountRepoStub
	reads          int
	requestedGroup *int64
	includeGrouped bool
	platforms      []string
}

type autoPlanCompositeRouteRepo struct {
	service.CompositeModelRouteRepository
	routes []service.CompositeModelRoute
}

func (r autoPlanCompositeRouteRepo) ListByGroup(context.Context, int64, bool) ([]service.CompositeModelRoute, error) {
	return r.routes, nil
}

func TestAskRoutesTextAdaptersInCodexGroup(t *testing.T) {
	accounts := []service.Account{
		{ID: 862, Platform: service.PlatformDeepseekWeb, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{"deepseek-web-chat": "deepseek-web-chat"}}},
		{ID: 863, Platform: service.PlatformDoubao, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{"doubao-pro": "doubao-pro"}}},
		{ID: 864, Platform: service.PlatformCursor, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{"cursor-model": "cursor-model"}}},
	}
	h := newAutoModelTestHandler(accounts)
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, 71)
	require.NoError(t, err)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	for _, virtual := range []string{"auto", "ask"} {
		body := []byte(`{"model":"` + virtual + `","input":"hello"}`)
		requestCtx := service.WithAutoModelRequestCapabilities(ctx, body)
		routes, _, err := h.autoModelPlan(requestCtx, group, nil, "/v1/responses", body, models)
		require.NoError(t, err)
		if virtual == "ask" {
			require.Len(t, routes, 3)
			require.True(t, h.askModelAvailable(requestCtx, group, models))
		} else {
			require.Empty(t, routes)
			require.False(t, h.autoModelAvailable(requestCtx, group, models))
		}
	}
}

func TestAutoModelPlanSelectsVerifiedClineBeforeGLM(t *testing.T) {
	accounts := autoModelTestAccounts()
	accounts[0].Credentials = map[string]any{"model_mapping": map[string]any{"deepseek-v4.1-flash": "cline-pass/deepseek-v4.1-flash", "cline-pass/deepseek-v4.1-flash": "cline-pass/deepseek-v4.1-flash"}}
	accounts[0].Extra = map[string]any{service.VerifiedModelsExtraKey: map[string]any{"cline-pass/deepseek-v4.1-flash": time.Now().Format(time.RFC3339Nano)}}
	accounts[0].SetUpstreamSupportedModelsSnapshot(service.UpstreamSupportedModelsSnapshot{Source: "upstream", SyncedAt: time.Now().Format(time.RFC3339), Models: []string{"deepseek/deepseek-v4.1-flash"}})
	accounts[1].Credentials = map[string]any{"model_mapping": map[string]any{"glm-5.3": "glm-5.3"}}
	h := newAutoModelTestHandler(accounts)
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, 71)
	require.NoError(t, err)
	routes, _, err := h.autoModelPlan(ctx, &service.Group{ID: 71, Platform: service.PlatformOpenAI}, nil, "/v1/responses", []byte(`{"model":"auto","input":"OK"}`), models)
	require.NoError(t, err)
	require.NotEmpty(t, routes)
	require.Equal(t, "deepseek-v4.1-flash", routes[0].model)
}

func (r *autoInventoryRepo) ListModelAvailabilityCandidates(ctx context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]service.Account, error) {
	r.reads++
	r.requestedGroup, r.platforms, r.includeGrouped = groupID, platforms, includeGrouped
	return r.autoModelAccountRepoStub.ListModelAvailabilityCandidates(ctx, groupID, platforms, includeGrouped)
}

// 高级任务（UI 设计/前端/架构推理/多模态）优先本站真实可用的旗舰 gpt-6-astra / gpt-6.1-sol；
// claude 系列在本站基本不可用，不再参与领域加成，通用档位顺序把它排到后面。
func TestAutoModelPlanPrefersFlagshipForAdvancedDomains(t *testing.T) {
	accounts := []service.Account{{
		ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"gpt-6-astra":         "gpt-6-astra",
			"deepseek-v4.1-flash": "deepseek-v4.1-flash",
			"claude-opus-4-7":     "claude-opus-4-7",
			"kimi-k3":             "kimi-k3",
		}},
	}}
	h := newAutoModelTestHandler(accounts)
	// gpt-6-astra 默认属于最高档并被排除，这里把它移出最高档以验证领域优先级。
	h.settingService = service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{
		service.SettingKeyModelFallbackPolicy: `{"enabled":true,"tiers":[` +
			`{"name":"旗舰","models":["gpt-6.1-sol"]},` +
			`{"name":"主力编码","models":["gpt-6-astra","deepseek-v4.1-flash","claude-opus-4-7","kimi-k3"]}]}`,
	}}, nil)
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, 71)
	require.NoError(t, err)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}

	for _, body := range []string{
		`{"model":"auto","input":"帮我做 UI 设计，降低卡片感的 AI 味"}`,
		`{"model":"auto","input":"调整这个 Vue 组件的样式"}`,
		`{"model":"auto","input":"重构这个分布式事务的数据库迁移"}`,
	} {
		routes, _, err := h.autoModelPlan(ctx, group, nil, "/v1/responses", []byte(body), models)
		require.NoError(t, err)
		require.NotEmpty(t, routes)
		require.Equal(t, "gpt-6-astra", routes[0].model, body)
	}

	// 通用任务仍保留 deepseek-v4.1-flash 首选。
	routes, _, err := h.autoModelPlan(ctx, group, nil, "/v1/responses", []byte(`{"model":"auto","input":"修复这个空指针"}`), models)
	require.NoError(t, err)
	require.NotEmpty(t, routes)
	require.Equal(t, "deepseek-v4.1-flash", routes[0].model)
}

// 没有旗舰候选时，claude 不得因领域加成反超首选 deepseek-v4.1-flash。
func TestAutoModelPlanDemotesClaudeWhenNoFlagshipAvailable(t *testing.T) {
	accounts := []service.Account{{
		ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"deepseek-v4.1-flash": "deepseek-v4.1-flash",
			"claude-opus-4-7":     "claude-opus-4-7",
			"kimi-k3":             "kimi-k3",
		}},
	}}
	h := newAutoModelTestHandler(accounts)
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, 71)
	require.NoError(t, err)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}

	routes, _, err := h.autoModelPlan(ctx, group, nil, "/v1/responses", []byte(`{"model":"auto","input":"帮我做 UI 设计，降低卡片感的 AI 味"}`), models)
	require.NoError(t, err)
	require.NotEmpty(t, routes)
	require.Equal(t, "deepseek-v4.1-flash", routes[0].model)
	require.NotEqual(t, "claude-opus-4-7", routes[0].model)
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

func TestSeedAutoModelFallbackPrefersMostSimilarModelID(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	routes := []autoModelRouteCandidate{
		{model: "glm-5.3", targetPlatform: service.PlatformOpenAI, upstreamModel: "glm-5.3"},
		{model: "gpt-5.5", targetPlatform: service.PlatformOpenAI, upstreamModel: "gpt-5.5"},
		{model: "glm-4.7", targetPlatform: service.PlatformOpenAI, upstreamModel: "glm-4.7"},
		{model: "glm-5.2", targetPlatform: service.PlatformOpenAI, upstreamModel: "glm-5.2"},
		{model: "glm-5.3", targetPlatform: service.PlatformZhipu, upstreamModel: "glm-5.3"},
		{model: "vendor/glm-5.3", targetPlatform: service.PlatformOpenAI, upstreamModel: "vendor/glm-5.3"},
	}

	seedAutoModelFallback(c, "glm-5.3", routes)
	state, found := c.Get(modelFallbackStateKey)
	require.True(t, found)
	fallback, ok := state.(*modelFallbackState)
	require.True(t, ok)
	require.Equal(t, []string{
		"glm-5.3", // 同一公开模型的备用平台先于任何跨模型降级。
		"vendor/glm-5.3",
		"glm-5.2",
		"glm-4.7",
		"gpt-5.5",
	}, []string{
		fallback.candidates[0].Model,
		fallback.candidates[1].Model,
		fallback.candidates[2].Model,
		fallback.candidates[3].Model,
		fallback.candidates[4].Model,
	})
	require.Equal(t, []string{
		service.PlatformZhipu,
		service.PlatformOpenAI,
		service.PlatformOpenAI,
		service.PlatformOpenAI,
		service.PlatformOpenAI,
	}, []string{
		fallback.targets[0].platform,
		fallback.targets[1].platform,
		fallback.targets[2].platform,
		fallback.targets[3].platform,
		fallback.targets[4].platform,
	})
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

func TestAutoModelPlanUsesCatalogScopeAndExplainsDiscoveryOnlyModels(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformComposite} {
		t.Run(platform, func(t *testing.T) {
			accounts := make([]service.Account, 5)
			for i, model := range []string{"vendor/healthy-model", "vendor/error-model", "private-model", "disabled-model", ""} {
				accounts[i] = service.Account{
					ID: int64(i + 1), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true,
					Credentials: map[string]any{"model_mapping": map[string]any{model: model}},
				}
			}
			accounts[1].Status = service.StatusError
			owner := int64(99)
			accounts[2].OwnerUserID = &owner
			accounts[3].Status = service.StatusDisabled
			accounts[4].Status = service.StatusError
			accounts[4].Credentials = map[string]any{}
			otherGroupAccount := accounts[0]
			otherGroupAccount.Credentials = map[string]any{"model_mapping": map[string]any{"other-group-model": "other-group-model"}}
			repo := &autoInventoryRepo{autoModelAccountRepoStub: autoModelAccountRepoStub{gatewayModelsAccountRepoStub{
				byGroup: map[int64][]service.Account{71: accounts, 72: {otherGroupAccount}},
			}}}
			h := newGatewayModelsHandlerForTest(repo)
			ctx, err := h.settingService.BindAutoModelRoutingPolicy(context.Background())
			require.NoError(t, err)
			ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, 71)
			require.NoError(t, err)
			require.ElementsMatch(t, []string{"vendor/healthy-model", "vendor/error-model"}, models)
			require.NotNil(t, repo.requestedGroup)
			require.Equal(t, int64(71), *repo.requestedGroup)
			require.False(t, repo.includeGrouped)
			require.Contains(t, repo.platforms, service.PlatformOpenAI)
			require.Contains(t, repo.platforms, service.PlatformDeepseek)

			group := &service.Group{ID: 71, Platform: platform}
			routes, plan, err := h.autoModelPlan(ctx, group, nil, "/v1/responses", []byte(`{"input":"hello"}`), models)
			require.NoError(t, err)
			require.Len(t, routes, 1)
			require.Equal(t, "vendor/healthy-model", routes[0].model)
			byModel := make(map[string]service.AutoModelCandidate)
			for _, candidate := range plan {
				byModel[candidate.Model] = candidate
			}
			require.Equal(t, "no_compatible_account", byModel["vendor/error-model"].Reason)
			require.NotContains(t, byModel, "private-model")
			require.NotContains(t, byModel, "disabled-model")
			require.NotContains(t, byModel, "other-group-model")
			publicModels := h.gatewayService.GetAvailableModels(ctx, &group.ID, service.PlatformOpenAI)
			require.Greater(t, len(publicModels), len(models), "unmapped accounts still expose the existing default discovery catalog")
			for _, model := range publicModels {
				entry, exists := byModel[model]
				require.True(t, exists, model)
				if model != "vendor/healthy-model" && model != "vendor/error-model" {
					require.False(t, entry.Eligible, model)
					require.Equal(t, "source_missing", entry.Reason, model)
					require.Empty(t, entry.Platform, model)
				}
			}
			require.Equal(t, 1, repo.reads, "catalog projection and Auto plan must reuse the same account snapshot")
			otherGroupID := int64(72)
			require.Equal(t, []string{"other-group-model"}, h.gatewayService.GetAvailableModels(ctx, &otherGroupID, service.PlatformOpenAI))
			require.Equal(t, 2, repo.reads, "a snapshot cannot leak into a different group")
		})
	}
}

// 决策适配器保存的共享目录不构成文本模型来源，离线文本账号仍应显示排除原因。
func TestAutoModelPlanExcludesDecisionPlatformCatalogPollution(t *testing.T) {
	for _, groupPlatform := range []string{service.PlatformOpenAI, service.PlatformComposite} {
		t.Run(groupPlatform, func(t *testing.T) {
			textModel := "deepseek-v4.1-flash"
			alias := "cn:deepseek-v4.1-flash"
			accounts := []service.Account{
				{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
					Credentials: map[string]any{"model_mapping": map[string]any{textModel: textModel}}},
				{ID: 2, Platform: service.PlatformWorkbuddy, Type: service.AccountTypeAPIKey, Status: service.StatusError, Schedulable: false,
					Credentials: map[string]any{"model_mapping": map[string]any{alias: textModel}}},
				{ID: 3, Platform: service.PlatformJev, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
					Credentials: map[string]any{"api_protocol": service.APIProtocolSystemOne, "model_mapping": map[string]any{service.DefaultJevModel: service.DefaultJevModel}}},
				{ID: 4, Platform: service.PlatformLaya, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
					Credentials: map[string]any{"api_protocol": service.APIProtocolSystemOne, "model_mapping": map[string]any{service.DefaultLayaModel: service.DefaultLayaModel}}},
				{ID: 5, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
					Credentials: map[string]any{"model_mapping": map[string]any{"claude-unroutable": "claude-unroutable"}}},
			}
			for i := 2; i < 4; i++ {
				accounts[i].SetUpstreamSupportedModelsSnapshot(service.UpstreamSupportedModelsSnapshot{
					Source: "upstream", SyncedAt: time.Now().UTC().Format(time.RFC3339),
					Models: []string{textModel, alias, "decision-only-chat"},
				})
			}
			h := newAutoModelTestHandler(accounts)
			ctx := service.WithModelAliases(context.Background(), &service.ModelAliasPolicy{Groups: []service.ModelAliasGroup{{Canonical: textModel, Aliases: []string{alias}}}})
			ctx, err := h.settingService.BindAutoModelRoutingPolicy(ctx)
			require.NoError(t, err)
			ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, 71)
			require.NoError(t, err)
			require.NotContains(t, models, "decision-only-chat")
			require.ElementsMatch(t, []string{service.PlatformOpenAI, service.PlatformWorkbuddy}, service.AutoModelInventoryPlatforms(ctx, textModel))

			group := &service.Group{ID: 71, Platform: groupPlatform}
			routes, plan, err := h.autoModelPlan(ctx, group, nil, "/v1/responses", []byte(`{"input":"hello"}`), models)
			require.NoError(t, err)
			require.Equal(t, []autoModelRouteCandidate{{model: textModel, targetPlatform: service.PlatformOpenAI, upstreamModel: textModel}}, routes)
			var offline, unsupported bool
			decisions := make(map[string]string)
			for _, entry := range plan {
				require.NotEqual(t, "decision-only-chat", entry.Model)
				if entry.Model == textModel {
					require.False(t, service.IsSystemOneDecisionPlatform(entry.Platform), entry)
				}
				if entry.Model == textModel && entry.Platform == service.PlatformWorkbuddy {
					offline = true
					require.False(t, entry.Eligible)
					require.Equal(t, "no_compatible_account", entry.Reason)
				}
				if entry.Model == "claude-unroutable" {
					unsupported = true
					require.False(t, entry.Eligible)
					require.Equal(t, "protocol_not_supported", entry.Reason)
				}
				if service.IsSystemOneDecisionPlatform(entry.Platform) {
					require.False(t, entry.Eligible)
					decisions[entry.Model] = entry.Reason
				}
			}
			require.True(t, offline)
			require.True(t, unsupported)
			require.Equal(t, map[string]string{service.DefaultJevModel: "not_text_generation", service.DefaultLayaModel: "not_text_generation"}, decisions)
			for _, platform := range []string{service.PlatformJev, service.PlatformLaya} {
				ordinary := h.gatewayService.GetAvailableModels(context.Background(), &group.ID, platform)
				if platform == service.PlatformJev {
					require.Equal(t, []string{service.DefaultJevModel}, ordinary)
				} else {
					require.Equal(t, []string{service.DefaultLayaModel}, ordinary)
				}
			}
		})
	}
}

func TestAutoModelPlanFiltersDecisionOnlyDiscoveryAndExplicitRoutes(t *testing.T) {
	accounts := autoModelTestAccounts()[:1]
	accounts = append(accounts, service.Account{
		ID: 3, Platform: service.PlatformJev, Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_protocol": service.APIProtocolSystemOne},
	})
	accounts[1].SetUpstreamSupportedModelsSnapshot(service.UpstreamSupportedModelsSnapshot{
		Source: "upstream", SyncedAt: time.Now().UTC().Format(time.RFC3339),
		Models: []string{service.DefaultJevModel, "decision-only-chat", "valid-public-alias"},
	})
	h := newAutoModelTestHandler(accounts)
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, 71)
	require.NoError(t, err)
	group := &service.Group{ID: 71, Platform: service.PlatformComposite}
	resolver := service.NewCompositeRouteResolver(autoPlanCompositeRouteRepo{routes: []service.CompositeModelRoute{
		{PublicModel: "misleading-text-alias", UpstreamModel: service.DefaultJevModel, TargetPlatform: service.PlatformJev,
			MatchType: service.CompositeRouteMatchExact, Endpoint: service.CompositeRouteEndpointAny, Enabled: true},
		{PublicModel: "valid-public-alias", UpstreamModel: "gpt-5.5", TargetPlatform: service.PlatformOpenAI,
			MatchType: service.CompositeRouteMatchExact, Endpoint: service.CompositeRouteEndpointAny, Enabled: true},
	}})
	routes, plan, err := h.autoModelPlan(ctx, group, resolver, "/v1/responses", []byte(`{"input":"hello"}`), models)
	require.NoError(t, err)
	require.Len(t, routes, 2)
	entries := make(map[string]service.AutoModelCandidate)
	for _, entry := range plan {
		entries[entry.Model] = entry
	}
	require.NotContains(t, entries, "decision-only-chat", "the shared discovery catalog must not reintroduce a source_missing candidate")
	require.NotContains(t, entries, "misleading-text-alias", "an explicit route cannot turn a decision platform into a text source")
	require.True(t, entries["valid-public-alias"].Eligible, "a valid explicit text source must take precedence over an unrelated polluted snapshot")
	require.Equal(t, "not_text_generation", entries[service.DefaultJevModel].Reason)
	ordinary := h.gatewayService.GetAvailableModels(ctx, &group.ID, service.PlatformJev)
	require.Contains(t, ordinary, service.DefaultJevModel, "the original decision catalog remains available in the same context")
	require.Contains(t, ordinary, "decision-only-chat", "the Auto projection must not mutate the saved account snapshot")
}
