package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func run() error {
	key := os.Getenv("DEEPSEEK_WEB_ADAPTER_KEY")
	if key == "" {
		return errors.New("DEEPSEEK_WEB_ADAPTER_KEY is required")
	}
	stateFile := os.Getenv("DEEPSEEK_WEB_STATE_FILE")
	if stateFile == "" {
		stateFile = "/app/data/accounts.json"
	}
	addr := os.Getenv("DEEPSEEK_WEB_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:7867"
	}
	apiBase := os.Getenv("DEEPSEEK_WEB_API_BASE")
	if apiBase == "" {
		apiBase = defaultAPIBase
	}
	wasmURL := os.Getenv("DEEPSEEK_WEB_WASM_URL")
	if wasmURL == "" {
		wasmURL = defaultWasmURL
	}
	client := &http.Client{
		Transport:     http.DefaultTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, wasmURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("DeepSeek PoW module is unavailable")
	}
	wasm, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	solver, err := newPowSolver(ctx, wasm)
	if err != nil {
		return err
	}
	defer solver.runtime.Close(context.Background())
	upstream := &upstreamClient{http: client, baseURL: apiBase}
	adapter, err := newAdapter(key, stateFile, upstream, solver)
	if err != nil {
		return err
	}
	browserExecutable := os.Getenv("DEEPSEEK_WEB_BROWSER_EXECUTABLE")
	if browserExecutable == "" {
		browserExecutable = "/usr/bin/chromium-browser"
	}
	adapter.loginBrowser = newChromiumLoginBrowser(browserExecutable, filepath.Join(filepath.Dir(stateFile), "browser-login"))
	server := &http.Server{Addr: addr, Handler: adapter.handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("DeepSeek web adapter listening on %s", addr)
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
