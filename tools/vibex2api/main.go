package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	key := os.Getenv("V2A_API_KEY")
	if key == "" {
		log.Fatal("V2A_API_KEY is required")
	}
	stateFile := os.Getenv("V2A_STATE_FILE")
	if stateFile == "" {
		stateFile = "/app/data/credential.json"
	}
	adapter, err := newAdapter(key, stateFile, "https://vibex.runninghub.cn")
	if err != nil {
		log.Fatal("Unable to load VibeX state; check file permissions and format")
	}
	addr := os.Getenv("V2A_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:7866"
	}
	server := &http.Server{Addr: addr, Handler: adapter.handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("VibeX adapter listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
