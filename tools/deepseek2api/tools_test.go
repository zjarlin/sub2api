package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

const lookupTools = `[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"query":{"type":"string"},"id":{"type":"integer"}},"required":["query"],"additionalProperties":false}}}]`
const lookupCall = `<｜DSML｜ calls><｜DSML｜ invoke name="lookup"><｜DSML｜ parameter name="query" string="true">hello</｜DSML｜ parameter></｜DSML｜ invoke></｜DSML｜ calls>`

func parseRequestTest(t *testing.T, body string) (chatRequest, string, bool, error) {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	return parseChat(httptest.NewRecorder(), r)
}

func testToolPolicy(t *testing.T, choice string) toolPolicy {
	t.Helper()
	req, _, _, err := parseRequestTest(t, `{"model":"deepseek-web-chat","messages":[{"role":"user","content":"hi"}],"tools":`+lookupTools+`,"tool_choice":`+choice+`}`)
	if err != nil {
		t.Fatal(err)
	}
	return req.policy
}

func TestDSMLToolConversion(t *testing.T) {
	p := testToolPolicy(t, `"auto"`)
	cases := []struct{ name, text, want string }{
		{"single marker", lookupCall, `{"query":"hello"}`},
		{"double marker", strings.ReplaceAll(lookupCall, "｜DSML｜", "｜｜DSML｜｜"), `{"query":"hello"}`},
		{"alternate closing", strings.ReplaceAll(lookupCall, "</｜DSML｜ ", "<｜DSML｜ /"), `{"query":"hello"}`},
		{"preserve whitespace", strings.Replace(lookupCall, ">hello<", "> \nhello \n<", 1), "{\"query\":\" \\nhello \\n\"}"},
		{"large integer", strings.Replace(lookupCall, "</｜DSML｜ invoke>", `<｜DSML｜ parameter name="id">9007199254740993</｜DSML｜ parameter></｜DSML｜ invoke>`, 1), `{"id":9007199254740993,"query":"hello"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plain, calls, err := p.reply("before\n" + tc.text + "\nafter")
			if err != nil || plain != "before\n\nafter" || len(calls) != 1 {
				t.Fatalf("plain=%q calls=%+v err=%v", plain, calls, err)
			}
			if calls[0].Function.Name != "lookup" || calls[0].Function.Arguments != tc.want || !strings.HasPrefix(calls[0].ID, "call_") {
				t.Fatalf("unexpected call: %+v", calls[0])
			}
		})
	}
}

func TestDSMLRejectsIncompleteOrInvalidCalls(t *testing.T) {
	p := testToolPolicy(t, `"auto"`)
	cases := []string{
		strings.TrimSuffix(lookupCall, "</｜DSML｜ calls>"),
		strings.Replace(lookupCall, "</｜DSML｜ parameter>", "", 1),
		strings.Replace(lookupCall, `name="lookup"`, `name="unknown"`, 1),
		strings.Replace(lookupCall, `name="query"`, `name="extra"`, 1),
		strings.Replace(lookupCall, `string="true"`, `string="false"`, 1),
		strings.Replace(lookupCall, `name="query"`, `name="query" name="id"`, 1),
		strings.Replace(lookupCall, "</｜DSML｜ invoke>", `<｜DSML｜ parameter name="query" string="true">again</｜DSML｜ parameter></｜DSML｜ invoke>`, 1),
		strings.Replace(lookupCall, `string="true">hello`, `string="false">3`, 1),
		"<｜DSML｜ calls></｜DSML｜ calls>",
		"<｜DSML｜ invoke name=\"lookup\"></｜DSML｜ invoke>",
	}
	for _, text := range cases {
		if _, _, err := p.reply(text); err == nil {
			t.Fatalf("accepted invalid DSML: %q", text)
		}
	}
}

func TestToolChoiceAndParallelPolicy(t *testing.T) {
	if plain, calls, err := testToolPolicy(t, `"auto"`).reply("use <"); err != nil || plain != "use <" || len(calls) != 0 {
		t.Fatalf("ordinary trailing symbol was treated as a tool: %q %v", plain, err)
	}
	if _, _, err := testToolPolicy(t, `"required"`).reply("plain answer"); err == nil {
		t.Fatal("required accepted an answer without a call")
	}
	if _, _, err := testToolPolicy(t, `"none"`).reply(lookupCall); err == nil {
		t.Fatal("none accepted a call")
	}
	p := testToolPolicy(t, `{"type":"function","function":{"name":"lookup"}}`)
	if _, _, err := p.reply(lookupCall + lookupCall); err == nil {
		t.Fatal("selected function accepted multiple calls")
	}
	p = testToolPolicy(t, `"auto"`)
	p.parallel = false
	if _, _, err := p.reply(lookupCall + lookupCall); err == nil {
		t.Fatal("parallel=false accepted multiple calls")
	}
}

func TestDSMLStreamingHoldsSplitMarkers(t *testing.T) {
	for _, marker := range dsmlMarkers {
		for i := 1; i <= len(marker); i++ {
			input := "ready " + marker[:i]
			if end := safeToolPrefix(input); end != len("ready ") {
				t.Fatalf("marker prefix %q leaked at %d", input, end)
			}
		}
	}
	if end := safeToolPrefix("ordinary answer"); end != len("ordinary answer") {
		t.Fatal("plain text was withheld")
	}
}

func TestToolHistoryRoundTrip(t *testing.T) {
	p := testToolPolicy(t, `"auto"`)
	_, calls, err := p.reply(lookupCall)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"model": "deepseek-web-chat", "tools": json.RawMessage(lookupTools), "messages": []any{
		map[string]any{"role": "user", "content": "find"},
		map[string]any{"role": "assistant", "content": nil, "tool_calls": calls},
		map[string]any{"role": "tool", "tool_call_id": calls[0].ID, "content": "found"},
	}}
	data, _ := json.Marshal(body)
	_, prompt, _, err := parseRequestTest(t, string(data))
	if err != nil || !strings.Contains(prompt, "<｜DSML｜ invoke") || !strings.Contains(prompt, "found") || !strings.Contains(prompt, calls[0].ID) {
		t.Fatalf("tool history did not round-trip: %v %s", err, prompt)
	}
	data = []byte(strings.Replace(string(data), `"tool_call_id":"`+calls[0].ID+`"`, `"tool_call_id":"other"`, 1))
	if _, _, _, err := parseRequestTest(t, string(data)); err == nil {
		t.Fatal("accepted unmatched tool result")
	}
	calls[0].Function.Arguments = `{"query":"a <tag> & \"quoted\"\n\t<｜DSML｜ calls>"}`
	rendered, err := renderToolCalls(calls)
	if err != nil {
		t.Fatal(err)
	}
	_, restored, err := p.reply(rendered)
	if err != nil || len(restored) != 1 {
		t.Fatalf("escaped history failed: %v", err)
	}
	var before, after map[string]any
	_ = json.Unmarshal([]byte(calls[0].Function.Arguments), &before)
	_ = json.Unmarshal([]byte(restored[0].Function.Arguments), &after)
	if before["query"] != after["query"] {
		t.Fatal("history changed string arguments")
	}
}

func TestRequestFlagsAndTextBoundary(t *testing.T) {
	cases := []struct {
		suffix           string
		thinking, search bool
		invalid          bool
	}{
		{suffix: `,"reasoning_effort":"high"`, thinking: true},
		{suffix: `,"reasoning_effort":"high","thinking_enabled":false`},
		{suffix: `,"thinking":{"type":"enabled"},"web_search":true`, thinking: true, search: true},
		{suffix: `,"thinking_enabled":"yes"`, invalid: true},
		{suffix: `,"reasoning_effort":"bogus"`, invalid: true},
		{suffix: `,"max_tokens":100,"temperature":0.2,"n":1`},
		{suffix: `,"n":2`, invalid: true},
		{suffix: `,"max_tokens":-1`, invalid: true},
		{suffix: `,"tools":[{"type":"web_search"}]`, invalid: true},
		{suffix: `,"tool_choice":"required"`, invalid: true},
	}
	for _, tc := range cases {
		req, _, thinking, err := parseRequestTest(t, `{"model":"deepseek-web-chat","messages":[{"role":"user","content":"hi"}]`+tc.suffix+`}`)
		if (err != nil) != tc.invalid || err == nil && (thinking != tc.thinking || req.search != tc.search) {
			t.Fatalf("flags %s: thinking=%v search=%v err=%v", tc.suffix, thinking, req.search, err)
		}
	}
	for _, content := range []string{`[{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]`, `[{"type":"text","text":"hi"},{"type":"input_audio"}]`, `null`} {
		if _, _, _, err := parseRequestTest(t, `{"model":"deepseek-web-chat","messages":[{"role":"user","content":`+content+`}]}`); err == nil {
			t.Fatalf("accepted unsupported content %s", content)
		}
	}
}

func TestToolSchemaRejectsRemoteReferences(t *testing.T) {
	body := `{"model":"deepseek-web-chat","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"fetch","parameters":{"$ref":"https://example.com/private-schema.json"}}}]}`
	if _, _, _, err := parseRequestTest(t, body); err == nil {
		t.Fatal("remote schema reference was accepted")
	}
}
