package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sub2api/builtinlogin"
)

type credential struct {
	Token    string `json:"token"`
	UID      string `json:"uid"`
	Nickname string `json:"nickname"`
	TenantID string `json:"tenant_id"`
	AppID    string `json:"app_id,omitempty"`
}

func tokenIdentity(input string) (string, string, error) {
	token := strings.TrimSpace(input)
	if strings.HasPrefix(token, "https://") {
		u, err := url.Parse(token)
		if err != nil || u.User != nil || u.Host != "vibex.runninghub.cn" {
			return "", "", problem(400, "invalid_credential", "Use the RunningHub access token or VibeX sign-in return URL")
		}
		fragment, err := url.ParseQuery(u.Fragment)
		if err != nil {
			return "", "", problem(400, "invalid_credential", "Invalid sign-in return URL")
		}
		token = fragment.Get("rh-sso-token")
	}
	token = strings.TrimPrefix(token, "Bearer ")
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 16<<10 {
		return "", "", problem(400, "invalid_credential", "Invalid RunningHub access token")
	}
	// JWT 仅用于读取身份提示；授权必须通过上游账号接口验证。
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Sub string `json:"sub"`
		Exp int64  `json:"exp"`
	}
	if err != nil || json.Unmarshal(data, &claims) != nil || claims.Sub == "" || claims.Exp <= time.Now().Unix() {
		return "", "", problem(400, "invalid_credential", "RunningHub access token is invalid or expired")
	}
	for _, part := range parts {
		if _, err := base64.RawURLEncoding.DecodeString(part); err != nil {
			return "", "", problem(400, "invalid_credential", "Invalid RunningHub access token")
		}
	}
	return token, claims.Sub, nil
}

func (a *adapter) save(c credential) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.stateFile), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(a.stateFile), ".vibex-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), a.stateFile)
}

func (a *adapter) snapshot() (credential, error) {
	a.mu.RLock()
	c := a.credential
	a.mu.RUnlock()
	if c.Token == "" {
		return c, problem(401, "vibex_login_required", "Sign in to RunningHub first")
	}
	if _, _, err := tokenIdentity(c.Token); err != nil {
		return c, problem(401, "vibex_login_required", "RunningHub login expired; sign in again")
	}
	return c, nil
}

func (a *adapter) beginLogin(context.Context) (*builtinlogin.Flow, error) {
	return &builtinlogin.Flow{URL: "https://www.runninghub.cn/sso-login?returnUrl=https%3A%2F%2Fvibex.runninghub.cn%2F", Mode: "callback", Complete: func(ctx context.Context, input string) (*builtinlogin.Account, error) {
		token, uid, err := tokenIdentity(input)
		if err != nil {
			return nil, &builtinlogin.PublicError{Status: 400, Message: "Use a valid RunningHub access token or VibeX sign-in return URL"}
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		c := credential{Token: token, UID: uid, TenantID: hex.EncodeToString(random[:])}
		var result struct {
			Data struct {
				ID       json.RawMessage `json:"id"`
				Nickname string          `json:"nickName"`
			} `json:"data"`
		}
		if err := a.call(ctx, c, http.MethodPost, "/uc/getUserInfo", map[string]string{"userId": uid}, &result); err != nil {
			return nil, err
		}
		var verifiedID string
		if json.Unmarshal(result.Data.ID, &verifiedID) != nil {
			verifiedID = string(result.Data.ID)
		}
		if verifiedID == "" || verifiedID != uid {
			return nil, problem(502, "invalid_identity", "Unable to verify RunningHub identity")
		}
		c.Nickname = result.Data.Nickname
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.credential.UID == uid {
			c.TenantID = a.credential.TenantID
			c.AppID = a.credential.AppID
		}
		if err := a.save(c); err != nil {
			return nil, err
		}
		a.credential = c
		return &builtinlogin.Account{UID: uid, Nickname: c.Nickname}, nil
	}}, nil
}
