// Package builtinlogin 管理仅供可信后台使用的短期登录会话。
package builtinlogin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"
)

var ErrPending = errors.New("authorization pending")

// PublicError 只携带可以向管理员展示的错误，禁止包含 token 或原始上游响应。
type PublicError struct {
	Status  int
	Message string
}

func (e *PublicError) Error() string { return e.Message }

type Account struct {
	UID      string `json:"uid"`
	Nickname string `json:"nickname,omitempty"`
}

type Flow struct {
	URL      string
	Mode     string
	Complete func(context.Context, string) (*Account, error)
}

type Begin func(context.Context) (*Flow, error)

type result struct {
	ID        string   `json:"session_id"`
	URL       string   `json:"auth_url,omitempty"`
	Mode      string   `json:"mode"`
	Status    string   `json:"status"`
	ExpiresAt int64    `json:"expires_at"`
	Account   *Account `json:"account,omitempty"`
}

type session struct {
	mu        sync.Mutex
	owner     string
	expires   time.Time
	flow      *Flow
	result    result
	lastPoll  time.Time
	cancelled bool
}

type Handler struct {
	mu       sync.Mutex
	begin    Begin
	sessions map[string]*session
	now      func() time.Time
}

func New(begin Begin) *Handler {
	return &Handler{begin: begin, sessions: make(map[string]*session), now: time.Now}
}

func (h *Handler) Register(mux *http.ServeMux, authorize func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /internal/login/sessions", authorize(h.start))
	mux.HandleFunc("POST /internal/login/sessions/{id}/poll", authorize(h.complete))
	mux.HandleFunc("POST /internal/login/sessions/{id}/callback", authorize(h.complete))
	mux.HandleFunc("DELETE /internal/login/sessions/{id}", authorize(h.cancel))
}

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, message string) {
	write(w, status, map[string]string{"message": message})
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	owner := r.Header.Get("X-Login-Owner")
	if owner == "" {
		fail(w, 403, "Login owner is required")
		return
	}
	h.mu.Lock()
	for id, s := range h.sessions {
		if !h.now().Before(s.expires) {
			delete(h.sessions, id)
		}
	}
	if len(h.sessions) >= 256 {
		h.mu.Unlock()
		fail(w, 429, "Too many login sessions")
		return
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		h.mu.Unlock()
		fail(w, 500, "Unable to create login session")
		return
	}
	id := hex.EncodeToString(random[:])
	s := &session{owner: owner, expires: h.now().Add(10 * time.Minute)}
	// 先预留名额，避免并发创建越过容量限制。
	s.mu.Lock()
	h.sessions[id] = s
	h.mu.Unlock()
	defer s.mu.Unlock()
	flow, err := h.begin(r.Context())
	if err != nil || flow == nil {
		h.mu.Lock()
		delete(h.sessions, id)
		h.mu.Unlock()
		fail(w, 502, "Unable to start authorization; retry later")
		return
	}
	u, err := url.Parse(flow.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || flow.Complete == nil || (flow.Mode != "poll" && flow.Mode != "callback") {
		h.mu.Lock()
		delete(h.sessions, id)
		h.mu.Unlock()
		fail(w, 502, "Invalid authorization response")
		return
	}
	s.flow = flow
	s.result = result{ID: id, URL: flow.URL, Mode: flow.Mode, Status: "pending", ExpiresAt: s.expires.UnixMilli()}
	write(w, 201, s.result)
}

func (h *Handler) find(r *http.Request) *session {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[r.PathValue("id")]
	if s == nil || s.owner != r.Header.Get("X-Login-Owner") || !h.now().Before(s.expires) {
		return nil
	}
	return s
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request) {
	s := h.find(r)
	if s == nil {
		fail(w, 404, "Login session expired or not found")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelled || !h.now().Before(s.expires) {
		fail(w, 404, "Login session expired or cancelled")
		return
	}
	if s.result.Status == "completed" {
		write(w, 200, s.result)
		return
	}
	if s.flow == nil {
		fail(w, 409, "Login is not ready")
		return
	}
	var callback string
	if s.flow.Mode == "callback" {
		var body struct {
			CallbackURL string `json:"callback_url"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CallbackURL == "" {
			fail(w, 400, "Callback URL is required")
			return
		}
		callback = body.CallbackURL
	} else if h.now().Sub(s.lastPoll) < 2*time.Second {
		write(w, 200, s.result)
		return
	}
	s.lastPoll = h.now()
	account, err := s.flow.Complete(r.Context(), callback)
	if errors.Is(err, ErrPending) {
		write(w, 200, s.result)
		return
	}
	if err != nil {
		var public *PublicError
		if errors.As(err, &public) {
			fail(w, public.Status, public.Message)
			return
		}
		fail(w, 502, "Authorization failed; retry or start a new login")
		return
	}
	if account == nil || account.UID == "" {
		fail(w, 502, "Missing authorized account")
		return
	}
	s.result.Status = "completed"
	s.result.Account = account
	s.result.URL = ""
	s.flow = nil
	write(w, 200, s.result)
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	s := h.find(r)
	if s == nil {
		fail(w, 404, "Login session expired or not found")
		return
	}
	s.mu.Lock()
	s.cancelled = true
	s.flow = nil
	s.mu.Unlock()
	h.mu.Lock()
	delete(h.sessions, r.PathValue("id"))
	h.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
