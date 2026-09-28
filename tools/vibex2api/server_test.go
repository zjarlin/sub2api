package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func fixtureToken(uid string) string {
	claims, _ := json.Marshal(map[string]any{"sub": uid, "exp": time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix()})
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + "." + base64.RawURLEncoding.EncodeToString([]byte("fixture-signature"))
}

type testAdapter struct {
	*adapter
	router http.Handler
}

func fixtureAdapter(t *testing.T, upstream http.Handler) *testAdapter {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	a, err := newAdapter("adapter-key", filepath.Join(t.TempDir(), "credential.json"), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	a.credential = credential{UID: "42", Token: fixtureToken("42"), TenantID: "tenant", AppID: "dedicated"}
	return &testAdapter{adapter: a, router: a.handler()}
}

func invoke(a *testAdapter, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer adapter-key")
	r.Header.Set("X-Login-Owner", "admin:1")
	w := httptest.NewRecorder()
	a.router.ServeHTTP(w, r)
	return w
}

func TestLoginPersistsValidatedIdentityAndHidesToken(t *testing.T) {
	a := fixtureAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/uc/getUserInfo" || r.Header.Get("Authorization") != "Bearer "+fixtureToken("42") {
			t.Error("invalid identity request")
		}
		writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{"id": 42, "nickName": "Test"}})
	}))
	w := invoke(a, "POST", "/internal/login/sessions", "{}")
	var session struct {
		ID string `json:"session_id"`
	}
	if json.Unmarshal(w.Body.Bytes(), &session) != nil || session.ID == "" {
		t.Fatal(w.Body.String())
	}
	body, _ := json.Marshal(map[string]string{"callback_url": fixtureToken("42")})
	w = invoke(a, "POST", "/internal/login/sessions/"+session.ID+"/callback", string(body))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "completed") || strings.Contains(w.Body.String(), "fixture-signature") || strings.Contains(w.Body.String(), fixtureToken("42")) {
		t.Fatal(w.Code, w.Body.String())
	}
	info, err := os.Stat(a.stateFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions", err)
	}
	reloaded, err := newAdapter(a.key, a.stateFile, a.origin)
	if err != nil || reloaded.credential.AppID != "dedicated" || reloaded.credential.Token != fixtureToken("42") {
		t.Fatal("state was not preserved", err)
	}
}

func TestLoginRejectsUnverifiedIdentity(t *testing.T) {
	a := fixtureAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"code": 403, "msg": "TOKEN_INVALID"})
	}))
	flow, err := a.beginLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := flow.Complete(context.Background(), fixtureToken("42")); err == nil {
		t.Fatal("accepted unverified token")
	}
	if _, err := os.Stat(a.stateFile); !os.IsNotExist(err) {
		t.Fatal("saved rejected credentials")
	}
}

func TestModelsUsesLiveCatalogAndRejectsHTTP200AuthEnvelope(t *testing.T) {
	var expired atomic.Bool
	a := fixtureAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expired.Load() {
			writeJSON(w, 200, map[string]any{"code": 403, "msg": "TOKEN_MISSION"})
			return
		}
		writeJSON(w, 200, map[string]any{"providers": []any{map[string]any{"id": "actual-provider"}, map[string]any{"id": "disabled", "enabled": false}}})
	}))
	w := invoke(a, "GET", "/v1/models", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "actual-provider") || strings.Contains(w.Body.String(), "disabled") {
		t.Fatal(w.Code, w.Body.String())
	}
	expired.Store(true)
	w = invoke(a, "GET", "/v1/models", "")
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestUnsupportedChatInputsDoNotReachUpstream(t *testing.T) {
	a := fixtureAdapter(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unsupported input reached upstream") }))
	for _, extra := range []string{`,"tools":[{"type":"function"}]`, `,"temperature":0`, `,"max_tokens":100`} {
		w := invoke(a, "POST", "/v1/chat/completions", `{"model":"model","messages":[{"role":"user","content":"hello"}]`+extra+`}`)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, message := range []string{`{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/image"}}]}`, `{"role":"assistant","content":"","tool_calls":[{}]}`} {
		w := invoke(a, "POST", "/v1/chat/completions", `{"model":"model","messages":[`+message+`]}`)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func chatFixture(t *testing.T, busy bool, failCode string, sessions *atomic.Int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/vc/api/apps/dedicated" {
			writeJSON(w, 200, map[string]any{"live": map[string]string{"status": "running"}})
			return
		}
		if r.URL.Path == "/vc/api/llm-providers" {
			writeJSON(w, 200, map[string]any{"providers": []any{map[string]string{"id": "live-model"}}})
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/app-ws/") {
			writeJSON(w, 200, map[string]bool{"ok": true})
			return
		}
		if r.Header.Get("RH-TOKEN") == "" || r.Header.Get("Cookie") == "" {
			t.Error("missing websocket authentication")
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		e, err := readWS(ctx, conn)
		if err != nil || eventType(e) != "init" {
			t.Error("missing init")
			return
		}
		_ = sendWS(ctx, conn, map[string]any{"type": "session_info", "session_id": "old"})
		_ = sendWS(ctx, conn, map[string]any{"type": "run_status", "active": busy})
		_ = sendWS(ctx, conn, map[string]any{"type": "ready"})
		if busy {
			_, _ = readWS(ctx, conn)
			return
		}
		e, err = readWS(ctx, conn)
		if err != nil || eventType(e) != "new_session" {
			t.Error("missing new_session")
			return
		}
		id := sessions.Add(1)
		_ = sendWS(ctx, conn, map[string]any{"type": "session_info", "session_id": fmt.Sprintf("fresh-%d", id)})
		e, err = readWS(ctx, conn)
		if err != nil || eventType(e) != "prompt" {
			t.Error("missing prompt")
			return
		}
		if failCode == "WAIT_FOR_CANCEL" {
			sessions.Add(1)
			e, err = readWS(ctx, conn)
			if err == nil && eventType(e) == "cancel" {
				sessions.Add(1)
			}
			return
		}
		if failCode != "" {
			_ = sendWS(ctx, conn, map[string]string{"type": "error", "code": failCode, "message": "secret-token-must-not-leak"})
			_, _ = readWS(ctx, conn)
			return
		}
		text := fmt.Sprintf("reply-%d", id)
		_ = sendWS(ctx, conn, map[string]any{"type": "claude_event", "event": map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "delta": map[string]string{"type": "text_delta", "text": text}}}})
		_ = sendWS(ctx, conn, map[string]any{"type": "claude_event", "event": map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}}}})
		_ = sendWS(ctx, conn, map[string]any{"type": "claude_event", "event": map[string]any{"type": "result", "usage": map[string]int{"input_tokens": 5, "output_tokens": 2}}})
		_ = sendWS(ctx, conn, map[string]string{"type": "done"})
		_, _ = readWS(ctx, conn)
	})
}

