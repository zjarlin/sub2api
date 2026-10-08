// browser_login.go 码道 Web 登录：用独立 Chromium 打开华为云登录页，
// 用户在浏览器里完成账号登录（可能含验证码 / 短信二次验证），
// 适配器轮询导出会话 Cookie（含 cftk）并交给 importCredential 校验落盘。
//
// 与 DeepSeek 适配器的差别：码道没有可自动化的邮箱密码表单（华为云 SSO 通常
// 需要验证码 / 二次验证），因此只提供"人工在浏览器里登录 + 截图轮询"的流程。
package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/cdproto/network"
)

type browserLoginSession interface {
	Screenshot(context.Context) ([]byte, error)
	Credential(context.Context) (credential, bool, error)
	Close()
}

type loginBrowser interface {
	Start(context.Context) (browserLoginSession, error)
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

func (b *chromiumLoginBrowser) Start(ctx context.Context) (browserLoginSession, error) {
	b.mu.Lock()
	if b.active {
		b.mu.Unlock()
		return nil, errors.New("CodeArts browser login is already running")
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
		chromedp.Flag("lang", "zh-CN"),
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
	session.expireAfter(15 * time.Minute)

	startupDone := make(chan error, 1)
	go func() {
		startupDone <- chromedp.Run(browserContext,
			chromedp.Navigate(webLoginURL),
			chromedp.WaitReady("body", chromedp.ByQuery),
			chromedp.Sleep(2*time.Second),
		)
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
		return nil, errors.New("CodeArts login browser startup timed out")
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

// Credential 导出码道域下的全部 Cookie。cftk 单独抽出，方便运行期拼头。
// 只有当会话 Cookie 与 cftk 同时存在时才算就绪。
func (s *chromiumLoginSession) Credential(ctx context.Context) (credential, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw []*network.Cookie
	evaluateContext, cancel := context.WithTimeout(s.context, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if err := chromedp.Run(evaluateContext, chromedp.ActionFunc(func(ctx context.Context) error {
		cookies, err := network.GetCookies().WithURLs([]string{loginOrigin + "/"}).Do(ctx)
		if err != nil {
			return err
		}
		raw = cookies
		return nil
	})); err != nil {
		return credential{}, false, err
	}
	select {
	case <-ctx.Done():
		return credential{}, false, ctx.Err()
	default:
	}
	var result credential
	for _, item := range raw {
		if item == nil || strings.TrimSpace(item.Name) == "" || item.Value == "" {
			continue
		}
		result.Cookies = append(result.Cookies, cookiePair{Name: item.Name, Value: item.Value})
		if item.Name == cftkCookieName {
			result.Cftk = item.Value
		}
	}
	result.Cookies = normalizeCookies(result.Cookies)
	return result, result.usable(), nil
}

func (s *chromiumLoginSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelBrowser()
		s.cancelAllocator()
		_ = os.RemoveAll(filepath.Clean(s.profile))
		s.release()
	})
}
