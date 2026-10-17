//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestModelSystemPromptPersistenceAndValidation(t *testing.T) {
	ctx := context.Background()
	repo := newMockSettingRepo()
	settings := NewSettingService(repo, nil)
	policy := &ModelSystemPromptPolicy{Entries: []ModelSystemPromptEntry{{Model: "deepseek-v4-pro", Prompt: "始终使用简体中文回答。"}}}
	require.NoError(t, settings.SetModelSystemPromptPolicy(ctx, policy))
	read, err := settings.GetModelSystemPromptPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, policy, read)
	for _, bad := range []*ModelSystemPromptPolicy{
		{Entries: []ModelSystemPromptEntry{{Model: " x ", Prompt: "p"}}},
		{Entries: []ModelSystemPromptEntry{{Model: "x", Prompt: ""}}},
		{Entries: []ModelSystemPromptEntry{{Model: "x*", Prompt: "p"}}},
		{Entries: []ModelSystemPromptEntry{{Model: "x", Prompt: "a"}, {Model: "x", Prompt: "b"}}},
	} {
		require.Error(t, bad.Validate())
	}
	require.Empty(t, (&ModelSystemPromptPolicy{}).PromptFor("deepseek-v4-pro"))
	require.Equal(t, "始终使用简体中文回答。", policy.PromptFor("deepseek-v4-pro"))
	require.Empty(t, policy.PromptFor("deepseek-v4-flash"))
}

// 与模型别名同入口的提示词策略在 DB 瞬时故障时同样沿用最近一次成功快照。
func TestModelSystemPromptPolicySurvivesSettingsReadFailure(t *testing.T) {
	ctx := context.Background()
	repo := newMockSettingRepo()
	settings := NewSettingService(repo, nil)
	policy := &ModelSystemPromptPolicy{Entries: []ModelSystemPromptEntry{{Model: "deepseek-v4-pro", Prompt: "始终使用简体中文回答。"}}}
	require.NoError(t, settings.SetModelSystemPromptPolicy(ctx, policy))
	_, err := settings.GetModelSystemPromptPolicy(ctx)
	require.NoError(t, err)

	repo.getValueErr = errors.New("the database system is in recovery mode")
	degraded, err := settings.GetModelSystemPromptPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, "始终使用简体中文回答。", degraded.PromptFor("deepseek-v4-pro"))
}

// 冷启动读库失败与非法配置都必须显式报错，不能被降级掩盖。
func TestModelSystemPromptPolicyFailsClosedWithoutSnapshot(t *testing.T) {
	repo := newMockSettingRepo()
	repo.getValueErr = errors.New("the database system is in recovery mode")
	_, err := NewSettingService(repo, nil).GetModelSystemPromptPolicy(context.Background())
	require.Error(t, err)

	repo = newMockSettingRepo()
	settings := NewSettingService(repo, nil)
	require.NoError(t, settings.SetModelSystemPromptPolicy(context.Background(), &ModelSystemPromptPolicy{Entries: []ModelSystemPromptEntry{{Model: "m", Prompt: "p"}}}))
	_, err = settings.GetModelSystemPromptPolicy(context.Background())
	require.NoError(t, err)
	repo.data[SettingKeyModelSystemPrompts] = `{bad`
	_, err = settings.GetModelSystemPromptPolicy(context.Background())
	require.Error(t, err)
}

func TestInjectModelSystemPromptChatCompletions(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"system","content":"client"},{"role":"user","content":"hi"}]}`)
	updated, changed := InjectModelSystemPrompt(body, "始终使用简体中文回答。")
	require.True(t, changed)
	require.Equal(t, int64(3), gjson.GetBytes(updated, "messages.#").Int())
	require.Equal(t, "system", gjson.GetBytes(updated, "messages.0.role").String())
	require.Equal(t, "client", gjson.GetBytes(updated, "messages.0.content").String())
	require.Equal(t, "始终使用简体中文回答。", gjson.GetBytes(updated, "messages.1.content").String())
	require.Equal(t, "user", gjson.GetBytes(updated, "messages.2.role").String())

	// 无 system 时插到最前面。
	plain := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	updated, changed = InjectModelSystemPrompt(plain, "中文")
	require.True(t, changed)
	require.Equal(t, "system", gjson.GetBytes(updated, "messages.0.role").String())
	require.Equal(t, "user", gjson.GetBytes(updated, "messages.1.role").String())

	// 空提示词或非法 JSON 不改写。
	_, changed = InjectModelSystemPrompt(body, "   ")
	require.False(t, changed)
	_, changed = InjectModelSystemPrompt([]byte("{bad"), "中文")
	require.False(t, changed)
}

func TestInjectModelSystemPromptResponsesAndMessages(t *testing.T) {
	// Responses instructions 合并。
	body := []byte(`{"model":"m","instructions":"existing","input":[{"role":"user","content":"hi"}]}`)
	updated, changed := InjectModelSystemPrompt(body, "中文")
	require.True(t, changed)
	require.Equal(t, "existing\n\n中文", gjson.GetBytes(updated, "instructions").String())

	// Responses input 数组。
	body = []byte(`{"model":"m","input":[{"role":"user","content":"hi"}]}`)
	updated, changed = InjectModelSystemPrompt(body, "中文")
	require.True(t, changed)
	require.Equal(t, "system", gjson.GetBytes(updated, "input.0.role").String())

	// Anthropic Messages 字符串 system。
	body = []byte(`{"model":"m","system":"base","messages":[{"role":"user","content":"hi"}]}`)
	updated, changed = InjectModelSystemPrompt(body, "中文")
	require.True(t, changed)
	require.Equal(t, "base\n\n中文", gjson.GetBytes(updated, "system").String())

	// Anthropic Messages system 数组。
	body = []byte(`{"model":"m","system":[{"type":"text","text":"base"}],"messages":[]}`)
	updated, changed = InjectModelSystemPrompt(body, "中文")
	require.True(t, changed)
	require.Equal(t, int64(2), gjson.GetBytes(updated, "system.#").Int())
	require.Equal(t, "中文", gjson.GetBytes(updated, "system.1.text").String())
}
