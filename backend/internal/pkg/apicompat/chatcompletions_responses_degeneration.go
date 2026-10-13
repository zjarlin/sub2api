package apicompat

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ChatDegeneration 描述一次被判定为"退化重复"的流式输出：上游模型卡在重复里反复
// 吐同一段文本，直到 max_output_tokens 才停。
//
// 实测样本：
//   - 2026-10-12 15:32 cline-pass 转发的 deepseek-v4.1-flash，36 rune 周期复读
//     131324 字符 / 65536 输出 token / 515 秒；
//   - 2026-10-13 11:1x deepseek-harness 走同一 relay，逐行复读
//     `Let me write. / Go. / Now. / OK. / Emit.`（周期约 100 rune，远超旧上限 48）。
//
// 所以检测分两条通道：先看尾部是否为"短周期重复"，再看尾部若干行是否只由极少数短行
// 反复组成（覆盖周期较长、或准周期交替的复读）。
type ChatDegeneration struct {
	// Kind 是命中的检测通道："period"（尾部短周期重复）或 "line"（行级复读）。
	Kind string
	// Channel 是判定命中的输出通道："reasoning"（DeepSeek 式思考通道）或 "message"。
	Channel string
	// PeriodRunes 是尾部最小重复周期（rune 数），仅 period 通道有效。
	PeriodRunes int
	// Repeats 是判定窗口内完整重复的次数（period）或参与判定的行数（line）。
	Repeats int
	// DistinctLines 是行级检测里判定窗口内的不同短行数，仅 line 通道有效。
	DistinctLines int
	// TotalRunes 是判定时该通道累积的 rune 数。
	TotalRunes int
}

// ChatDegenerationGuard 是退化重复检测的阈值集合。零值不可用，用
// DefaultChatDegenerationGuard 取值再按需覆盖。
//
// 两条通道都刻意保守：period 通道要求"尾部窗口是短周期（<= MaxPeriod rune）连续完整
// 重复至少 MinRepeats 次"，且重复单元含字母/数字与至少 3 个不同 rune；line 通道要求
// "尾部至少 LineMinCount 个非空行只由 3..LineMaxDistinct 种短行组成，且平均每种重复
// 至少 6 次"。因此普通排版（分隔线、空行、表格边框、逐条不同的正文）不会误判。
type ChatDegenerationGuard struct {
	// MinTotalRunes 是启动检测的最小累积长度（rune）。
	MinTotalRunes int
	// WindowRunes 是 period 通道的尾部检测窗口长度（rune）。
	WindowRunes int
	// MinPeriod / MaxPeriod 是接受的重复周期范围（rune）。
	MinPeriod int
	MaxPeriod int
	// MinRepeats 是 period 通道窗口内需要完整重复的最少次数。
	MinRepeats int
	// CheckEveryBytes 是增量检测的步长：累积长度每增长这么多字节才做一次分析，
	// 避免每个 chunk 都复制整段累积文本。
	CheckEveryBytes int

	// LineWindow 是 line 通道从尾部取样的最大非空行数。
	LineWindow int
	// LineMinCount 是 line 通道要求的最少非空行数。
	LineMinCount int
	// LineMaxDistinct 是 line 通道允许的不同短行数上限。
	LineMaxDistinct int
	// LineMaxRunes 是 line 通道里"短行"的最大 rune 数。
	LineMaxRunes int
	// LineTailBytes 是 line 通道取样时读取的尾部字节数。
	LineTailBytes int
}

// DefaultChatDegenerationGuard 返回生产默认阈值。
func DefaultChatDegenerationGuard() ChatDegenerationGuard {
	return ChatDegenerationGuard{
		MinTotalRunes:   12000,
		WindowRunes:     4608,
		MinPeriod:       4,
		MaxPeriod:       192,
		MinRepeats:      24,
		CheckEveryBytes: 8192,

		LineWindow:      96,
		LineMinCount:    48,
		LineMaxDistinct: 10,
		LineMaxRunes:    48,
		LineTailBytes:   16 << 10,
	}
}

// Normalize 补齐非正数字段，避免零值守卫直接生效。
func (g ChatDegenerationGuard) Normalize() ChatDegenerationGuard {
	def := DefaultChatDegenerationGuard()
	if g.MinTotalRunes <= 0 {
		g.MinTotalRunes = def.MinTotalRunes
	}
	if g.WindowRunes <= 0 {
		g.WindowRunes = def.WindowRunes
	}
	if g.MinPeriod <= 0 {
		g.MinPeriod = def.MinPeriod
	}
	if g.MaxPeriod <= 0 {
		g.MaxPeriod = def.MaxPeriod
	}
	if g.MaxPeriod < g.MinPeriod {
		g.MaxPeriod = g.MinPeriod
	}
	if g.MinRepeats <= 0 {
		g.MinRepeats = def.MinRepeats
	}
	if g.CheckEveryBytes <= 0 {
		g.CheckEveryBytes = def.CheckEveryBytes
	}
	if g.WindowRunes < g.MaxPeriod*g.MinRepeats {
		g.WindowRunes = g.MaxPeriod * g.MinRepeats
	}
	if g.LineWindow <= 0 {
		g.LineWindow = def.LineWindow
	}
	if g.LineMinCount <= 0 {
		g.LineMinCount = def.LineMinCount
	}
	if g.LineMaxDistinct <= 0 {
		g.LineMaxDistinct = def.LineMaxDistinct
	}
	if g.LineMaxRunes <= 0 {
		g.LineMaxRunes = def.LineMaxRunes
	}
	if g.LineTailBytes <= 0 {
		g.LineTailBytes = def.LineTailBytes
	}
	if g.LineMinCount < g.LineMaxDistinct {
		g.LineMinCount = g.LineMaxDistinct
	}
	return g
}

