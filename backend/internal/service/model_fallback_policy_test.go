//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

func TestModelFallbackPolicyOrderAndPersistence(t *testing.T) {
	ctx := context.Background()
	repo := newMockSettingRepo()
	s := NewSettingService(repo, nil)
	preset, err := s.GetModelFallbackPolicy(ctx)
	require.NoError(t, err)
	require.NoError(t, preset.Validate())
	policy := &ModelFallbackPolicy{Enabled: true, Tiers: []ModelCapabilityTier{
		{Name: "high", Models: []string{"top"}},
		{Name: "middle", Models: []string{"peer", "requested"}},
		{Name: "low", Models: []string{"low-a", "low-b"}},
	}}
	require.NoError(t, s.SetModelFallbackPolicy(ctx, policy))
	loaded, err := s.GetModelFallbackPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, policy, loaded)
	require.Equal(t, []ModelFallbackCandidate{{"peer", "middle"}, {"low-a", "low"}, {"low-b", "low"}, {"top", "high"}}, loaded.Candidates("requested"))
	require.Equal(t, []ModelFallbackCandidate{{"low-b", "low"}, {"peer", "middle"}, {"requested", "middle"}, {"top", "high"}}, loaded.Candidates("low-a"))
	require.Equal(t, []ModelFallbackCandidate{{"peer", "middle"}, {"requested", "middle"}, {"low-a", "low"}, {"low-b", "low"}}, loaded.Candidates("top"))
	require.Empty(t, loaded.Candidates("unknown"))
	loaded.Enabled = false
	require.NoError(t, s.SetModelFallbackPolicy(ctx, loaded))
	loaded, err = s.GetModelFallbackPolicy(ctx)
	require.NoError(t, err)
	require.Empty(t, loaded.Candidates("requested"))
	// 损坏的配置必须暴露读取错误，不能悄悄重新开启推荐策略。
	repo.data[SettingKeyModelFallbackPolicy] = `{"enabled":true,"tiers":[]}`
	_, err = s.GetModelFallbackPolicy(ctx)
	require.Error(t, err)
}

func TestModelFallbackPolicyKeepsTerraBehindGLMAndQwen(t *testing.T) {
	policy := &ModelFallbackPolicy{Enabled: true, Tiers: []ModelCapabilityTier{{
		Name: "main",
		Models: []string{
			"deepseek-v4.1-flash",
			"glm-5.3",
			"glm-5.3-flash",
			"qwen3.8-max",
			"other-model",
			"gpt-5.6-terra",
		},
	}}}

	candidates := policy.Candidates("deepseek-v4.1-flash")
	require.Equal(t, []string{
		"glm-5.3",
		"glm-5.3-flash",
		"qwen3.8-max",
		"other-model",
		"gpt-5.6-terra",
	}, []string{
		candidates[0].Model,
		candidates[1].Model,
		candidates[2].Model,
		candidates[3].Model,
		candidates[4].Model,
	})
}

func TestDefaultModelFallbackPolicyIncludesGLM52AndSensenova(t *testing.T) {
	var models []string
	for _, tier := range DefaultModelFallbackPolicy().Tiers {
		models = append(models, tier.Models...)
	}
	require.Contains(t, models, "glm-5.2")
	require.Contains(t, models, "sensenova-6.8-flash-lite")
}

func TestModelFallbackPolicyRejectsAmbiguousOrUnboundedConfig(t *testing.T) {
	for _, policy := range []*ModelFallbackPolicy{
		nil,
		{Enabled: true},
		{Tiers: []ModelCapabilityTier{{Name: "x", Models: []string{"a"}}, {Name: "y", Models: []string{"a"}}}},
		{Tiers: []ModelCapabilityTier{{Name: "x", Models: []string{"a"}}, {Name: "x", Models: []string{"b"}}}},
		{Tiers: []ModelCapabilityTier{{Name: "x", Models: []string{"gpt-*"}}}},
		{Tiers: []ModelCapabilityTier{{Name: "x", Models: []string{" "}}}},
		{Tiers: []ModelCapabilityTier{{Name: "x"}}},
	} {
		require.Error(t, policy.Validate())
	}
}

func TestModelFallbackPolicyBounds(t *testing.T) {
	policy := &ModelFallbackPolicy{Enabled: true, Tiers: []ModelCapabilityTier{{Name: "tier"}}}
	for i := 0; i <= maxModelFallbackModels; i++ {
		policy.Tiers[0].Models = append(policy.Tiers[0].Models, fmt.Sprintf("m%d", i))
	}
	require.Error(t, policy.Validate())
	policy.Tiers[0].Models = policy.Tiers[0].Models[:maxModelFallbackModels]
	require.NoError(t, policy.Validate())
}

