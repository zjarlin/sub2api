package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT55RetirementModelSupport(t *testing.T) {
	for _, typ := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
		for _, passthrough := range []bool{false, true} {
			t.Run(typ+"/passthrough="+map[bool]string{true: "true", false: "false"}[passthrough], func(t *testing.T) {
				account := &Account{Platform: PlatformOpenAI, Type: typ, Extra: map[string]any{"openai_passthrough": passthrough}}
				// 与上游差异：fork 要求 OpenAI API Key 必须有模型支持依据，空映射
				// 一律不可调度（上游是「无映射 = 允许所有」）。因此这里两种账号都
				// 返回 false，但原因不同：OAuth/SetupToken 被订阅退休守卫拦下，
				// API Key 本来就无依据。见 TestIsModelSupported_OpenAIAPIKeyEmptyMappingRejectsUnknownModels。
				for _, model := range []string{"gpt-5.5", "openai/gpt-5.5-high", "gpt-5.5-2026-04-23", "GPT_5.5", "gpt-5.5-openai-compact"} {
					require.False(t, account.IsModelSupported(model), model)
				}
				require.Equal(t, typ != AccountTypeAPIKey, account.IsModelSupported("gpt-6.1-sol"))
				// 给出显式映射后退休守卫成为唯一拦截来源：API Key 上游不受订阅退休影响。
				account.Credentials = map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"}}
				require.Equal(t, typ == AccountTypeAPIKey, account.IsModelSupported("gpt-5.5"), "only subscription (OAuth/setup-token) is retired")
			})
		}
	}
	for _, mapping := range []map[string]any{
		{"legacy": "gpt-5.5"}, {"legacy*": "gpt-5.5"},
	} {
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"model_mapping": mapping}}
		require.False(t, account.IsModelSupported("legacy"))
	}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-6.1-sol"}}}
	require.True(t, account.IsModelSupported("gpt-5.5"), "explicit administrator replacement remains authoritative")
	account.Type = AccountTypeAPIKey
	require.True(t, account.IsModelSupported("gpt-5.5"))
	parent := int64(3)
	shadow := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, ParentAccountID: &parent}
	require.False(t, shadow.IsModelSupported("gpt-5.5"))
	require.False(t, isRetiredOpenAISubscriptionModel(&Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}, "gpt-5.5"))
	require.False(t, isRetiredOpenAISubscriptionModel(account, "gpt-5.50"))
}

func TestGPT55RetirementCatalogs(t *testing.T) {
	oauth := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	// 与上游差异：API Key 账号必须显式声明 gpt-5.5 才有支持依据（上游空映射放行），
	// 否则它无法在退休过滤里成为「仍有可用来源」的反证。
	api := Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"}}}
	group := &Group{ID: 1, Platform: PlatformOpenAI}
	for _, tc := range []struct {
		name      string
		accounts  []Account
		wantGPT55 bool
	}{
		{"oauth", []Account{oauth}, false}, {"api", []Account{api}, true}, {"mixed", []Account{oauth, api}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{1: tc.accounts}}
			gateway := &GatewayService{accountRepo: repo}
			models := gateway.GetAvailableModels(context.Background(), &group.ID, PlatformOpenAI)
			if models != nil {
				require.Equal(t, tc.wantGPT55, containsModelID(models, "gpt-5.5"))
			}
			body, err := buildCodexModelsManifestForAccounts(PlatformOpenAI, []string{"gpt-5.5", "gpt-6.1-sol"}, tc.accounts, group, nil, true)
			require.NoError(t, err)
			require.Equal(t, tc.wantGPT55, containsManifestModel(body, "gpt-5.5"))
			// A stale/pinned upstream body must be filtered before evaluating its old ETag.
			original := []byte(`{"models":[{"slug":"gpt-5.5","unknown":1},{"slug":"gpt-6.1-sol","unknown":2}],"unknown_top":3}`)
			etag := CodexModelsManifestETag(original)
			manifest := &OpenAIModelsResponse{Body: original, ETag: etag}
			service := &OpenAIGatewayService{accountRepo: repo}
			require.NoError(t, service.MergeGroupConfiguredCodexModels(context.Background(), group, manifest, etag))
			if !tc.wantGPT55 {
				require.False(t, manifest.NotModified)
				require.NotEqual(t, etag, manifest.ETag)
				require.False(t, containsManifestModel(manifest.Body, "gpt-5.5"))
				require.Equal(t, int64(3), gjson.GetBytes(manifest.Body, "unknown_top").Int())
				require.Equal(t, int64(2), gjson.GetBytes(manifest.Body, "models.0.unknown").Int())
			}
		})
	}
	// All configured entries retired: plain discovery must remain an authoritative empty list.
	oauth.Credentials = map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"}}
	repo := codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{1: {oauth}}}
	models := (&GatewayService{accountRepo: repo}).GetAvailableModels(context.Background(), &group.ID, PlatformOpenAI)
	require.NotNil(t, models)
	require.Empty(t, models)
}

