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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"sub2api/builtinlogin"
)

type browserLoginSession interface {
	Screenshot(context.Context) ([]byte, error)
	Credential(context.Context) (credential, bool, error)
	Input(context.Context, builtinlogin.Input) error
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
	b := &chromiumLoginBrowser{executable: executable, profileRoot: profileRoot}
	// 进程重启后旧会话的临时 profile 目录与 Chromium 都不会自动清理；
	// 残留目录会让下次登录误判"已在运行"。启动时先清空登录 profile 根目录。
	if err := os.MkdirAll(profileRoot, 0700); err == nil {
		if entries, err := os.ReadDir(profileRoot); err == nil {
			for _, entry := range entries {
				if entry.IsDir() && strings.HasPrefix(entry.Name(), "session-") {
					_ = os.RemoveAll(filepath.Join(profileRoot, entry.Name()))
				}
			}
		}
	}
	return b
}

// forceResetLocked 回收卡住的登录会话：结束残留 Chromium 进程并清空 profile 目录。
// 调用方需持有 b.mu；用于 active=true 但实际已无可用会话的场景。
func (b *chromiumLoginBrowser) forceResetLocked() {
	entries, err := os.ReadDir(b.profileRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "session-") {
			continue
		}
		profile := filepath.Join(b.profileRoot, entry.Name())
		// 结束仍指向该 profile 的 Chromium 进程，再删除目录。
		terminateProfileProcesses(profile)
		_ = os.RemoveAll(profile)
	}
}

// reapOrphansLocked 删除登录 profile 根目录下的历史 session-* 目录（调用方需持有 b.mu）。
func (b *chromiumLoginBrowser) reapOrphansLocked() {
	entries, err := os.ReadDir(b.profileRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "session-") {
			_ = os.RemoveAll(filepath.Join(b.profileRoot, entry.Name()))
		}
	}
}

func (b *chromiumLoginBrowser) Start(ctx context.Context) (browserLoginSession, error) {
	b.mu.Lock()
	if b.active {
		// 上一次登录会话可能因管理页关闭、会话过期或进程异常而没走 Close()，
		// 留下 active=true 的假状态。这里强制回收：杀掉残留 Chromium 并清空
		// profile，然后继续新建，避免用户侧一直看到 502。
		b.forceResetLocked()
	}
	b.active = true
	b.mu.Unlock()

	if err := os.MkdirAll(b.profileRoot, 0700); err != nil {
		b.release()
		return nil, err
	}
	// 没有活跃会话时清理历史残留 profile，避免磁盘堆积与误判。
	b.reapOrphansLocked()
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

// Input 把管理页面的一次交互（鼠标/键盘/滚轮）转发到隔离浏览器，
// 使「截图式登录」具备可操作性：用户能在浏览器画面里点选并输入。
func (s *chromiumLoginSession) Input(ctx context.Context, event builtinlogin.Input) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	runContext, cancel := context.WithTimeout(s.context, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	var action chromedp.Action
	switch event.Type {
	case "click":
		action = chromedp.ActionFunc(func(ctx context.Context) error {
			if err := input.DispatchMouseEvent(input.MouseMoved, event.X, event.Y).Do(ctx); err != nil {
				return err
			}
			if err := input.DispatchMouseEvent(input.MousePressed, event.X, event.Y).
				WithButton(input.Left).WithClickCount(1).WithButtons(1).Do(ctx); err != nil {
				return err
			}
			return input.DispatchMouseEvent(input.MouseReleased, event.X, event.Y).
				WithButton(input.Left).WithClickCount(1).WithButtons(0).Do(ctx)
		})
	case "move":
		action = chromedp.ActionFunc(func(ctx context.Context) error {
			return input.DispatchMouseEvent(input.MouseMoved, event.X, event.Y).Do(ctx)
		})
	case "wheel":
		action = chromedp.ActionFunc(func(ctx context.Context) error {
			return input.DispatchMouseEvent(input.MouseWheel, event.X, event.Y).
				WithDeltaY(event.DeltaY).WithDeltaX(event.DeltaX).Do(ctx)
		})
	case "text":
		action = chromedp.ActionFunc(func(ctx context.Context) error {
			return input.InsertText(event.Text).Do(ctx)
		})
	case "key":
		action = chromedp.ActionFunc(func(ctx context.Context) error {
			return dispatchKey(ctx, event.Key)
		})
	default:
		return fmt.Errorf("unsupported input event")
	}
	if err := chromedp.Run(runContext, action); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// dispatchKey 发送一次按键（按下 + 抬起）。覆盖登录常见按键。
func dispatchKey(ctx context.Context, key string) error {
	type keySpec struct {
		name string
		code string
		vk   int64
	}
	specs := map[string]keySpec{
		"Enter":     {"Enter", "Enter", 13},
		"Tab":       {"Tab", "Tab", 9},
		"Backspace": {"Backspace", "Backspace", 8},
		"Escape":    {"Escape", "Escape", 27},
		"ArrowLeft": {"ArrowLeft", "ArrowLeft", 37},
		"ArrowUp":   {"ArrowUp", "ArrowUp", 38},
		"ArrowRight": {"ArrowRight", "ArrowRight", 39},
		"ArrowDown": {"ArrowDown", "ArrowDown", 40},
		"Delete":    {"Delete", "Delete", 46},
		"Space":     {" ", "Space", 32},
	}
	spec, ok := specs[key]
	if !ok {
		return fmt.Errorf("unsupported key")
	}
	if err := input.DispatchKeyEvent(input.KeyDown).
		WithKey(spec.name).WithCode(spec.code).WithWindowsVirtualKeyCode(spec.vk).Do(ctx); err != nil {
		return err
	}
	return input.DispatchKeyEvent(input.KeyUp).
		WithKey(spec.name).WithCode(spec.code).WithWindowsVirtualKeyCode(spec.vk).Do(ctx)
}

func (s *chromiumLoginSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelBrowser()
		s.cancelAllocator()
		_ = os.RemoveAll(filepath.Clean(s.profile))
		s.release()
	})
}
