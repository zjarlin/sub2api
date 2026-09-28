package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const testTools = `[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"city":{"type":"string","enum":["Tianjin"]}},"required":["city"],"additionalProperties":false}}}]`

func decodeTestChat(t *testing.T, body string) (chatRequest, string, error) {
	t.Helper()
	return decodeChat(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
}

func TestToolSchemaAndChoiceValidation(t *testing.T) {
	request, _, err := decodeTestChat(t, `{"model":"m","messages":[{"role":"user","content":"weather"}],"tools":`+testTools+`,"tool_choice":"required","parallel_tool_calls":false}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, reply := range []string{
		`{"content":"guess","tool_calls":[]}`,
		`{"content":null,"tool_calls":[{"name":"exec","arguments":{}}]}`,
		`{"content":null,"tool_calls":[{"name":"lookup","arguments":{"city":"Beijing"}}]}`,
		`{"content":null,"tool_calls":[{"name":"lookup","arguments":{"city":"Tianjin","extra":1}}]}`,
		`{"content":null,"tool_calls":[{"name":"lookup","arguments":{"city":"Tianjin"}},{"name":"lookup","arguments":{"city":"Tianjin"}}]}`,
	} {
		if _, _, err = request.policy.reply(reply); err == nil {
			t.Fatal("invalid reply accepted", reply)
		}
	}
	message, finish, err := request.policy.reply(`{"content":null,"tool_calls":[{"name":"lookup","arguments":{"city":"Tianjin"}}]}`)
	if err != nil || finish != "tool_calls" || len(message["tool_calls"].([]toolCall)) != 1 {
		t.Fatal(message, finish, err)
	}
	_, _, err = decodeTestChat(t, `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"$ref":"http://127.0.0.1/private"}}}]}`)
	if err == nil {
		t.Fatal("external schema reference accepted")
	}
}

func TestToolArgumentsKeepIntegerPrecision(t *testing.T) {
	request, _, err := decodeTestChat(t, `{"model":"m","messages":[{"role":"user","content":"lookup"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"id":{"const":9007199254740993}},"required":["id"]}}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	message, _, err := request.policy.reply(`{"content":null,"tool_calls":[{"name":"lookup","arguments":{"id":9007199254740993}}]}`)
	if err != nil || message["tool_calls"].([]toolCall)[0].Function.Arguments != `{"id":9007199254740993}` {
		t.Fatal(message, err)
	}
}