func containsModelID(models []string, id string) bool {
	for _, model := range models {
		if model == id {
			return true
		}
	}
	return false
}
func containsManifestModel(body []byte, id string) bool {
	for _, m := range gjson.GetBytes(body, "models").Array() {
		if m.Get("slug").String() == id {
			return true
		}
	}
	return false
}

func TestGPT55RetirementAccountProjection(t *testing.T) {
	body := []byte(`{"object":"list","data":[{"id":"gpt-5.5"},{"id":"gpt-6.1-sol","unknown":true}],"unknown_top":true}`)
	for _, typ := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		account := &Account{Platform: PlatformOpenAI, Type: typ}
		got, err := projectAccountModelsBody(body, account, nil, false)
		require.NoError(t, err)
		require.Equal(t, typ == AccountTypeAPIKey, strings.Contains(string(got), "gpt-5.5"))
		require.True(t, gjson.GetBytes(got, "unknown_top").Bool())
		require.True(t, json.Valid(got))
	}
}

func TestGPT55RetirementProjectionUsesPublicNames(t *testing.T) {
	for _, codex := range []bool{false, true} {
		field, idField := "data", "id"
		if codex {
			field, idField = "models", "slug"
		}
		for _, tc := range []struct {
			name        string
			mapping     map[string]any
			passthrough bool
			api         bool
			want        map[string]string
		}{
			{"one-hop", map[string]any{"legacy": "gpt-5.5", "gpt-5.5": "gpt-6.1-sol"}, false, false, map[string]string{"gpt-5.5": "live"}},
			{"crossed", map[string]any{"gpt-6.1-sol": "gpt-5.5", "gpt-5.5": "gpt-6.1-sol"}, false, false, map[string]string{"gpt-5.5": "live"}},
			{"wildcard", map[string]any{"legacy*": "gpt-5.5", "gpt-5.5": "gpt-6.1-sol"}, false, false, map[string]string{"gpt-5.5": "live"}},
			// fork：透传账号的目录也按显式映射投影（上游会跳过 passthrough），
			// 因此存活项与上游相反——gpt-5.5 映射到 live 目标，gpt-6.1-sol 指向退休目标被剔除。
			{"passthrough", map[string]any{"gpt-6.1-sol": "gpt-5.5", "gpt-5.5": "gpt-6.1-sol"}, true, false, map[string]string{"gpt-5.5": "live"}},
			{"api", map[string]any{"legacy": "gpt-5.5", "gpt-5.5": "gpt-6.1-sol"}, false, true, map[string]string{"legacy": "retired", "gpt-5.5": "live"}},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth,
					Credentials: map[string]any{"model_mapping": tc.mapping}, Extra: map[string]any{"openai_passthrough": tc.passthrough}}
				if tc.api {
					account.Type = AccountTypeAPIKey
				}
				group := &Group{Platform: PlatformOpenAI, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"legacy", "gpt-5.5", "gpt-6.1-sol"}}}
				body, err := json.Marshal(map[string]any{field: []map[string]any{
					{idField: "gpt-5.5", "source": "retired"}, {idField: "gpt-6.1-sol", "source": "live"},
				}, "unknown_top": true})
				require.NoError(t, err)
				original := string(body)
				got, err := projectAccountModelsBody(body, account, group, codex)
				require.NoError(t, err)
				require.Equal(t, original, string(body), "shared upstream cache must remain unchanged")
				require.True(t, gjson.GetBytes(got, "unknown_top").Bool())
				entries := gjson.GetBytes(got, field).Array()
				require.Len(t, entries, len(tc.want), string(got))
				for _, entry := range entries {
					id := entry.Get(idField).String()
					require.Contains(t, tc.want, id)
					require.Equal(t, tc.want[id], entry.Get("source").String(), "metadata must come from the one-hop upstream target")
					require.True(t, account.IsModelSupported(id), "catalog and routing must agree for %s", id)
				}
				if codex {
					response := &OpenAIModelsResponse{Body: body, ETag: CodexModelsManifestETag(body)}
					require.NoError(t, ApplyPinnedCodexModelsMapping(response, account, group))
					require.JSONEq(t, string(got), string(response.Body))
					require.Equal(t, codexModelsManifestBodyETag(response.Body), response.ETag)
					group.ID = 1
					group.CodexModelsManifestConfig.Enabled = true
					service := &OpenAIGatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{1: {*account}}}}
					require.NoError(t, service.MergeGroupConfiguredCodexModels(context.Background(), group, response, CodexModelsManifestETag(body)))
					require.False(t, response.NotModified, "an upstream ETag must not hide the projected catalog")
					require.Equal(t, codexModelsManifestBodyETag(response.Body), response.ETag)
					require.NoError(t, service.MergeGroupConfiguredCodexModels(context.Background(), group, response, response.ETag))
					require.True(t, response.NotModified, "the final public ETag must support conditional requests")
				}
			})
		}
	}
}

