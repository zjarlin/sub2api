package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// chatgpt.com 首页；适配器只用匿名（未登录）网页，不做任何账号登录。
const chatgptHomeURL = "https://chatgpt.com/"

const chatgptUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36"

// composerSelectors 覆盖匿名移动壳与桌面壳的输入框。
var composerSelectors = []string{
	"#prompt-textarea",
	"#mobile-composer-prompt",
	`div[contenteditable="true"]`,
	"textarea",
}

// readAnswerJS 读取助手回答：优先结构化 transcript，其次整页文本中最后一个 "ChatGPT said:"。
const readAnswerJS = `(() => {
  const clean = (s) => (s || '')
    .replace(/\n?\s*ChatGPT is AI[\s\S]*$/i, '')
    .replace(/\n?\s*Chat is temporarily[\s\S]*$/i, '')
    .trim();
  const t = document.getElementById('octane-conversation-transcript');
  if (t) {
    try {
      const j = JSON.parse(t.textContent);
      const rows = (j.rows || []).filter((r) => r.role === 'assistant');
      if (rows.length && !j.streaming) {
        const html = rows.map((r) => r.markdownHtml || '').join('\n');
        const d = document.createElement('div');
        d.innerHTML = html;
        const s = (d.innerText || d.textContent || '').trim();
        if (s) return JSON.stringify({ text: s });
      }
    } catch (e) {}
  }
  const b = document.body ? (document.body.innerText || '') : '';
  const i = b.lastIndexOf('ChatGPT said:');
  if (i >= 0) {
    const s = clean(b.slice(i + 'ChatGPT said:'.length));
    if (s) return JSON.stringify({ text: s, url: location.href, tail: b.slice(-80) });
  }
  return JSON.stringify({ text: '', url: location.href, tail: b.replace(/\s+/g, ' ').slice(-80) });
})()`

// submitJS 优先点击发送按钮，其次提交表单；两者都不可用时返回 none。
const submitJS = `(() => {
  const button = [...document.querySelectorAll('button')].find((b) =>
    /send message|^send$/i.test((b.getAttribute('aria-label') || '').trim()) ||
    b.getAttribute('data-testid') === 'send-button');
  if (button && !button.disabled) { button.click(); return 'button'; }
  const form = document.querySelector('form');
  if (form && form.requestSubmit) { form.requestSubmit(); return 'form'; }
  return 'none';
})()`

// startedJS 判断是否已进入会话（离开首页）或出现流式/回答迹象。
const startedJS = `String(location.pathname.startsWith('/c/') || location.pathname.startsWith('/uc/') ||
  [...document.querySelectorAll('button')].some((b) => /stop generating|stop streaming/i.test(b.getAttribute('aria-label') || '')) ||
  (document.body ? /ChatGPT said:/.test(document.body.innerText || '') : false))`

// streamingJS 返回字符串 "true"/"false"，便于 chromedp 反序列化。
const streamingJS = `String([...document.querySelectorAll('button')].some((b) =>
  /stop generating|stop streaming/i.test(b.getAttribute('aria-label') || '')))`

// chatgptBrowser 复用单个 Chromium 实例，串行处理匿名问答。
type chatgptBrowser struct {
	proxy       string
	headless    bool
	userAgent   string
	execPath    string
	answerWait  time.Duration
	pollEvery   time.Duration
	navTimeout  time.Duration
	debug       bool
	mu          sync.Mutex
	allocCancel context.CancelFunc
	ctxCancel   context.CancelFunc
	browserCtx  context.Context
	started     bool
	startedAt   time.Time
	asks        int
	maxAsks     int
	maxAge      time.Duration
	jsHeapMB    int
	idleTimeout time.Duration
	lastUsed    time.Time
	startErr    error
	startFailed time.Time
}

func (b *chatgptBrowser) logf(format string, args ...any) {
	if b.debug {
		log.Printf("chatgpt-web: "+format, args...)
	}
}

func newChatGPTBrowser(proxy, userAgent, execPath string, headless bool, answerWait time.Duration) *chatgptBrowser {
	if strings.TrimSpace(userAgent) == "" {
		userAgent = chatgptUserAgent
	}
	maxAsks := intFromEnv(os.Getenv("CGW_BROWSER_MAX_ASKS"), 100)
	if maxAsks < 1 {
		maxAsks = 100
	}
	maxAge := time.Duration(intFromEnv(os.Getenv("CGW_BROWSER_MAX_AGE_MIN"), 120)) * time.Minute
	if maxAge <= 0 {
		maxAge = 120 * time.Minute
	}
	jsHeapMB := intFromEnv(os.Getenv("CGW_BROWSER_JS_HEAP_MB"), 384)
	if jsHeapMB < 128 {
		jsHeapMB = 384
	}
	idleMin := intFromEnv(os.Getenv("CGW_BROWSER_IDLE_MIN"), 10)
	if idleMin < 0 {
		idleMin = 10
	}
	return &chatgptBrowser{
		proxy: proxy, headless: headless, userAgent: userAgent, execPath: execPath,
		answerWait: answerWait, pollEvery: time.Second, navTimeout: 60 * time.Second,
		maxAsks: maxAsks, maxAge: maxAge, jsHeapMB: jsHeapMB,
		idleTimeout: time.Duration(idleMin) * time.Minute,
		debug:       os.Getenv("CGW_DEBUG") == "1",
	}
}

