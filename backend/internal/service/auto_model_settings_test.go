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
	} {
		require.Error(t, policy.Validate())
	}
	for _, raw := range []string{`{bad`, `{}`, `{"blacklist":null}`, `{"blacklist":["a*b"]}`} {
		repo := newMockSettingRepo()
		repo.data[SettingKeyAutoModelPolicy] = raw
		_, err := NewSettingService(repo, nil).BindAutoModelRoutingPolicy(context.Background())
		require.Error(t, err)
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
	} {
		invalid := defaults
		mutate(&invalid)
		policy.VerticalRouting = &invalid
		require.Error(t, policy.Validate())
	}
}
