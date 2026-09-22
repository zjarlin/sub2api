package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// zz_probe_tool_output_test.go —— 临时诊断复现：工具输出在 DeepSeek 调用链中是否被吞。
// 模拟豆包客户端 agent 回传 exec_command 输出的各种形态，走完整转换链。

func zzRun(t *testing.T, name string, fn func(t *testing.T)) {
	t.Run(name, fn)
}

func TestZZProbeChatToolToResponses(t *testing.T) {
	// 出站方向：豆包客户端 Chat Completions 消息（role=tool）→ Responses function_call_output
	// 模拟 exec_command 输出形态
	cases := []struct {
		name    string
		content string // JSON 编码的 content 字段
		want    string
	}{
		{"plain_hello", `"hello\n"`, "hello\\n"},
		{"multiline", `"line1\nline2"`, "line1\\nline2"},
		{"json_object_stdout", `"{\"stdout\":\"hello\",\"stderr\":\"\",\"exit_code\":0}"`, `{\"stdout\":\"hello\",\"stderr\":\"\",\"exit_code\":0}`},
		{"array_text_parts", `[{"type":"text","text":"hello"}]`, "hello"},
		{"empty_string", `""`, ""},
		{"whitespace_only", `"   "`, "   "},
	}
	for _, tc := range cases {
		zzRun(t, tc.name, func(t *testing.T) {
			m := apicompat.ChatMessage{Role: "tool", ToolCallID: "call_1", Content: json.RawMessage(tc.content)}
			req, err := apicompat.ChatCompletionsToResponses(&apicompat.ChatCompletionsRequest{
				Model:    "deepseek-chat",
				Messages: []apicompat.ChatMessage{m},
			})
			require.NoError(t, err)
			var items []apicompat.ResponsesInputItem
			require.NoError(t, json.Unmarshal(req.Input, &items))
			require.Len(t, items, 1)
			out := items[0].Output
			t.Logf("function_call_output.output = %q", out)
			if tc.want == "" {
				require.Equal(t, "", out)
			} else {
				require.Contains(t, out, tc.want)
			}
		})
	}
}

func TestZZProbeNormalizeDeepSeekRequestBody(t *testing.T) {
	// 出站方向：normalizeDeepSeekResponsesRequestBody（9/20 后覆盖 OpenAI+deepseek 账号）
	// 工具输出经 LiftResponsesToolOutputMedia 后的形态
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.deepseek.com"}}
	cases := []struct {
		name   string
		output string // JSON 编码的 output 字段
	}{
		{"plain_hello", `"hello\n"`},
		{"json_object", `"{\"stdout\":\"hello\",\"exit_code\":0}"`},
		{"array_text", `[{"type":"input_text","text":"hello"}]`},
		{"array_empty", `[]`},
		{"null_output", `null`},
		{"empty_string", `""`},
	}
	for _, tc := range cases {
		zzRun(t, tc.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"store":true,"input":[
				{"type":"function_call_output","call_id":"call_1","output":%s}
			]}`, tc.output))
			normalized := normalizeDeepSeekResponsesRequestBody(account, body)
			item := gjson.GetBytes(normalized, "input.0")
			t.Logf("normalized output raw = %s", item.Get("output").Raw)
			require.Equal(t, gjson.String, item.Get("output").Type, "output 必须是字符串")
			require.NotEqual(t, `""`, item.Get("output").String(), "字符串化后的输出不能是空字符串")
		})
	}
}

func TestZZProbeBuildChatMessagesFromItemsToolOutput(t *testing.T) {
	// 回退方向：Responses input（function_call_output）→ Chat messages（role=tool）
	// 走 buildChatMessagesFromItems + normalizeChatMessages 全链
	cases := []struct {
		name   string
		output string // JSON 编码的 output
	}{
		{"plain_hello", `"hello\n"`},
		{"json_object", `"{\"stdout\":\"hello\",\"exit_code\":0}"`},
		{"array_text", `[{"type":"input_text","text":"hello"}]`},
		{"array_empty", `[]`},
		{"null_output", `null`},
		{"empty_string", `""`},
	}
	for _, tc := range cases {
		zzRun(t, tc.name, func(t *testing.T) {
			req := &apicompat.ResponsesRequest{
				Model: "deepseek-chat",
				Input: json.RawMessage(fmt.Sprintf(`[
					{"type":"function_call","call_id":"call_1","name":"exec","arguments":"{\"command\":\"echo hello\"}"},
					{"type":"function_call_output","call_id":"call_1","output":%s}
				]`, tc.output)),
			}
			chatReq, err := apicompat.ResponsesToChatCompletionsRequest(req)
			require.NoError(t, err)
			require.Len(t, chatReq.Messages, 2, "应有 assistant + tool 两条消息")
			toolMsg := chatReq.Messages[1]
			require.Equal(t, "tool", toolMsg.Role)
			require.Equal(t, "call_1", toolMsg.ToolCallID)
			var content string
			require.NoError(t, json.Unmarshal(toolMsg.Content, &content))
			t.Logf("chat tool content = %q", content)
			require.NotEqual(t, "", content, "tool 输出在回退链中不得为空")
		})
	}
}

func TestZZProbeNormalizeChatMessagesOrphan(t *testing.T) {
	// normalizeChatMessages 的 orphan 丢弃逻辑：assistant tool_calls 与 tool 回复 call_id 不匹配
	// 时 tool 回复是否被整体丢弃 = 工具输出为空
	req := &apicompat.ResponsesRequest{
		Model: "deepseek-chat",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"call_A","name":"exec","arguments":"{\"command\":\"echo hello\"}"},
			{"type":"function_call_output","call_id":"call_A","output":"hello\n"},
			{"type":"function_call","call_id":"call_B","name":"exec","arguments":"{\"command\":\"echo world\"}"},
			{"type":"function_call_output","call_id":"call_B","output":"world\n"}
		]`),
	}
	chatReq, err := apicompat.ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	for i, m := range chatReq.Messages {
		content := ""
		_ = json.Unmarshal(m.Content, &content)
		t.Logf("msg[%d] role=%s tool_call_id=%q content=%q tool_calls=%d", i, m.Role, m.ToolCallID, content, len(m.ToolCalls))
	}
	// 两个输出都必须保留
	var sawA, sawB bool
	for _, m := range chatReq.Messages {
		if m.Role != "tool" {
			continue
		}
		var content string
		_ = json.Unmarshal(m.Content, &content)
		if m.ToolCallID == "call_A" && content == "hello\n" {
			sawA = true
		}
		if m.ToolCallID == "call_B" && content == "world\n" {
			sawB = true
		}
	}
	require.True(t, sawA, "call_A 输出必须保留")
	require.True(t, sawB, "call_B 输出必须保留")
}

