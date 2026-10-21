//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const ds1017 = "deepseek/deepseek-v4.1-flash-1017"
const dsLegacy = "cline-pass/deepseek-v4.1-flash"

func dsTreePolicy() *ModelAliasPolicy {
	return &ModelAliasPolicy{Groups: []ModelAliasGroup{
		{Canonical: "ds"},
		{Canonical: "deepseek-flash", Parent: "ds", Aliases: []string{ds1017, dsLegacy, "deepseek-v4.1-flash"}, Routes: []ModelAliasRoute{
			{Model: ds1017, BaseURLs: []string{"https://api.cline.bot/api"}, Priority: 0},
			{Model: dsLegacy, Priority: 1},
		}},
		{Canonical: "deepseek-pro", Parent: "ds", Aliases: []string{"provider/pro"}},
	}}
}

func dsTreeAccount() *Account {
	a := &Account{ID: 872, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"base_url": "https://api.cline.bot/api", "model_mapping": map[string]any{dsLegacy: dsLegacy}},
		Extra:       map[string]any{ModelRouteProbesExtraKey: ModelRouteProbeSnapshot{BaseURL: "https://api.cline.bot/api", CheckedAt: time.Now(), Models: map[string]string{ds1017: "supported"}}}}
	a.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{Source: "upstream", SyncedAt: time.Now().Format(time.RFC3339), Models: []string{dsLegacy}})
	return a
}

func TestModelAliasTreeCodexCatalogInheritsDeepSeekReasoningLevels(t *testing.T) {
	t.Parallel()

	for _, modelID := range []string{ds1017, dsLegacy, "deepseek-flash"} {
		t.Run(modelID, func(t *testing.T) {
			// 同时列出通用根节点，验证优先叶子的能力确实替换根节点默认值。
			body, err := BuildCodexModelsManifest([]string{"ds", modelID})
			require.NoError(t, err)
			body, err = dsTreePolicy().CanonicalizeCatalog(body)
			require.NoError(t, err)
			models := decodeCodexManifestModels(t, body)
			require.Len(t, models, 1)
			require.Equal(t, "ds", models[0]["slug"])
			require.Equal(t, "high", models[0]["default_reasoning_level"])
			require.Equal(t, []string{"low", "high", "max"}, effortsFromManifestModel(t, models[0]))
			require.EqualValues(t, 262_144, models[0]["context_window"])
		})
	}
}

func TestModelAliasTreePriorityExactEvidenceAndBaseURL(t *testing.T) {
	p := dsTreePolicy()
	require.NoError(t, p.Validate())
	ctx := WithModelAliases(context.Background(), p)
	a := dsTreeAccount()
	before, _ := json.Marshal(a)
	routed := accountWithModelAliases(ctx, a)
	require.Equal(t, ds1017, routed.GetMappedModel("ds"))
	require.True(t, routed.IsModelSupported("ds"))
	require.Equal(t, ds1017, routed.GetMappedModel("deepseek-flash"))
	// 旧目录条目不能被当作新日期版的支持证据。
	delete(a.Extra, ModelRouteProbesExtraKey)
	require.Equal(t, dsLegacy, accountWithModelAliases(ctx, a).GetMappedModel("ds"))
	// 地址切换后，旧地址的成功证据与路由约束均不得泄漏。
	a = dsTreeAccount()
	a.Credentials["base_url"] = "https://other.example/api"
	require.Equal(t, dsLegacy, accountWithModelAliases(ctx, a).GetMappedModel("ds"))
	a = dsTreeAccount()
	after, _ := json.Marshal(a)
	var b, c map[string]any
	require.NoError(t, json.Unmarshal(before, &b))
	require.NoError(t, json.Unmarshal(after, &c))
	require.Equal(t, b["Credentials"], c["Credentials"])
	require.Nil(t, a.globalModelMapping)
}

