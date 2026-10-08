// credential.go 码道凭据：一整套华为云 Web 会话 Cookie（含 cftk）。
//
// 码道没有 OAuth2 授权码流程，也没有 API Key；授权等价于"浏览器登录后的会话导入"。
// 适配器用独立 Chromium 打开码道登录页，由用户完成华为云登录（可能含验证码），
// 登录成功后从浏览器上下文导出 Cookie 落盘。运行时用这些 Cookie + cftk 头访问站内接口。
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

// credential 是一次码道 Web 会话的等价物。Cookie 已按名排序，便于稳定比较与落盘。
type credential struct {
	UID      string       `json:"uid"`
	Nickname string       `json:"nickname,omitempty"`
	Cftk     string       `json:"cftk"`
	Cookies  []cookiePair `json:"cookies"`
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

// usable 判断凭据是否具备最小可用信息：cftk 与至少一个会话 Cookie。
func (c credential) usable() bool {
	return strings.TrimSpace(c.Cftk) != "" && len(c.Cookies) > 0
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
		return c, problem(401, "madao_login_required", "Sign in to CodeArts (码道) first")
	}
	return c, nil
}

// importCredential 用一次真实站内请求（GET /rest/me）验证会话，验证通过才落盘。
func (a *adapter) importCredential(ctx context.Context, imported credential) (*builtinlogin.Account, error) {
	imported.Cftk = strings.TrimSpace(imported.Cftk)
	imported.Cookies = normalizeCookies(imported.Cookies)
	if !imported.usable() {
		return nil, &builtinlogin.PublicError{Status: 400, Message: "A CodeArts session cookie and cftk token are required"}
	}
	me, err := a.verifySession(ctx, imported)
	if err != nil {
		return nil, &builtinlogin.PublicError{Status: 400, Message: "CodeArts could not verify this browser session"}
	}
	imported.UID = me.UserID
	if imported.Nickname == "" {
		imported.Nickname = me.NickName
	}
	if imported.Nickname == "" {
		imported.Nickname = me.UserName
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// 同一账号重新登录时以最新 Cookie 覆盖旧值；不同账号则替换当前会话。
	if err := a.save(imported); err != nil {
		return nil, err
	}
	a.credential = imported
	return &builtinlogin.Account{UID: imported.UID, Nickname: imported.Nickname}, nil
}