func TestZZProbeNormalizeChatMessagesCallIDMismatch(t *testing.T) {
	// 关键场景：assistant tool_calls 的 id 与 tool 回复的 call_id 不匹配（如空 id / 前后空格）
	// 验证 orphan tool 回复是否被丢弃
	req := &apicompat.ResponsesRequest{
		Model: "deepseek-chat",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"","name":"exec","arguments":"{\"command\":\"echo hello\"}"},
			{"type":"function_call_output","call_id":"","output":"hello\n"}
		]`),
	}
	chatReq, err := apicompat.ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	for i, m := range chatReq.Messages {
		content := ""
		_ = json.Unmarshal(m.Content, &content)
		t.Logf("msg[%d] role=%s tool_call_id=%q content=%q tool_calls=%d", i, m.Role, m.ToolCallID, content, len(m.ToolCalls))
	}
}

func TestZZProbeNormalizeChatMessagesMismatchedNonEmptyID(t *testing.T) {
	// 决定性场景：assistant 声明 call_A，tool 回复携带 call_B → orphan 丢弃
	req := &apicompat.ResponsesRequest{
		Model: "deepseek-chat",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"call_A","name":"exec","arguments":"{\"command\":\"echo hello\"}"},
			{"type":"function_call_output","call_id":"call_B","output":"hello\n"}
		]`),
	}
	chatReq, err := apicompat.ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	t.Logf("== mismatched call_id 结果：共 %d 条消息 ==", len(chatReq.Messages))
	for i, m := range chatReq.Messages {
		content := ""
		_ = json.Unmarshal(m.Content, &content)
		t.Logf("msg[%d] role=%s tool_call_id=%q content=%q tool_calls=%d", i, m.Role, m.ToolCallID, content, len(m.ToolCalls))
	}
	// 工具输出 "hello" 必须仍能到达上游；若被丢弃则断言失败
	var sawHello bool
	for _, m := range chatReq.Messages {
		if m.Role != "tool" {
			continue
		}
		var content string
		_ = json.Unmarshal(m.Content, &content)
		if content == "hello\n" {
			sawHello = true
		}
	}
	require.True(t, sawHello, "call_id 不匹配时 tool 输出不得被丢弃（orphan 丢弃机制）")
}

func TestZZProbeNormalizeChatMessagesParallelWithNotice(t *testing.T) {
	// 并行工具调用 + 中间 developer 通知（acc05620c 场景）→ 输出不得丢失
	req := &apicompat.ResponsesRequest{
		Model: "deepseek-chat",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"call_A","name":"exec","arguments":"{\"command\":\"echo A\"}"},
			{"type":"function_call","call_id":"call_B","name":"exec","arguments":"{\"command\":\"echo B\"}"},
			{"role":"developer","content":"Approved command prefix saved"},
			{"type":"function_call_output","call_id":"call_A","output":"A\n"},
			{"type":"function_call_output","call_id":"call_B","output":"B\n"}
		]`),
	}
	chatReq, err := apicompat.ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	t.Logf("== 并行+通知：共 %d 条消息 ==", len(chatReq.Messages))
	for i, m := range chatReq.Messages {
		content := ""
		_ = json.Unmarshal(m.Content, &content)
		t.Logf("msg[%d] role=%s tool_call_id=%q content=%q tool_calls=%d", i, m.Role, m.ToolCallID, content, len(m.ToolCalls))
	}
	var sawA, sawB bool
	for _, m := range chatReq.Messages {
		if m.Role != "tool" {
			continue
		}
		var content string
		_ = json.Unmarshal(m.Content, &content)
		if m.ToolCallID == "call_A" && content == "A\n" {
			sawA = true
		}
		if m.ToolCallID == "call_B" && content == "B\n" {
			sawB = true
		}
	}
	require.True(t, sawA && sawB, "并行工具输出不得丢失")
}
