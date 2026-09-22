//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 映射表是混合调度的单一权威来源；后续新增平台（如 zcode）只需扩展它。
func TestMixedSchedulingMapping(t *testing.T) {
	require.True(t, SupportsMixedScheduling(PlatformAntigravity))
	require.True(t, SupportsMixedScheduling(PlatformTraework))
	require.True(t, SupportsMixedScheduling(PlatformWorkbuddy))
	require.False(t, SupportsMixedScheduling(PlatformOpenAI))
	require.False(t, SupportsMixedScheduling(PlatformAnthropic))

	// 反向查询：哪些来源平台可加入 openai 分组。
	require.Equal(t, []string{PlatformTraework, PlatformWorkbuddy, PlatformZcode}, MixedSchedulingSourcePlatforms(PlatformOpenAI))
	require.Equal(t, []string{PlatformAntigravity}, MixedSchedulingSourcePlatforms(PlatformAnthropic))
	require.Equal(t, []string{PlatformAntigravity}, MixedSchedulingSourcePlatforms(PlatformGemini))
	require.Empty(t, MixedSchedulingSourcePlatforms(PlatformGrok))

	require.Equal(t, []string{PlatformOpenAI}, MixedSchedulingTargetPlatforms(PlatformTraework))
	require.Equal(t, []string{PlatformAnthropic, PlatformGemini}, MixedSchedulingTargetPlatforms(PlatformAntigravity))
}

func mixedSchedulingAccount(platform string, enabled bool) *Account {
	extra := map[string]any{}
	if enabled {
		extra["mixed_scheduling"] = true
	}
	return &Account{Platform: platform, Extra: extra, Status: StatusActive, Schedulable: true}
}

func TestIsMixedSchedulingEnabledByPlatform(t *testing.T) {
	require.True(t, mixedSchedulingAccount(PlatformTraework, true).IsMixedSchedulingEnabled())
	require.True(t, mixedSchedulingAccount(PlatformWorkbuddy, true).IsMixedSchedulingEnabled())
	require.True(t, mixedSchedulingAccount(PlatformAntigravity, true).IsMixedSchedulingEnabled())
	require.False(t, mixedSchedulingAccount(PlatformTraework, false).IsMixedSchedulingEnabled())
	// 不支持的平台即便写入开关也不生效。
	require.False(t, mixedSchedulingAccount(PlatformOpenAI, true).IsMixedSchedulingEnabled())
	require.False(t, mixedSchedulingAccount(PlatformKimi, true).IsMixedSchedulingEnabled())
}

// OpenAI/Codex 分组：启用混合调度的 traework/workbuddy 账号可被选中，未启用则被过滤。
func TestOpenAIAccountMatchesPlatformWithMixedScheduling(t *testing.T) {
	require.True(t, openAIAccountMatchesPlatform(&Account{Platform: PlatformOpenAI}, PlatformOpenAI))
	require.True(t, openAIAccountMatchesPlatform(mixedSchedulingAccount(PlatformTraework, true), PlatformOpenAI))
	require.True(t, openAIAccountMatchesPlatform(mixedSchedulingAccount(PlatformWorkbuddy, true), PlatformOpenAI))
	require.False(t, openAIAccountMatchesPlatform(mixedSchedulingAccount(PlatformTraework, false), PlatformOpenAI))
	require.False(t, openAIAccountMatchesPlatform(mixedSchedulingAccount(PlatformWorkbuddy, false), PlatformOpenAI))
	// traework 账号不能服务 anthropic 分组。
	require.False(t, openAIAccountMatchesPlatform(mixedSchedulingAccount(PlatformTraework, true), PlatformAnthropic))
}

// 调度（selectByLoadBalance 与 sticky 命中）必须用与 openAIAccountMatchesPlatform
// 一致的平台判定，否则 traework/workbuddy 账号会在调度阶段被 platform_mismatch 过滤。
func TestSchedulerPlatformMatchUsesMixedMapping(t *testing.T) {
	require.True(t, openAIAccountMatchesPlatform(mixedSchedulingAccount(PlatformTraework, true), PlatformOpenAI))
	require.True(t, openAIAccountMatchesPlatform(mixedSchedulingAccount(PlatformWorkbuddy, true), PlatformOpenAI))
	require.False(t, openAIAccountMatchesPlatform(mixedSchedulingAccount(PlatformTraework, false), PlatformOpenAI))
	// 原生 openai 账号不受影响。
	require.True(t, openAIAccountMatchesPlatform(&Account{Platform: PlatformOpenAI}, PlatformOpenAI))
	// 非兼容平台仍被拒绝。
	require.False(t, openAIAccountMatchesPlatform(&Account{Platform: PlatformKimi}, PlatformOpenAI))
}
