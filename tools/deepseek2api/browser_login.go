package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

const deepseekLoginURL = "https://chat.deepseek.com/sign_in"

type browserLoginOptions struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	AutoRelogin bool   `json:"auto_relogin"`
}

type browserLoginSession interface {
	Screenshot(context.Context) ([]byte, error)
	Credential(context.Context) (webCredential, bool, error)
	Close()
}

type browserLoginSessionRestriction interface {
	AccountRestriction(context.Context) (string, bool, error)
}

type loginBrowser interface {
	Start(context.Context, browserLoginOptions) (browserLoginSession, error)
}

type chromiumLoginBrowser struct {
	executable  string
	profileRoot string
	mu          sync.Mutex
	active      bool
}

func newChromiumLoginBrowser(executable, profileRoot string) *chromiumLoginBrowser {
	return &chromiumLoginBrowser{executable: executable, profileRoot: profileRoot}
}

func (b *chromiumLoginBrowser) Start(ctx context.Context, login browserLoginOptions) (browserLoginSession, error) {
	b.mu.Lock()
	if b.active {
		b.mu.Unlock()
		return nil, errors.New("DeepSeek browser login is already running")
	}
	b.active = true
	b.mu.Unlock()

	if err := os.MkdirAll(b.profileRoot, 0700); err != nil {
		b.release()
		return nil, err
	}
	profile, err := os.MkdirTemp(b.profileRoot, "session-")
	if err != nil {
		b.release()
		return nil, err
	}
	if err := os.Chmod(profile, 0700); err != nil {
		_ = os.RemoveAll(profile)
		b.release()
		return nil, err
	}

	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(b.executable),
		chromedp.UserDataDir(profile),
		chromedp.WindowSize(1280, 900),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-software-rasterizer", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("lang", "en-US"),
	)
	allocatorContext, allocatorCancel := chromedp.NewExecAllocator(context.Background(), options...)
	browserContext, browserCancel := chromedp.NewContext(allocatorContext)
	session := &chromiumLoginSession{
		context:         browserContext,
		cancelBrowser:   browserCancel,
		cancelAllocator: allocatorCancel,
		profile:         profile,
		release:         b.release,
	}
	// 管理页面异常退出后也会主动关闭浏览器，避免占用自动登录的唯一槽位。
	session.expireAfter(10 * time.Minute)

	actions := []chromedp.Action{
		chromedp.Navigate(deepseekLoginURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(2 * time.Second),
	}
	if login.Email != "" {
		var passwordModeOpened bool
		var submitted bool
		actions = append(actions,
			chromedp.Evaluate(`(() => {
				const target = [...document.querySelectorAll('button,[role="button"],a,div')]
					.find((element) => (element.textContent || '').trim() === 'Login with password');
				if (!target) return false;
				target.click();
				return true;
			})()`, &passwordModeOpened),
			chromedp.ActionFunc(func(context.Context) error {
				if !passwordModeOpened {
					return errors.New("DeepSeek password login is unavailable")
				}
				return nil
			}),
			chromedp.WaitVisible(`input[placeholder="Phone number / email address"]`, chromedp.ByQuery),
			chromedp.SendKeys(`input[placeholder="Phone number / email address"]`, login.Email, chromedp.ByQuery),
			chromedp.SendKeys(`input[placeholder="Password"]`, login.Password, chromedp.ByQuery),
			chromedp.Evaluate(`(() => {
				const target = [...document.querySelectorAll('button,[role="button"]')]
					.find((element) => (element.textContent || '').trim() === 'Log in');
				if (!target) return false;
				target.click();
				return true;
			})()`, &submitted),
			chromedp.ActionFunc(func(context.Context) error {
				if !submitted {
					return errors.New("DeepSeek login submission is unavailable")
				}
				return nil
			}),
		)
	}
	actions = append(actions, chromedp.Sleep(3*time.Second))

	startupDone := make(chan error, 1)
	go func() {
		startupDone <- chromedp.Run(browserContext, actions...)
	}()
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	select {
	case err := <-startupDone:
		if err != nil {
			session.Close()
			return nil, err
		}
		return session, nil
	case <-timer.C:
		session.Close()
		return nil, errors.New("DeepSeek login browser startup timed out")
	case <-ctx.Done():
		session.Close()
		return nil, ctx.Err()
	}
}