func TestModelAliasTreeLeafCooldownAndRequestRetry(t *testing.T) {
	p := dsTreePolicy()
	ctx := WithModelAliases(context.Background(), p)
	a := dsTreeAccount()
	a = accountWithModelAliases(ctx, a)
	require.True(t, TryNextModelAlias(ctx, a, "ds", http.StatusTooManyRequests))
	next := accountWithModelAliases(ctx, a)
	require.Equal(t, dsLegacy, next.GetMappedModel("ds"))
	require.False(t, TryNextModelAlias(ctx, next, "ds", http.StatusTooManyRequests))
	require.False(t, accountWithModelAliases(ctx, a).IsModelSupported("ds"))
	// 下一请求保留独立尝试记录，叶子级持久冷却只阻断对应地址与精确目标。
	ctx = WithModelAliases(context.Background(), p)
	a = dsTreeAccount()
	setAccountModelRateLimitSnapshot(a, modelAliasRateLimitKey(a, dsLegacy), time.Now().Add(time.Hour), "test", time.Now())
	routed := accountWithModelAliases(ctx, a)
	require.Equal(t, ds1017, routed.GetMappedModel("ds"))
	require.Zero(t, routed.GetModelRateLimitRemainingTime("ds"))
	setAccountModelRateLimitSnapshot(a, modelAliasRateLimitKey(a, ds1017), time.Now().Add(time.Hour), "test", time.Now())
	require.False(t, accountWithModelAliases(ctx, a).IsModelSupported("ds"))
}

func TestModelAliasTree429DoesNotBlockAccount(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &RateLimitService{accountRepo: repo}
	ctx := WithModelAliases(context.Background(), dsTreePolicy())
	a := accountWithModelAliases(ctx, dsTreeAccount())
	require.True(t, svc.handleModelAlias429(ctx, a, 429, http.Header{"Retry-After": []string{"90"}}, []byte(`{"error":{"message":"rate limited"}}`), dsLegacy))
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, modelAliasRateLimitKey(a, dsLegacy), repo.modelRateLimitCalls[0].scope)
	require.WithinDuration(t, time.Now().Add(90*time.Second), repo.modelRateLimitCalls[0].resetAt, 2*time.Second)
	require.Zero(t, repo.tempCalls)
	require.Nil(t, a.RateLimitResetAt)
	require.True(t, a.Schedulable)
	require.False(t, svc.handleModelAlias429(ctx, a, 401, nil, nil, dsLegacy))
}

// 连续网关故障按账号/地址/精确模型逐步退避；成功响应必须能抬升下一次可用。
func TestModelAliasFailureLearningRelaxesAfterSuccess(t *testing.T) {
	policy := dsTreePolicy()
	ctx := WithModelAliases(context.Background(), policy)
	repo := &modelNotFoundAccountRepoStub{}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
	account := dsTreeAccount()
	account = accountWithModelAliases(ctx, account)
	failure := &UpstreamFailoverError{StatusCode: http.StatusBadGateway}
	for range 3 {
		svc.learnModelAliasFailure(ctx, dsTreeAccount(), ds1017, failure)
	}
	cooled := dsTreeAccount()
	setAccountModelRateLimitSnapshot(cooled, modelAliasRateLimitKey(cooled, ds1017), time.Now().Add(time.Minute), "model_alias_leaf_transient", time.Now())
	require.Equal(t, dsLegacy, accountWithModelAliases(ctx, cooled).GetMappedModel("ds"), "冷却期内应选择下一条同义词")
	cooled = dsTreeAccount()
	svc.ReportOpenAIAccountScheduleResult(account, ds1017, true, nil)
	require.True(t, svc.getOpenAIAccountModelTransientState().isBlocked(account.ID, modelAliasRateLimitKey(account, ds1017), time.Now()) == false, "成功后连续失败窗口应清零")
	require.Equal(t, ds1017, accountWithModelAliases(ctx, cooled).GetMappedModel("ds"), "同一成功请求直接恢复优先叶子")
}

// 学习失败：同义词内先试下一条，并把真实账号与上游 ID 写进错误链。
func TestModelAliasFailureLearningKeepsAccountAttribution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &modelNotFoundAccountRepoStub{}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
	ctx := WithModelAliases(context.Background(), dsTreePolicy())
	a := accountWithModelAliases(ctx, dsTreeAccount())
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	SetOpsAttemptModel(c, "ds")
	failure := &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: []byte(`{"error":{"message":"upstream busy"}}`)}
	require.True(t, svc.TryNextModelAlias(ctx, c, a, "ds", failure))
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, int64(872), repo.modelRateLimitCalls[0].accountID)
	require.Equal(t, modelAliasRateLimitKey(a, ds1017), repo.modelRateLimitCalls[0].scope)
	require.Equal(t, "model_alias_leaf_transient", repo.modelRateLimitCalls[0].reason)
	events := c.MustGet(OpsUpstreamErrorsKey).([]*OpsUpstreamErrorEvent)
	require.Len(t, events, 1)
	require.Equal(t, int64(872), events[0].AccountID)
	require.Equal(t, ds1017, events[0].Model)
	require.Equal(t, 502, events[0].UpstreamStatusCode)
}

