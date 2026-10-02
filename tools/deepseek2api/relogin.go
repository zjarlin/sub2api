package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const refreshFailureCooldown = time.Minute

type credentialRefresh struct {
	done       chan struct{}
	cancel     context.CancelFunc
	waiters    int
	credential webCredential
	err        error
}

type refreshFailure struct {
	token string
	until time.Time
	err   error
}

func loginRequired(message string) error {
	return apiError{status: 401, code: "deepseek_login_required", message: message}
}

func reloginFailed(message string) error {
	return apiError{status: 503, code: "deepseek_relogin_failed", message: message}
}

func (a *adapter) loginCipher() (cipher.AEAD, error) {
	key := sha256.Sum256([]byte("deepseek-web-auto-relogin-v1\x00" + a.key))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (a *adapter) encryptLogin(uid string, options browserLoginOptions) (string, error) {
	seal, err := a.loginCipher()
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}{Email: options.Email, Password: options.Password})
	if err != nil {
		return "", err
	}
	nonce := make([]byte, seal.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	// 账号 UID 绑定密文，防止不同账号间误用已保存的密码。
	encrypted := seal.Seal(nonce, nonce, data, []byte(uid))
	return "v1:" + base64.RawStdEncoding.EncodeToString(encrypted), nil
}

func (a *adapter) decryptLogin(credential webCredential) (browserLoginOptions, error) {
	var options browserLoginOptions
	seal, err := a.loginCipher()
	if err != nil {
		return options, err
	}
	if !strings.HasPrefix(credential.ReloginCredentials, "v1:") {
		return options, errors.New("unsupported saved DeepSeek login format")
	}
	encrypted, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(credential.ReloginCredentials, "v1:"))
	if err != nil || len(encrypted) < seal.NonceSize()+seal.Overhead() {
		return options, errors.New("invalid saved DeepSeek login")
	}
	data, err := seal.Open(nil, encrypted[:seal.NonceSize()], encrypted[seal.NonceSize():], []byte(credential.UID))
	if err != nil {
		return options, errors.New("could not decrypt saved DeepSeek login")
	}
	if json.Unmarshal(data, &options) != nil || options.Email == "" || options.Password == "" {
		return browserLoginOptions{}, errors.New("invalid saved DeepSeek login")
	}
	options.AutoRelogin = true
	return options, nil
}

func (a *adapter) currentCredential(uid string) (webCredential, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, credential := range a.accounts {
		if credential.UID == uid {
			return credential, true
		}
	}
	return webCredential{}, false
}

func (a *adapter) renewCredential(ctx context.Context, stale webCredential) (webCredential, error) {
	for {
		if err := ctx.Err(); err != nil {
			return webCredential{}, err
		}
		a.refreshMu.Lock()
		current, ok := a.currentCredential(stale.UID)
		if !ok {
			a.refreshMu.Unlock()
			return webCredential{}, loginRequired("DeepSeek account is unavailable; sign in again in account settings")
		}
		// 其他请求或手动登录已经更新过凭据时，直接使用已验证的新会话。
		if current.Token != stale.Token || current.DeviceID != stale.DeviceID {
			a.refreshMu.Unlock()
			return current, nil
		}
		if current.ReloginCredentials == "" {
			a.refreshMu.Unlock()
			return webCredential{}, loginRequired("DeepSeek session expired; sign in again and enable automatic sign-in in account settings")
		}
		if failure, ok := a.refreshFailures[current.UID]; ok && failure.token == current.Token && time.Now().Before(failure.until) {
			a.refreshMu.Unlock()
			return webCredential{}, failure.err
		}
		refresh := a.refreshes[current.UID]
		if refresh != nil && refresh.waiters == 0 {
			// 已取消的任务先释放浏览器，新请求随后重新登录，不继承取消结果。
			a.refreshMu.Unlock()
			select {
			case <-ctx.Done():
				return webCredential{}, ctx.Err()
			case <-refresh.done:
				continue
			}
		}
		if refresh == nil {
			// 自动登录属于正在等待的请求；最后一个请求取消后立即关闭浏览器。
			refreshCtx, cancel := context.WithTimeout(context.Background(), a.refreshTimeout)
			refresh = &credentialRefresh{done: make(chan struct{}), cancel: cancel}
			a.refreshes[current.UID] = refresh
			go a.runCredentialRefresh(refreshCtx, current, refresh)
		}
		refresh.waiters++
		a.refreshMu.Unlock()
		defer func() {
			a.refreshMu.Lock()
			refresh.waiters--
			if refresh.waiters == 0 {
				refresh.cancel()
			}
			a.refreshMu.Unlock()
		}()
		select {
		case <-ctx.Done():
			return webCredential{}, ctx.Err()
		case <-refresh.done:
			return refresh.credential, refresh.err
		}
	}
}

