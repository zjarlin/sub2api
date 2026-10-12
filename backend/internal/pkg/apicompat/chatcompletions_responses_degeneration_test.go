package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// degenerateTailRunes 是 2026-10-12 事故样本的重复单元（36 个 rune）：cline-pass 转发的
// deepseek-v4.1-flash 在 35 万 token 上下文下卡在这个周期上复读，直到 65536 输出上限。
const degenerateTailRunes = "。\n\n（写。）\n\n我输出。\n\n写。\n\n（写。）\n\n我输出工具调用。\n\n写"

func testDegenerationGuard() ChatDegenerationGuard {
	return ChatDegenerationGuard{
		MinTotalRunes:   12000,
		WindowRunes:     2048,
		MinPeriod:       4,
		MaxPeriod:       48,
		MinRepeats:      40,
		CheckEveryBytes: 4096,
	}
}

func TestChatDegenerationGuard_DetectsProductionRunawayRepetition(t *testing.T) {
	guard := testDegenerationGuard()
	text := "关键发现！先看这段分析。\n\n" + strings.Repeat(degenerateTailRunes, 400)

	detected := guard.Detect(text, "reasoning")
	require.NotNil(t, detected, "复读到输出上限的流必须被判定为退化重复")
	require.Equal(t, "reasoning", detected.Channel)
	require.Equal(t, len([]rune(degenerateTailRunes)), detected.PeriodRunes)
	require.GreaterOrEqual(t, detected.Repeats, guard.MinRepeats)
}

func TestChatDegenerationGuard_KeepsLegitimateOutput(t *testing.T) {
	guard := testDegenerationGuard()

	varied := make([]string, 0, 2000)
	for i := 0; i < 2000; i++ {
		varied = append(varied, "- 第 "+string(rune('一'+i%40))+" 项结论：检查 "+strings.Repeat("轨迹", i%7+1)+" 的中间状态")
	}
	cases := map[string]string{
		"变异正文":     strings.Join(varied, "\n"),
		"同一字符长串":   strings.Repeat("a", 20000),
		"空行重复":     strings.Repeat("\n", 20000),
		"表格分隔线重复":  strings.Repeat("|---|---|\n", 1500),
		"空白列表项重复":  strings.Repeat("- [ ] \n", 3000),
		"短复读未达门槛":  strings.Repeat("写。\n\n（写。）\n\n", 20),
		"长复读但周期过长": strings.Repeat(strings.Repeat("x", 200)+"y", 60),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			require.Nil(t, guard.Detect(text, "message"), "正常输出不得被误判为退化重复")
		})
	}
}

func TestChatDegenerationGuard_NormalizeFillsDefaults(t *testing.T) {
	normalized := ChatDegenerationGuard{}.Normalize()
	require.Equal(t, DefaultChatDegenerationGuard(), normalized)

	bumped := ChatDegenerationGuard{WindowRunes: 16, MaxPeriod: 48, MinRepeats: 40}.Normalize()
	require.GreaterOrEqual(t, bumped.WindowRunes, 48*40, "窗口必须容得下 MaxPeriod*MinRepeats 个 rune")

	repaired := ChatDegenerationGuard{MinPeriod: 20, MaxPeriod: 4}.Normalize()
	require.Equal(t, 20, repaired.MaxPeriod)
}

// TestStreamState_DetectDegenerationFollowsChunkFlow 覆盖 chunk → delta 的累积路径：
// 连续下发的 reasoning_content 复读必须在累积到阈值后被识别，且判定结果被记住。
func TestStreamState_DetectDegenerationFollowsChunkFlow(t *testing.T) {
	guard := testDegenerationGuard()
	state := NewChatCompletionsToResponsesStreamState("deepseek-v4.1-flash")
	state.DegenerationGuard = &guard
	_, _ = state.Reasoning.WriteString("关键发现！先看这段分析。\n\n")

	var detected *ChatDegeneration
	for i := 0; i < 900 && detected == nil; i++ {
		payload, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"reasoning_content": degenerateTailRunes}}},
		})
		require.NoError(t, err)
		var chunk ChatCompletionsChunk
		require.NoError(t, json.Unmarshal(payload, &chunk))
		ChatCompletionsChunkToResponsesEvents(&chunk, state)
		detected = state.DetectDegeneration()
	}

	require.NotNil(t, detected, "累积到阈值的复读流必须命中")
	require.Equal(t, state.Degeneration, state.DetectDegeneration(), "命中结果必须被记住")
}

func TestStreamState_DetectDegenerationDisabledWithoutGuard(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("deepseek-v4.1-flash")
	_, _ = state.Reasoning.WriteString(strings.Repeat(degenerateTailRunes, 400))
	require.Nil(t, state.DetectDegeneration(), "未注入守卫时不做检测")
	require.Nil(t, state.Degeneration)
}