func (b *chromiumLoginBrowser) release() {
	b.mu.Lock()
	b.active = false
	b.mu.Unlock()
}

type chromiumLoginSession struct {
	context         context.Context
	cancelBrowser   context.CancelFunc
	cancelAllocator context.CancelFunc
	profile         string
	release         func()
	mu              sync.Mutex
	closeOnce       sync.Once
}

func (s *chromiumLoginSession) expireAfter(lifetime time.Duration) {
	go func() {
		timer := time.NewTimer(lifetime)
		defer timer.Stop()
		select {
		case <-s.context.Done():
		case <-timer.C:
		}
		s.Close()
	}()
}

func (s *chromiumLoginSession) Screenshot(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var screenshot []byte
	captureContext, cancel := context.WithTimeout(s.context, 15*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if err := chromedp.Run(captureContext, chromedp.CaptureScreenshot(&screenshot)); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return screenshot, nil
	}
}

func (s *chromiumLoginSession) Credential(ctx context.Context) (webCredential, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var credential webCredential
	evaluateContext, cancel := context.WithTimeout(s.context, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	const expression = `(() => {
		const raw = localStorage.getItem('userToken');
		let stored = null;
		try { stored = JSON.parse(raw || 'null'); } catch { stored = raw; }
		const token = typeof stored === 'string' ? stored : stored?.value || stored?.token || '';
		const device_id = localStorage.getItem('deepseek-device-id:chat') || '';
		return { token, device_id };
	})()`
	if err := chromedp.Run(evaluateContext, chromedp.Evaluate(expression, &credential)); err != nil {
		return webCredential{}, false, err
	}
	select {
	case <-ctx.Done():
		return webCredential{}, false, ctx.Err()
	default:
	}
	credential.Token = strings.TrimSpace(strings.TrimPrefix(credential.Token, "Bearer "))
	credential.DeviceID = strings.TrimSpace(credential.DeviceID)
	return credential, credential.Token != "" && credential.DeviceID != "", nil
}

var suspendedUntilPattern = regexp.MustCompile(`(?i)account has been suspended until\s+([A-Za-z]{3,9}\s+\d{1,2},\s+\d{4}\s+\d{1,2}:\d{2})`)

func deepseekAccountRestriction(text string) (string, bool) {
	if len(text) > 256<<10 {
		text = text[:256<<10]
	}
	if match := suspendedUntilPattern.FindStringSubmatch(text); len(match) == 2 {
		return "DeepSeek account is suspended until " + strings.TrimSpace(match[1]) + "; wait for the restriction to end, then try again", true
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "account has been suspended") || strings.Contains(lower, "account is suspended") {
		return "DeepSeek account is suspended; wait for the restriction to end, then try again", true
	}
	return "", false
}

func (s *chromiumLoginSession) AccountRestriction(ctx context.Context) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var text string
	evaluateContext, cancel := context.WithTimeout(s.context, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if err := chromedp.Run(evaluateContext, chromedp.Evaluate(`document.body ? document.body.innerText : ''`, &text)); err != nil {
		return "", false, err
	}
	select {
	case <-ctx.Done():
		return "", false, ctx.Err()
	default:
	}
	message, restricted := deepseekAccountRestriction(text)
	return message, restricted, nil
}

func sessionAccountRestriction(ctx context.Context, session browserLoginSession) (string, bool, error) {
	reporter, ok := session.(browserLoginSessionRestriction)
	if !ok {
		return "", false, nil
	}
	return reporter.AccountRestriction(ctx)
}

func (s *chromiumLoginSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelBrowser()
		s.cancelAllocator()
		_ = os.RemoveAll(filepath.Clean(s.profile))
		s.release()
	})
}
