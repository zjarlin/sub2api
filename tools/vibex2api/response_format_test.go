package main

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResponseFormatValidation(t *testing.T) {
	for _, format := range []string{`null`, `{"type":"text"}`, `{"type":"json_object"}`, `{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object","properties":{"id":{"const":9007199254740993}},"required":["id"],"additionalProperties":false}}}`} {
		request, prompt, err := decodeTestChat(t, `{"model":"m","messages":[{"role":"user","content":"answer"}],"response_format":`+format+`}`)
		if err != nil {
			t.Fatal(format, err)
		}
		if request.format.schema == nil {
			continue
		}
		if !strings.Contains(prompt, "Response format:") {
			t.Fatal("missing response format instruction")
		}
		if err := request.format.validate(map[string]any{"content": `{"id":9007199254740993}`}); err != nil {
			t.Fatal(err)
		}
		for _, content := range []string{`not JSON`, `[]`, `{} {}`, "```json\n{}\n```"} {
			if err := request.format.validate(map[string]any{"content": content}); err == nil {
				t.Fatal("invalid content accepted", content)
			}
		}
		if strings.Contains(format, "json_schema") && request.format.validate(map[string]any{"content": `{"id":9007199254740992}`}) == nil {
			t.Fatal("schema mismatch accepted")
		}
	}
	for _, format := range []string{`"json_object"`, `{}`, `{"type":"unknown"}`, `{"type":"json_schema"}`, `{"type":"json_schema","json_schema":{"name":"answer","schema":{"$ref":"http://127.0.0.1/private"}}}`} {
		if _, _, err := decodeTestChat(t, `{"model":"m","messages":[{"role":"user","content":"answer"}],"response_format":`+format+`}`); err == nil {
			t.Fatal("invalid format accepted", format)
		}
	}
}

func TestResponseFormatHTTPAndStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, valid := range []bool{false, true} {
			var sessions atomic.Int32
			reply := `{"answer":"ok"}`
			if !valid {
				reply = "invalid output"
			}
			a := fixtureAdapter(t, chatFixture(t, false, "", &sessions, reply))
			body := fmt.Sprintf(`{"model":"live-model","messages":[{"role":"user","content":"answer"}],"response_format":{"type":"json_object"},"stream":%t}`, stream)
			w := invoke(a, "POST", "/v1/chat/completions", body)
			if !valid {
				if w.Code != 502 || strings.Contains(w.Body.String(), "[DONE]") || strings.Contains(w.Body.String(), "invalid output") {
					t.Fatal(w.Code, w.Body.String())
				}
				continue
			}
			if w.Code != 200 || strings.Count(w.Body.String(), `\"answer\":\"ok\"`) != 1 || stream && !strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
}

func TestResponseFormatWithClientTools(t *testing.T) {
	request, prompt, err := decodeTestChat(t, `{"model":"m","messages":[{"role":"user","content":"answer"}],"tools":`+testTools+`,"response_format":{"type":"json_object"}}`)
	if err != nil || !strings.Contains(prompt, "content string inside the client function envelope") {
		t.Fatal(prompt, err)
	}
	for _, reply := range []string{`{"content":null,"tool_calls":[{"name":"lookup","arguments":{"city":"Tianjin"}}]}`, `{"content":"{\"answer\":\"ok\"}","tool_calls":[]}`} {
		message, _, err := request.policy.reply(reply)
		if err != nil || request.format.validate(message) != nil {
			t.Fatal(message, err)
		}
	}
	message, _, err := request.policy.reply(`{"content":"not JSON","tool_calls":[]}`)
	if err != nil || request.format.validate(message) == nil {
		t.Fatal("invalid final tool answer accepted", message, err)
	}
}