func TestModelFallbackChecksAccountCapabilities(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
		"target": {ID: "target", ContextWindow: 500, MaxOutputTokens: 100, InputModalities: []string{"text"}, SupportedReasoningLevels: []string{"low", "high"}, CodexToolCapabilities: map[string]json.RawMessage{"supports_function_calling": json.RawMessage("false")}},
	}})
	require.True(t, ModelFallbackAccountCompatible(account, "target", []byte(`{"input":"hi","reasoning":{"effort":"high"},"max_output_tokens":50}`)))
	for _, body := range []string{
		`{"input":"` + strings.Repeat("x", 500) + `"}`,
		`{"input":"hi","max_output_tokens":101}`,
		`{"input":"hi","reasoning":{"effort":"xhigh"}}`,
		`{"input":[{"type":"input_image","image_url":"https://image.invalid/a"}]}`,
		`{"input":[{"type":"item_reference","id":"x"}]}`,
		`{"input":[{"type":"reasoning","encrypted_content":"opaque"}]}`,
		`{"tools":[{"type":"function","name":"read"}]}`,
	} {
		require.False(t, ModelFallbackAccountCompatible(account, "target", []byte(body)), body)
	}
}

func TestModelAccountEncryptedReasoningAllowsNativeResponsesOrKnownChatBridge(t *testing.T) {
	body := []byte(`{"model":"auto","input":[{"type":"reasoning","summary":[],"encrypted_content":"opaque"},{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}],"tools":[{"type":"function","name":"shell","parameters":{"type":"object"}}]}`)
	for _, tc := range []struct {
		name     string
		account  Account
		accepted bool
	}{
		{"native", Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}, true},
		{"oauth", Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, true},
		{"unknown", Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, false},
		{"chat_only", Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false}}, true},
		{"forced_chat", Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true, openai_compat.ExtraKeyResponsesMode: "force_chat_completions"}}, true},
		{"other_provider", Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.accepted, ModelAccountCompatible(&tc.account, "gpt-5.5", body))
			require.Equal(t, tc.accepted, AutoModelRequestAccountCompatible(context.Background(), &tc.account, "deepseek-v4.1-flash", body))
			require.Equal(t, tc.accepted, ModelFallbackAccountCompatible(&tc.account, "gpt-5.5", body))
		})
	}
	require.True(t, ModelFallbackRequestPortable(body))
	for _, encrypted := range []string{`null`, `""`} {
		plain := []byte(`{"input":[{"type":"reasoning","summary":[],"encrypted_content":` + encrypted + `}]}`)
		require.True(t, ModelFallbackAccountCompatible(&Account{Platform: PlatformOpenAI}, "gpt-5.5", plain))
	}
}

func TestAutoModelRoutingPolicyDefaultHighestTier(t *testing.T) {
	ctx, err := (&SettingService{}).BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	require.False(t, AutoModelAllowed(ctx, "gpt-6-astra"))
	require.True(t, AutoModelAllowed(ctx, "gpt-5.6-sol", "gpt-6-astra-high", "openai/gpt-6-astra"))
	require.True(t, AutoModelAllowed(context.Background(), "gpt-6-astra"))
}

func TestAutoModelRoutingPolicyUsesConfiguredOrderAndAliases(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("fallback_enabled_%t", enabled), func(t *testing.T) {
			repo := newMockSettingRepo()
			settings := NewSettingService(repo, nil)
			policy := &ModelFallbackPolicy{Enabled: enabled, Tiers: []ModelCapabilityTier{
				{Name: "custom first", Models: []string{"Expensive"}},
				{Name: "custom second", Models: []string{"gpt-6-astra"}},
			}}
			require.NoError(t, settings.SetModelFallbackPolicy(context.Background(), policy))
			require.NoError(t, settings.SetModelAliasPolicy(context.Background(), &ModelAliasPolicy{Groups: []ModelAliasGroup{{
				Canonical: "gpt-5.5", Aliases: []string{"Expensive", "provider/gpt-5.5"},
			}}}))
			ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
			require.NoError(t, err)
			for _, model := range []string{"gpt-5.5", "Expensive", "provider/gpt-5.5"} {
				require.False(t, AutoModelAllowed(ctx, "gpt-6-astra", model), model)
			}
			require.True(t, AutoModelAllowed(ctx, "gpt-6-astra", "expensive", "gpt-5.5-latest"))
		})
	}
}