func TestGPT55RetirementCompactFallbackKeepsSelectedModel(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
			Credentials: map[string]any{"access_token": "test-token"}, Extra: map[string]any{"openai_passthrough": passthrough}}
		upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"compact","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`))}}
		svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAICompactModel: "gpt-5.5"}}, httpUpstream: upstream}
		require.Empty(t, svc.resolveOpenAICompactFallbackModel(account, "gpt-6.1-sol"))
		c := newOpenAICompactFallbackTestContext(t, "/v1/responses/compact")
		_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-6.1-sol","input":[],"instructions":"compact","stream":false}`))
		require.NoError(t, err)
		require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(upstream.lastBody, "model").String())
		account.Type = AccountTypeAPIKey
		require.Equal(t, "gpt-5.5", svc.resolveOpenAICompactFallbackModel(account, "gpt-6.1-sol"))
	}
}

func TestGPT55RetirementSchedulerAndDiagnosis(t *testing.T) {
	ctx := context.Background()
	groupID := int64(1)
	oauth := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	// 与上游差异：API Key 账号需显式声明 gpt-5.5 才有支持依据（上游空映射放行），
	// 否则它在诊断里无法充当「仍有可用来源」的反证。
	api := Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"}}}
	for _, typ := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
		account := oauth
		account.Type = typ
		scheduler := &defaultOpenAIAccountScheduler{}
		// 与上游差异：fork 要求 API Key 账号有模型支持依据，空映射不可调度，
		// 因此三种类型在空映射下都不可用（上游的 API Key 会因「无映射 = 允许所有」通过）。
		ok, _ := scheduler.isAccountRequestCompatibleReason(ctx, &account, OpenAIAccountScheduleRequest{RequestedModel: "gpt-5.5"})
		require.False(t, ok, typ)
		reason := openAICompatibleAccountEligibilityFailureReasonBeforeProfit(ctx, &account, PlatformOpenAI, "gpt-5.5", false, "")
		require.NotEqual(t, "", reason, typ)
		// 显式声明后退休守卫成为唯一理由：API Key 上游不受订阅退休影响。
		account.Credentials = map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"}}
		ok, _ = scheduler.isAccountRequestCompatibleReason(ctx, &account, OpenAIAccountScheduleRequest{RequestedModel: "gpt-5.5"})
		require.Equal(t, typ == AccountTypeAPIKey, ok, typ)
	}
	oauth.Credentials = map[string]any{"compact_model_mapping": map[string]any{"gpt-6.1-sol": "gpt-5.5"}}
	scheduler := &defaultOpenAIAccountScheduler{}
	ok, reason := scheduler.isAccountRequestCompatibleReason(ctx, &oauth, OpenAIAccountScheduleRequest{RequestedModel: "gpt-6.1-sol", RequireCompact: true})
	require.False(t, ok)
	require.Equal(t, "model_not_supported", reason)
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAICompactModel: "gpt-6.1-sol"}}}
	require.Empty(t, svc.resolveOpenAICompactFallbackModel(&oauth, "gpt-6.1-sol"), "native compact retries must not use a retired explicit target")
	oauth.Credentials = map[string]any{"compact_model_mapping": map[string]any{"gpt-5.5": "gpt-6.1-sol"}}
	ok, _ = scheduler.isAccountRequestCompatibleReason(ctx, &oauth, OpenAIAccountScheduleRequest{RequestedModel: "gpt-5.5", RequireCompact: true})
	require.True(t, ok, "an explicit compact replacement remains usable")
	for _, accounts := range [][]Account{{oauth}, {oauth, api}} {
		svc := &OpenAIGatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{groupID: accounts}}}
		diagnosis := svc.DiagnoseModelAvailabilityForPlatform(ctx, &groupID, "gpt-5.5", PlatformOpenAI)
		require.True(t, diagnosis.HasAccountsInPool)
		require.True(t, diagnosis.HasRetiredSubscriptionModel)
		// 仅 oauth（订阅已退休）时无可用来源；加上显式声明 gpt-5.5 的 API Key 账号后才有。
		require.Equal(t, len(accounts) == 2, diagnosis.HasModelSupport, "only the evidence-backed API key account keeps GPT-5.5")
	}
}

func TestGPT55RetirementEmptyListingSource(t *testing.T) {
	// A configured alias targeting retired GPT-5.5 must not reappear in a generated catalog.
	account := Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"model_mapping": map[string]any{"legacy": "gpt-5.5"}}}
	require.Empty(t, openAIConfiguredCodexModelIDsForGroup([]Account{account}, nil))
	body, err := buildCodexModelsManifestForAccounts(PlatformComposite, []string{"gpt-5.5", "gpt-6.1-sol"}, []Account{account}, nil, nil, true)
	require.NoError(t, err)
	require.False(t, containsManifestModel(body, "gpt-5.5"))
}