// Detect 在累积文本上做一次退化检测；返回 nil 表示未命中。
//
// 累积长度用一次线性扫描计数；period 通道只看尾部 WindowRunes 个 rune，line 通道只看
// 尾部 LineTailBytes 字节，所以代价与累积长度无关。
func (g ChatDegenerationGuard) Detect(text, channel string) *ChatDegeneration {
	g = g.Normalize()
	totalRunes := utf8.RuneCountInString(text)
	if totalRunes < g.MinTotalRunes {
		return nil
	}
	if detected := g.detectPeriod(tailRunes(text, g.WindowRunes), channel, totalRunes); detected != nil {
		return detected
	}
	return g.detectLineRepetition(lastBytes(text, g.LineTailBytes), channel, totalRunes)
}

// detectPeriod 检查尾部窗口是否为短周期重复。
func (g ChatDegenerationGuard) detectPeriod(window []rune, channel string, totalRunes int) *ChatDegeneration {
	for period := g.MinPeriod; period <= g.MaxPeriod; period++ {
		need := period * g.MinRepeats
		if len(window) < need {
			return nil
		}
		ok := true
		for i := len(window) - 1; i >= len(window)-need; i-- {
			if window[i] != window[i-period] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if !degenerateUnit(window[len(window)-period:]) {
			return nil
		}
		return &ChatDegeneration{
			Kind:        "period",
			Channel:     channel,
			PeriodRunes: period,
			Repeats:     len(window) / period,
			TotalRunes:  totalRunes,
		}
	}
	return nil
}

// detectLineRepetition 检查尾部若干非空行是否只由极少数短行反复组成。
//
// 这条通道覆盖 period 通道抓不到的形态：周期超过 MaxPeriod 的逐行复读（例如
// `Let me write. / Go. / Now. / OK. / Emit.` 交替重复），以及不完全严格周期的交替。
func (g ChatDegenerationGuard) detectLineRepetition(tail, channel string, totalRunes int) *ChatDegeneration {
	raw := strings.Split(tail, "\n")
	lines := make([]string, 0, g.LineWindow)
	for i := len(raw) - 1; i >= 0 && len(lines) < g.LineWindow; i-- {
		line := strings.TrimSpace(raw[i])
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) < g.LineMinCount {
		return nil
	}

	distinct := make(map[string]struct{}, g.LineMaxDistinct+1)
	for _, line := range lines {
		if utf8.RuneCountInString(line) > g.LineMaxRunes || !degenerateUnit([]rune(line)) {
			return nil
		}
		distinct[line] = struct{}{}
		if len(distinct) > g.LineMaxDistinct {
			return nil
		}
	}
	if len(distinct) < 3 {
		return nil
	}
	// 每种短行平均至少重复 6 次，避免把"少量短行 + 大量不同内容"的正常输出误判。
	if len(lines) < len(distinct)*6 {
		return nil
	}
	return &ChatDegeneration{
		Kind:          "line",
		Channel:       channel,
		Repeats:       len(lines),
		DistinctLines: len(distinct),
		TotalRunes:    totalRunes,
	}
}

// degenerateUnit 判断文本是否像"模型退化"而不是正常排版：必须含字母/数字（含 CJK），
// 且至少有 3 个不同的 rune。
func degenerateUnit(unit []rune) bool {
	if len(unit) == 0 {
		return false
	}
	distinct := make(map[rune]struct{}, len(unit))
	hasWord := false
	for _, r := range unit {
		distinct[r] = struct{}{}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			hasWord = true
		}
	}
	return hasWord && len(distinct) >= 3
}

// tailRunes 返回 s 末尾最多 n 个 rune；从尾部按 rune 反向解码，代价只与 n 有关。
func tailRunes(s string, n int) []rune {
	if n <= 0 || s == "" {
		return nil
	}
	out := make([]rune, 0, n)
	for len(s) > 0 && len(out) < n {
		r, size := utf8.DecodeLastRuneInString(s)
		out = append(out, r)
		s = s[:len(s)-size]
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// lastBytes 返回 s 末尾最多 n 字节，并丢掉被切断的首个残缺 rune。
func lastBytes(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	tail := s[len(s)-n:]
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return tail
}
