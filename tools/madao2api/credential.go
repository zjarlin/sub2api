// credential.go 保存官方 Ask 授权及刷新上下文，旧网页凭据只保留用于识别重新授权。
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sub2api/builtinlogin"
)

// credential 对应一个码道账号，IAM 授权用于纯文本 Ask 对话。
type credential struct {
	UID      string       `json:"uid"`
	Nickname string       `json:"nickname,omitempty"`
	Cftk     string       `json:"cftk,omitempty"`
	Cookies  []cookiePair `json:"cookies,omitempty"`
	IAM      *iamGrant    `json:"iam,omitempty"`
}

type cookiePair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (c credential) cookieHeader() string {
	pairs := make([]string, 0, len(c.Cookies))
	for _, item := range c.Cookies {
		if item.Name == "" || item.Value == "" {
			continue
		}
		pairs = append(pairs, item.Name+"="+item.Value)
	}
	return strings.Join(pairs, "; ")
}

// usable 仅接受官方 Ask 临时凭据，不把旧 Cookie 登录当作 Ask 授权。
func (c credential) usable() bool {
	return c.IAM != nil && c.IAM.Credentials.usable()
}

func normalizeCookies(cookies []cookiePair) []cookiePair {
	seen := make(map[string]int, len(cookies))
	out := make([]cookiePair, 0, len(cookies))
	for _, item := range cookies {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		if index, ok := seen[name]; ok {
			out[index] = cookiePair{Name: name, Value: item.Value}
			continue
		}
		seen[name] = len(out)
		out = append(out, cookiePair{Name: name, Value: item.Value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (a *adapter) save(c credential) error {
	c.Cookies = normalizeCookies(c.Cookies)
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.stateFile), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(a.stateFile), ".madao-*")
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
	if !c.usable() {
		return c, problem(401, "madao_login_required", "Authorize CodeArts Ask again; web cookies cannot access Ask")
	}
	return c, nil
}

// importCredential 使用签名的当前用户接口验证 Ask 授权，验证通过才落盘。
func (a *adapter) importCredential(ctx context.Context, imported credential) (*builtinlogin.Account, error) {
	imported.Cftk = strings.TrimSpace(imported.Cftk)
	imported.Cookies = normalizeCookies(imported.Cookies)
	if !imported.usable() {
		return nil, &builtinlogin.PublicError{Status: 400, Message: "A CodeArts Ask authorization is required"}
	}
	me, err := a.verifyAskSession(ctx, imported)
	if err != nil {
		return nil, &builtinlogin.PublicError{Status: 400, Message: "CodeArts could not verify this Ask authorization"}
	}
	imported.UID = me.UserID
	grant := *imported.IAM
	grant.UserName = me.UserName
	imported.IAM = &grant
	if imported.Nickname == "" {
		imported.Nickname = me.NickName
	}
	if imported.Nickname == "" {
		imported.Nickname = me.UserName
	}
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	// 使用最新授权替换当前凭据，保留经上游验证的账号身份。
	if err := a.save(imported); err != nil {
		return nil, err
	}
	a.credential = imported
	return &builtinlogin.Account{UID: imported.UID, Nickname: imported.Nickname}, nil
}
