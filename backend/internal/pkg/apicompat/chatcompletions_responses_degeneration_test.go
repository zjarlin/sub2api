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

// degenerateMarkupCycle 是 2026-10-23 事故样本的重复单元：上游把一次 wait_agent 调用
// 写坏后，无限复读一串闭合标签。周期 53 rune 超出生产 MaxPeriod(48)，且上游本身
// 不保证发换行（客户端看到的是软折行），period / line 两条通道都会漏掉。
const degenerateMarkupCycle = "</invoke> </parameter> </invoke> </invoke> </invoke> "

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

// TestChatDegenerationGuard_DetectsMarkupFloodWithoutNewlines 固定 2026-10-23 事故形态：
// 复读的闭合标签连成一行（上游没有任何换行），period 通道因周期超限、line 通道因
// 取不到行而双双失效，必须由 markup 通道兜住。
func TestChatDegenerationGuard_DetectsMarkupFloodWithoutNewlines(t *testing.T) {
	// 生产阈值：MaxPeriod=48 < 单元周期 53，且窗口只有 2048 rune。
	guard := ChatDegenerationGuard{
		MinTotalRunes:    12000,
		WindowRunes:      2048,
		MinPeriod:        4,
		MaxPeriod:        48,
		MinRepeats:       40,
		CheckEveryBytes:  8192,
		LineWindow:       96,
		LineMinCount:     48,
		LineMaxDistinct:  10,
		LineMaxRunes:     48,
		LineTailBytes:    16 << 10,
		MarkupTailBytes:  16 << 10,
		MarkupMinCloses:  120,
		MarkupMinDensity: 0.55,
	}.Normalize()

	text := strings.Repeat(degenerateMarkupCycle, 900)
	require.Greater(t, len([]rune(degenerateMarkupCycle)), guard.MaxPeriod,
		"前提：单元周期必须超出 period 通道上限，period 通道才抓不到")
	require.GreaterOrEqual(t, len([]rune(text)), guard.MinTotalRunes)

	detected := guard.Detect(text, "message")
	require.NotNil(t, detected, "连成一行的闭合标签复读必须被判定为退化重复")
	require.Equal(t, "markup", detected.Kind)
	require.Equal(t, "message", detected.Channel)
	require.GreaterOrEqual(t, detected.MarkupCloses, guard.MarkupMinCloses)
}

// TestChatDegenerationGuard_MarkupFloodSurvivesSoftWrapping 覆盖"上游不换行、客户端
// 软折行"的真实形态：把同一段复读插进不同数量的换行，都必须命中同一类判定。
func TestChatDegenerationGuard_MarkupFloodSurvivesSoftWrapping(t *testing.T) {
	guard := ChatDegenerationGuard{MinTotalRunes: 12000, MaxPeriod: 48, MinRepeats: 40, WindowRunes: 2048}.Normalize()
	cases := map[string]string{
		"无换行":  strings.Repeat(degenerateMarkupCycle, 900),
		"周期换行": repeatLines([]string{degenerateMarkupCycle}, 900),
		"夹空行":  strings.Repeat(degenerateMarkupCycle+"\n\n", 900),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			detected := guard.Detect(text, "message")
			require.NotNil(t, detected, "闭合标签复读不得因为换行形态不同而漏判")
		})
	}
}

// TestChatDegenerationGuard_KeepsLegitimateMarkup 确认 markup 通道不会把正常的工具
// 标记正文误判：一次真实调用、成对开闭、密度低。
func TestChatDegenerationGuard_KeepsLegitimateMarkup(t *testing.T) {
	guard := ChatDegenerationGuard{MinTotalRunes: 12000, MaxPeriod: 48, MinRepeats: 40, WindowRunes: 2048}.Normalize()
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("<|DSML|invoke name=\"exec\"><|DSML|parameter name=\"input\" string=\"true\">pwd</|DSML|parameter></|DSML|invoke>\n")
	}
	require.GreaterOrEqual(t, len([]rune(b.String())), guard.MinTotalRunes)
	// 每行都是完整成对的调用，闭合标签密度不到一半；即便被判退化也应是别的通道，
	// 这里只确认 markup 通道本身不误判。
	require.Nil(t, guard.detectMarkupFlood(b.String(), "message", len([]rune(b.String()))))
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
