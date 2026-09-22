//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func aliasTestPolicy() *ModelAliasPolicy {
	return &ModelAliasPolicy{Groups: []ModelAliasGroup{{Canonical: "deepseek-v4-flash", Aliases: []string{"DeepSeek-V4-Flash", "cn:deepseek-v4-flash"}}}}
}

func TestGlobalModelAliasPersistenceAndValidation(t *testing.T) {
	ctx := context.Background()
	repo := newMockSettingRepo()
	settings := NewSettingService(repo, nil)
	p := aliasTestPolicy()
	require.NoError(t, settings.SetModelAliasPolicy(ctx, p))
	read, err := settings.GetModelAliasPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, p, read)
	for _, bad := range []*ModelAliasPolicy{
		nil,
		{Groups: []ModelAliasGroup{{Canonical: " x "}}},
		{Groups: []ModelAliasGroup{{Canonical: "x", Aliases: []string{"x"}}}},
		{Groups: []ModelAliasGroup{{Canonical: "x", Aliases: []string{"y"}}, {Canonical: "y", Aliases: []string{"x"}}}},
		{Groups: []ModelAliasGroup{{Canonical: "x", Aliases: []string{"*"}}}},
	} {
		require.Error(t, bad.Validate())
	}
	repo.data[SettingKeyModelAliases] = `{bad`
	_, err = settings.GetModelAliasPolicy(ctx)
	require.Error(t, err)
	require.Equal(t, "global:deepseek-v4-flash", p.Canonicalize("global:deepseek-v4-flash"))
}

func TestGlobalModelAliasAccountRoutingAndIsolation(t *testing.T) {
	p := aliasTestPolicy()
	ctx := WithModelAliases(context.Background(), p)
	for _, passthrough := range []bool{false, true} {
		for _, native := range []string{"cn:deepseek-v4-flash", "DeepSeek-V4-Flash", "deepseek-v4-flash"} {
			t.Run(native+string(rune('0'+boolInt(passthrough))), func(t *testing.T) {
				account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{native: native}}, Extra: map[string]any{"openai_passthrough": passthrough}}
				account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{Source: "upstream", SyncedAt: time.Now().Format(time.RFC3339), Models: []string{native}})
				before, _ := json.Marshal(account)
				routed := accountWithModelAliases(ctx, account)
				require.True(t, routed.IsModelSupported("deepseek-v4-flash"))
				require.Equal(t, native, routed.GetMappedModel("deepseek-v4-flash"))
				require.Equal(t, normalizeUnsupportedModelKey(native), unsupportedModelKeyForAccount(routed, "deepseek-v4-flash"))
				after, _ := json.Marshal(account)
				require.JSONEq(t, string(before), string(after))
				require.Nil(t, account.globalModelMapping)
				require.False(t, routed.IsModelSupported("unknown"))
			})
		}
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestGlobalModelAliasDoesNotInventAvailability(t *testing.T) {
	ctx := WithModelAliases(context.Background(), aliasTestPolicy())
	unknown := &Account{Platform: PlatformWorkbuddy, Type: AccountTypeAPIKey}
	routed := accountWithModelAliases(ctx, unknown)
	require.Empty(t, routed.globalModelMapping)
	require.False(t, routed.IsModelSupported("deepseek-v4-flash"))
	native := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"deepseek-v4-flash": "private-target", "cn:deepseek-v4-flash": "cn:deepseek-v4-flash"}}}
	require.Equal(t, "private-target", accountWithModelAliases(ctx, native).GetMappedModel("deepseek-v4-flash"))
}

func TestGlobalModelAliasCatalogAndFallback(t *testing.T) {
	p := aliasTestPolicy()
	body, err := p.CanonicalizeCatalog([]byte(`{"data":[{"id":"cn:deepseek-v4-flash","context_length":100},{"id":"deepseek-v4-flash","context_length":200},{"id":"DeepSeek-V4-Flash","context_length":300},{"id":"unknown"}]}`))
	require.NoError(t, err)
	require.Equal(t, int64(2), gjson.GetBytes(body, "data.#").Int())
	require.Equal(t, int64(200), gjson.GetBytes(body, "data.0.context_length").Int())
	require.Equal(t, "deepseek-v4-flash", gjson.GetBytes(body, "data.0.id").String())
	policy := &ModelFallbackPolicy{Enabled: true, Tiers: []ModelCapabilityTier{{Name: "first", Models: []string{"cn:deepseek-v4-flash", "peer"}}, {Name: "later", Models: []string{"DeepSeek-V4-Flash", "low"}}}}
	canonical := p.NormalizeFallback(policy)
	require.Equal(t, []ModelFallbackCandidate{{Model: "peer", Tier: "first"}, {Model: "low", Tier: "later"}}, canonical.Candidates("deepseek-v4-flash"))
	require.Equal(t, "cn:deepseek-v4-flash", policy.Tiers[0].Models[0])
}