func TestAutoModelRoutingPolicyRejectsUnavailableConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fallback string
		aliases  string
		readErr  error
	}{
		{name: "empty tiers", fallback: `{"enabled":false,"tiers":[]}`},
		{name: "malformed fallback", fallback: `{bad`},
		{name: "invalid fallback", fallback: `{"enabled":true,"tiers":[{"name":"empty","models":[]}]}`},
		{name: "malformed aliases", aliases: `{bad`},
		{name: "settings read error", readErr: errors.New("settings unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMockSettingRepo()
			repo.data[SettingKeyModelFallbackPolicy] = tc.fallback
			repo.data[SettingKeyModelAliases] = tc.aliases
			repo.getValueErr = tc.readErr
			_, err := NewSettingService(repo, nil).BindAutoModelRoutingPolicy(context.Background())
			require.Error(t, err)
		})
	}
}

func TestAutoModelRoutingPolicySnapshotSurvivesSettingsUpdates(t *testing.T) {
	repo := newMockSettingRepo()
	settings := NewSettingService(repo, nil)
	aliases := &ModelAliasPolicy{Groups: []ModelAliasGroup{{Canonical: "gpt-6-astra", Aliases: []string{"provider/expensive"}}}}
	require.NoError(t, settings.SetModelAliasPolicy(context.Background(), aliases))
	ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	require.False(t, AutoModelAllowed(ctx, "provider/expensive"))

	require.NoError(t, settings.SetModelFallbackPolicy(context.Background(), &ModelFallbackPolicy{Enabled: false, Tiers: []ModelCapabilityTier{
		{Name: "new highest", Models: []string{"gpt-5.5"}},
	}}))
	aliases.Groups[0] = ModelAliasGroup{Canonical: "gpt-5.5", Aliases: []string{"provider/expensive"}}
	require.NoError(t, settings.SetModelAliasPolicy(context.Background(), aliases))
	updated, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	require.False(t, AutoModelAllowed(updated, "gpt-5.5", "provider/expensive"))
	require.True(t, AutoModelAllowed(updated, "gpt-6-astra"))
	reads := repo.getValueCalls
	repo.getValueErr = errors.New("settings unavailable")
	rebound, err := settings.BindAutoModelRoutingPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, reads, repo.getValueCalls)
	require.Same(t, ctx, rebound)
	require.False(t, AutoModelAllowed(rebound, "gpt-6-astra", "provider/expensive"))
	require.True(t, AutoModelAllowed(rebound, "gpt-5.5"))
}

func TestAutoModelRoutingPolicyCopiesBoundAliases(t *testing.T) {
	aliases := &ModelAliasPolicy{Groups: []ModelAliasGroup{{Canonical: "gpt-6-astra", Aliases: []string{"provider/expensive"}}}}
	ctx := WithModelAliases(context.Background(), aliases)
	ctx, err := (&SettingService{}).BindAutoModelRoutingPolicy(ctx)
	require.NoError(t, err)
	aliases.Groups[0].Canonical = "gpt-5.5"
	aliases.Groups[0].Aliases[0] = "provider/changed"
	require.False(t, AutoModelAllowed(ctx, "provider/expensive"))
	require.True(t, AutoModelAllowed(ctx, "gpt-5.5", "provider/changed"))
}

func TestModelFallbackAutoSentinelCoversUntieredAndExhaustedTiers(t *testing.T) {
	// 未评级模型没有任何档位候选，AutoFallback 开启时仍要给出 auto 兜底。
	withAuto := &ModelFallbackPolicy{Enabled: true, AutoFallback: true, Tiers: []ModelCapabilityTier{
		{Name: "high", Models: []string{"top"}},
		{Name: "guard", Models: []string{"meta/llama-guard-4-12b"}},
	}}
	guardCandidates := withAuto.Candidates("meta/llama-guard-4-12b")
	require.NotEmpty(t, guardCandidates)
	require.Equal(t, ModelFallbackCandidate{Model: ModelFallbackAutoModel, Tier: "auto"},
		guardCandidates[len(guardCandidates)-1], "guard 档候选耗尽后以 auto 收尾")

	untiered := withAuto.Candidates("totally-unknown-model")
	require.Equal(t, []ModelFallbackCandidate{{Model: ModelFallbackAutoModel, Tier: "auto"}}, untiered,
		"未评级模型直接以 auto 作为唯一候选")

	// 关闭 AutoFallback 时恢复旧行为：未评级模型不降级。
	withoutAuto := *withAuto
	withoutAuto.AutoFallback = false
	require.Empty(t, withoutAuto.Candidates("totally-unknown-model"))
	// 已评级模型仍然只在档位内互降，不带 auto 尾巴。
	for _, candidate := range withoutAuto.Candidates("meta/llama-guard-4-12b") {
		require.NotEqual(t, ModelFallbackAutoModel, candidate.Model)
	}
}

