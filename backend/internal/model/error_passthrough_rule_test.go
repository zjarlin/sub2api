package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllPlatformsIncludesEveryConcretePlatform(t *testing.T) {
	require.ElementsMatch(t, []string{
		"anthropic",
		"openai",
		"gemini",
		"antigravity",
		"grok",
		"kimi",
		"zhipu",
		"deepseek",
		"minimax",
		"opencode_go",
		// 上游新增平台 + fork 适配器平台都在平台清单内，AllPlatforms 必须完整覆盖。
		"typesafe",
		"command_code",
		"cline",
		"deepseek_web",
		"madao",
		"arena",
		"cursor",
		"windsurf",
		"teleagent",
		"kilo",
		"doubao",
		"traework",
		"workbuddy",
		"vibex",
		"zcode",
		"qoder",
		"systemone",
		"model3d",
		"v0",
		"laya",
		"jev",
	}, AllPlatforms())
}
