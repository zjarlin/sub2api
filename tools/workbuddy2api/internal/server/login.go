package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"sub2api/builtinlogin"
	"workbuddy2api/internal/auth"
)

func (h *Handler) beginLogin(ctx context.Context) (*builtinlogin.Flow, error) {
	return beginWorkbuddyLogin(ctx, h.cfg.Upstream.HTTP, "https://copilot.tencent.com", func(a *auth.Auth) error {
		if err := os.MkdirAll(h.cfg.AuthDir, 0700); err != nil {
			return err
		}
		a.FilePath = filepath.Join(h.cfg.AuthDir, "workbuddy-"+a.UID+".json")
		if err := a.SaveAtomic(); err != nil {
			return err
		}
		h.cfg.Pool.Add(a)
		h.cfg.Pool.ReviveDisabled(a.UID)
		return nil
	})
}

// beginWorkbuddyLogin 复用上游设备授权协议；凭证只保存在服务端。
func beginWorkbuddyLogin(ctx context.Context, client *http.Client, base string, save func(*auth.Auth) error) (*builtinlogin.Flow, error) {
	var start struct {
		State string `json:"state"`
		URL   string `json:"authUrl"`
	}
	if err := loginJSON(ctx, client, http.MethodPost, base+"/v2/plugin/auth/state?platform=CLI", "", &start); err != nil {
		return nil, err
	}
	if start.State == "" || start.URL == "" {
		return nil, errors.New("missing authorization state")
	}
	query := "?state=" + url.QueryEscape(start.State)
	var token struct {
		Access  string `json:"accessToken"`
		Refresh string `json:"refreshToken"`
		Expires int64  `json:"expiresIn"`
		Domain  string `json:"domain"`
	}
	var expiresAt int64
	flow := &builtinlogin.Flow{URL: start.URL, Mode: "poll"}
	flow.Complete = func(ctx context.Context, _ string) (*builtinlogin.Account, error) {
		if token.Access == "" {
			if err := loginJSON(ctx, client, http.MethodGet, base+"/v2/plugin/auth/token"+query, "", &token); err != nil {
				return nil, err
			}
			if token.Access == "" {
				return nil, builtinlogin.ErrPending
			}
			if token.Refresh == "" || token.Expires <= 0 {
				token.Access = ""
				return nil, errors.New("invalid token response")
			}
			expiresAt = time.Now().Add(time.Duration(token.Expires) * time.Second).Unix()
		}
		var account struct {
			UID          string `json:"uid"`
			Nickname     string `json:"nickname"`
			EnterpriseID string `json:"enterpriseId"`
		}
		if err := loginJSON(ctx, client, http.MethodGet, base+"/v2/plugin/login/account"+query, token.Access, &account); err != nil {
			return nil, err
		}
		if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`).MatchString(account.UID) {
			return nil, errors.New("invalid account uid")
		}
		a := &auth.Auth{AccessToken: token.Access, RefreshToken: token.Refresh, ExpiresAt: expiresAt, Domain: token.Domain,
			UID: account.UID, Nickname: account.Nickname, EnterpriseID: account.EnterpriseID}
		if err := save(a); err != nil {
			return nil, err
		}
		return &builtinlogin.Account{UID: a.UID, Nickname: a.Nickname}, nil
	}
	return flow, nil
}

func loginJSON(ctx context.Context, client *http.Client, method, endpoint, token string, target any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader("{}"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", "https://www.codebuddy.cn")
	req.Header.Set("Referer", "https://www.codebuddy.cn/")
	req.Header.Set("User-Agent", "CLI/2.63.2 CodeBuddy/2.63.2")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("authorization upstream status %d", res.StatusCode)
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"msg"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&envelope); err != nil {
		return err
	}
	if envelope.Code != 0 {
		// 仅将上游明确的等待状态视为未完成，其他错误不能伪装成永久等待。
		msg := strings.ToLower(strings.TrimSpace(envelope.Message))
		if strings.Contains(endpoint, "/auth/token?") && (envelope.Code == 11217 || msg == "login ing" || msg == "waiting for login") {
			return builtinlogin.ErrPending
		}
		return &builtinlogin.PublicError{Status: 502, Message: "WorkBuddy rejected authorization; start a new login"}
	}
	return json.Unmarshal(envelope.Data, target)
}
