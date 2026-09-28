package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"glm-zcode-2api/internal/config"
	"glm-zcode-2api/internal/credential"
	"sub2api/builtinlogin"
)

func TestBigmodelCodingPlanUsesNativeBusinessAuthorization(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "bigmodel-access" {
			t.Errorf("BigModel business authentication must use the raw access token")
		}
		switch r.URL.Path {
		case "/biz/customer/getCustomerInfo":
			_, _ = io.WriteString(w, `{"data":{"organizations":[{"organizationId":"org","projects":[{"projectId":"proj"}]}]}}`)
		case "/biz/v1/organization/org/projects/proj/api_keys":
			if r.Method == http.MethodPost {
				_, _ = io.WriteString(w, `{"data":{"apiKey":"coding-key"}}`)
			} else {
				_, _ = io.WriteString(w, `{"data":[]}`)
			}
		case "/biz/v1/organization/org/projects/proj/api_keys/copy/coding-key":
			_, _ = io.WriteString(w, `{"data":{"secretKey":"coding-secret"}}`)
		default:
			t.Errorf("unexpected business path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	before := zcodeBigmodelBizBaseURL
	zcodeBigmodelBizBaseURL = upstream.URL + "/biz"
	defer func() { zcodeBigmodelBizBaseURL = before }()
	data := map[string]any{"bigmodel": map[string]any{"access_token": "bigmodel-access"}}
	cred, err := buildZcodeCredential(context.Background(), upstream.Client(), data, "bigmodel")
	if err != nil || cred.APIKey != "coding-key.coding-secret" || cred.Plan != credential.PlanCoding || calls != 4 {
		t.Fatalf("Coding Plan authorization failed: err=%v calls=%d", err, calls)
	}
}

// fakeZcodeOAuth 模拟官方轮询授权和 Coding Plan API Key 提取链。
func fakeZcodeOAuth(t *testing.T, seenPollToken *string) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/init":
			body, _ := io.ReadAll(r.Body)
			var payload map[string]string
			_ = json.Unmarshal(body, &payload)
			if payload["provider"] != "zai" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				t.Errorf("invalid polling initialization: provider=%q", payload["provider"])
			}
			*seenPollToken = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"flow_id": "test-flow", "authorize_url": "https://chat.z.ai/api/oauth/authorize?state=flow-state",
				"expires_at": time.Now().Add(5 * time.Minute).Unix(), "poll_interval_sec": 2,
			}})
		case "/poll/test-flow":
			if r.Header.Get("Authorization") != "Bearer "+*seenPollToken {
				t.Error("poll token changed during login")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"status": "ready",
					"token":  "coding-plan-jwt",
					"zai":    map[string]any{"access_token": "oauth-access", "refresh_token": "oauth-refresh"},
					"user":   map[string]any{"user_id": "user-1", "email": "me@example.com"},
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
	var seenPollToken string
	oauth := fakeZcodeOAuth(t, &seenPollToken)
	base := oauth.URL

	restore := overrideZcodeEndpoints(t, base)
	defer restore()

	upstream, capture := fakeUpstream(t, sseScript)
	storePath := filepath.Join(t.TempDir(), "credential.json")
	cfg := config.Default()
	cfg.Listen = "0.0.0.0:9898"
	cfg.APIKey = "local-key"
	cfg.Upstream.BaseURL = upstream.URL
	cfg.Upstream.GatewayOrigin = upstream.URL
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
	if session.Mode != "poll" || session.AuthURL == "" {
		t.Fatalf("unexpected session: %+v", session)
	}
	if !strings.Contains(session.AuthURL, "app_version%3D3.14.3") || seenPollToken == "" {
		t.Fatalf("authorization URL or poll token missing: %s", session.AuthURL)
	}

	callback := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/internal/login/sessions/"+session.SessionID+"/poll", nil)
	req.Header.Set("Authorization", "Bearer local-key")
	req.Header.Set("X-Login-Owner", "admin:1")
	handler.ServeHTTP(callback, req)
	if callback.Code != http.StatusOK {
		t.Fatalf("callback status = %d: %s", callback.Code, callback.Body.String())
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

func TestZcodePollingRejectsIncompleteResults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		status  int
		pending bool
	}{
		{"pending", `{"code":0,"data":{"status":"pending"}}`, 200, true},
		{"failed", `{"code":0,"data":{"status":"failed"}}`, 200, false},
		{"missing provider token", `{"code":0,"data":{"status":"ready","token":"secret","user":{"user_id":"u1"}}}`, 200, false},
		{"upstream error", `{"secret":"must-not-leak"}`, 500, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer poll-secret" || r.URL.Path != "/poll/flow" {
					t.Error("poll request lost its session binding")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			before := zcodeOAuthCLIURL
			zcodeOAuthCLIURL = server.URL
			defer func() { zcodeOAuthCLIURL = before }()
			_, err := pollZcodeOAuthFlow(context.Background(), server.Client(), "poll-secret", "flow", "bigmodel")
			if tc.pending {
				if !errors.Is(err, builtinlogin.ErrPending) {
					t.Fatalf("want pending, got %v", err)
				}
				return
			}
			var public *builtinlogin.PublicError
			if !errors.As(err, &public) || strings.Contains(public.Message, "secret") {
				t.Fatalf("want sanitized public error, got %v", err)
			}
		})
	}
}

func overrideZcodeEndpoints(t *testing.T, base string) func() {
	t.Helper()
	prev := []string{zcodeOAuthCLIURL, zcodeUserInfoURL, zcodeCustomerInfoURL, zcodeBizLoginURL, zcodeBizBaseURL}
	zcodeOAuthCLIURL = base
	zcodeUserInfoURL = base + "/userinfo"
	zcodeBizLoginURL = base + "/biz/login"
	zcodeCustomerInfoURL = base + "/customer"
	zcodeBizBaseURL = base + "/biz"
	return func() {
		zcodeOAuthCLIURL, zcodeUserInfoURL, zcodeCustomerInfoURL, zcodeBizLoginURL, zcodeBizBaseURL = prev[0], prev[1], prev[2], prev[3], prev[4]
	}
}
