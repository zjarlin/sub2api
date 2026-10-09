package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 验证服务端能从公开 JWK 校验 ES256 签名，且证明只绑定当前令牌请求。
func checkDPoP(t *testing.T, proof, tokenURL string) {
	t.Helper()
	parts := strings.Split(proof, ".")
	if len(parts) != 3 {
		t.Fatal("invalid DPoP JWT")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		Alg string
		Typ string
		JWK map[string]string
	}
	var claims struct {
		Htm string
		Htu string
		Iat int64
		Jti string
	}
	if json.Unmarshal(headerBytes, &header) != nil || json.Unmarshal(claimsBytes, &claims) != nil {
		t.Fatal("invalid DPoP payload")
	}
	if header.Alg != "ES256" || header.Typ != "dpop+jwt" || header.JWK["kty"] != "EC" || header.JWK["crv"] != "P-256" || header.JWK["d"] != "" {
		t.Fatal("invalid public proof key")
	}
	if claims.Htm != http.MethodPost || claims.Htu != tokenURL || len(claims.Jti) != 64 || claims.Iat < time.Now().Unix()-30 || claims.Iat > time.Now().Unix()+1 {
		t.Fatal("invalid proof binding")
	}
	x, err := base64.RawURLEncoding.DecodeString(header.JWK["x"])
	if err != nil {
		t.Fatal(err)
	}
	y, err := base64.RawURLEncoding.DecodeString(header.JWK["y"])
	if err != nil {
		t.Fatal(err)
	}
	key := ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(signature) != 64 || !ecdsa.Verify(&key, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		t.Fatal("invalid DPoP signature")
	}
}

func TestOAuthPKCEAndLoopbackCallback(t *testing.T) {
	flow, err := newOAuthLogin()
	if err != nil {
		t.Fatal(err)
	}
	defer flow.Close()
	authorize, err := url.Parse(flow.authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	query := authorize.Query()
	challenge := sha256.Sum256([]byte(flow.grant.CodeVerifier))
	if query.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(challenge[:]) || query.Get("code_challenge_method") != "SHA-256" || query.Get("client_id") != askOAuthClientID || query.Get("auth_callback_url") != flow.redirectURI {
		t.Fatal("invalid authorization parameters")
	}
	callback, err := url.Parse(flow.redirectURI)
	if err != nil {
		t.Fatal(err)
	}
	if callback.Hostname() != "127.0.0.1" || callback.Port() != query.Get("port") {
		t.Fatal("callback must use the allocated loopback port")
	}
	callback.RawQuery = url.Values{"code": {"first-code"}}.Encode()
	response, err := http.Get(callback.String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("callback rejected")
	}
	if received := <-flow.callback; received.code != "first-code" || received.denied {
		t.Fatal("callback was not captured")
	}
	callback.RawQuery = url.Values{"code": {"second-code"}}.Encode()
	response, err = http.Get(callback.String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	select {
	case <-flow.callback:
		t.Fatal("duplicate callback must not replay token exchange")
	default:
	}
}

func TestIAMRefreshSerializesRotationAndPersistsContext(t *testing.T) {
	grant, err := newIAMGrant()
	if err != nil {
		t.Fatal(err)
	}
	grant.RefreshToken = "old-refresh"
	grant.UserName = "native-user"
	grant.Credentials = iamCredentials{AccessKeyID: "old-ak", SecretAccessKey: "old-sk", SecurityToken: "old-token", Expiration: time.Now().Add(-time.Minute).Format(time.RFC3339)}
	var requests atomic.Int64
	var tokenURL string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		checkDPoP(t, r.Header.Get("DPoP"), tokenURL)
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Method != http.MethodPost || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("client_id") != askOAuthClientID || r.Form.Get("code_verifier") != grant.CodeVerifier || r.Form.Get("refresh_token") != "old-refresh" || r.Form.Has("redirect_uri") {
			t.Error("invalid refresh request")
		}
		writeJSON(w, 200, map[string]any{"credentials": iamCredentials{AccessKeyID: "new-ak", SecretAccessKey: "new-sk", SecurityToken: "new-token", Expiration: time.Now().Add(time.Hour).Format(time.RFC3339)}, "refresh_token": "rotated-refresh"})
	}))
	defer upstream.Close()
	tokenURL = upstream.URL
	a, err := newAdapter("test-key", t.TempDir()+"/state.json", defaultAskBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	a.tokenURL = tokenURL
	a.credential = credential{UID: "verified-user", Nickname: "display-name", IAM: grant}
	var wg sync.WaitGroup
	gate := make(chan struct{})
	errors := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			<-gate
			c, err := a.currentCredential(context.Background())
			if err == nil && (c.IAM.Credentials.AccessKeyID != "new-ak" || c.IAM.UserName != "native-user" || c.UID != "verified-user") {
				t.Error("refresh changed identity or returned stale credentials")
			}
			errors <- err
		})
	}
	close(gate)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("refresh requests=%d", requests.Load())
	}
	reloaded, err := newAdapter("test-key", a.stateFile, defaultAskBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	stored := reloaded.credential.IAM
	if stored.RefreshToken != "rotated-refresh" || stored.CodeVerifier != grant.CodeVerifier || stored.PrivateKeyPEM != grant.PrivateKeyPEM || stored.Credentials.AccessKeyID != "new-ak" {
		t.Fatal("refresh context was not persisted")
	}
	info, err := os.Stat(a.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatal("credential file must be private")
	}
}

func TestIAMTokenErrorsDoNotLeakOrReplaceCredentials(t *testing.T) {
	grant, err := newIAMGrant()
	if err != nil {
		t.Fatal(err)
	}
	grant.RefreshToken = "private-refresh"
	grant.Credentials = iamCredentials{AccessKeyID: "private-ak", SecretAccessKey: "private-sk", SecurityToken: "private-token", Expiration: time.Now().Add(-time.Minute).Format(time.RFC3339)}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"error_description":"private-refresh private-ak private-sk private-token"}`)
	}))
	defer upstream.Close()
	a, err := newAdapter("test-key", t.TempDir()+"/state.json", defaultAskBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	a.tokenURL = upstream.URL
	a.credential = credential{IAM: grant}
	_, err = a.currentCredential(context.Background())
	assertAskError(t, err, "madao_login_required")
	if a.credential.IAM != grant {
		t.Fatal("failed refresh replaced the grant")
	}
	for _, secret := range []string{"private-refresh", "private-ak", "private-sk", "private-token"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("authorization error leaked a secret")
		}
	}
}

func TestLegacyCookieCannotReachAskOrCloudAgent(t *testing.T) {
	var requests atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("legacy credential reached upstream")
	}))
	defer upstream.Close()
	a, err := newAdapter("test-key", t.TempDir()+"/state.json", upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	a.credential = credential{Cftk: "legacy", Cookies: []cookiePair{{Name: "sso", Value: "cookie"}}}
	for _, path := range []string{"/healthz", "/v1/models", "/v1/chat/completions"} {
		method := http.MethodGet
		if path == "/v1/chat/completions" {
			method = http.MethodPost
		}
		req := httptest.NewRequest(method, path, strings.NewReader(`{"model":"GLM-5.2","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer test-key")
		rec := httptest.NewRecorder()
		a.handler().ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatalf("%s HTTP %d", path, rec.Code)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("legacy auth must require reauthorization")
	}
}
