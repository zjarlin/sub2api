package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"glm-zcode-2api/internal/config"
	"glm-zcode-2api/internal/credential"
)

// fakeZcodeOAuth 模拟 Z.AI 授权、token 兑换和 API Key 提取链。
func fakeZcodeOAuth(t *testing.T, seenCode *string) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			body, _ := io.ReadAll(r.Body)
			var payload map[string]string
			_ = json.Unmarshal(body, &payload)
			*seenCode = payload["code"]
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"data": map[string]any{
					"token": "coding-plan-jwt",
					"zai":   map[string]any{"access_token": "oauth-access", "refresh_token": "oauth-refresh"},
					"user":  map[string]any{"email": "me@example.com"},
				},
			})
		case "/userinfo":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"email": "me@example.com"}})
		case "/biz/login":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"access_token": "biz-token"}})
		case "/customer":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"organizations": []map[string]any{{
				"organizationId":   "org-1",
				"organizationName": "默认机构",
				"projects":         []map[string]any{{"projectId": "proj-1", "projectName": "默认项目"}},
			}}}})
		case "/biz/v1/organization/org-1/projects/proj-1/api_keys":
			if r.Method == http.MethodPost {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"apiKey": "zcode-key"}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{}})
		case "/biz/v1/organization/org-1/projects/proj-1/api_keys/copy/zcode-key":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"secretKey": "zcode-secret"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// 网页授权完成后凭据落盘，且优先于桌面 config.json 被用于上游请求。
func TestZcodeWebAuthorizationPersistsCredential(t *testing.T) {
	var seenCode string
	oauth := fakeZcodeOAuth(t, &seenCode)
	base := oauth.URL

	restore := overrideZcodeEndpoints(t, base)
	defer restore()

	upstream, capture := fakeUpstream(t, sseScript)
	storePath := filepath.Join(t.TempDir(), "credential.json")
	cfg := config.Default()
	cfg.APIKey = "local-key"
	cfg.Upstream.BaseURL = upstream.URL
	cfg.Upstream.APIKey = ""
	cfg.Upstream.CredentialStorePath = storePath
	cfg.Upstream.CredentialConfigPath = filepath.Join(t.TempDir(), "missing.json")
	srv := New(cfg, log.New(io.Discard, "", 0))
	srv.loginHTTP = oauth.Client()
	handler := srv.Handler()

	start := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/login/sessions", nil)
	req.Header.Set("Authorization", "Bearer local-key")
	req.Header.Set("X-Login-Owner", "admin:1")
	handler.ServeHTTP(start, req)
	if start.Code != http.StatusCreated {
		t.Fatalf("start status = %d: %s", start.Code, start.Body.String())
	}
	var session struct {
		SessionID string `json:"session_id"`
		AuthURL   string `json:"auth_url"`
		Mode      string `json:"mode"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.Mode != "callback" || session.AuthURL == "" {
		t.Fatalf("unexpected session: %+v", session)
	}

	callback := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/internal/login/sessions/"+session.SessionID+"/callback", strings.NewReader(`{"callback_url":"http://127.0.0.1:7865/login/callback?code=auth-code"}`))
	req.Header.Set("Authorization", "Bearer local-key")
	req.Header.Set("X-Login-Owner", "admin:1")
	handler.ServeHTTP(callback, req)
	if callback.Code != http.StatusOK {
		t.Fatalf("callback status = %d: %s", callback.Code, callback.Body.String())
	}
	if seenCode != "auth-code" {
		t.Fatalf("authorization code not forwarded: %q", seenCode)
	}

	stored, ok, err := (&credential.CredentialStore{Path: storePath}).Load()
	if err != nil || !ok {
		t.Fatalf("credential not persisted: %v %v", ok, err)
	}
	if stored.APIKey != "zcode-key.zcode-secret" {
		t.Fatalf("unexpected stored key: %q", stored.APIKey)
	}

	response := post(t, handler, chatBody(false))
	if response.Code != http.StatusOK {
		t.Fatalf("chat status = %d: %s", response.Code, response.Body.String())
	}
	_, headers := capture.last(t)
	if headers.Get("x-api-key") != "zcode-key.zcode-secret" {
		t.Fatalf("stored credential not used: %q", headers.Get("x-api-key"))
	}
}

func overrideZcodeEndpoints(t *testing.T, base string) func() {
	t.Helper()
	prev := []string{zcodeAuthorizeURL, zcodeTokenURL, zcodeUserInfoURL, zcodeCustomerInfoURL, zcodeBizLoginURL, zcodeBizBaseURL}
	zcodeAuthorizeURL = base + "/authorize"
	zcodeTokenURL = base + "/token"
	zcodeUserInfoURL = base + "/userinfo"
	zcodeBizLoginURL = base + "/biz/login"
	zcodeCustomerInfoURL = base + "/customer"
	zcodeBizBaseURL = base + "/biz"
	return func() {
		zcodeAuthorizeURL, zcodeTokenURL, zcodeUserInfoURL, zcodeCustomerInfoURL, zcodeBizLoginURL, zcodeBizBaseURL = prev[0], prev[1], prev[2], prev[3], prev[4], prev[5]
	}
}
