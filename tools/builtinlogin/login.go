// Package builtinlogin 管理仅供可信后台使用的短期登录会话。
package builtinlogin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
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
	UID         string `json:"uid"`
	Nickname    string `json:"nickname,omitempty"`
	AutoRelogin *bool  `json:"auto_relogin,omitempty"`
}

type View struct {
	ContentType string
	Body        []byte
}

// Input 是发往隔离浏览器的用户交互事件（鼠标 / 键盘 / 滚轮）。
// 坐标使用 CSS 像素，相对浏览器视口左上角；仅供可信后台在登录会话上转发。
type Input struct {
	Type   string  `json:"type"`              // click | move | wheel | text | key
	X      float64 `json:"x,omitempty"`       // click/move/wheel：视口 X
	Y      float64 `json:"y,omitempty"`       // click/move/wheel：视口 Y
	DeltaX float64 `json:"delta_x,omitempty"` // wheel：横向滚动像素
	DeltaY float64 `json:"delta_y,omitempty"` // wheel：纵向滚动像素
	Text   string  `json:"text,omitempty"`    // text：插入的文本
	Key    string  `json:"key,omitempty"`     // key：按键名（Enter/Tab/Backspace/箭头等）
}

type Flow struct {
	URL      string
	Mode     string
	Complete func(context.Context, string) (*Account, error)
	View     func(context.Context) (*View, error)
	// Input 可选：向隔离浏览器转发一次用户交互（截图式登录必需）。
	Input func(context.Context, Input) error
	Close func()
}

type Begin func(context.Context) (*Flow, error)

type BeginWithOptions func(context.Context, json.RawMessage) (*Flow, error)

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
	begin    BeginWithOptions
	sessions map[string]*session
	now      func() time.Time
}

func New(begin Begin) *Handler {
	return NewWithOptions(func(ctx context.Context, _ json.RawMessage) (*Flow, error) {
		return begin(ctx)
	})
}

func NewWithOptions(begin BeginWithOptions) *Handler {
	return &Handler{begin: begin, sessions: make(map[string]*session), now: time.Now}
}

func (h *Handler) Register(mux *http.ServeMux, authorize func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /internal/login/sessions", authorize(h.start))
	mux.HandleFunc("POST /internal/login/sessions/{id}/poll", authorize(h.complete))
	mux.HandleFunc("POST /internal/login/sessions/{id}/callback", authorize(h.complete))
	mux.HandleFunc("GET /internal/login/sessions/{id}/view", authorize(h.view))
	mux.HandleFunc("POST /internal/login/sessions/{id}/input", authorize(h.input))
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
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, 400, "Invalid login options")
		return
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		body = []byte("{}")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		fail(w, 400, "Invalid login options")
		return
	}
	options := json.RawMessage(append([]byte(nil), body...))
	defer clear(options)
	h.mu.Lock()
	var expired []*session
	for id, s := range h.sessions {
		if !h.now().Before(s.expires) {
			delete(h.sessions, id)
			expired = append(expired, s)
		}
	}
	if len(h.sessions) >= 256 {
		h.mu.Unlock()
		for _, s := range expired {
			s.close()
		}
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
	for _, s := range expired {
		s.close()
	}
	defer s.mu.Unlock()
	flow, err := h.begin(r.Context(), options)
	if err != nil || flow == nil {
		h.mu.Lock()
		delete(h.sessions, id)
		h.mu.Unlock()
		fail(w, 502, "Unable to start authorization; retry later")
		return
	}
	u, err := url.Parse(flow.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || flow.Complete == nil || (flow.Mode != "poll" && flow.Mode != "callback") {
		if flow.Close != nil {
			flow.Close()
		}
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

func (s *session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.flow != nil && s.flow.Close != nil {
		s.flow.Close()
	}
	s.flow = nil
}

func (h *Handler) find(r *http.Request) *session {
	h.mu.Lock()
	s := h.sessions[r.PathValue("id")]
	if s == nil || s.owner != r.Header.Get("X-Login-Owner") {
		h.mu.Unlock()
		return nil
	}
	if !h.now().Before(s.expires) {
		delete(h.sessions, r.PathValue("id"))
		h.mu.Unlock()
		s.close()
		return nil
	}
	h.mu.Unlock()
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
	if s.flow.Close != nil {
		s.flow.Close()
	}
	s.flow = nil
	write(w, 200, s.result)
}

func (h *Handler) view(w http.ResponseWriter, r *http.Request) {
	s := h.find(r)
	if s == nil {
		fail(w, 404, "Login session expired or not found")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelled || !h.now().Before(s.expires) || s.flow == nil || s.flow.View == nil {
		fail(w, 404, "Login view expired or unavailable")
		return
	}
	view, err := s.flow.View(r.Context())
	if err != nil || view == nil || len(view.Body) == 0 || view.ContentType == "" {
		fail(w, 502, "Unable to load login view")
		return
	}
	w.Header().Set("Content-Type", view.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(view.Body)
}

// input 向隔离浏览器转发一次交互事件；会话必须属于调用者且在有效期内。
func (h *Handler) input(w http.ResponseWriter, r *http.Request) {
	s := h.find(r)
	if s == nil {
		fail(w, 404, "Login session expired or not found")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelled || !h.now().Before(s.expires) || s.flow == nil || s.flow.Input == nil {
		fail(w, 404, "Login view expired or unavailable")
		return
	}
	var payload Input
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		fail(w, 400, "Invalid input event")
		return
	}
	if !validInput(payload) {
		fail(w, 400, "Invalid input event")
		return
	}
	if err := s.flow.Input(r.Context(), payload); err != nil {
		fail(w, 502, "Unable to relay input to the browser")
		return
	}
	write(w, 200, map[string]bool{"ok": true})
}

// validInput 限制事件类型、坐标范围与文本长度，避免失控或超长输入。
func validInput(in Input) bool {
	const maxCoord = 20000
	switch in.Type {
	case "click", "move", "wheel":
		if in.X < 0 || in.Y < 0 || in.X > maxCoord || in.Y > maxCoord {
			return false
		}
		if in.DeltaX < -maxCoord || in.DeltaX > maxCoord || in.DeltaY < -maxCoord || in.DeltaY > maxCoord {
			return false
		}
		return true
	case "text":
		return in.Text != "" && len(in.Text) <= 4096
	case "key":
		return in.Key != "" && len(in.Key) <= 32
	default:
		return false
	}
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	s := h.find(r)
	if s == nil {
		fail(w, 404, "Login session expired or not found")
		return
	}
	s.mu.Lock()
	s.cancelled = true
	if s.flow != nil && s.flow.Close != nil {
		s.flow.Close()
	}
	s.flow = nil
	s.mu.Unlock()
	h.mu.Lock()
	delete(h.sessions, r.PathValue("id"))
	h.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
