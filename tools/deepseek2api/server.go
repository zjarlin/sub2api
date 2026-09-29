package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"sub2api/builtinlogin"
)

type proofOfWork interface {
	solveChallenge(context.Context, challenge) (string, error)
}

type adapter struct {
	key          string
	stateFile    string
	upstream     *upstreamClient
	pow          proofOfWork
	mu           sync.RWMutex
	accounts     []webCredential
	next         atomic.Uint64
	slots        chan struct{}
	loginBrowser loginBrowser
}

func newAdapter(key, stateFile string, upstream *upstreamClient, pow proofOfWork) (*adapter, error) {
	if strings.TrimSpace(key) == "" || upstream == nil || pow == nil {
		return nil, errors.New("DeepSeek adapter key, upstream and PoW solver are required")
	}
	a := &adapter{key: key, stateFile: stateFile, upstream: upstream, pow: pow, slots: make(chan struct{}, 4)}
	data, err := os.ReadFile(stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &a.accounts); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *adapter) save(accounts []webCredential) error {
	data, err := json.Marshal(accounts)
	if err != nil {
		return err
	}
	dir := filepath.Dir(a.stateFile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".deepseek-accounts-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), a.stateFile)
}

func (a *adapter) account() (webCredential, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.accounts) == 0 {
		return webCredential{}, apiError{status: 401, code: "deepseek_login_required", message: "Add a DeepSeek web session first"}
	}
	index := (a.next.Add(1) - 1) % uint64(len(a.accounts))
	return a.accounts[index], nil
}

func (a *adapter) beginLogin(ctx context.Context, rawOptions json.RawMessage) (*builtinlogin.Flow, error) {
	if a.loginBrowser == nil {
		return nil, errors.New("DeepSeek login browser is not configured")
	}
	var options browserLoginOptions
	if err := json.Unmarshal(rawOptions, &options); err != nil {
		return nil, errors.New("Invalid DeepSeek login options")
	}
	options.Email = strings.TrimSpace(options.Email)
	if (options.Email == "") != (options.Password == "") || len(options.Email) > 320 || len(options.Password) > 4096 {
		return nil, errors.New("DeepSeek email and password must be provided together")
	}
	session, err := a.loginBrowser.Start(ctx, options)
	if err != nil {
		return nil, err
	}
	return &builtinlogin.Flow{
		URL:  deepseekLoginURL,
		Mode: "poll",
		View: func(ctx context.Context) (*builtinlogin.View, error) {
			screenshot, err := session.Screenshot(ctx)
			if err != nil {
				return nil, err
			}
			return &builtinlogin.View{ContentType: "image/png", Body: screenshot}, nil
		},
		Complete: func(ctx context.Context, _ string) (*builtinlogin.Account, error) {
			credential, ready, err := session.Credential(ctx)
			if err != nil {
				return nil, err
			}
			if !ready {
				return nil, builtinlogin.ErrPending
			}
			return a.importCredential(ctx, credential)
		},
		Close: session.Close,
	}, nil
}

func (a *adapter) importCredential(ctx context.Context, imported webCredential) (*builtinlogin.Account, error) {
	imported.Token = strings.TrimSpace(strings.TrimPrefix(imported.Token, "Bearer "))
	imported.DeviceID = strings.TrimSpace(imported.DeviceID)
	if imported.Token == "" || imported.DeviceID == "" || len(imported.Token) > 16<<10 || len(imported.DeviceID) > 4096 {
		return nil, &builtinlogin.PublicError{Status: 400, Message: "A browser token and device ID are required"}
	}
	verified, err := a.upstream.verify(ctx, imported)
	if err != nil {
		return nil, &builtinlogin.PublicError{Status: 400, Message: "DeepSeek could not verify this browser session"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	accounts := append([]webCredential(nil), a.accounts...)
	replaced := false
	for i := range accounts {
		if accounts[i].UID == verified.UID {
			accounts[i] = verified
			replaced = true
			break
		}
	}
	if !replaced {
		if len(accounts) >= 32 {
			return nil, &builtinlogin.PublicError{Status: 409, Message: "DeepSeek account pool is full"}
		}
		accounts = append(accounts, verified)
	}
	if err := a.save(accounts); err != nil {
		return nil, err
	}
	a.accounts = accounts
	return &builtinlogin.Account{UID: verified.UID, Nickname: verified.Email}, nil
}

type apiError struct {
	status  int
	code    string
	message string
}

func (e apiError) Error() string { return e.message }

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	var public apiError
	if !errors.As(err, &public) {
		public = apiError{status: 502, code: "upstream_error", message: "DeepSeek web request failed"}
	}
	writeJSON(w, public.status, map[string]any{"error": map[string]string{
		"type": "upstream_error", "code": public.code, "message": public.message,
	}})
}

func (a *adapter) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(a.key)) != 1 {
			writeError(w, apiError{status: 401, code: "invalid_api_key", message: "Invalid adapter API key"})
			return
		}
		next(w, r)
	}
}

func (a *adapter) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /healthz", a.auth(func(w http.ResponseWriter, r *http.Request) {
		a.mu.RLock()
		count := len(a.accounts)
		a.mu.RUnlock()
		writeJSON(w, 200, map[string]int{"accounts": count})
	}))
	mux.HandleFunc("GET /v1/models", a.auth(a.models))
	mux.HandleFunc("POST /v1/chat/completions", a.auth(a.chat))
	builtinlogin.NewWithOptions(a.beginLogin).Register(mux, a.auth)
	return mux
}

func (a *adapter) models(w http.ResponseWriter, r *http.Request) {
	if _, err := a.account(); err != nil {
		writeError(w, err)
		return
	}
	items := []map[string]any{
		{"id": "deepseek-web-chat", "object": "model", "created": 0, "owned_by": "deepseek-web"},
		{"id": "deepseek-web-reasoner", "object": "model", "created": 0, "owned_by": "deepseek-web"},
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": items})
}

func (a *adapter) acquire(ctx context.Context) error {
	select {
	case a.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *adapter) release() { <-a.slots }

func decodeOne(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected extra JSON")
	}
	return nil
}