func TestModelAliasTreeCatalogRootsFallbackAndValidation(t *testing.T) {
	p := dsTreePolicy()
	for _, payload := range []string{
		`{"data":[{"id":"deepseek-v4.1-flash"},{"id":"deepseek/deepseek-v4.1-flash-1017"},{"id":"deepseek-flash"},{"id":"provider/pro"},{"id":"other"}]}`,
		`{"models":[{"slug":"deepseek-flash","context_window":262144},{"slug":"deepseek/deepseek-v4.1-flash-1017"},{"slug":"other"}]}`,
	} {
		body, err := p.CanonicalizeCatalog([]byte(payload))
		require.NoError(t, err)
		field, key := "data", "id"
		if gjson.GetBytes(body, "models").Exists() {
			field, key = "models", "slug"
		}
		rows := gjson.GetBytes(body, field).Array()
		require.Len(t, rows, 2)
		require.Equal(t, "ds", rows[0].Get(key).String())
		require.Equal(t, "other", rows[1].Get(key).String())
	}
	require.Equal(t, "deepseek-flash", p.Canonicalize("deepseek-v4.1-flash"))
	require.Equal(t, []ModelFallbackCandidate{{Model: "deepseek-pro", Tier: "ds"}}, p.TreeFallbackCandidates("deepseek-flash"))
	platform, ok := DetectModelPlatformWithAliases(WithModelAliases(context.Background(), p), "ds")
	require.True(t, ok)
	require.Equal(t, PlatformDeepseek, platform)
	p.Groups[0].Parent = "deepseek-flash"
	require.Error(t, p.Validate())
	p = dsTreePolicy()
	p.Groups[1].Routes[0].BaseURLs = []string{"https://user:secret@example.com"}
	require.Error(t, p.Validate())
	p = dsTreePolicy()
	p.Groups[1].Routes[0].Model = "unknown"
	require.Error(t, p.Validate())
}

func TestModelAliasTreeSchedulerPrefers1017AcrossAccounts(t *testing.T) {
	p := dsTreePolicy()
	ctx := WithModelAliases(context.Background(), p)
	latest := accountWithModelAliases(ctx, dsTreeAccount())
	old := dsTreeAccount()
	delete(old.Extra, ModelRouteProbesExtraKey)
	old.ID = 999
	old = accountWithModelAliases(ctx, old)
	require.Less(t, p.AccountRouteRank(latest, "ds"), p.AccountRouteRank(old, "ds"))
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{}}
	plan := openAIAccountLoadPlan{candidates: []openAIAccountCandidateScore{{account: old, loadInfo: &AccountLoadInfo{}, score: 100}, {account: latest, loadInfo: &AccountLoadInfo{}, score: 0}}, topK: 1}
	ordered := scheduler.buildOpenAISelectionOrder(OpenAIAccountScheduleRequest{RequestedModel: "ds", StickyWeighted: true, StickyAccountID: old.ID}, plan)
	require.Len(t, ordered, 2)
	require.Equal(t, latest.ID, ordered[0].account.ID)
}

