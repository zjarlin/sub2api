package service

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// degenerationMessage 生成退化重复熔断的用户可见错误信息。
//
// 三条检测通道（尾部短周期 / 行级复读 / 标签洪水）的细节不同，所以分形态描述；这条
// 信息同时进 ops_error_logs，便于事后判断是哪一类退化。
func degenerationMessage(degenerated *apicompat.ChatDegeneration) string {
	if degenerated == nil {
		return "Upstream output degenerated into a repeated loop; aborted instead of streaming it to the output limit"
	}
	if degenerated.Kind == "markup" {
		return fmt.Sprintf(
			"Upstream output degenerated into a repeated tool-markup loop (%s channel, %d closing tags filling the window); aborted instead of streaming it to the output limit",
			degenerated.Channel, degenerated.MarkupCloses,
		)
	}
	if degenerated.Kind == "line" {
		return fmt.Sprintf(
			"Upstream output degenerated into a repeated loop (%s channel, %d trailing lines drawn from only %d short lines); aborted instead of streaming it to the output limit",
			degenerated.Channel, degenerated.Repeats, degenerated.DistinctLines,
		)
	}
	return fmt.Sprintf(
		"Upstream output degenerated into a repeated loop (%s channel, repeating period %d runes, >=%d repeats); aborted instead of streaming it to the output limit",
		degenerated.Channel, degenerated.PeriodRunes, degenerated.Repeats,
	)
}
