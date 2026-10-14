package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeGenerator struct {
	prompts []string
	fail    error
}

func (f *fakeGenerator) ask(_ context.Context, prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	if f.fail != nil {
		return "", f.fail
	}
	return "你好", nil
}

func newTestServer(t *testing.T) (*httptest.Server, *fakeGenerator) {
	t.Helper()
	generator := &fakeGenerator{}
	adapter, err := newAdapter("secret", []string{"gpt-web"}, generator, 5*time.Second)
	if err != nil {
		t.Fatalf("newAdapter: %v", err)
	}
	server := httptest.NewServer(adapter.handler())
	t.Cleanup(server.Close)
	return server, generator
}

func doJSON(t *testing.T, server *httptest.Server, path, key, body string) (int, map[string]any) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var decoded map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return response.StatusCode, decoded
}

func TestPromptFolding(t *testing.T) {
	prompt, err := buildPrompt([]promptMessage{{role: "user", content: "hi"}})
	if err != nil || prompt != "hi" {
		t.Fatalf("single user: %q %v", prompt, err)
	}
	prompt, err = buildPrompt([]promptMessage{
		{role: "system", content: "be terse"},
		{role: "user", content: "q1"},
		{role: "assistant", content: "a1"},
		{role: "user", content: "q2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"be terse", "User: q1", "Assistant: a1"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt %q missing %q", prompt, want)
		}
	}
	if !strings.HasSuffix(prompt, "User: q2") {
		t.Fatalf("prompt tail: %q", prompt)
	}
}

func TestModelsAndAuth(t *testing.T) {
	server, _ := newTestServer(t)
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/models", nil)
	request.Header.Set("Authorization", "Bearer wrong")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("wrong key status %d", response.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/models", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(response.Body).Decode(&decoded)
	if len(decoded.Data) != 1 || decoded.Data[0].ID != "gpt-web" {
		t.Fatalf("models: %+v", decoded)
	}
}

func TestChatCompletion(t *testing.T) {
	server, generator := newTestServer(t)
	status, body := doJSON(t, server, "/v1/chat/completions", "secret",
		`{"model":"gpt-web","messages":[{"role":"user","content":"hi"}]}`)
	if status != 200 {
		t.Fatalf("status %d %+v", status, body)
	}
	choices := body["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	if message["content"] != "你好" {
		t.Fatalf("content %+v", message)
	}
	if len(generator.prompts) != 1 || generator.prompts[0] != "hi" {
		t.Fatalf("prompts %+v", generator.prompts)
	}
}

func TestResponsesNonStream(t *testing.T) {
	server, _ := newTestServer(t)
	status, body := doJSON(t, server, "/v1/responses", "secret", `{"model":"gpt-web","input":"hi"}`)
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	output := body["output"].([]any)
	content := output[0].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["text"] != "你好" {
		t.Fatalf("body %+v", body)
	}
	usage, ok := body["usage"].(map[string]any)
	if !ok || usage["total_tokens"].(float64) <= 0 {
		t.Fatalf("usage should carry estimated tokens: %+v", body["usage"])
	}
}

func readSSE(t *testing.T, server *httptest.Server, path string) string {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(`{"model":"gpt-web","input":"hi","stream":true}`))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return string(raw)
}

func TestResponsesStreamShape(t *testing.T) {
	server, _ := newTestServer(t)
	raw := readSSE(t, server, "/v1/responses")
	for _, want := range []string{"event: response.created", "event: response.output_text.delta", "event: response.completed"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("sse missing %q in %s", want, raw)
		}
	}
}

func TestChatStreamTerminates(t *testing.T) {
	server, _ := newTestServer(t)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-web","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	last := ""
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			last = line
		}
	}
	if last != "data: [DONE]" {
		t.Fatalf("stream did not terminate with [DONE]: %q", last)
	}
}

func TestUpstreamFailureIsGatewayError(t *testing.T) {
	server, generator := newTestServer(t)
	generator.fail = errors.New("blocked")
	status, body := doJSON(t, server, "/v1/chat/completions", "secret",
		`{"model":"gpt-web","messages":[{"role":"user","content":"hi"}]}`)
	if status != 502 {
		t.Fatalf("status %d %+v", status, body)
	}
}

func TestToolsRejected(t *testing.T) {
	server, _ := newTestServer(t)
	status, _ := doJSON(t, server, "/v1/chat/completions", "secret",
		`{"model":"gpt-web","messages":[{"role":"user","content":"x"}],"tools":[{"type":"function"}]}`)
	if status != 400 {
		t.Fatalf("status %d", status)
	}
}
