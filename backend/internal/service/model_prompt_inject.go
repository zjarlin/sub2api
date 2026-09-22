package service

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// InjectModelSystemPrompt 把服务端配置的提示词注入请求体。
//
// 支持三种入口形态：Chat Completions 的 messages、Responses 的 instructions/input、
// OpenAI 兼容 Anthropic Messages 的 system。提示词插在已有 system 段之后、首个
// 非 system 消息之前，保证它是最后一条系统指令，避免被客户端自带的 system 覆盖。
// 未命中配置或形态不认识时返回 changed=false，调用方据此保持原样。
func InjectModelSystemPrompt(body []byte, prompt string) ([]byte, bool) {
	prompt = strings.TrimSpace(prompt)
	if len(body) == 0 || prompt == "" {
		return body, false
	}
	if !gjson.ValidBytes(body) {
		return body, false
	}
	// OpenAI 兼容 Anthropic Messages 入口：顶层 system 字段（同时带 messages 数组）。
	// 必须先于 Chat Completions 判断，否则会把 system 塞进 Anthropic messages 里。
	if system := gjson.GetBytes(body, "system"); system.Exists() && (system.Type == gjson.String || system.IsArray()) {
		if updated, ok := injectIntoAnthropicSystem(body, prompt); ok {
			return updated, true
		}
		return body, false
	}
	// Chat Completions 入口：messages 数组。
	if messages := gjson.GetBytes(body, "messages"); messages.IsArray() {
		if updated, ok := injectIntoRoleArray(body, "messages", prompt); ok {
			return updated, true
		}
		return body, false
	}
	// Responses 入口：优先合并到 instructions，否则落到 input 数组。
	if instructions := gjson.GetBytes(body, "instructions"); instructions.Type == gjson.String {
		merged := strings.TrimSpace(instructions.String() + "\n\n" + prompt)
		if updated, err := sjson.SetBytes(body, "instructions", merged); err == nil {
			return updated, true
		}
		return body, false
	}
	if input := gjson.GetBytes(body, "input"); input.IsArray() {
		if updated, ok := injectIntoRoleArray(body, "input", prompt); ok {
			return updated, true
		}
		return body, false
	}
	return body, false
}

// injectIntoRoleArray 在 role/content 结构数组的 system 段末尾插入一条 system 消息。
func injectIntoRoleArray(body []byte, path, prompt string) ([]byte, bool) {
	var items []map[string]any
	if err := json.Unmarshal([]byte(gjson.GetBytes(body, path).Raw), &items); err != nil {
		return body, false
	}
	index := 0
	for index < len(items) {
		role, _ := items[index]["role"].(string)
		if !strings.EqualFold(role, "system") && !strings.EqualFold(role, "developer") {
			break
		}
		index++
	}
	injected := map[string]any{"role": "system", "content": prompt}
	items = append(items, nil)
	copy(items[index+1:], items[index:])
	items[index] = injected
	updated, err := sjson.SetBytes(body, path, items)
	if err != nil {
		return body, false
	}
	return updated, true
}

// injectIntoAnthropicSystem 把提示词追加到 Anthropic Messages 的 system 字段。
func injectIntoAnthropicSystem(body []byte, prompt string) ([]byte, bool) {
	system := gjson.GetBytes(body, "system")
	switch {
	case system.Type == gjson.String:
		merged := strings.TrimSpace(system.String() + "\n\n" + prompt)
		updated, err := sjson.SetBytes(body, "system", merged)
		if err != nil {
			return body, false
		}
		return updated, true
	case system.IsArray():
		var blocks []map[string]any
		if err := json.Unmarshal([]byte(system.Raw), &blocks); err != nil {
			return body, false
		}
		blocks = append(blocks, map[string]any{"type": "text", "text": prompt})
		updated, err := sjson.SetBytes(body, "system", blocks)
		if err != nil {
			return body, false
		}
		return updated, true
	default:
		return body, false
	}
}
