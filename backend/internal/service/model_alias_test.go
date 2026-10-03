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
	return &ModelAliasPolicy{Groups: []ModelAliasGroup{{Canonical: "deepseek-v4-flash", Aliases: []string{"DeepSeek-V4-Flash", "cn:deepseek-v4-flash", "deepseek/deepseek-v4-flash"}}}}
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
		for _, native := range []string{"cn:deepseek-v4-flash", "DeepSeek-V4-Flash", "deepseek-v4-flash", "deepseek/deepseek-v4-flash"} {
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

func TestGlobalModelAliasRoutesDefaultProviderIDs(t *testing.T) {
	policy := &ModelAliasPolicy{Groups: []ModelAliasGroup{
		{Canonical: "deepseek-v4.1-flash", Aliases: []string{"deepseek-flash"}},
		{Canonical: "minimax-m3", Aliases: []string{"MiniMax-M3"}},
		{Canonical: "minimax-m2.7", Aliases: []string{"MiniMax-M2.7"}},
	}}
	ctx := WithModelAliases(context.Background(), policy)
	for _, test := range []struct {
		platform  string
		canonical string
		upstream  string
	}{
		{PlatformDeepseek, "deepseek-v4.1-flash", "deepseek-flash"},
		{PlatformMiniMax, "minimax-m3", "MiniMax-M3"},
		{PlatformMiniMax, "minimax-m2.7", "MiniMax-M2.7"},
	} {
		account := &Account{Platform: test.platform, Type: AccountTypeAPIKey}
		routed := accountWithModelAliases(ctx, account)
		require.True(t, routed.IsModelSupported(test.canonical), "%s mapping=%v upstream=%t", test.platform, routed.GetModelMapping(), account.IsModelSupported(test.upstream))
		require.Equal(t, test.upstream, routed.GetMappedModel(test.canonical), test.platform)
		require.Empty(t, account.globalModelMapping)
	}
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

func TestGlobalModelAliasRespectsConfiguredAllowlist(t *testing.T) {
	ctx := WithModelAliases(context.Background(), aliasTestPolicy())
	for _, passthrough := range []bool{false, true} {
		account := &Account{
			Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Credentials: map[string]any{"model_mapping": testModelMapping("gpt-6-luna")},
			Extra:       map[string]any{"openai_passthrough": passthrough},
		}
		account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
			Source: "upstream", SyncedAt: time.Now().UTC().Format(time.RFC3339),
			Models: []string{"gpt-6-luna", "deepseek-v4-flash", "cn:deepseek-v4-flash"},
		})
		routed := accountWithModelAliases(ctx, account)
		require.Empty(t, routed.globalModelMapping)
		require.False(t, routed.IsModelSupported("deepseek-v4-flash"))
		require.False(t, routed.IsModelSupported("cn:deepseek-v4-flash"))
		require.True(t, routed.IsModelSupported("gpt-6-luna"))

		account.Credentials["model_mapping"] = testModelMapping("cn:deepseek-v4-flash")
		routed = accountWithModelAliases(ctx, account)
		require.True(t, routed.IsModelSupported("deepseek-v4-flash"))
		require.Equal(t, "cn:deepseek-v4-flash", routed.GetMappedModel("deepseek-v4-flash"))

		// 未设白名单时，全局别名仍可依据目录归一化，且不能限制目录中的其他模型。
		delete(account.Credentials, "model_mapping")
		routed = accountWithModelAliases(ctx, account)
		require.True(t, routed.IsModelSupported("deepseek-v4-flash"))
		require.True(t, routed.IsModelSupported("gpt-6-luna"))
	}
}

func TestDeepSeekV4FamilyCatalogCanonicalizesCompatIDs(t *testing.T) {
	policy := &ModelAliasPolicy{Groups: []ModelAliasGroup{{
		Canonical: "deepseek-v4.1-flash",
		Aliases:   []string{"deepseek-v4-flash", "deepseek-v4-pro"},
	}}}
	body, err := policy.CanonicalizeCatalog([]byte(`{"data":[{"id":"deepseek-v4-flash"},{"id":"deepseek-v4-pro"},{"id":"deepseek-v4.1-flash"},{"id":"other"}]}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"data":[{"id":"deepseek-v4.1-flash"},{"id":"other"}]}`, string(body))
}

func TestDeepSeekV4FamilyUsesAccountSpecificFallback(t *testing.T) {
	policy := &ModelAliasPolicy{Groups: []ModelAliasGroup{{
		Canonical: "deepseek-v4.1-flash",
		Aliases: []string{
			"deepseek-v4.1-flash",
			"deepseek-v4-flash",
			"deepseek-v4-pro",
		},
	}}}
	ctx := WithModelAliases(context.Background(), policy)

	for _, test := range []struct {
		name     string
		models   []string
		expected string
	}{
		{name: "v4.1 preferred", models: []string{"deepseek-v4.1-flash", "deepseek-v4-flash", "deepseek-v4-pro"}, expected: "deepseek-v4.1-flash"},
		{name: "v4 flash fallback", models: []string{"deepseek-v4-flash", "deepseek-v4-pro"}, expected: "deepseek-v4-flash"},
		{name: "v4 pro fallback", models: []string{"deepseek-v4-pro"}, expected: "deepseek-v4-pro"},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := &Account{
				Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"model_mapping": testModelMapping(test.models...)},
			}
			account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
				Source: "upstream", SyncedAt: time.Now().UTC().Format(time.RFC3339), Models: test.models,
			})

			routed := accountWithModelAliases(ctx, account)
			require.True(t, routed.IsModelSupported("deepseek-v4.1-flash"))
			require.Equal(t, test.expected, routed.GetMappedModel("deepseek-v4.1-flash"))
		})
	}
}
