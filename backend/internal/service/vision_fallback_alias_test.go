//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func visionAliasTestPolicy() *ModelAliasPolicy {
	return &ModelAliasPolicy{Groups: []ModelAliasGroup{{
		Canonical: "deepseek-v4.1-flash",
		Aliases:   []string{"cn:deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash"},
	}}}
}

func TestVisionFallbackAliasesSelectVerifiedAccountsInConfiguredOrder(t *testing.T) {
	aliases := visionAliasTestPolicy()
	for _, configured := range aliases.IDs("deepseek-v4.1-flash") {
		t.Run(configured, func(t *testing.T) {
			accounts := []Account{
				visionTestAccount(837, "deepseek-v4.1-flash", "image"),
				visionTestAccount(844, "cn:deepseek-v4.1-flash", "image"),
				visionTestAccount(850, "deepseek/deepseek-v4.1-flash", "image"),
				visionTestAccount(1, "preferred", "image"),
				visionTestAccount(2, "deepseek-v4.1-flash", "text"),
				visionTestAccount(3, "deepseek-v4.1-flash"),
				visionTestAccount(4, "deepseek-v4.1-flash", "image"),
			}
			accounts[1].Platform = PlatformWorkbuddy
			accounts[6].Schedulable = false
			accounts[2].Credentials["model_mapping"].(map[string]any)["deepseek-v4.1-flash"] = "deepseek/deepseek-v4.1-flash"
			accounts[2].Credentials["model_mapping"].(map[string]any)["duplicate-target"] = "deepseek/deepseek-v4.1-flash"
			before, err := json.Marshal(accounts)
			require.NoError(t, err)
			p := DefaultVisionFallbackPolicy(visionTestConfig())
			p.Models = []string{"preferred", configured, "deepseek-v4.1-flash", "duplicate-target"}
			p.AllowUnlistedModels = false
			for _, unlisted := range []bool{false, true} {
				p.AllowUnlistedModels = unlisted
				candidates := visionFallbackCandidatesWithPolicy(accounts, p, nil, aliases)
				require.Len(t, candidates, 4)
				for i, id := range []int64{1, 837, 844, 850} {
					require.Equal(t, id, candidates[i].account.ID)
				}
				_, upstream := resolveOpenAIForwardMappedModels(candidates[3].account, candidates[3].model, false)
				require.Equal(t, "deepseek/deepseek-v4.1-flash", upstream)
			}
			group := &Group{ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"deepseek-v4.1-flash"}}}
			require.Len(t, visionFallbackCandidatesWithPolicy(accounts, p, group, aliases), 3)
			group.ModelAllowlist.Models = []string{"unrelated"}
			require.Empty(t, visionFallbackCandidatesWithPolicy(accounts, p, group, aliases))
			after, err := json.Marshal(accounts)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestVisionFallbackAliasesForwardRealProviderIDAndAdvertiseAssistance(t *testing.T) {
	ctx := context.Background()
	cfg := visionTestConfig()
	settings := NewSettingService(newMockSettingRepo(), cfg)
	require.NoError(t, settings.SetModelAliasPolicy(ctx, visionAliasTestPolicy()))
	p := DefaultVisionFallbackPolicy(cfg)
	p.Models, p.AllowUnlistedModels = []string{"cn:deepseek-v4.1-flash"}, false
	require.NoError(t, settings.SetVisionFallbackPolicy(ctx, p))
	primary := visionTestAccount(1, "text-model", "text")
	helper := visionTestAccount(850, "deepseek/deepseek-v4.1-flash", "image")
	accounts := []Account{primary, helper}
	calls := 0
	svc := &OpenAIGatewayService{cfg: cfg, settingService: settings,
		accountRepo: &countingCodexModelsAccountRepo{accounts: accounts},
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
			calls++
			require.Equal(t, int64(850), id)
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, "deepseek/deepseek-v4.1-flash", gjson.GetBytes(body, "model").String())
			require.Contains(t, string(body), "data:image/png;base64,AAAA")
			return visionTestResponse("deepseek/deepseek-v4.1-flash", "provider image observation"), nil
		}},
	}
	c, _ := visionTestContext([]byte(visionTestInput), 9, 7)
	converted, err := svc.prepareVisionFallback(ctx, c, &primary, []byte(visionTestInput))
	require.NoError(t, err)
	require.Contains(t, string(converted), "provider image observation")
	require.Equal(t, 1, calls)
	usage := TakeVisionFallbackUsage(c)
	require.Len(t, usage, 1)
	require.Equal(t, int64(850), usage[0].Account.ID)
	manifest := []byte(`{"models":[{"slug":"text-model","input_modalities":["text"]}]}`)
	assisted, err := applyConfiguredVisionFallbackManifest(ctx, settings, manifest, cfg, nil, PlatformOpenAI, accounts, nil, true)
	require.NoError(t, err)
	require.Contains(t, string(assisted), `"image"`)
	plain, err := applyConfiguredVisionFallbackManifest(ctx, settings, manifest, cfg, nil, PlatformOpenAI, []Account{primary}, nil, true)
	require.NoError(t, err)
	require.JSONEq(t, string(manifest), string(plain))
}

