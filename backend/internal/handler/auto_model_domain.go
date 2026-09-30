package handler

import (
	"strings"

	"github.com/tidwall/gjson"
)

type autoModelDomain string

const (
	autoModelDomainGeneral    autoModelDomain = "general"
	autoModelDomainUIDesign   autoModelDomain = "ui-design"
	autoModelDomainFrontend   autoModelDomain = "frontend"
	autoModelDomainReasoning  autoModelDomain = "reasoning"
	autoModelDomainMultimodal autoModelDomain = "multimodal"
)

// autoModelRequestDomain classifies the request without inferring model ability from its name.
func autoModelRequestDomain(body []byte) autoModelDomain {
	text := strings.ToLower(strings.Join([]string{
		gjson.GetBytes(body, "instructions").String(),
		gjson.GetBytes(body, "input").String(),
		gjson.GetBytes(body, "prompt").String(),
	}, "\n"))
	if text == "" {
		text = strings.ToLower(string(body))
	}
	if hasAny(text, []string{
		"ui 设计", "ui设计", "界面设计", "交互设计", "视觉设计", "设计稿", "figma", "ui/ux",
		"ui design", "user interface", "visual design", "design system",
	}) {
		return autoModelDomainUIDesign
	}
	if hasAny(text, []string{
		"前端", "页面", "组件", "样式", "css", "scss", "tailwind", "react", "vue", "svelte",
		"frontend", "front-end", "web ui", "responsive", "layout",
	}) {
		return autoModelDomainFrontend
	}
	if hasAny(text, []string{
		"架构", "重构", "迁移", "并发", "事务", "数据库", "安全", "鉴权", "性能", "分布式",
		"architecture", "refactor", "migration", "concurrency", "database", "security", "performance",
	}) {
		return autoModelDomainReasoning
	}
	if input := gjson.GetBytes(body, "input"); input.IsArray() {
		for _, item := range input.Array() {
			if item.Get("type").String() == "input_image" {
				return autoModelDomainMultimodal
			}
		}
	}
	return autoModelDomainGeneral
}

// autoModelDomainPriority orders a domain preference. Existing eligibility still decides adoption.
func autoModelDomainPriority(domain autoModelDomain, model string) (int, bool) {
	name := strings.ToLower(strings.TrimSpace(model))
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	switch domain {
	case autoModelDomainUIDesign:
		switch {
		case name == "claude-opus-4-7" || name == "claude-opus-4-6" || name == "claude-opus-5":
			return 0, true
		case name == "claude-sonnet-4-6" || name == "claude-sonnet-4-5" || name == "claude-sonnet-5":
			return 1, true
		case strings.HasPrefix(name, "kimi-") || strings.HasPrefix(name, "glm-"):
			return 2, true
		}
	case autoModelDomainFrontend:
		switch {
		case strings.HasPrefix(name, "claude-"), strings.HasPrefix(name, "gpt-6"):
			return 0, true
		case strings.HasPrefix(name, "kimi-"), strings.HasPrefix(name, "glm-"):
			return 1, true
		}
	case autoModelDomainReasoning, autoModelDomainMultimodal:
		switch {
		case strings.HasPrefix(name, "gpt-6"), strings.HasPrefix(name, "claude-opus"):
			return 0, true
		case strings.HasPrefix(name, "deepseek-v4"), strings.HasPrefix(name, "kimi-"), strings.HasPrefix(name, "glm-"):
			return 1, true
		}
	}
	return 0, false
}

func hasAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
