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
//
// 只读取用户真实请求文本，不读取客户端注入的 `instructions` 系统/开发者指令：
// Codex 等 Harness 会把技能、插件与工具说明整段注入，其中固定出现 figma、react、
// vue、css、页面、组件、layout 等词，导致几乎每个请求都被判成 UI 设计或前端，
// 从而让 claude 系列反超用户指定的首选 deepseek-v4.1-flash。
func autoModelRequestDomain(body []byte) autoModelDomain {
	text := strings.ToLower(autoModelUserRequestText(body))
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

// autoModelUserRequestText 汇总最近一条用户消息，兼容 Responses `input` 与 Chat `messages`。
// 工具循环中最后一项是工具结果，此时回溯到仍在历史里的用户请求，保持同一领域判断。
func autoModelUserRequestText(body []byte) string {
	var parts []string
	if input := gjson.GetBytes(body, "input"); input.Exists() {
		switch {
		case input.Type == gjson.String:
			parts = append(parts, input.String())
		case input.IsArray():
			if text := lastRoleMessageText(input, "user"); len(text) > 0 {
				parts = append(parts, text...)
			} else {
				parts = append(parts, rolelessTextItems(input)...)
			}
		}
	}
	if messages := gjson.GetBytes(body, "messages"); messages.IsArray() {
		parts = append(parts, lastRoleMessageText(messages, "user")...)
	}
	if prompt := gjson.GetBytes(body, "prompt"); prompt.Type == gjson.String {
		parts = append(parts, prompt.String())
	}
	return strings.Join(parts, "\n")
}

// lastRoleMessageText 从数组末尾取最近一条指定角色消息的文本；空消息继续向前回溯。
func lastRoleMessageText(items gjson.Result, role string) []string {
	array := items.Array()
	for i := len(array) - 1; i >= 0; i-- {
		item := array[i]
		if !strings.EqualFold(strings.TrimSpace(item.Get("role").String()), role) {
			continue
		}
		if parts := messageContentText(item.Get("content")); len(parts) > 0 {
			return parts
		}
	}
	return nil
}

// rolelessTextItems 兼容不带 role 的顶层 input_text 项。
func rolelessTextItems(items gjson.Result) []string {
	var parts []string
	items.ForEach(func(_, item gjson.Result) bool {
		if item.Get("role").Exists() {
			return true
		}
		if kind := item.Get("type").String(); kind == "input_text" || kind == "text" {
			if text := strings.TrimSpace(item.Get("text").String()); text != "" {
				parts = append(parts, text)
			}
		}
		return true
	})
	return parts
}

// messageContentText 兼容字符串与内容块两种 content 形态，只取用户可见文本。
func messageContentText(content gjson.Result) []string {
	switch {
	case content.Type == gjson.String:
		if text := strings.TrimSpace(content.String()); text != "" {
			return []string{text}
		}
	case content.IsArray():
		var parts []string
		content.ForEach(func(_, part gjson.Result) bool {
			switch part.Get("type").String() {
			case "input_text", "text", "output_text":
				if text := strings.TrimSpace(part.Get("text").String()); text != "" {
					parts = append(parts, text)
				}
			}
			return true
		})
		return parts
	}
	return nil
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
