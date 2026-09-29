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

func (r *autoInventoryRepo) ListModelAvailabilityCandidates(ctx context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]service.Account, error) {
	r.reads++
	r.requestedGroup, r.platforms, r.includeGrouped = groupID, platforms, includeGrouped
	return r.autoModelAccountRepoStub.ListModelAvailabilityCandidates(ctx, groupID, platforms, includeGrouped)
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
