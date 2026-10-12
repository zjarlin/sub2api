//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoModelBlacklistDefaultsAndPersistence(t *testing.T) {
	settings := NewSettingService(newMockSettingRepo(), nil)
	ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	for _, model := range []string{"doubao", "doubao-seed-2.0", "Doubao-pro", "volcengine/doubao-seed", "provider/nested/doubao-pro"} {
		require.False(t, AutoModelAllowed(ctx, model), model)
		require.True(t, AutoModelAllowed(context.Background(), model), model)
		require.Error(t, checkAutoModelUpstream(ctx, "gpt-5.5", model))
	}
	require.True(t, AutoModelAllowed(ctx, "gpt-5.5", "not-doubao"))
	askCtx := WithAutoModelRequestCapabilities(ctx, []byte(`{"model":"ask","input":"hello"}`))
	require.True(t, AutoModelAllowed(askCtx, "doubao-pro"))
	require.False(t, AutoModelAllowed(askCtx, "gpt-6-astra"))
	require.True(t, AutoModelPlatformAllowed(askCtx, PlatformDoubao))
	require.True(t, AutoModelPlatformAllowed(askCtx, PlatformDeepseekWeb))
	require.True(t, AutoModelPlatformAllowed(askCtx, PlatformCursor))
	require.False(t, AutoModelPlatformAllowed(ctx, PlatformDoubao))
	require.False(t, AutoModelPlatformAllowed(ctx, PlatformDeepseekWeb))
	require.False(t, AutoModelPlatformAllowed(ctx, PlatformCursor))
	require.NoError(t, settings.SetAutoModelPolicy(context.Background(), &AutoModelPolicy{Blacklist: []string{}}))
	loaded, err := settings.GetAutoModelPolicy(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{}, loaded.Blacklist)
	updated, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	require.True(t, AutoModelAllowed(updated, "doubao-seed"))
	require.False(t, AutoModelAllowed(updated, "gpt-6-astra"))
	rebound, err := settings.BindAutoModelRoutingPolicy(ctx)
	require.NoError(t, err)
	require.False(t, AutoModelAllowed(rebound, "doubao-seed"))
}

func TestAutoModelBlacklistAliasesAndConfiguredRules(t *testing.T) {
	settings := NewSettingService(newMockSettingRepo(), nil)
	require.NoError(t, settings.SetModelAliasPolicy(context.Background(), &ModelAliasPolicy{Groups: []ModelAliasGroup{
		{Canonical: "hidden-model", Aliases: []string{"doubao-seed", "safe-looking-name"}},
		{Canonical: "doubao-pro", Aliases: []string{"provider/other-name"}},
	}}))
	ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	for _, model := range []string{"hidden-model", "safe-looking-name", "provider/other-name"} {
		require.False(t, AutoModelAllowed(ctx, model), model)
	}
	require.NoError(t, settings.SetAutoModelPolicy(context.Background(), &AutoModelPolicy{Blacklist: []string{"gpt-5.5", "provider/private*"}}))
	ctx, err = settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	require.False(t, AutoModelAllowed(ctx, "gpt-5.5"))
	require.False(t, AutoModelAllowed(ctx, "provider/private-v2"))
	require.True(t, AutoModelAllowed(ctx, "gpt-5.5-mini", "other/private-v2", "doubao-pro"))
}

func TestAutoModelBlacklistInvalidPolicy(t *testing.T) {
	for _, policy := range []*AutoModelPolicy{
		nil, {}, {Blacklist: []string{""}}, {Blacklist: []string{"a b"}},
		{Blacklist: []string{"doubao*", "Doubao*"}}, {Blacklist: []string{"*doubao*"}},
		{Blacklist: []string{"a?"}}, {Blacklist: []string{strings.Repeat("a", 201)}},
		{Blacklist: make([]string, 129)},
		// 空白名单表示不限制，但一旦填写就沿用黑名单的规则约束。
		{Blacklist: []string{}, Whitelist: []string{""}}, {Blacklist: []string{}, Whitelist: []string{"a b"}},
		{Blacklist: []string{}, Whitelist: []string{"gpt-5.5", "GPT-5.5"}},
		{Blacklist: []string{}, Whitelist: []string{"*gpt*"}}, {Blacklist: []string{}, Whitelist: []string{"a?"}},
		{Blacklist: []string{}, Whitelist: []string{strings.Repeat("a", 201)}},
		{Blacklist: []string{}, Whitelist: make([]string, 129)},
	} {
		require.Error(t, policy.Validate())
	}
	for _, raw := range []string{`{bad`, `{}`, `{"blacklist":null}`, `{"blacklist":["a*b"]}`,
		`{"blacklist":[],"whitelist":["a*b"]}`, `{"blacklist":[],"whitelist":["a","a"]}`} {
		repo := newMockSettingRepo()
		repo.data[SettingKeyAutoModelPolicy] = raw
		_, err := NewSettingService(repo, nil).BindAutoModelRoutingPolicy(context.Background())
		require.Error(t, err)
	}
}

