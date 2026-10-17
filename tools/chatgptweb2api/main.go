package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func run() error {
	key := os.Getenv("CGW_API_KEY")
	if key == "" {
		return errors.New("CGW_API_KEY is required")
	}
	models := splitModels(os.Getenv("CGW_MODELS"))
	if len(models) == 0 {
		return errors.New("CGW_MODELS is required")
	}
	addr := os.Getenv("CGW_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:7881"
	}
	answerWait := time.Duration(intFromEnv(os.Getenv("CGW_ANSWER_TIMEOUT"), 180)) * time.Second
	browser := newChatGPTBrowser(
		os.Getenv("CGW_UPSTREAM_PROXY"),
		os.Getenv("CGW_USER_AGENT"),
		os.Getenv("CGW_BROWSER_EXECUTABLE"),
		os.Getenv("CGW_HEADLESS") != "0",
		answerWait,
	)
	if err := startBrowserWithBackoff(browser); err != nil {
		return err
	}
	defer browser.stop()
	adapter, err := newAdapter(key, models, browser, answerWait)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: addr, Handler: adapter.handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go browser.reapIdle(ctx)
	go func() {
		<-ctx.Done()
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownContext)
	}()
	log.Printf("ChatGPT web adapter listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func splitModels(raw string) []string {
	models := make([]string, 0, 4)
	for _, model := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(model); trimmed != "" {
			models = append(models, trimmed)
		}
	}
	return models
}

// startBrowserWithBackoff 防止“启动失败 -> 容器退出 -> 立即重启”把宿主拖入
// chromium fork 风暴；持续失败时交给 Docker 的退避策略处理。
func startBrowserWithBackoff(browser *chatgptBrowser) error {
	delays := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}
	var err error
	for attempt := 0; ; attempt++ {
		if err = browser.start(); err == nil {
			return nil
		}
		if attempt >= len(delays) {
			return err
		}
		log.Printf("ChatGPT web browser start failed (attempt %d/%d): %v; retrying in %s",
			attempt+1, len(delays)+1, err, delays[attempt])
		time.Sleep(delays[attempt])
	}
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
