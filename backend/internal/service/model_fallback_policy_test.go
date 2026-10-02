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
			require.False(t, ModelFallbackAccountCompatible(&tc.account, "gpt-5.5", body))
		})
	}
	require.False(t, ModelFallbackRequestPortable(body))
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