func TestAutoModelWhitelistRestrictsAutoButNotAsk(t *testing.T) {
	settings := NewSettingService(newMockSettingRepo(), nil)
	require.NoError(t, settings.SetAutoModelPolicy(context.Background(), &AutoModelPolicy{
		Blacklist: []string{"doubao*"},
		Whitelist: []string{"gpt-5.5", "provider/private*", "doubao*"},
	}))
	loaded, err := settings.GetAutoModelPolicy(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5.5", "provider/private*", "doubao*"}, loaded.Whitelist)
	ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	for _, model := range []string{"gpt-5.5", "provider/private-v2"} {
		require.True(t, AutoModelAllowed(ctx, model), model)
	}
	// 白名单命中但同时在黑名单中的模型仍被排除。
	require.False(t, AutoModelAllowed(ctx, "volcengine/doubao-seed"))
	for _, model := range []string{"glm-5.3", "claude-opus-4-7", "unknown-vendor/model"} {
		require.False(t, AutoModelAllowed(ctx, model), model)
	}
	// 黑名单仍优先于白名单：同时在两个列表中的模型不参与 Auto。
	require.False(t, AutoModelAllowed(ctx, "doubao-pro"))
	require.False(t, AutoModelAllowed(ctx, "gpt-5.5", "glm-5.3"))
	require.Error(t, checkAutoModelUpstream(ctx, "glm-5.3"))
	// ask 既不受白名单也不受黑名单限制，只保留最高档排除。
	askCtx := WithAutoModelRequestCapabilities(ctx, []byte(`{"model":"ask","input":"hello"}`))
	require.True(t, AutoModelAllowed(askCtx, "glm-5.3"))
	require.True(t, AutoModelAllowed(askCtx, "doubao-pro"))
	require.False(t, AutoModelAllowed(askCtx, "gpt-6-astra"))
	require.True(t, AutoModelWhitelisted(askCtx, "glm-5.3"))
	require.False(t, AutoModelWhitelisted(ctx, "glm-5.3"))
	// 清空白名单后恢复为不限制，仅黑名单与最高档继续生效。
	require.NoError(t, settings.SetAutoModelPolicy(context.Background(), &AutoModelPolicy{Blacklist: []string{"doubao*"}}))
	ctx, err = settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	require.True(t, AutoModelAllowed(ctx, "glm-5.3"))
	require.False(t, AutoModelAllowed(ctx, "doubao-pro"))
	require.True(t, AutoModelWhitelisted(ctx, "glm-5.3"))
}

func TestAutoModelWhitelistMatchesAliasesAndNamespaces(t *testing.T) {
	settings := NewSettingService(newMockSettingRepo(), nil)
	require.NoError(t, settings.SetModelAliasPolicy(context.Background(), &ModelAliasPolicy{Groups: []ModelAliasGroup{
		{Canonical: "hidden-model", Aliases: []string{"safe-looking-name"}},
		{Canonical: "other-model", Aliases: []string{"other-alias"}},
	}}))
	require.NoError(t, settings.SetAutoModelPolicy(context.Background(), &AutoModelPolicy{Blacklist: []string{}, Whitelist: []string{"safe-looking-name"}}))
	ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	// 别名组内任一名称命中白名单时，规范 ID 与别名都允许。
	for _, model := range []string{"hidden-model", "safe-looking-name"} {
		require.True(t, AutoModelAllowed(ctx, model), model)
	}
	for _, model := range []string{"other-model", "other-alias", "gpt-5.5"} {
		require.False(t, AutoModelAllowed(ctx, model), model)
	}
}

func TestVerticalRoutingPolicyDefaultsAndValidation(t *testing.T) {
	policy := DefaultAutoModelPolicy()
	defaults := policy.VerticalPolicy()
	require.True(t, defaults.Enabled)
	require.Equal(t, 0.8, defaults.MinConfidence)
	require.Equal(t, 1500, defaults.TimeoutMS)
	policy.VerticalRouting = &defaults
	require.NoError(t, policy.Validate())
	for _, mutate := range []func(*VerticalRoutingPolicy){
		func(v *VerticalRoutingPolicy) { v.MinConfidence = 0.79 },
		func(v *VerticalRoutingPolicy) { v.TimeoutMS = 0 },
		func(v *VerticalRoutingPolicy) { v.ImageModel = "gpt-image-*" },
		func(v *VerticalRoutingPolicy) { v.VideoModel = "invalid model" },
		func(v *VerticalRoutingPolicy) { v.ImageFallbackModels = []string{""} },
		func(v *VerticalRoutingPolicy) { v.ImageFallbackModels = []string{"wan2.7-image", "wan2.7-image"} },
		func(v *VerticalRoutingPolicy) { v.ImageFallbackModels = []string{"gpt-image-*"} },
		func(v *VerticalRoutingPolicy) { v.ImageFallbackModels = []string{"a", "b", "c", "d", "e"} },
	} {
		invalid := defaults
		mutate(&invalid)
		policy.VerticalRouting = &invalid
		require.Error(t, policy.Validate())
	}
}
