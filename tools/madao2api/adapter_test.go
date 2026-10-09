package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFlattenMessagesRejectsTools(t *testing.T) {
	if _, err := flattenMessages([]json.RawMessage{json.RawMessage(`{"role":"user","content":"hi","tool_calls":[{"id":"1"}]}`)}); err == nil {
		t.Fatal("expected tool_calls to be rejected")
	}
}

func TestFlattenMessagesBuildsSinglePrompt(t *testing.T) {
	prompt, err := flattenMessages([]json.RawMessage{
		json.RawMessage(`{"role":"system","content":"be brief"}`),
		json.RawMessage(`{"role":"user","content":[{"type":"text","text":"hello"}]}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "[系统指令] be brief") || !strings.Contains(prompt, "[用户] hello") {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
}

func TestResolveModelAlias(t *testing.T) {
	if got := resolveModel("madao"); got != "GLM-5.2" {
		t.Fatalf("alias not resolved: %q", got)
	}
	if got := resolveModel("GLM-5.1"); got != "GLM-5.1" {
		t.Fatalf("unknown model should pass through: %q", got)
	}
}

func TestClassifyStatusLoginRedirect(t *testing.T) {
	err := classifyStatus(http.StatusOK, []byte("<script>HW-AJAX-REDIRECT</script>"))
	var p *upstreamError
	if err == nil || !errors.As(err, &p) || p.status != 401 {
		t.Fatalf("expected login-required error, got %v", err)
	}
}

func TestAuthRejectsWrongKey(t *testing.T) {
	a := &adapter{key: "secret"}
	handler := a.auth(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}