func TestClientToolsStreamAndResultContinuation(t *testing.T) {
	var sessions atomic.Int32
	a := fixtureAdapter(t, chatFixture(t, false, "", &sessions,
		`{"content":null,"tool_calls":[{"name":"lookup","arguments":{"city":"Tianjin"}}]}`,
		`{"content":"weather: 17","tool_calls":[]}`))
	first := invoke(a, "POST", "/v1/chat/completions", `{"model":"live-model","messages":[{"role":"user","content":"weather"}],"tools":`+testTools+`,"tool_choice":"required","stream":true,"stream_options":{"include_usage":true}}`)
	if first.Code != 200 || !strings.Contains(first.Body.String(), `"finish_reason":"tool_calls"`) || !strings.Contains(first.Body.String(), `"index":0`) || strings.Contains(first.Body.String(), `"content":null,"tool_calls"`) {
		t.Fatal(first.Code, first.Body.String())
	}
	var call toolCall
	for _, line := range strings.Split(first.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Calls []toolCall `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk) == nil && len(chunk.Choices) > 0 && len(chunk.Choices[0].Delta.Calls) > 0 {
			call = chunk.Choices[0].Delta.Calls[0]
		}
	}
	if call.ID == "" || call.Function.Name != "lookup" {
		t.Fatal("missing standard tool call")
	}
	data, _ := json.Marshal(map[string]any{"model": "live-model", "messages": []any{map[string]any{"role": "user", "content": "weather"}, map[string]any{"role": "assistant", "content": nil, "tool_calls": []toolCall{call}}, map[string]any{"role": "tool", "tool_call_id": call.ID, "content": "17"}}, "tools": json.RawMessage(testTools)})
	second := invoke(a, "POST", "/v1/chat/completions", string(data))
	if second.Code != 200 || !strings.Contains(second.Body.String(), `"content":"weather: 17"`) || !strings.Contains(second.Body.String(), `"finish_reason":"stop"`) {
		t.Fatal(second.Code, second.Body.String())
	}
	for _, messages := range []string{
		`[{"role":"tool","tool_call_id":"missing","content":"x"}]`,
		`[{"role":"assistant","content":null,"tool_calls":[{"id":"x","type":"function","function":{"name":"f","arguments":"{}"}}]}]`,
		`[{"role":"user","content":"x","tool_call_id":"unexpected"}]`,
	} {
		if _, _, err := decodeTestChat(t, `{"model":"m","messages":`+messages+`}`); err == nil {
			t.Fatal("invalid tool history accepted")
		}
	}
}

func TestImageUploadAndFinalAnswer(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1400, 700))
	img.Set(50, 50, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if png.Encode(&encoded, img) != nil {
		t.Fatal("encode")
	}
	source := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	var sessions, uploads atomic.Int32
	base := chatFixture(t, false, "", &sessions)
	a := fixtureAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/attachments") {
			if r.Header.Get("RH-TOKEN") != fixtureToken("42") {
				t.Error("missing upload authentication")
			}
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			config, format, err := image.DecodeConfig(part)
			if err != nil || format != "jpeg" || config.Width != 1024 || config.Height != 512 || part.FormName() != "files" {
				t.Error(config, format, err)
			}
			uploads.Add(1)
			writeJSON(w, 200, map[string]any{"attachments": []any{map[string]string{"container_path": "/workspace/app/.attachments/image.jpg"}}})
			return
		}
		base.ServeHTTP(w, r)
	}))
	body, _ := json.Marshal(map[string]any{"model": "live-model", "messages": []any{map[string]any{"role": "user", "content": []any{map[string]string{"type": "text", "text": "describe"}, map[string]any{"type": "image_url", "image_url": map[string]string{"url": source}}}}}})
	w := invoke(a, "POST", "/v1/chat/completions", string(body))
	if w.Code != 200 || uploads.Load() != 1 {
		t.Fatal(w.Code, w.Body.String(), uploads.Load())
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.1.1", "::1", "fd00::1", "64:ff9b::7f00:1"} {
		if publicImageIP(net.ParseIP(ip)) {
			t.Fatal("private image IP accepted", ip)
		}
	}
	if !publicImageIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public IP rejected")
	}
	if _, err := parseImage("data:image/png;base64,aGVsbG8="); err == nil {
		t.Fatal("invalid image accepted")
	}
}

func TestTransientHandshakeRetriesBeforeCreatingOneSession(t *testing.T) {
	var sessions, dials atomic.Int32
	base := chatFixture(t, false, "", &sessions)
	a := fixtureAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/app-ws/") && dials.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		base.ServeHTTP(w, r)
	}))
	w := invoke(a, "POST", "/v1/chat/completions", `{"model":"live-model","messages":[{"role":"user","content":"hello"}]}`)
	if w.Code != 200 || dials.Load() != 3 || sessions.Load() != 1 {
		t.Fatal(w.Code, dials.Load(), sessions.Load(), w.Body.String())
	}
}

func TestHandshakeAccountFailuresAreNotRetried(t *testing.T) {
	for _, status := range []int{401, 402, 429} {
		var sessions, dials atomic.Int32
		base := chatFixture(t, false, "", &sessions)
		a := fixtureAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/app-ws/") {
				dials.Add(1)
				w.WriteHeader(status)
				return
			}
			base.ServeHTTP(w, r)
		}))
		w := invoke(a, "POST", "/v1/chat/completions", `{"model":"live-model","messages":[{"role":"user","content":"hello"}]}`)
		if w.Code != status || dials.Load() != 1 || sessions.Load() != 0 {
			t.Fatal(status, w.Code, dials.Load(), sessions.Load())
		}
	}
}

func TestTransientGETRetriesAndMutationDoesNot(t *testing.T) {
	var queries, mutations atomic.Int32
	a := fixtureAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if queries.Add(1) < 3 {
				w.WriteHeader(503)
				return
			}
			writeJSON(w, 200, map[string]any{"providers": []any{map[string]string{"id": "model"}}})
			return
		}
		mutations.Add(1)
		w.WriteHeader(503)
	}))
	w := invoke(a, "GET", "/v1/models", "")
	if w.Code != 200 || queries.Load() != 3 {
		t.Fatal(w.Code, queries.Load(), w.Body.String())
	}
	c, _ := a.snapshot()
	if err := a.call(context.Background(), c, "POST", "/vc/api/apps/dedicated/start", nil, nil); err == nil || mutations.Load() != 1 {
		t.Fatal(err, mutations.Load())
	}
}
