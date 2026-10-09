// main.go 码道（华为云 CodeArts 代码智能体 / 码道）适配器进程入口。
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func run() error {
	key := os.Getenv("MADAO_ADAPTER_KEY")
	if key == "" {
		return errString("MADAO_ADAPTER_KEY is required")
	}
	stateFile := os.Getenv("MADAO_STATE_FILE")
	if stateFile == "" {
		stateFile = "/app/data/credential.json"
	}
	origin := os.Getenv("MADAO_ASK_BASE_URL")
	if origin == "" {
		origin = defaultAskBaseURL
	}
	addr := os.Getenv("MADAO_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:7870"
	}
	adapter, err := newAdapter(key, stateFile, origin)
	if err != nil {
		return err
	}
	browserExecutable := os.Getenv("MADAO_BROWSER_EXECUTABLE")
	if browserExecutable == "" {
		browserExecutable = "/usr/bin/chromium-browser"
	}
	adapter.loginBrowser = newChromiumLoginBrowser(browserExecutable, filepath.Join(filepath.Dir(stateFile), "browser-login"))

	server := &http.Server{Addr: addr, Handler: adapter.handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("CodeArts (码道) adapter listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
