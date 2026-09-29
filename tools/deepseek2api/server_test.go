package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sub2api/builtinlogin"
)

type fakePow struct {
	input challenge
}

func (f *fakePow) solveChallenge(_ context.Context, input challenge) (string, error) {
	f.input = input
	return "proof", nil
}

func testEnvelope(data any) any {
	return map[string]any{"code": 0, "data": map[string]any{"biz_code": 0, "biz_data": data}}
}

type fakeLoginBrowser struct {
	session *fakeBrowserSession
	options browserLoginOptions
}

func (f *fakeLoginBrowser) Start(_ context.Context, options browserLoginOptions) (browserLoginSession, error) {
	f.options = options
	return f.session, nil
}

type fakeBrowserSession struct {
	credential webCredential
	closed     bool
}

func (f *fakeBrowserSession) Screenshot(context.Context) ([]byte, error) {
	return []byte("png"), nil
}

func (f *fakeBrowserSession) Credential(context.Context) (webCredential, bool, error) {
	return f.credential, true, nil
}

func (f *fakeBrowserSession) Close() {
	f.closed = true
}

func TestBrowserSessionImportAndChat(t *testing.T) {
	var sawSession, sawProof, deleted bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer browser-token" || r.Header.Get("X-Device-Id") != "browser-device" {
			t.Errorf("missing browser identity on %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/users/current":
			_ = json.NewEncoder(w).Encode(testEnvelope(map[string]any{"id": "user-1", "email": "user@example.com", "chat": map[string]any{"is_muted": 0}}))
		case "/users/auth_token/check_device":
			_ = json.NewEncoder(w).Encode(testEnvelope(map[string]any{"rotate": nil}))
		case "/chat_session/create":
			sawSession = true
			_ = json.NewEncoder(w).Encode(testEnvelope(map[string]any{"chat_session": map[string]string{"id": "session-1"}}))
		case "/chat/create_pow_challenge":
			_ = json.NewEncoder(w).Encode(testEnvelope(map[string]any{"challenge": map[string]any{
				"algorithm": "DeepSeekHashV1", "challenge": "abc", "salt": "salt", "signature": "sig",
				"difficulty": 100, "expire_at": 1, "target_path": completionPath,
			}}))
		case "/chat/completion":
			sawProof = r.Header.Get("X-Ds-Pow-Response") == "proof"
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"v\":{\"response\":{\"fragments\":[{\"type\":\"RESPONSE\",\"content\":\"\"}],\"status\":\"WIP\"}}}\n\n"))
			_, _ = w.Write([]byte("data: {\"p\":\"response/fragments/-1/content\",\"o\":\"APPEND\",\"v\":\"ready\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"p\":\"response/accumulated_token_usage\",\"o\":\"SET\",\"v\":3}\n\n"))
			_, _ = w.Write([]byte("data: {\"p\":\"response/status\",\"o\":\"SET\",\"v\":\"FINISHED\"}\n\n"))
		case "/chat_session/delete":
			deleted = true
			_ = json.NewEncoder(w).Encode(testEnvelope(nil))
		default:
			t.Errorf("unexpected upstream path %s", r.URL.Path)
		}
	}))
	defer upstream.Close()

	stateFile := filepath.Join(t.TempDir(), "accounts.json")
	solver := &fakePow{}
	a, err := newAdapter("adapter-key", stateFile, &upstreamClient{http: upstream.Client(), baseURL: upstream.URL}, solver)
	if err != nil {
		t.Fatal(err)
	}
	browserSession := &fakeBrowserSession{credential: webCredential{Token: "browser-token", DeviceID: "browser-device"}}
	browser := &fakeLoginBrowser{session: browserSession}
	a.loginBrowser = browser
	server := httptest.NewServer(a.handler())
	defer server.Close()
	client := server.Client()
	request := func(method, path, body, owner string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer adapter-key")
		req.Header.Set("X-Login-Owner", owner)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	start := request("POST", "/internal/login/sessions", `{"email":"user@example.com","password":"browser-password"}`, "admin:1")
	if start.StatusCode != 201 {
		t.Fatalf("login start: %d", start.StatusCode)
	}
	var session struct {
		ID string `json:"session_id"`
	}
	if err := json.NewDecoder(start.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	start.Body.Close()
	if browser.options.Email != "user@example.com" || browser.options.Password != "browser-password" {
		t.Fatalf("login options not forwarded: %#v", browser.options)
	}
	otherOwner := request("POST", "/internal/login/sessions/"+session.ID+"/poll", `{}`, "admin:2")
	if otherOwner.StatusCode != 404 {
		t.Fatalf("session owner isolation: %d", otherOwner.StatusCode)
	}
	otherOwner.Body.Close()
	view := request("GET", "/internal/login/sessions/"+session.ID+"/view", "", "admin:1")
	if view.StatusCode != http.StatusOK || view.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("login view: %d %s", view.StatusCode, view.Header.Get("Content-Type"))
	}
	view.Body.Close()
	complete := request("POST", "/internal/login/sessions/"+session.ID+"/poll", `{}`, "admin:1")
	var result map[string]any
	if err := json.NewDecoder(complete.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	complete.Body.Close()
	if complete.StatusCode != 200 || result["status"] != "completed" {
		t.Fatalf("login completion: %d %#v", complete.StatusCode, result)
	}
	responseJSON, _ := json.Marshal(result)
	if strings.Contains(string(responseJSON), "browser-token") || strings.Contains(string(responseJSON), "browser-device") {
		t.Fatal("login response leaked browser credentials")
	}
	if !browserSession.closed {
		t.Fatal("login browser was not closed after completion")
	}
	data, err := os.ReadFile(stateFile)
	if err != nil || !strings.Contains(string(data), "browser-token") {
		t.Fatalf("credential was not persisted: %v", err)
	}

	chat := request("POST", "/v1/chat/completions", `{"model":"deepseek-web-chat","messages":[{"role":"user","content":"hi"}]}`, "")
	var output struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(chat.Body).Decode(&output); err != nil {
		t.Fatal(err)
	}
	chat.Body.Close()
	if chat.StatusCode != 200 || len(output.Choices) != 1 || output.Choices[0].Message.Content != "ready" {
		t.Fatalf("chat response: %d %#v", chat.StatusCode, output)
	}
	if output.Usage.CompletionTokens != 3 || output.Usage.TotalTokens != output.Usage.PromptTokens+3 {
		t.Fatalf("chat usage: %+v", output.Usage)
	}
	if !sawSession || !sawProof || !deleted || solver.input.TargetPath != completionPath {
		t.Fatalf("upstream flow incomplete: session=%v proof=%v delete=%v challenge=%+v", sawSession, sawProof, deleted, solver.input)
	}
}

func TestTextOnlyRequestRejectsTools(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"deepseek-web-chat","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function"}]}`))
	_, _, _, err := parseChat(httptest.NewRecorder(), req)
	if err == nil {
		t.Fatal("tool request should be rejected")
	}
}

var _ builtinlogin.BeginWithOptions = (*adapter)(nil).beginLogin
