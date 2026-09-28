package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"glm-zcode-2api/internal/config"
	"glm-zcode-2api/internal/credential"
)

func startPlanJWT(expires int64) string {
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, expires))) + ".signature"
}

func startPlanServer(t *testing.T, base, verifierURL string) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.APIKey = "local-key"
	cfg.Upstream.APIKey = "old-coding-plan-key"
	cfg.Upstream.BaseURL = "https://incorrect-coding-plan.invalid/api/anthropic"
	cfg.Upstream.StartPlanVerifierURL = verifierURL
	cfg.Upstream.StartPlanVerifierKey = "verifier-key"
	cfg.Upstream.CredentialStorePath = filepath.Join(t.TempDir(), "credential.json")
	s := New(cfg, log.New(io.Discard, "", 0))
	cred := credential.Credential{
		APIKey:     startPlanJWT(time.Now().Add(time.Hour).Unix()),
		BaseURL:    base + "/api/v1/zcode-plan/anthropic",
		ProviderID: "account:bigmodel-start-plan", Plan: credential.PlanStart,
	}
	if err := s.loginCredStore.Save(cred); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStartPlanUsesJWTDedicatedEndpointAndFreshVerification(t *testing.T) {
	var proofs int
	verifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/verify" || r.Header.Get("Authorization") != "Bearer verifier-key" {
			t.Errorf("unexpected verifier request: %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "old-coding-plan-key") || strings.Contains(string(body), "signature") {
			t.Error("upstream credential leaked to verification service")
		}
		proofs++
		_ = json.NewEncoder(w).Encode(map[string]string{"captcha_verify_param": fmt.Sprintf("proof-%d", proofs), "captcha_region": "cn"})
	}))
	defer verifier.Close()
	var keys, params []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/zcode-plan/anthropic/v1/messages" {
			t.Errorf("Start Plan used wrong endpoint: %s", r.URL.Path)
		}
		keys = append(keys, r.Header.Get("x-api-key"))
		params = append(params, r.Header.Get("X-Aliyun-Captcha-Verify-Param"))
		if r.Header.Get("X-Aliyun-Captcha-Verify-Region") != "cn" || r.Header.Get("X-ZCode-App-Version") != "3.14.3" {
			t.Error("native request headers missing")
		}
		_, _ = io.WriteString(w, sseScript)
	}))
	defer upstream.Close()
	s := startPlanServer(t, upstream.URL, verifier.URL)
	for _, stream := range []bool{false, true} {
		response := post(t, s.Handler(), chatBody(stream))
		if response.Code != http.StatusOK {
			t.Fatalf("chat failed: %d %s", response.Code, response.Body.String())
		}
	}
	if proofs != 2 || len(keys) != 2 || keys[0] == "old-coding-plan-key" || keys[0] != keys[1] || params[0] != "proof-1" || params[1] != "proof-2" {
		t.Fatalf("wrong plan credentials or proof lifecycle: proofs=%d params=%v", proofs, params)
	}
}

func TestStartPlanVerificationFailureDoesNotSendModelRequest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"interactive", 409, `{"secret":"must-not-leak"}`, 409},
		{"failed", 500, `{"secret":"must-not-leak"}`, 502},
		{"empty proof", 200, `{"captcha_verify_param":"","captcha_region":"cn"}`, 502},
		{"invalid region", 200, `{"captcha_verify_param":"proof","captcha_region":"invalid"}`, 502},
		{"invalid header", 200, `{"captcha_verify_param":"proof\r\nInjected: value","captcha_region":"cn"}`, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer verifier.Close()
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
			defer upstream.Close()
			response := post(t, startPlanServer(t, upstream.URL, verifier.URL).Handler(), chatBody(true))
			if response.Code != tc.want || calls != 0 || strings.Contains(response.Body.String(), "must-not-leak") {
				t.Fatalf("failure was not isolated: status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
			}
		})
	}
}

func TestStartPlanMissingVerifierStopsBeforeUpstream(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer upstream.Close()
	response := post(t, startPlanServer(t, upstream.URL, "").Handler(), chatBody(false))
	if response.Code != http.StatusServiceUnavailable || calls != 0 || !strings.Contains(response.Body.String(), "start_plan_verifier_unavailable") {
		t.Fatalf("missing verifier was not surfaced: %d calls=%d %s", response.Code, calls, response.Body.String())
	}
}

func TestStartPlanModelsRejectCorruptCredential(t *testing.T) {
	s := startPlanServer(t, "http://unused.invalid", "")
	if err := os.WriteFile(s.cfg.Upstream.CredentialStorePath, []byte(`{"plan":"start-plan"}`), 0600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer local-key")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "upstream_credential_unavailable") {
		t.Fatalf("corrupt credential concealed: %d %s", response.Code, response.Body.String())
	}
}

func TestStartPlanModelsUseActiveEntitlements(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/zcode-plan/billing/balance" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer header.") {
			t.Errorf("wrong entitlement request: %s", r.URL.Path)
		}
		_, _ = fmt.Fprintf(w, `{"code":0,"data":{"balances":[{"capabilities":["model:GLM-5.3-Flash"],"expires_at":%d},{"capabilities":["model:glm-5.3"],"expires_at":1}]}}`, time.Now().Add(time.Hour).Unix())
	}))
	defer upstream.Close()
	s := startPlanServer(t, upstream.URL, "")
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer local-key")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"id":"glm-5.3"`) || !strings.Contains(response.Body.String(), `"id":"glm-5.3-flash"`) {
		t.Fatalf("catalog does not match active grants: %d %s", response.Code, response.Body.String())
	}
}