func TestVisionFallbackAliasSnapshotAndInvalidConfiguration(t *testing.T) {
	ctx := context.Background()
	cfg := visionTestConfig()
	repo := newMockSettingRepo()
	settings := NewSettingService(repo, cfg)
	aliases := visionAliasTestPolicy()
	require.NoError(t, settings.SetModelAliasPolicy(ctx, aliases))
	svc := &OpenAIGatewayService{cfg: cfg, settingService: settings}
	c, _ := visionTestContext([]byte(visionTestInput), 9, 7)
	state, err := svc.visionFallbackState(ctx, c)
	require.NoError(t, err)
	require.Equal(t, aliases, state.aliases)
	require.NoError(t, settings.SetModelAliasPolicy(ctx, &ModelAliasPolicy{}))
	retry, err := svc.visionFallbackState(ctx, c)
	require.NoError(t, err)
	require.Same(t, state, retry)
	BeginOpsStreamTurn(c, 2)
	next, err := svc.visionFallbackState(ctx, c)
	require.NoError(t, err)
	require.Empty(t, next.aliases.Groups)
	repo.data[SettingKeyModelAliases] = "broken"
	BeginOpsStreamTurn(c, 3)
	_, err = svc.visionFallbackState(ctx, c)
	require.Error(t, err)
	_, err = applyConfiguredVisionFallbackManifest(ctx, settings, []byte(`{"models":[]}`), cfg, nil, PlatformOpenAI, nil, nil, true)
	require.Error(t, err)
	bound, err := svc.visionFallbackState(WithModelAliases(ctx, aliases), c)
	require.NoError(t, err)
	require.Same(t, aliases, bound.aliases)
}

func TestVisionFallbackAliasPassthroughKeepsNativeImageAndProviderID(t *testing.T) {
	ctx := WithModelAliases(context.Background(), visionAliasTestPolicy())
	account := visionTestAccount(850, "deepseek/deepseek-v4.1-flash", "image")
	account.Extra["openai_passthrough"] = true
	// 使用真实透传路径，不能让测试账号的强制 Chat 模式提前接管请求。
	delete(account.Extra, openai_compat.ExtraKeyResponsesMode)
	account = *accountWithModelAliases(ctx, &account)
	require.False(t, shouldForwardOpenAIResponsesViaRawChatCompletions(&account))
	require.True(t, accountHasNativeVision(&account, "deepseek-v4.1-flash"))
	for _, compact := range []bool{false, true} {
		require.Equal(t, "deepseek/deepseek-v4.1-flash", resolveOpenAIAccountUpstreamModelForRequest(&account, "deepseek-v4.1-flash", compact))
	}
	calls := 0
	svc := &OpenAIGatewayService{cfg: visionTestConfig(),
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
			calls++
			require.Equal(t, int64(850), id)
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, "deepseek/deepseek-v4.1-flash", gjson.GetBytes(body, "model").String())
			require.Contains(t, string(body), "data:image/png;base64,AAAA")
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"resp-vision","status":"completed","model":"deepseek/deepseek-v4.1-flash","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"native observation"}]}],"usage":{"input_tokens":10,"output_tokens":3}}`))}, nil
		}},
	}
	body := []byte(strings.Replace(visionTestInput, "text-model", "deepseek-v4.1-flash", 1))
	c, _ := visionTestContext(body, 9, 7)
	_, err := svc.Forward(ctx, c, &account, body)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Empty(t, TakeVisionFallbackUsage(c))
}
