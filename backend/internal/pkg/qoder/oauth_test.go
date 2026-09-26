package qoder

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeneratePKCEChallengeMatchesVerifier(t *testing.T) {
	pair, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	if len(pair.Verifier) < 43 || len(pair.Verifier) > 128 {
		t.Fatalf("verifier length = %d, want 43..128", len(pair.Verifier))
	}
	sum := sha256.Sum256([]byte(pair.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if pair.Challenge != want {
		t.Fatalf("challenge = %q, want %q", pair.Challenge, want)
	}
}

func TestAuthBaseIsQoderCom(t *testing.T) {
	// qodercli 的 loginWithDeviceFlow 传入 lN("base") = https://qoder.com，
	// 不是 center.qoder.sh（后者 /device/selectAccounts 返回 404）。
	if ProdAuthBaseURL != "https://qoder.com" {
		t.Fatalf("ProdAuthBaseURL = %q, want https://qoder.com", ProdAuthBaseURL)
	}
	t.Setenv(envAuthBaseURL, "")
	t.Setenv(envCenterBaseURL, "")
	if authBase() != "https://qoder.com" {
		t.Fatalf("authBase() = %q", authBase())
	}
	raw, err := BuildAuthURL("c", "n", "m", DeviceFlowClientID)
	if err != nil {
		t.Fatalf("BuildAuthURL: %v", err)
	}
	if !strings.HasPrefix(raw, "https://qoder.com/device/selectAccounts?") {
		t.Fatalf("auth url = %q", raw)
	}
}

func TestBuildAuthURLUsesDeviceFlowParams(t *testing.T) {
	nonce, _ := GenerateNonce()
	machine, _ := MachineID()
	raw, err := BuildAuthURL("CHALLENGE", nonce, machine, DeviceFlowClientID)
	if err != nil {
		t.Fatalf("BuildAuthURL: %v", err)
	}
	parsed, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	query := parsed.URL.Query()
	if parsed.URL.Path != "/device/selectAccounts" {
		t.Fatalf("path = %q", parsed.URL.Path)
	}
	if query.Get("challenge") != "CHALLENGE" || query.Get("challenge_method") != "S256" {
		t.Fatalf("unexpected challenge params: %v", query)
	}
	if query.Get("nonce") != nonce || query.Get("machine_id") != machine {
		t.Fatalf("unexpected nonce/machine params: %v", query)
	}
	if query.Get("client_id") != DeviceFlowClientID {
		t.Fatalf("client_id = %q", query.Get("client_id"))
	}
}

func TestPollOncePendingAndDone(t *testing.T) {
	var authorized bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/deviceToken/poll" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if !authorized {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"device-token","refresh_token":"rt"}`))
	}))
	defer server.Close()

	t.Setenv(envOpenAPIBaseURL, server.URL)
	client := &Client{HTTPClient: server.Client()}

	pending, err := client.PollOnce(context.Background(), "nonce", "verifier", "S256")
	if err != nil {
		t.Fatalf("PollOnce pending: %v", err)
	}
	if pending.Done {
		t.Fatal("expected pending poll to return Done=false")
	}

	authorized = true
	done, err := client.PollOnce(context.Background(), "nonce", "verifier", "S256")
	if err != nil {
		t.Fatalf("PollOnce done: %v", err)
	}
	if !done.Done || done.Token == nil || done.Token.AccessToken != "device-token" {
		t.Fatalf("unexpected done result: %+v", done)
	}
	if done.Token.RefreshToken != "rt" {
		t.Fatalf("refresh_token = %q", done.Token.RefreshToken)
	}
}

func TestRefreshReturnsRotatedToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/deviceToken/refresh" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_token":"new-token","refresh_token":"new-rt","expires_at":"2030-01-02T03:04:05Z"}`))
	}))
	defer server.Close()

	t.Setenv(envOpenAPIBaseURL, server.URL)
	client := &Client{HTTPClient: server.Client()}

	result, err := client.Refresh(context.Background(), "old-rt")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if result.AccessToken != "new-token" || result.RefreshToken != "new-rt" {
		t.Fatalf("unexpected refresh result: %+v", result)
	}
	if result.ExpiresAt == 0 {
		t.Fatal("expected expires_at to be parsed")
	}
}

func TestClientIDSwitchesForCNRegion(t *testing.T) {
	t.Setenv(envRegion, "")
	if ClientID() != DeviceFlowClientID {
		t.Fatalf("default client id = %q", ClientID())
	}
	t.Setenv(envRegion, "cn")
	if ClientID() != DeviceFlowClientIDCN {
		t.Fatalf("cn client id = %q", ClientID())
	}
}