func TestStartPlanExpiredLoginStopsBeforeVerification(t *testing.T) {
	s := startPlanServer(t, "http://unused.invalid", "http://unused.invalid")
	cred, _, _ := s.loginCredStore.Load()
	cred.APIKey = startPlanJWT(time.Now().Add(-time.Hour).Unix())
	if err := s.loginCredStore.Save(cred); err != nil {
		t.Fatal(err)
	}
	response := post(t, s.Handler(), chatBody(false))
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "start_plan_reauthorization_required") {
		t.Fatalf("expired login was not surfaced: %d %s", response.Code, response.Body.String())
	}
}

func TestStartPlanWebLoginDoesNotDeriveCodingPlanAPIKey(t *testing.T) {
	jwt := startPlanJWT(time.Now().Add(time.Hour).Unix())
	requests := 0
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/init":
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["provider"] != "bigmodel" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				t.Errorf("wrong polling initialization: %v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"flow_id": "start-flow", "authorize_url": "https://bigmodel.cn/login?appId=zcode&state=flow-state",
				"expires_at": time.Now().Add(5 * time.Minute).Unix(), "poll_interval_sec": 2,
			}})
		case "/poll/start-flow":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"status": "ready", "token": jwt, "user": map[string]string{"user_id": "user-1", "name": "Start Plan user"},
				"bigmodel": map[string]string{"access_token": "bigmodel-access"},
			}})
		case "/api/v1/zcode-plan/billing/balance":
			_, _ = io.WriteString(w, `{"code":0,"data":{"balances":[{"capabilities":["model:glm-5.3"]}]}}`)
		default:
			t.Errorf("Start Plan attempted Coding Plan key derivation: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer oauth.Close()
	restore := overrideZcodeEndpoints(t, oauth.URL)
	defer restore()
	cfg := config.Default()
	cfg.APIKey = "local-key"
	cfg.Upstream.GatewayOrigin = oauth.URL
	cfg.Upstream.CredentialStorePath = filepath.Join(t.TempDir(), "credential.json")
	s := New(cfg, log.New(io.Discard, "", 0))
	handler := s.Handler()
	call := func(path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer local-key")
		request.Header.Set("X-Login-Owner", "admin:1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	start := call("/internal/login/sessions", `{"plan":"start-plan","provider":"bigmodel"}`)
	var session struct {
		ID  string `json:"session_id"`
		URL string `json:"auth_url"`
	}
	_ = json.Unmarshal(start.Body.Bytes(), &session)
	if start.Code != 201 || !strings.Contains(session.URL, "bigmodel.cn/login?") || !strings.Contains(session.URL, "appId=zcode") || !strings.Contains(session.URL, "app_version%3D3.14.3") {
		t.Fatalf("wrong authorization entry: %d %s", start.Code, start.Body.String())
	}
	callback := call("/internal/login/sessions/"+session.ID+"/poll", `{}`)
	if callback.Code != 200 || strings.Contains(callback.Body.String(), jwt) {
		t.Fatalf("login failed or exposed credentials: %d %s", callback.Code, callback.Body.String())
	}
	cred, ok, err := s.loginCredStore.Load()
	if err != nil || !ok || cred.Plan != credential.PlanStart || cred.APIKey != jwt || cred.ProviderID != "account:bigmodel-start-plan" || requests != 3 {
		t.Fatalf("wrong persisted plan or request chain: ok=%v err=%v requests=%d", ok, err, requests)
	}
}

func TestStartPlanFailedLoginPreservesExistingCredential(t *testing.T) {
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/init" {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"flow_id": "failed-flow", "authorize_url": "https://bigmodel.cn/login?state=failed-state",
				"expires_at": time.Now().Add(5 * time.Minute).Unix(), "poll_interval_sec": 2,
			}})
			return
		}
		_, _ = io.WriteString(w, `{"code":0,"data":{"status":"failed"}}`)
	}))
	defer oauth.Close()
	restore := overrideZcodeEndpoints(t, oauth.URL)
	defer restore()
	s := startPlanServer(t, oauth.URL, "")
	s.cfg.Upstream.GatewayOrigin = oauth.URL
	before, _, _ := s.loginCredStore.Load()
	handler := s.Handler()
	call := func(path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer local-key")
		request.Header.Set("X-Login-Owner", "admin:1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	start := call("/internal/login/sessions", `{"plan":"start-plan","provider":"bigmodel"}`)
	var session struct {
		ID string `json:"session_id"`
	}
	if start.Code != http.StatusCreated || json.Unmarshal(start.Body.Bytes(), &session) != nil || session.ID == "" {
		t.Fatalf("start login: %d %s", start.Code, start.Body.String())
	}
	result := call("/internal/login/sessions/"+session.ID+"/poll", `{}`)
	after, ok, err := s.loginCredStore.Load()
	if result.Code != http.StatusForbidden || err != nil || !ok || before.APIKey != after.APIKey || before.Plan != after.Plan {
		t.Fatalf("failed login replaced credentials: status=%d ok=%v err=%v", result.Code, ok, err)
	}
}