func (b *chatgptBrowser) start() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return nil
	}
	return b.startLocked()
}

// startLocked 启动浏览器并重置回收计数；调用方必须持有 b.mu。
func (b *chatgptBrowser) startLocked() error {
	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(1440, 900),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-software-rasterizer", true),
		// 限制渲染进程的 JS 堆与进程数，避免长跑页把宿主 swap 吃满。
		chromedp.Flag("js-flags", fmt.Sprintf("--max-old-space-size=%d", b.jsHeapMB)),
		chromedp.Flag("renderer-process-limit", "2"),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("lang", "en-US"),
		chromedp.UserAgent(b.userAgent),
	)
	if b.headless {
		options = append(options, chromedp.Flag("headless", "new"))
	}
	if b.execPath != "" {
		options = append(options, chromedp.ExecPath(b.execPath))
	}
	if b.proxy != "" {
		options = append(options, chromedp.ProxyServer(b.proxy))
	}
	allocatorContext, allocCancel := chromedp.NewExecAllocator(context.Background(), options...)
	browserContext, ctxCancel := chromedp.NewContext(allocatorContext)
	b.allocCancel = allocCancel
	b.ctxCancel = ctxCancel
	b.browserCtx = browserContext
	startDone := make(chan error, 1)
	go func() { startDone <- chromedp.Run(browserContext) }()
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	select {
	case err := <-startDone:
		if err != nil {
			b.release()
			return err
		}
	case <-timer.C:
		b.release()
		return errors.New("ChatGPT web browser startup timed out")
	}
	b.started = true
	b.startedAt = time.Now()
	b.lastUsed = time.Now()
	b.asks = 0
	return nil
}

// release 在持锁内关闭浏览器；调用方不得在未持锁时使用。
func (b *chatgptBrowser) release() {
	if b.ctxCancel != nil {
		b.ctxCancel()
		b.ctxCancel = nil
	}
	if b.allocCancel != nil {
		b.allocCancel()
		b.allocCancel = nil
	}
	b.started = false
}

func (b *chatgptBrowser) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.release()
}

// detectComposer 返回首个存在的输入框选择器，用于匿名移动壳与桌面壳兼容。
func detectComposer(ctx context.Context) (string, error) {
	for _, selector := range composerSelectors {
		var present bool
		probe := "!!document.querySelector(" + jsString(selector) + ")"
		if err := chromedp.Run(ctx, chromedp.Evaluate(probe, &present)); err != nil {
			return "", err
		}
		if present {
			return selector, nil
		}
	}
	return "", errors.New("chatgpt.com composer did not appear")
}

// dismissOverlaysJS 关闭可能阻塞输入的登录提示/弹窗。
const dismissOverlaysJS = `(() => {
  for (const b of [...document.querySelectorAll('button')]) {
    const label = (b.getAttribute('aria-label') || '').trim();
    const text = (b.textContent || '').trim();
    if (/^(dismiss|close|no thanks|not now|maybe later)$/i.test(label) || /^(dismiss|close|no thanks|not now|maybe later)$/i.test(text)) {
      b.click();
    }
  }
  return 'ok';
})()`

// preparePrompt 打开首页、关闭弹窗并写入提示词；返回输入框选择器。
func (b *chatgptBrowser) preparePrompt(runContext, requestContext context.Context, prompt string) (string, error) {
	if err := chromedp.Run(runContext,
		chromedp.Navigate(chatgptHomeURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Evaluate(dismissOverlaysJS, new(string)),
	); err != nil {
		return "", err
	}
	composer, err := detectComposer(runContext)
	if err != nil {
		return "", err
	}
	if err := chromedp.Run(runContext,
		chromedp.Evaluate(dismissOverlaysJS, new(string)),
		chromedp.Focus(composer, chromedp.ByQuery),
		chromedp.ActionFunc(func(runCtx context.Context) error {
			return input.InsertText(prompt).Do(runCtx)
		}),
	); err != nil {
		return "", err
	}
	return composer, nil
}

// pressSend 尝试点击可用的发送按钮；按钮长期禁用时回落为回车。
func (b *chatgptBrowser) pressSend(runContext, requestContext context.Context) (string, error) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		var mode string
		if err := chromedp.Run(runContext, chromedp.Evaluate(submitJS, &mode)); err != nil {
			return "", err
		}
		if mode == "button" {
			return "button", nil
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-requestContext.Done():
			return "", requestContext.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
	if err := chromedp.Run(runContext, chromedp.KeyEvent("\r")); err != nil {
		return "", err
	}
	return "enter", nil
}

// ensureStartedLocked 按需启动浏览器；启动失败后在冷却期内不再重试，
// 避免每个请求都去拉起一个注定失败的 chromium。
func (b *chatgptBrowser) ensureStartedLocked() error {
	if b.startErr != nil && time.Since(b.startFailed) < 30*time.Second {
		return b.startErr
	}
	if err := b.startLocked(); err != nil {
		b.startErr = err
		b.startFailed = time.Now()
		return err
	}
	b.startErr = nil
	return nil
}

// reapIdle 在空闲超时后关闭浏览器；下一次请求会按需重新拉起。
func (b *chatgptBrowser) reapIdle(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		b.mu.Lock()
		if b.started && b.idleTimeout > 0 && time.Since(b.lastUsed) >= b.idleTimeout {
			b.logf("closing idle browser idle=%s", time.Since(b.lastUsed).Round(time.Minute))
			b.release()
		}
		b.mu.Unlock()
	}
}

