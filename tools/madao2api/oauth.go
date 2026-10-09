// oauth.go 实现官方插件的 PKCE、DPoP 授权和临时 IAM 凭据刷新。
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultTokenURL    = "https://sts.cn-north-4.myhuaweicloud.com/v1/oauth2/tokens"
	portalAuthorizeURL = "https://codearts.huaweicloud.com/portal/authorize"
	askOAuthClientID   = "vscode-codebot"
)

type iamCredentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SecurityToken   string `json:"security_token"`
	Expiration      string `json:"expiration"`
}

type iamGrant struct {
	Credentials   iamCredentials `json:"credentials"`
	RefreshToken  string         `json:"refresh_token"`
	CodeVerifier  string         `json:"code_verifier"`
	PrivateKeyPEM string         `json:"private_key"`
	UserName      string         `json:"user_name,omitempty"`
}

func (c iamCredentials) usable() bool {
	return c.AccessKeyID != "" && c.SecretAccessKey != "" && c.SecurityToken != ""
}

func (c iamCredentials) needsRefresh() bool {
	expires, err := time.Parse(time.RFC3339, c.Expiration)
	return err != nil || time.Now().Add(2*time.Minute).After(expires)
}

func newIAMGrant() (*iamGrant, error) {
	var verifierBytes [32]byte
	if _, err := rand.Read(verifierBytes[:]); err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return &iamGrant{
		CodeVerifier:  base64.RawURLEncoding.EncodeToString(verifierBytes[:]),
		PrivateKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})),
	}, nil
}

func makeDPoP(grant *iamGrant, tokenURL string) (string, error) {
	block, _ := pem.Decode([]byte(grant.PrivateKeyPEM))
	if block == nil {
		return "", errors.New("Invalid CodeArts authorization key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", errors.New("Invalid CodeArts authorization key")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return "", errors.New("Invalid CodeArts authorization key")
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	header, err := json.Marshal(map[string]any{
		"alg": "ES256", "typ": "dpop+jwt",
		"jwk": map[string]string{
			"kty": "EC", "crv": "P-256",
			"x": base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))),
			"y": base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32))),
		},
	})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{
		"htm": http.MethodPost, "htu": tokenURL, "iat": time.Now().Unix(), "jti": hex.EncodeToString(nonce[:]),
	})
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func requestIAMToken(ctx context.Context, client *http.Client, tokenURL string, grant *iamGrant, form url.Values) (*iamGrant, error) {
	proof, err := makeDPoP(grant, tokenURL)
	if err != nil {
		return nil, problem(401, "madao_login_required", "CodeArts Ask authorization is invalid; sign in again")
	}
	requestContext, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestContext, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("DPoP", proof)
	response, err := client.Do(req)
	if err != nil {
		return nil, problem(502, "upstream_unavailable", "CodeArts Ask authorization is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusTooManyRequests {
			return nil, problem(429, "quota_exceeded", "CodeArts authorization is rate limiting; retry later")
		}
		if response.StatusCode >= 500 {
			return nil, problem(502, "upstream_error", "CodeArts authorization is unavailable")
		}
		return nil, problem(401, "madao_login_required", "CodeArts Ask authorization expired; sign in again")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, problem(502, "invalid_upstream_response", "Invalid CodeArts authorization response")
	}
	var token struct {
		Credentials  iamCredentials `json:"credentials"`
		RefreshToken string         `json:"refresh_token"`
	}
	if json.Unmarshal(data, &token) != nil || !token.Credentials.usable() {
		return nil, problem(502, "invalid_upstream_response", "CodeArts did not return Ask credentials")
	}
	expires, err := time.Parse(time.RFC3339, token.Credentials.Expiration)
	if err != nil || !expires.After(time.Now()) {
		return nil, problem(502, "invalid_upstream_response", "CodeArts returned expired Ask credentials")
	}
	updated := *grant
	updated.Credentials = token.Credentials
	if token.RefreshToken != "" {
		updated.RefreshToken = token.RefreshToken
	}
	if updated.RefreshToken == "" {
		return nil, problem(502, "invalid_upstream_response", "CodeArts did not return an Ask refresh token")
	}
	return &updated, nil
}

// currentCredential 在请求前刷新即将到期的临时密钥，并原子保存轮换后的刷新令牌。
func (a *adapter) currentCredential(ctx context.Context) (credential, error) {
	c, err := a.snapshot()
	if err != nil || !c.IAM.Credentials.needsRefresh() {
		return c, err
	}
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	c, err = a.snapshot()
	if err != nil || !c.IAM.Credentials.needsRefresh() {
		return c, err
	}
	if c.IAM.RefreshToken == "" || c.IAM.CodeVerifier == "" {
		return c, problem(401, "madao_login_required", "CodeArts Ask authorization expired; sign in again")
	}
	grant, err := requestIAMToken(ctx, a.client, a.tokenURL, c.IAM, url.Values{
		"client_id": {askOAuthClientID}, "code_verifier": {c.IAM.CodeVerifier},
		"grant_type": {"refresh_token"}, "refresh_token": {c.IAM.RefreshToken},
	})
	if err != nil {
		return c, err
	}
	c.IAM = grant
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.save(c); err != nil {
		return c, err
	}
	a.credential = c
	return c, nil
}

type oauthCallback struct {
	code   string
	denied bool
}

type oauthLogin struct {
	grant        *iamGrant
	redirectURI  string
	authorizeURL string
	callback     chan oauthCallback
	server       *http.Server
}

// newOAuthLogin 只在隔离浏览器所在机器的回环地址接收授权码。
func newOAuthLogin() (*oauthLogin, error) {
	grant, err := newIAMGrant()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	flow := &oauthLogin{grant: grant, redirectURI: "http://127.0.0.1:" + strconv.Itoa(port) + "/oauth/callback", callback: make(chan oauthCallback, 1)}
	challenge := sha256.Sum256([]byte(grant.CodeVerifier))
	var ticket [16]byte
	if _, err := rand.Read(ticket[:]); err != nil {
		listener.Close()
		return nil, err
	}
	params := url.Values{
		"theme": {"light"}, "locale": {"zh-cn"}, "uri_scheme": {askOAuthClientID}, "client_id": {askOAuthClientID},
		"port": {strconv.Itoa(port)}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"SHA-256"}, "ticket_id": {hex.EncodeToString(ticket[:])},
		"auth_callback_url": {flow.redirectURI}, "plugin-name": {"snap_vscode"}, "plugin-version": {askPluginVersion},
	}
	flow.authorizeURL = portalAuthorizeURL + "?" + params.Encode()
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		denied := r.URL.Query().Get("error") != ""
		if (!denied && code == "") || len(code) > 8192 {
			http.Error(w, "Invalid authorization callback", http.StatusBadRequest)
			return
		}
		once.Do(func() { flow.callback <- oauthCallback{code: code, denied: denied} })
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, "<p>码道 Ask 授权已接收，请返回账号页面完成验证。</p>")
	})
	flow.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second}
	go func() {
		_ = flow.server.Serve(listener)
	}()
	return flow, nil
}

func (o *oauthLogin) Close() {
	_ = o.server.Close()
}
