//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

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
	require.Equal(t, []ModelFallbackCandidate{{"peer", "middle"}, {"low-a", "low"}, {"low-b", "low"}}, loaded.Candidates("requested"))
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
	for i := 0; i < 65; i++ {
		policy.Tiers[0].Models = append(policy.Tiers[0].Models, fmt.Sprintf("m%d", i))
	}
	require.Error(t, policy.Validate())
	policy.Tiers[0].Models = policy.Tiers[0].Models[:64]
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
