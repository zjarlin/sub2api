package service

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

const dsmlExecCall = "<|DSML|tool_calls>\n" +
	"<|DSML|invoke name=\"exec\">\n" +
	"<|DSML|parameter name=\"input\" string=\"true\">const r = await tools.exec_command({\"cmd\":\"dir\"});\ntext(r.output);</|DSML|parameter>\n" +
	"</|DSML|invoke>\n" +
	"</|DSML|tool_calls>"

func markupTestSet() responsesToolNames {
	return collectResponsesToolNames([]byte(`{"tools":[{"type":"custom","name":"exec"},{"type":"function","name":"exec_command"}]}`))
}

func TestParseToolMarkupCustomExec(t *testing.T) {
	parsed := markupTestSet().parseToolMarkup("我先看看目录。\n" + dsmlExecCall)
	if len(parsed.Calls) != 1 || parsed.Partial || parsed.Malformed {
		t.Fatalf("parsed=%+v", parsed)
	}
	call := parsed.Calls[0]
	if call.Name != "exec" {
		t.Fatalf("name=%s", call.Name)
	}
	if call.Input != "const r = await tools.exec_command({\"cmd\":\"dir\"});\ntext(r.output);" {
		t.Fatalf("input=%q", call.Input)
	}
}

func TestParseToolMarkupFunctionArgumentsJSON(t *testing.T) {
	body := `<|DSML|invoke name="exec_command">` +
		`<|DSML|parameter name="cmd" string="true">"ls -a"</|DSML|parameter>` +
		`<|DSML|parameter name="workdir" string="true">"/tmp"</|DSML|parameter>` +
		`</|DSML|invoke>`
	parsed := markupTestSet().parseToolMarkup(body)
	if len(parsed.Calls) != 1 {
		t.Fatalf("parsed=%+v", parsed)
	}
	args := gjson.Parse(parsed.Calls[0].Arguments)
	if args.Get("cmd").String() != "ls -a" || args.Get("workdir").String() != "/tmp" {
		t.Fatalf("arguments=%s", parsed.Calls[0].Arguments)
	}
}

func TestParseToolMarkupIgnoresUnknownTool(t *testing.T) {
	parsed := markupTestSet().parseToolMarkup(`<|DSML|invoke name="not_declared"><|DSML|parameter name="a" string="true">1</|DSML|parameter></|DSML|invoke>`)
	if len(parsed.Calls) != 0 || !parsed.Malformed {
		t.Fatalf("未声明工具不应下放，parsed=%+v", parsed)
	}
}

func TestParseToolMarkupSkipsFencedExample(t *testing.T) {
	text := "格式是这样的：\n\n```text\n" + dsmlExecCall + "\n```\n\n仅作示例。"
	parsed := markupTestSet().parseToolMarkup(text)
	if len(parsed.Calls) != 0 {
		t.Fatalf("代码块里的示例不能被执行，parsed=%+v", parsed)
	}
}

func TestParseToolMarkupTruncatedIsPartial(t *testing.T) {
	parsed := markupTestSet().parseToolMarkup(`<|DSML|invoke name="exec"><|DSML|parameter name="input" string="true">const r = await to`)
	if len(parsed.Calls) != 0 || !parsed.Partial {
		t.Fatalf("截断调用不应下发，parsed=%+v", parsed)
	}
}