func TestModelAliasTreeMappedTargetCannotBypassBaseURL(t *testing.T) {
	p := dsTreePolicy()
	a := dsTreeAccount()
	a.Credentials["base_url"] = "https://other.example/api"
	a.Credentials["model_mapping"] = map[string]any{"deepseek-flash": ds1017}
	require.Equal(t, dsLegacy, accountWithModelAliases(WithModelAliases(context.Background(), p), a).GetMappedModel("ds"))
}
func TestModelAliasTreeClinePassResetAndAccountQuota(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &RateLimitService{accountRepo: repo}
	ctx := WithModelAliases(context.Background(), dsTreePolicy())
	a := accountWithModelAliases(ctx, dsTreeAccount())
	body := []byte(`{"error":{"code":"INFERENCE_CAP_ERROR","message":"Error 429: You have reached your monthly Clinepass limit. The limit resets in 13d, please try again later."}}`)
	require.False(t, svc.HandleUpstreamError(ctx, a, 429, nil, body, dsLegacy))
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.WithinDuration(t, time.Now().Add(13*24*time.Hour), repo.modelRateLimitCalls[0].resetAt, 2*time.Second)
	require.True(t, accountWithModelAliases(ctx, dsTreeAccount()).IsModelSupported("ds"))
	require.False(t, svc.handleModelAlias429(ctx, a, 429, nil, []byte(`{"error":{"code":"insufficient_quota"}}`), ds1017))
	gateway := &OpenAIGatewayService{rateLimitService: svc}
	require.False(t, gateway.handleOpenAIAccountUpstreamError(ctx, a, 429, nil, body, dsLegacy))
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(a))
}
func TestModelAliasTreeCatalogRetainsLeafCapabilities(t *testing.T) {
	p := dsTreePolicy()
	body, err := p.CanonicalizeCatalog([]byte(`{"models":[{"slug":"ds","context_window":1000000},{"slug":"deepseek/deepseek-v4.1-flash-1017","context_window":262144},{"slug":"deepseek-flash","context_window":1000000}]}`))
	require.NoError(t, err)
	require.Equal(t, int64(262144), gjson.GetBytes(body, "models.0.context_window").Int())
	require.True(t, isDeepSeekV41FlashCodexModel("deepseek-flash"))
	require.False(t, isDeepSeekV41FlashCodexModel("ds"))
}
func TestModelAliasTreeCompositeOwnershipPrefersExactFirstLeaf(t *testing.T) {
	a := dsTreeAccount()
	a.Credentials["model_mapping"] = map[string]any{ds1017: ds1017}
	repo := &compositeOwnershipAccountRepo{accounts: []Account{*a, {ID: 908, Platform: PlatformDeepseek, Credentials: map[string]any{"model_mapping": map[string]any{"deepseek-flash": "deepseek-flash"}}}}}
	ctx := WithModelAliases(context.Background(), dsTreePolicy())
	ownership, err := (&GatewayService{accountRepo: repo}).resolveCompositeModelOwnership(ctx, 7, "ds")
	require.NoError(t, err)
	require.Equal(t, PlatformOpenAI, ownership.TargetPlatform)
}

func TestModelAliasTreeAdminTest429OnlyCoolsExactLeaf(t *testing.T) {
	for _, model := range []string{ds1017, dsLegacy} {
		t.Run(model, func(t *testing.T) {
			ctx, _ := newTestContext()
			ctx.Request = ctx.Request.WithContext(WithModelAliases(ctx.Request.Context(), dsTreePolicy()))
			repo := &modelNotFoundAccountRepoStub{}
			a := dsTreeAccount()
			a.Credentials["api_key"] = "test-only"
			body := `{"error":{"code":"INFERENCE_CAP_ERROR","message":"Error 429: You have reached your monthly Clinepass limit. The limit resets in 13d, please try again later."}}`
			upstream := &queuedHTTPUpstream{responses: []*http.Response{{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}}}
			settings := NewSettingService(newMockSettingRepo(), nil)
			require.NoError(t, settings.SetModelAliasPolicy(context.Background(), dsTreePolicy()))
			testService := &AccountTestService{accountRepo: repo, httpUpstream: upstream, settingService: settings, cfg: &config.Config{}}
			err := testService.testOpenAIAccountConnection(ctx, a, model, "Reply OK.", AccountTestModeDefault)
			require.Error(t, err)
			require.Len(t, upstream.requests, 1, err.Error())
			require.Len(t, repo.modelRateLimitCalls, 1, err.Error())
			require.Equal(t, modelAliasRateLimitKey(a, model), repo.modelRateLimitCalls[0].scope)
			sent, _ := io.ReadAll(upstream.requests[0].Body)
			require.Equal(t, model, gjson.GetBytes(sent, "model").String(), fmt.Sprintf("wrong probe: %s", sent))
			require.Nil(t, a.RateLimitResetAt)
		})
	}
}
