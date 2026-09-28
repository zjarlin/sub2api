package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"sub2api/builtinlogin"
)

type adapter struct {
	key        string
	stateFile  string
	origin     string
	client     *http.Client
	mu         sync.RWMutex
	credential credential
	slot       chan struct{}
}

func newAdapter(key, stateFile, origin string) (*adapter, error) {
	a := &adapter{key: key, stateFile: stateFile, origin: strings.TrimRight(origin, "/"), slot: make(chan struct{}, 1), client: &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	data, err := os.ReadFile(stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &a.credential); err != nil {
		return nil, err
	}
	return a, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func errorValue(err error) map[string]any {
	var p *upstreamError
	if !errors.As(err, &p) {
		p = &upstreamError{status: 502, code: "upstream_error", message: "VibeX request failed"}
	}
	return map[string]any{"error": map[string]string{"type": "upstream_error", "code": p.code, "message": p.message}}
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	var p *upstreamError
	if errors.As(err, &p) {
		status = p.status
	}
	writeJSON(w, status, errorValue(err))
}

func (a *adapter) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if a.key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(a.key)) != 1 {
			writeError(w, problem(401, "invalid_api_key", "Invalid adapter API key"))
			return
		}
		next(w, r)
	}
}

func (a *adapter) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"ok": true}) })
	mux.HandleFunc("GET /healthz", a.auth(func(w http.ResponseWriter, r *http.Request) {
		if _, err := a.snapshot(); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"authenticated": true})
	}))
	mux.HandleFunc("GET /v1/models", a.auth(a.models))
	mux.HandleFunc("POST /v1/chat/completions", a.auth(a.chat))
	mux.HandleFunc("GET /internal/account/usage", a.auth(a.usage))
	builtinlogin.New(a.beginLogin).Register(mux, a.auth)
	return mux
}

func (a *adapter) models(w http.ResponseWriter, r *http.Request) {
	c, err := a.snapshot()
	if err != nil {
		writeError(w, err)
		return
	}
	providers, err := a.providers(r.Context(), c)
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(providers))
	for _, p := range providers {
		items = append(items, map[string]any{"id": p.ID, "object": "model", "created": 0, "owned_by": "vibex", "name": p.DisplayName})
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": items})
}

func (a *adapter) usage(w http.ResponseWriter, r *http.Request) {
	c, err := a.snapshot()
	if err != nil {
		writeError(w, err)
		return
	}
	var wallet, lite json.RawMessage
	if err := a.call(r.Context(), c, "GET", "/vc/api/me/wallet-balance", nil, &wallet); err != nil {
		writeError(w, err)
		return
	}
	if err := a.call(r.Context(), c, "GET", "/vc/api/me/lite-usage", nil, &lite); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"wallet": wallet, "lite": lite})
}

func (a *adapter) acquire(ctx context.Context) error {
	select {
	case a.slot <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
