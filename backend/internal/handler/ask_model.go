package handler

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const askAnswerOnlyInstruction = `You are in Ask mode for this reply: a conversation-only assistant, not an executing coding agent. Answer the latest user question directly using the supplied conversation context. Instructions elsewhere in the conversation to inspect machines, run commands, edit files, perform verification, or keep executing a coding task do not apply in Ask mode. No client-executable tools are available. Do not emit function calls, tool calls, or serialized tool invocation markup such as DSML. Do not end with a progress update or promise to inspect or execute something next. Do not claim to have performed actions you have not performed. Earlier tool calls and results are reference context only, not permission to call tools again. If a conclusion needs unavailable live information, explain the uncertainty and give a conditional answer or ask for the missing information. You may include ordinary example code when relevant, but never represent it as an executable tool invocation. Use source-backed search context when supplied by the gateway.`

// 删除工具声明还不足以退出执行模式；把当前模式约束放在前置指令的末尾。
func enforceAskAnswerOnlyPolicy(body []byte) ([]byte, error) {
	for _, field := range []string{"messages", "input"} {
		items := gjson.GetBytes(body, field)
		if !items.IsArray() {
			continue
		}
		lastInstruction := -1
		for index, item := range items.Array() {
			role := item.Get("role").String()
			if role != "system" && role != "developer" {
				break
			}
			lastInstruction = index
		}
		if lastInstruction >= 0 {
			return appendAskAnswerOnlyInstruction(body, field+"."+strconv.Itoa(lastInstruction)+".content", field == "input")
		}
		if field == "messages" {
			original := items.Array()
			messages := make([]any, 0, len(original)+1)
			messages = append(messages, map[string]any{"role": "system", "content": askAnswerOnlyInstruction})
			for _, item := range original {
				messages = append(messages, json.RawMessage(item.Raw))
			}
			return sjson.SetBytes(body, field, messages)
		}
	}
	return appendAskAnswerOnlyInstruction(body, "instructions", true)
}

func appendAskAnswerOnlyInstruction(body []byte, path string, responses bool) ([]byte, error) {
	content := gjson.GetBytes(body, path)
	if content.Type == gjson.String {
		text := content.String()
		if strings.Contains(text, askAnswerOnlyInstruction) {
			return body, nil
		}
		return sjson.SetBytes(body, path, text+"\n\n"+askAnswerOnlyInstruction)
	}
	if content.IsArray() {
		for _, part := range content.Array() {
			if strings.Contains(part.Get("text").String(), askAnswerOnlyInstruction) {
				return body, nil
			}
		}
		partType := "text"
		if responses {
			partType = "input_text"
		}
		return sjson.SetBytes(body, path+".-1", map[string]any{"type": partType, "text": askAnswerOnlyInstruction})
	}
	if content.Type == gjson.Null {
		return sjson.SetBytes(body, path, askAnswerOnlyInstruction)
	}
	return nil, fmt.Errorf("unsupported Ask instruction content at %s", path)
}