func TestChatIsolationAndStreaming(t *testing.T) {
	var sessions atomic.Int32
	a := fixtureAdapter(t, chatFixture(t, false, "", &sessions))
	for i, stream := range []bool{false, true} {
		body := fmt.Sprintf(`{"model":"live-model","messages":[{"role":"user","content":"hello"}],"stream":%t,"stream_options":{"include_usage":true}}`, stream)
		w := invoke(a, "POST", "/v1/chat/completions", body)
		if w.Code != 200 || strings.Count(w.Body.String(), fmt.Sprintf("reply-%d", i+1)) != 1 {
			t.Fatal(w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"total_tokens":7`) {
			t.Fatal("missing upstream usage", w.Body.String())
		}
		if stream && !strings.Contains(w.Body.String(), "data: [DONE]") {
			t.Fatal("missing stream finish")
		}
	}
}

func TestFirstRequestCreatesAndPersistsDedicatedProject(t *testing.T) {
	var sessions, creations atomic.Int32
	base := chatFixture(t, false, "", &sessions)
	a := fixtureAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/vc/api/apps" {
			creations.Add(1)
			writeJSON(w, 200, map[string]any{"app": map[string]string{"app_id": "dedicated"}})
			return
		}
		base.ServeHTTP(w, r)
	}))
	a.credential.AppID = ""
	for range 2 {
		w := invoke(a, "POST", "/v1/chat/completions", `{"model":"live-model","messages":[{"role":"user","content":"hello"}]}`)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if creations.Load() != 1 {
		t.Fatal("created duplicate projects")
	}
	reloaded, err := newAdapter(a.key, a.stateFile, a.origin)
	if err != nil || reloaded.credential.AppID != "dedicated" {
		t.Fatal("project ID was not persisted", err)
	}
}

func TestChatRefusesBusyProjectAndMapsQuotaErrors(t *testing.T) {
	for _, item := range []struct {
		busy   bool
		code   string
		status int
	}{{true, "", 409}, {false, "FREE_LLM_QUOTA_EXCEEDED", 429}, {false, "BALANCE_INSUFFICIENT", 402}} {
		var sessions atomic.Int32
		a := fixtureAdapter(t, chatFixture(t, item.busy, item.code, &sessions))
		w := invoke(a, "POST", "/v1/chat/completions", `{"model":"live-model","messages":[{"role":"user","content":"hello"}]}`)
		if w.Code != item.status || strings.Contains(w.Body.String(), "secret-token") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestUnauthenticatedAdapterAndQueueCancellation(t *testing.T) {
	a := fixtureAdapter(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unexpected upstream request") }))
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, httptest.NewRequest("GET", "/v1/models", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	a.slot <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if a.acquire(ctx) == nil {
		t.Fatal("accepted cancelled request")
	}
}

func TestRequestCancellationReachesUpstream(t *testing.T) {
	var progress atomic.Int32
	a := fixtureAdapter(t, chatFixture(t, false, "WAIT_FOR_CANCEL", &progress))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"live-model","messages":[{"role":"user","content":"hello"}]}`)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer adapter-key")
	done := make(chan struct{})
	go func() { a.router.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for progress.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if progress.Load() < 2 {
		t.Fatal("generation did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled handler did not exit")
	}
	for progress.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if progress.Load() != 3 {
		t.Fatal("cancel was not sent to the upstream")
	}
}