func (a *adapter) runCredentialRefresh(ctx context.Context, stale webCredential, refresh *credentialRefresh) {
	defer refresh.cancel()
	credential, err := a.loginAndReplaceCredential(ctx, stale)
	if errors.Is(err, context.DeadlineExceeded) {
		err = reloginFailed("DeepSeek automatic sign-in timed out; sign in again in account settings. Retrying is paused for one minute")
	}
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	refresh.credential, refresh.err = credential, err
	delete(a.refreshes, stale.UID)
	if err == nil {
		delete(a.refreshFailures, stale.UID)
	} else if !errors.Is(err, context.Canceled) {
		a.refreshFailures[stale.UID] = refreshFailure{token: stale.Token, until: time.Now().Add(refreshFailureCooldown), err: err}
	}
	close(refresh.done)
}

func (a *adapter) loginAndReplaceCredential(ctx context.Context, stale webCredential) (webCredential, error) {
	failed := reloginFailed("DeepSeek automatic sign-in failed; sign in again in account settings (verification may be required). Retrying is paused for one minute")
	options, err := a.decryptLogin(stale)
	if err != nil || a.loginBrowser == nil {
		return webCredential{}, failed
	}
	session, err := a.loginBrowser.Start(ctx, options)
	if err != nil {
		if ctx.Err() == context.Canceled {
			return webCredential{}, ctx.Err()
		}
		return webCredential{}, failed
	}
	defer session.Close()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		message, restricted, err := sessionAccountRestriction(ctx, session)
		if err != nil {
			return webCredential{}, failed
		}
		if restricted {
			return webCredential{}, reloginFailed(message)
		}
		credential, ready, err := session.Credential(ctx)
		if ctx.Err() == context.Canceled {
			return webCredential{}, ctx.Err()
		}
		if err != nil {
			return webCredential{}, failed
		}
		if ready {
			verified, err := a.upstream.verify(ctx, credential)
			if ctx.Err() == context.Canceled {
				return webCredential{}, ctx.Err()
			}
			if err != nil || verified.UID != stale.UID || verified.Token == "" || verified.DeviceID == "" {
				return webCredential{}, failed
			}
			return a.replaceRefreshedCredential(ctx, stale, verified)
		}
		select {
		case <-ctx.Done():
			if ctx.Err() == context.Canceled {
				return webCredential{}, ctx.Err()
			}
			return webCredential{}, failed
		case <-ticker.C:
		}
	}
}

func (a *adapter) replaceRefreshedCredential(ctx context.Context, stale, verified webCredential) (webCredential, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return webCredential{}, err
	}
	accounts := append([]webCredential(nil), a.accounts...)
	for i, current := range accounts {
		if current.UID != stale.UID {
			continue
		}
		if current.Token != stale.Token || current.DeviceID != stale.DeviceID {
			return current, nil
		}
		verified.ReloginCredentials = current.ReloginCredentials
		accounts[i] = verified
		if err := a.save(accounts); err != nil {
			return webCredential{}, reloginFailed("DeepSeek refreshed session could not be saved; sign in again in account settings")
		}
		a.accounts = accounts
		return verified, nil
	}
	return webCredential{}, loginRequired("DeepSeek account is unavailable; sign in again in account settings")
}

func (a *adapter) rejectRefreshedCredential(credential webCredential) error {
	err := reloginFailed("DeepSeek rejected the refreshed session; sign in again in account settings. Retrying is paused for one minute")
	a.refreshMu.Lock()
	a.refreshFailures[credential.UID] = refreshFailure{token: credential.Token, until: time.Now().Add(refreshFailureCooldown), err: err}
	a.refreshMu.Unlock()
	return err
}