func TestRecoverResponsesToolMarkupBody(t *testing.T) {
	textJSON, err := json.Marshal("我会先核对目录。\n" + dsmlExecCall)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"id":"resp_1","status":"completed","output":[{"type":"reasoning","summary":[]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":` + string(textJSON) + `}]}]}`)
	if !gjson.ValidBytes(body) {
		t.Fatalf("fixture 必须是合法 JSON: %s", body)
	}
	recovered, changed := recoverResponsesToolMarkupBody(body, markupTestSet())
	if !changed {
		t.Fatal("应识别并还原调用")
	}
	output := gjson.GetBytes(recovered, "output")
	if output.Get("#").Int() != 3 {
		t.Fatalf("output=%s", output.Raw)
	}
	if got := output.Get("1.content.0.text").String(); got != "我会先核对目录。" {
		t.Fatalf("正文应只保留说明，got=%q", got)
	}
	call := output.Get("2")
	if call.Get("type").String() != "custom_tool_call" || call.Get("name").String() != "exec" {
		t.Fatalf("call=%s", call.Raw)
	}
	if !strings.Contains(call.Get("input").String(), "tools.exec_command") || call.Get("call_id").String() == "" {
		t.Fatalf("input/call_id 缺失: %s", call.Raw)
	}
}

func TestRecoverResponsesToolMarkupBodyLeavesFencedExample(t *testing.T) {
	textJSON, _ := json.Marshal("文档里写作：\n```\n" + dsmlExecCall + "\n```")
	body := []byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":` + string(textJSON) + `}]}]}`)
	if _, changed := recoverResponsesToolMarkupBody(body, markupTestSet()); changed {
		t.Fatal("代码块里的示例不能被当成调用")
	}
}

func TestToolMarkupStreamSplitAcrossDeltas(t *testing.T) {
	set := markupTestSet()
	recoverer := newToolMarkupRecoverer(set)
	var calls []recoveredToolCall
	var emitted strings.Builder
	for _, delta := range []string{"我先", "看一眼\n<|DS", "ML|invoke name=\"exec\"><|DSML|parameter name=\"input\" string=\"true\">pw", "d</|DSML|parameter></|DSML|invoke>"} {
		frame := []byte(`{"type":"response.output_text.delta","sequence_number":7,"item_id":"msg_1","output_index":0,"content_index":0,"delta":` + quoteJSON(delta) + `}`)
		next, replaced := rewriteToolCallSSE(frame, "response.output_text.delta", &calls, recoverer)
		if !replaced {
			emitted.WriteString(delta)
			continue
		}
		emitted.Write(next)
	}
	emitted.Write(flushToolMarkupSSE(recoverer, &calls, 20))
	out := emitted.String()
	if len(calls) != 1 || calls[0].Name != "exec" || calls[0].Input != "pwd" {
		t.Fatalf("calls=%+v", calls)
	}
	if strings.Contains(out, "DSML") {
		t.Fatalf("标记不应泄漏: %s", out)
	}
	if !strings.Contains(out, `"type":"response.output_item.done"`) {
		t.Fatalf("应生成调用事件: %s", out)
	}
	if !strings.HasPrefix(out, "我先") || !strings.Contains(out, "看一眼") {
		t.Fatalf("标记前的正文应保留: %s", out)
	}
}

func TestToolMarkupStreamPlainBracketFlushed(t *testing.T) {
	set := markupTestSet()
	recoverer := newToolMarkupRecoverer(set)
	var calls []recoveredToolCall
	if _, replaced := rewriteToolCallSSE([]byte(`{"type":"response.output_text.delta","delta":"a < b"}`), "response.output_text.delta", &calls, recoverer); replaced {
		t.Fatal("普通小于号不应进入暂存")
	}
	next, replaced := rewriteToolCallSSE([]byte(`{"type":"response.output_text.delta","delta":"<"}`), "response.output_text.delta", &calls, recoverer)
	if !replaced || len(next) != 0 {
		t.Fatalf("孤立 < 需要暂存，next=%s", next)
	}
	if text, taken := recoverer.Flush(); text != "<" || len(taken) != 0 {
		t.Fatalf("text=%q taken=%+v 应原样放出", text, taken)
	}
}

func buildToolMarkupSSE(deltas []string) string {
	var b strings.Builder
	b.WriteString("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n")
	for i, delta := range deltas {
		b.WriteString("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":" + strconv.Itoa(i) + ",\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":" + quoteJSON(delta) + "}\n\n")
	}
	b.WriteString("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[]}}\n\n")
	return b.String()
}

func readAllBody(t *testing.T, body io.ReadCloser) string {
	t.Helper()
	raw, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	return string(raw)
}

func TestToolMarkupStreamBodyRestoresSplitCall(t *testing.T) {
	sse := buildToolMarkupSSE([]string{
		"先核对目录。\n", "<|DS",
		"ML|invoke name=\"exec\"><|DSML|parameter name=\"input\" string=\"true\">const a = 1;\n",
		"text(a);</|DSML|parameter></|DSML|invoke>",
	})
	out := readAllBody(t, newToolMarkupStreamBody(io.NopCloser(strings.NewReader(sse)), markupTestSet(), 1<<20))

	if strings.Contains(out, "DSML") {
		t.Fatalf("标记不应泄漏给客户端:\n%s", out)
	}
	if !strings.Contains(out, `"type":"custom_tool_call"`) || !strings.Contains(out, `"name":"exec"`) {
		t.Fatalf("应生成 exec 调用:\n%s", out)
	}
	if !strings.Contains(out, `"input":"const a = 1;\ntext(a);"`) {
		t.Fatalf("调用输入应完整且已反转义:\n%s", out)
	}
	if !strings.Contains(out, "event: response.completed") {
		t.Fatalf("终态事件必须保留:\n%s", out)
	}
	if !strings.Contains(out, "先核对目录。") {
		t.Fatalf("标记前正文应保留:\n%s", out)
	}
}

func TestToolMarkupStreamBodyKeepsPlainAngleBracket(t *testing.T) {
	sse := buildToolMarkupSSE([]string{"条件 a <", " b 时继续", "，不是标记。"})
	out := readAllBody(t, newToolMarkupStreamBody(io.NopCloser(strings.NewReader(sse)), markupTestSet(), 1<<20))
	if strings.Contains(out, `"type":"custom_tool_call"`) || strings.Contains(out, `"type":"function_call"`) {
		t.Fatalf("普通正文不应生成调用:\n%s", out)
	}
	if got := visibleText(out); got != "条件 a < b 时继续，不是标记。" {
		t.Fatalf("正文应完整保留，got=%q", got)
	}
}

func TestToolMarkupStreamBodyTruncatedCallStaysText(t *testing.T) {
	// 输出在调用中途截断：宁可保留原文，也不下发执行一半的参数。
	sse := buildToolMarkupSSE([]string{"准备执行 <|DSML|invoke name=\"exec\"><|DSML|parameter name=\"input\" string=\"true\">rm -rf"})
	out := readAllBody(t, newToolMarkupStreamBody(io.NopCloser(strings.NewReader(sse)), markupTestSet(), 1<<20))
	if strings.Contains(out, `"type":"custom_tool_call"`) || strings.Contains(out, `"type":"function_call"`) {
		t.Fatalf("截断调用不应执行:\n%s", out)
	}
	if !strings.Contains(visibleText(out), "rm -rf") {
		t.Fatalf("截断内容应以文本保留:\n%s", out)
	}
}

func TestToolMarkupStreamBodyFencedExampleUntouched(t *testing.T) {
	sse := buildToolMarkupSSE([]string{"格式如下：\n```text\n", dsmlExecCall, "\n```\n示例结束。"})
	out := readAllBody(t, newToolMarkupStreamBody(io.NopCloser(strings.NewReader(sse)), markupTestSet(), 1<<20))
	if strings.Contains(out, `"type":"custom_tool_call"`) || strings.Contains(out, `"type":"function_call"`) {
		t.Fatalf("代码块示例不应被执行:\n%s", out)
	}
	if !strings.Contains(out, "示例结束。") {
		t.Fatalf("正文应保留:\n%s", out)
	}
}

func TestToolMarkupStreamBodyTerminalCarriesRecoveredCall(t *testing.T) {
	// 终态 response.completed 缺少 output：应补上流中已还原的调用。
	sse := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":0,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":" + quoteJSON("执行中 <|DSML|invoke name=\"exec\"><|DSML|parameter name=\"input\" string=\"true\">pwd</|DSML|parameter></|DSML|invoke>") + "}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[]}}\n\n"
	out := readAllBody(t, newToolMarkupStreamBody(io.NopCloser(strings.NewReader(sse)), markupTestSet(), 1<<20))

	var terminal string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "data: {\"type\":\"response.completed\"") {
			terminal = strings.TrimPrefix(line, "data: ")
		}
	}
	if terminal == "" {
		t.Fatalf("缺少终态事件:\n%s", out)
	}
	callID := gjson.Get(terminal, "response.output.0.call_id").String()
	if gjson.Get(terminal, "response.output.0.name").String() != "exec" || callID == "" {
		t.Fatalf("终态应带已还原调用: %s", terminal)
	}
	if !strings.Contains(out, callID) {
		t.Fatalf("终态 call_id 应与增量一致: %s", callID)
	}
}

// visibleText 汇总所有 output_text.delta 的正文，模拟客户端看到的文本。
func visibleText(sse string) string {
	var b strings.Builder
	for _, line := range strings.Split(sse, "\n") {
		data, ok := extractOpenAISSEDataLine(line)
		if !ok || !gjson.Valid(data) {
			continue
		}
		if gjson.Get(data, "type").String() != "response.output_text.delta" {
			continue
		}
		b.WriteString(gjson.Get(data, "delta").String())
	}
	return b.String()
}

func quoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