// recycleDueLocked 判断是否需要重启浏览器以释放长期累积的内存。
func (b *chatgptBrowser) recycleDueLocked() bool {
	if !b.started {
		return false
	}
	if b.maxAsks > 0 && b.asks >= b.maxAsks {
		return true
	}
	return b.maxAge > 0 && time.Since(b.startedAt) >= b.maxAge
}

// ask 打开首页并提交一条提示，返回助手文本。
func (b *chatgptBrowser) ask(ctx context.Context, prompt string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	defer func() { b.lastUsed = time.Now() }()
	if !b.started {
		if err := b.ensureStartedLocked(); err != nil {
			return "", err
		}
	}
	if b.recycleDueLocked() {
		b.logf("recycling browser asks=%d age=%s", b.asks, time.Since(b.startedAt).Round(time.Minute))
		b.release()
		if err := b.ensureStartedLocked(); err != nil {
			return "", err
		}
	}
	b.asks++
	// 每条请求使用独立标签页，请求结束即关闭，释放该轮 DOM/JS 堆。
	tabContext, closeTab := chromedp.NewContext(b.browserCtx)
	defer closeTab()
	// chromedp 要求上下文派生自浏览器上下文；请求取消时同步终止本轮。
	runContext, cancel := context.WithTimeout(tabContext, b.navTimeout+b.answerWait+45*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	// 匿名网页偶发完全无响应（既不流式也不出现回答），因此每轮给一段短窗口，
	// 未开始生成就重开首页再提交，直到总超时。
	overall := time.Now().Add(b.answerWait)
	for attempt := 0; ; attempt++ {
		composer, err := b.preparePrompt(runContext, ctx, prompt)
		if err != nil {
			return "", err
		}
		mode, err := b.pressSend(runContext, ctx)
		if err != nil {
			return "", err
		}
		attemptDeadline := time.Now().Add(b.attemptWindow())
		if attemptDeadline.After(overall) {
			attemptDeadline = overall
		}
		b.logf("attempt=%d composer=%s submitted=%s window=%s", attempt, composer, mode, time.Until(attemptDeadline).Round(time.Second))
		text, answered, err := b.waitAnswer(runContext, ctx, attemptDeadline)
		if err != nil {
			return "", err
		}
		if answered {
			return text, nil
		}
		if time.Now().After(overall.Add(-b.attemptWindow()/2)) || attempt >= 5 {
			return "", errors.New("timed out waiting for a chatgpt.com answer")
		}
	}
}

// attemptWindow 返回单轮等待窗口：总预算的四分之一，且不少于 30 秒。
func (b *chatgptBrowser) attemptWindow() time.Duration {
	window := b.answerWait / 4
	if window < 30*time.Second {
		window = 30 * time.Second
	}
	return window
}

// waitAnswer 在窗口内轮询，直到拿到稳定助手文本；answered=false 表示本轮没有生成。
func (b *chatgptBrowser) waitAnswer(runContext, requestContext context.Context, attemptDeadline time.Time) (string, bool, error) {
	last, stable := "", 0
	for time.Now().Before(attemptDeadline) {
		select {
		case <-requestContext.Done():
			return "", false, requestContext.Err()
		case <-time.After(b.pollEvery):
		}
		readContext, readCancel := context.WithTimeout(runContext, 15*time.Second)
		var raw, streaming string
		err := chromedp.Run(readContext,
			chromedp.Evaluate(readAnswerJS, &raw),
			chromedp.Evaluate(streamingJS, &streaming),
		)
		readCancel()
		if err != nil {
			continue // 客户端软导航期间上下文短暂失效，忽略后重试。
		}
		probe := decodeAnswerProbe(raw)
		b.logf("poll len=%d streaming=%s url=%s tail=%s", len(probe.Text), streaming, probe.URL, probe.Tail)
		text := probe.Text
		if text == "" || streaming == "true" {
			stable = 0
			continue
		}
		if text == last {
			stable++
			if stable >= 2 {
				return text, true, nil
			}
		} else {
			stable = 0
			last = text
		}
	}
	if last != "" {
		return last, true, nil
	}
	return "", false, nil
}
