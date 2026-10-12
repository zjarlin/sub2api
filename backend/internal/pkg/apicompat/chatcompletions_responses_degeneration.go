package apicompat

import (
	"unicode"
	"unicode/utf8"
)

// ChatDegeneration 描述一次被判定为"退化重复"的流式输出：上游模型卡在一个短周期
// 里反复吐同一段文本（例如 `我输出工具调用。\n\n写。\n\n（写。）` 反复数千次），
// 直到 max_output_tokens 才停。
//
// 实测（2026-10-12，cline-pass 转发的 deepseek-v4.1-flash）：单次响应在 35 万 token
// 上下文下跑了 515 秒、吐了 65536 个输出 token 的纯重复文本，对用户毫无价值且按量
// 计费。所以在网关侧检测并掐断，而不是让它跑满上限。
type ChatDegeneration struct {
	// Channel 是判定命中的通道："reasoning"（DeepSeek 式思考通道）或 "message"。
	Channel string
	// PeriodRunes 是尾部最小重复周期（rune 数）。
	PeriodRunes int
	// Repeats 是检测窗口内该周期完整重复的次数。
	Repeats int
	// TotalRunes 是判定时该通道累积的 rune 数。
	TotalRunes int
}

// ChatDegenerationGuard 是退化重复检测的阈值集合。零值不可用，用
// DefaultChatDegenerationGuard 取值再按需覆盖。
//
// 阈值刻意保守：命中要求尾部窗口是"短周期（<= MaxPeriod 个 rune）连续完整重复至少
// MinRepeats 次"，并且重复单元本身含字母/数字与至少 3 个不同 rune。这样普通的重复
// 排版（分隔线、空行、表格边框）不会误判。
type ChatDegenerationGuard struct {
	// MinTotalRunes 是启动检测的最小累积长度（rune）。
	MinTotalRunes int
	// WindowRunes 是尾部检测窗口长度（rune）。
	WindowRunes int
	// MinPeriod / MaxPeriod 是接受的重复周期范围（rune）。
	MinPeriod int
	MaxPeriod int
	// MinRepeats 是窗口内需要完整重复的最少次数。
	MinRepeats int
	// CheckEveryBytes 是增量检测的步长：累积长度每增长这么多字节才做一次周期分析，
	// 避免每个 chunk 都复制整段累积文本。
	CheckEveryBytes int
}

// DefaultChatDegenerationGuard 返回生产默认阈值。
func DefaultChatDegenerationGuard() ChatDegenerationGuard {
	return ChatDegenerationGuard{
		MinTotalRunes:   12000,
		WindowRunes:     2048,
		MinPeriod:       4,
		MaxPeriod:       48,
		MinRepeats:      40,
		CheckEveryBytes: 4096,
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
	return g
}

// Detect 在累积文本上做一次退化检测；返回 nil 表示未命中。
//
// 只有尾部 WindowRunes 个 rune 参与周期分析，累积长度用一次线性扫描计数，避免为几十万
// 字符的累积文本反复分配 []rune。
func (g ChatDegenerationGuard) Detect(text, channel string) *ChatDegeneration {
	g = g.Normalize()
	totalRunes := utf8.RuneCountInString(text)
	if totalRunes < g.MinTotalRunes {
		return nil
	}
	return g.detectWindow(totalRunes, tailRunes(text, g.WindowRunes), channel)
}

// detectWindow 是周期分析本体：window 是文本尾部、totalRunes 是该通道的累积 rune 数。
func (g ChatDegenerationGuard) detectWindow(totalRunes int, window []rune, channel string) *ChatDegeneration {
	period := g.smallestPeriod(window)
	if period == 0 {
		return nil
	}
	if !degenerateUnit(window[len(window)-period:]) {
		return nil
	}
	return &ChatDegeneration{
		Channel:     channel,
		PeriodRunes: period,
		Repeats:     len(window) / period,
		TotalRunes:  totalRunes,
	}
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

// smallestPeriod 返回窗口内最小重复周期（rune），0 表示窗口不是"短周期重复"。
// 判定方式：对每个候选周期 p，要求尾部 p*MinRepeats 个 rune 满足 x[i] == x[i-p]。
func (g ChatDegenerationGuard) smallestPeriod(window []rune) int {
	for period := g.MinPeriod; period <= g.MaxPeriod; period++ {
		check := period * g.MinRepeats
		if len(window) < check {
			return 0
		}
		ok := true
		for i := len(window) - 1; i >= len(window)-check; i-- {
			if window[i] != window[i-period] {
				ok = false
				break
			}
		}
		if ok {
			return period
		}
	}
	return 0
}

// degenerateUnit 判断重复单元是否像"模型退化"而不是正常排版：必须含字母/数字（含
// CJK），且至少有 3 个不同的 rune。
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
