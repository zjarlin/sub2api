package apicompat

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// degenerateTailRunes 是 2026-10-12 事故样本的重复单元（36 个 rune）：cline-pass 转发的
// deepseek-v4.1-flash 在 35 万 token 上下文下卡在这个周期上复读，直到 65536 输出上限。
const degenerateTailRunes = "。\n\n（写。）\n\n我输出。\n\n写。\n\n（写。）\n\n我输出工具调用。\n\n写"

// degenerateLineCycle 是 2026-10-13 事故样本的逐行复读单元（deepseek-harness 走
// cline-pass relay 的 deepseek-v4.1-flash）：`Let me write. / Go. / Now. / OK. / Emit.`
// 反复交替。单行很短，但整段周期超过 MaxPeriod 时只能靠行级通道抓到。
var degenerateLineCycle = []string{"Let me write.", "Go.", "Now.", "OK.", "Emit.", "OK."}

// degenerateLongLineCycle 是同一形态的长行版本：整段周期约 300 rune，超出 MaxPeriod，
// period 通道必然抓不到，用于固定行级通道的独立性。
var degenerateLongLineCycle = []string{
	"Let me write the patch to the file now.",
	"Go ahead and emit the write tool call.",
	"Now write the requested file to disk.",
	"OK, emitting the write tool call now.",
	"Emit the write tool call immediately.",
}

func testDegenerationGuard() ChatDegenerationGuard {
	return ChatDegenerationGuard{
		MinTotalRunes:   12000,
		WindowRunes:     4096,
		MinPeriod:       4,
		MaxPeriod:       192,
		MinRepeats:      24,
		CheckEveryBytes: 4096,
		LineWindow:      96,
		LineMinCount:    48,
		LineMaxDistinct: 10,
		LineMaxRunes:    48,
		LineTailBytes:   16 << 10,
	}
}

func repeatLines(cycle []string, times int) string {
	parts := make([]string, 0, len(cycle)*times)
	for i := 0; i < times; i++ {
		parts = append(parts, cycle...)
	}
	return strings.Join(parts, "\n") + "\n"
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

func TestChatDegenerationGuard_DetectsLineLevelRepetition(t *testing.T) {
	guard := testDegenerationGuard().Normalize()
	cycleRunes := len([]rune(repeatLines(degenerateLongLineCycle, 1)))
	require.Greater(t, cycleRunes, guard.MaxPeriod, "前提：整段周期必须超出 period 通道的检测范围")

	// 重复次数取到累积长度超过 MinTotalRunes，保证走的是完整检测入口。
	repeats := guard.MinTotalRunes/cycleRunes + 10
	text := "Let me start.\n\n" + repeatLines(degenerateLongLineCycle, repeats)
	require.GreaterOrEqual(t, len([]rune(text)), guard.MinTotalRunes)
	detected := guard.Detect(text, "reasoning")
	require.NotNil(t, detected, "逐行复读到输出上限的流必须被判定为退化重复")
	require.Equal(t, "line", detected.Kind)
	require.Equal(t, "reasoning", detected.Channel)
	require.GreaterOrEqual(t, detected.Repeats, guard.LineMinCount)
	require.LessOrEqual(t, detected.DistinctLines, guard.LineMaxDistinct)
}

func TestChatDegenerationGuard_KeepsLegitimateOutput(t *testing.T) {
	guard := testDegenerationGuard()

	varied := make([]string, 0, 2000)
	for i := 0; i < 2000; i++ {
		varied = append(varied, "- 第 "+string(rune('一'+i%40))+" 项结论：检查 "+strings.Repeat("轨迹", i%7+1)+" 的中间状态")
	}
	cases := map[string]string{
		"变异正文":      strings.Join(varied, "\n"),
		"同一字符长串":    strings.Repeat("a", 20000),
		"空行重复":      strings.Repeat("\n", 20000),
		"表格分隔线重复":   strings.Repeat("|---|---|\n", 1500),
		"空白列表项重复":   strings.Repeat("- [ ] \n", 3000),
		"短复读未达门槛":   strings.Repeat("写。\n\n（写。）\n\n", 20),
		"长复读但周期过长":  strings.Repeat(strings.Repeat("x", 200)+"y", 60),
		"行级复读未达门槛":  repeatLines(degenerateLineCycle, 3),
		"短行很多但各不相同": distinctShortLines(400),
		"编号清单各不相同":  numberedSteps(400),
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
	require.GreaterOrEqual(t, bumped.LineMinCount, bumped.LineMaxDistinct, "行级窗口至少要能装下所有不同短行")

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

// distinctShortLines 生成大量"短但互不相同"的行，用于确认行级通道不误判。
func distinctShortLines(n int) string {
	lines := make([]string, 0, n)
	for i := 0; i < n; i++ {
		lines = append(lines, fmt.Sprintf("step-%d", i))
	}
	return strings.Join(lines, "\n")
}

// numberedSteps 生成"短行 + 各不相同的说明"，模拟正常的编号清单输出。
func numberedSteps(n int) string {
	lines := make([]string, 0, n)
	for i := 0; i < n; i++ {
		lines = append(lines, fmt.Sprintf("%d. 第 %d 项检查结论", i+1, i+1))
	}
	return strings.Join(lines, "\n")
}
