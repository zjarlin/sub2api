// server.go 码道适配器 HTTP 服务：对 Sub2API 暴露 OpenAI 兼容接口，
// 并把内置登录会话（网页登录）注册在 /internal/login。
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sub2api/builtinlogin"
)

type adapter struct {
	key           string
	stateFile     string
	origin        string
	client        *http.Client
	streamClient  *http.Client
	streamTimeout time.Duration
	loginBrowser  loginBrowser
	mu            sync.RWMutex
	credential    credential
	slots         chan struct{}
	next          atomic.Uint64
}

func newAdapter(key, stateFile, origin string) (*adapter, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("CodeArts adapter key is required")
	}
	short, stream := newHTTPClients()
	a := &adapter{
		key: key, stateFile: stateFile, origin: strings.TrimRight(strings.TrimSpace(origin), "/"),
		client: short, streamClient: stream, streamTimeout: 30 * time.Minute,
		slots: make(chan struct{}, 2),
	}
	data, err := os.ReadFile(stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(data, &a.credential) != nil {
		return nil, errors.New("Invalid CodeArts credential file")
	}
	a.credential.Cookies = normalizeCookies(a.credential.Cookies)
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
	message := "CodeArts request failed"
	if !errors.As(err, &p) {
		p = &upstreamError{status: 502, code: "upstream_error", message: "CodeArts request failed"}
	} else {
		message = err.Error()
	}
	return map[string]any{"error": map[string]string{"type": "upstream_error", "code": p.code, "message": message}}
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
	builtinlogin.New(a.beginLogin).Register(mux, a.auth)
	return mux
}

func (a *adapter) models(w http.ResponseWriter, r *http.Request) {
	c, err := a.snapshot()
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(kernelModels))
	for _, m := range a.listModels(r.Context(), c) {
		items = append(items, map[string]any{"id": m.ID, "object": "model", "created": 0, "owned_by": "madao", "name": m.Label})
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": items})
}

func (a *adapter) chat(w http.ResponseWriter, r *http.Request) {
	c, err := a.snapshot()
	if err != nil {
		writeError(w, err)
		return
	}
	req, prompt, model, err := parseChat(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	created := time.Now().Unix()
	if req.Stream {
		a.streamChat(w, r, c, prompt, model, created)
		return
	}
	text, err := a.generate(r.Context(), c, prompt, model, nil)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, completionBody(newCompletionID(), model, text, created))
}

// streamChat 以 SSE 转发文本增量。上游失败若尚未输出任何内容，则回退为 JSON 错误。
func (a *adapter) streamChat(w http.ResponseWriter, r *http.Request, c credential, prompt, model string, created int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, problem(500, "streaming_unsupported", "Streaming is not supported"))
		return
	}
	id := newCompletionID()
	wroteHeader := false
	emit := func(chunk string) error {
		if !wroteHeader {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(http.StatusOK)
			wroteHeader = true
		}
		payload := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": chunk}, "finish_reason": nil}},
		}
		data, _ := json.Marshal(payload)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	_, err := a.generate(r.Context(), c, prompt, model, emit)
	if err != nil {
		if !wroteHeader {
			writeError(w, err)
			return
		}
		// 已开始输出：以 error 事件收尾，随后发送 [DONE]。
		data, _ := json.Marshal(map[string]any{"error": errorValue(err)["error"]})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	if !wroteHeader {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
	}
	final := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	}
	data, _ := json.Marshal(final)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
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

// beginLogin 启动一次网页登录（poll 模式，管理页面轮询 + 截图）。
func (a *adapter) beginLogin(ctx context.Context) (*builtinlogin.Flow, error) {
	if a.loginBrowser == nil {
		return nil, errors.New("CodeArts login browser is not configured")
	}
	session, err := a.loginBrowser.Start(ctx)
	if err != nil {
		return nil, err
	}
	return &builtinlogin.Flow{
		URL:  webLoginURL,
		Mode: "poll",
		View: func(ctx context.Context) (*builtinlogin.View, error) {
			screenshot, err := session.Screenshot(ctx)
			if err != nil {
				return nil, err
			}
			return &builtinlogin.View{ContentType: "image/png", Body: screenshot}, nil
		},
		Complete: func(ctx context.Context, _ string) (*builtinlogin.Account, error) {
			imported, ready, err := session.Credential(ctx)
			if err != nil {
				return nil, err
			}
			if !ready {
				return nil, builtinlogin.ErrPending
			}
			return a.importCredential(ctx, imported)
		},
		Input: func(ctx context.Context, event builtinlogin.Input) error {
			return session.Input(ctx, event)
		},
		Close: session.Close,
	}, nil
}
