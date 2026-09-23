package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"sub2api/builtinlogin"
	"traework2api/internal/auth"
	"traework2api/internal/upstream"
)

func randomLoginID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// beginLogin 复用官方桌面授权入口，凭证通过已认证后台提交，服务器不监听用户电脑的回环端口。
func (h *Handler) beginLogin(ctx context.Context) (*builtinlogin.Flow, error) {
	machine, err := randomLoginID()
	if err != nil {
		return nil, err
	}
	device, err := randomLoginID()
	if err != nil {
		return nil, err
	}
	q := url.Values{
		"login_version": {"1"}, "auth_from": {"solo"}, "login_channel": {"native_ide"},
		"plugin_version": {"2.3.62834"}, "auth_type": {"local"}, "client_id": {upstream.ClientID},
		"redirect": {"0"}, "login_trace_id": {device[:16]},
		"auth_callback_url": {"http://127.0.0.1:18080/authorize"},
		"machine_id":        {machine}, "device_id": {device}, "x_device_id": {device}, "x_machine_id": {machine},
		"x_device_brand": {"PC"}, "x_device_type": {"PC"}, "x_os_version": {"1.0"},
		"x_app_version": {upstream.IdeVersion}, "x_app_type": {"stable"},
	}
	var candidate *auth.Auth
	flow := &builtinlogin.Flow{URL: upstream.ConsoleHost + "/authorization?" + q.Encode(), Mode: "callback"}
	flow.Complete = func(ctx context.Context, callback string) (*builtinlogin.Account, error) {
		if candidate == nil {
			refresh, err := callbackRefreshToken(callback)
			if err != nil {
				return nil, err
			}
			a := &auth.Auth{RefreshToken: refresh, Domain: "trae.cn", MachineID: machine, DeviceID: device}
			if err := h.cfg.Upstream.RefreshTokenContext(ctx, a); err != nil {
				return nil, err
			}
			// 换 token 后保留候选凭证，磁盘或用户信息请求失败时允许安全重试。
			candidate = a
		}
		uid, nickname, enterprise, err := h.cfg.Upstream.GetUserInfoContext(ctx, candidate)
		if err != nil {
			return nil, err
		}
		if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`).MatchString(uid) {
			return nil, errors.New("invalid account uid")
		}
		candidate.UID, candidate.Nickname, candidate.EnterpriseID = uid, nickname, enterprise
		candidate.FilePath = filepath.Join(h.cfg.AuthDir, "trae-"+uid+".json")
		if err := os.MkdirAll(h.cfg.AuthDir, 0700); err != nil {
			return nil, err
		}
		if err := candidate.SaveAtomic(); err != nil {
			return nil, err
		}
		h.cfg.Pool.Add(candidate)
		h.cfg.Pool.ReviveLogin(uid)
		return &builtinlogin.Account{UID: uid, Nickname: nickname}, nil
	}
	return flow, nil
}

func callbackRefreshToken(raw string) (string, error) {
	invalid := &builtinlogin.PublicError{Status: 400, Message: "Paste the full TRAE callback URL from http://127.0.0.1:18080/authorize"}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" || u.Host != "127.0.0.1:18080" || u.Path != "/authorize" || u.User != nil {
		return "", invalid
	}
	if token := u.Query().Get("refreshToken"); token != "" {
		return token, nil
	}
	jwt := u.Query().Get("userJwt")
	for i := 0; i < 2; i++ {
		var value struct {
			RefreshToken string `json:"RefreshToken"`
		}
		if json.Unmarshal([]byte(jwt), &value) == nil && value.RefreshToken != "" {
			return value.RefreshToken, nil
		}
		jwt, err = url.QueryUnescape(jwt)
		if err != nil {
			break
		}
	}
	return "", invalid
}