func TestModelFallbackPolicyGuardTierKeepsGuardModelsTogether(t *testing.T) {
	policy := &ModelFallbackPolicy{Enabled: true, Tiers: []ModelCapabilityTier{
		{Name: "旗舰", Models: []string{"gpt-6-astra"}},
		{Name: "内容安全轻量", Models: []string{
			"meta/llama-guard-4-12b",
			"nvidia/llama-3.1-nemoguard-8b-content-safety",
			"nvidia/llama-3.1-nemoguard-8b-topic-control",
			"nvidia/llama-3.1-nemotron-safety-guard-8b-v3",
		}},
	}}
	models := policy.Candidates("meta/llama-guard-4-12b")
	// guard 档内部互为降级目标，且不向上借通用旗舰模型。
	require.Equal(t, "nvidia/llama-3.1-nemoguard-8b-content-safety", models[0].Model)
	require.Equal(t, "内容安全轻量", models[0].Tier)
	require.Equal(t, "gpt-6-astra", models[len(models)-1].Model)
}

func TestModelFallbackPolicyRejectsAutoSentinelInTier(t *testing.T) {
	policy := &ModelFallbackPolicy{Enabled: true, Tiers: []ModelCapabilityTier{
		{Name: "x", Models: []string{ModelFallbackAutoModel}},
	}}
	require.Error(t, policy.Validate())
}

func TestDefaultModelFallbackPolicyDeclaresGuardTierAndAutoFallback(t *testing.T) {
	policy := DefaultModelFallbackPolicy()
	require.True(t, policy.AutoFallback)
	require.NoError(t, policy.Validate())
	last := policy.Tiers[len(policy.Tiers)-1]
	require.Equal(t, "内容安全轻量", last.Name)
	require.Contains(t, last.Models, "meta/llama-guard-4-12b")
}
// Auto 账号级预检必须把「服务端引用型条目」当作可由真实转发路径规范化/丢弃，
// 不能判成 no_compatible_account；否则 model=auto 的 /v1/responses 会对整池账号
// 一律返回 503「Auto model routing has no eligible model」（2026-10-12 事故）。
func TestAutoModelReferenceInputDoesNotExcludeEveryAccount(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
		"target": {ID: "target", ContextWindow: 200000, MaxOutputTokens: 8192, InputModalities: []string{"text", "image"}},
	}})
	ctx := WithAutoModelRequestCapabilities(context.Background(), []byte(`{"model":"auto","input":"hi"}`))
	for _, body := range []string{
		`{"model":"auto","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},{"type":"item_reference","id":"rs_1"}]}`,
		`{"model":"auto","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_id":"file-1"}]}]}`,
		`{"model":"auto","input":[{"type":"message","role":"user","content":[{"type":"input_file","filename":"a.py","file_data":"data:text/plain;base64,aGk="}]}]}`,
		`{"model":"auto","input":[{"type":"message","role":"user","content":[{"type":"input_audio","input_audio":{"data":"aGk=","format":"wav"}}]}]}`,
		`{"model":"auto","input":[{"type":"message","role":"user","content":[{"type":"audio","id":"audio_1"}]}]}`,
		`{"model":"auto","input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}],"audio":{"id":"audio_1"}}]}`,
		`{"model":"auto","input":[{"type":"message","role":"user","content":[{"type":"video_url","video_url":"https://example.com/a.mp4"}]}]}`,
		`{"model":"auto","input":[{"type":"message","role":"user","content":[{"type":"input_video","video_url":"https://example.com/a.mp4"}]}]}`,
	} {
		bodyBytes := []byte(body)
		require.True(t, AutoModelRequestAccountCompatible(WithAutoModelRequestCapabilities(ctx, bodyBytes), account, "target", bodyBytes), body)
		// 换模型重放仍必须严格：这些条目跨上游不可移植。
		require.False(t, ModelFallbackAccountCompatible(account, "target", bodyBytes), body)
		require.False(t, ModelAccountCompatible(account, "target", bodyBytes), body)
		require.False(t, ModelFallbackRequestPortable(bodyBytes), body)
	}
	// 上游明确拒绝的加密思考项对任何账号都不可用，Auto 也不能放宽。
	redacted := []byte(`{"model":"auto","input":[{"type":"redacted_thinking","data":"abc"}]}`)
	require.False(t, AutoModelRequestAccountCompatible(WithAutoModelRequestCapabilities(ctx, redacted), account, "target", redacted))
	require.False(t, ModelFallbackAccountCompatible(account, "target", redacted))
}
