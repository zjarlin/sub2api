package builtinlogin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func loginRequest(h http.Handler, method, path, owner, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-Login-Owner", owner)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestLoginIsolationCompletionCancellationAndExpiry(t *testing.T) {
	var calls atomic.Int32
	h := New(func(context.Context) (*Flow, error) {
		return &Flow{URL: "https://example.com/authorize", Mode: "callback", Complete: func(context.Context, string) (*Account, error) { calls.Add(1); return &Account{UID: "u1"}, nil }}, nil
	})
	now := time.Now()
	h.now = func() time.Time { return now }
	mux := http.NewServeMux()
	h.Register(mux, func(next http.HandlerFunc) http.HandlerFunc { return next })
	if w := loginRequest(mux, "POST", "/internal/login/sessions", "", "{}"); w.Code != 403 {
		t.Fatalf("missing owner: %d", w.Code)
	}
	w := loginRequest(mux, "POST", "/internal/login/sessions", "admin:1", "{}")
	var started result
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	path := "/internal/login/sessions/" + started.ID
	if w := loginRequest(mux, "POST", path+"/callback", "admin:2", `{"callback_url":"secret"}`); w.Code != 404 {
		t.Fatalf("owner isolation: %d", w.Code)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := loginRequest(mux, "POST", path+"/callback", "admin:1", `{"callback_url":"secret"}`)
			if w.Code != 200 {
				t.Errorf("complete %d", w.Code)
			}
			if strings.Contains(w.Body.String(), "secret") {
				t.Error("callback leaked")
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("duplicate exchange %d", calls.Load())
	}
	if w := loginRequest(mux, "DELETE", path, "admin:1", ""); w.Code != 204 {
		t.Fatalf("cancel %d", w.Code)
	}
	if w := loginRequest(mux, "POST", path+"/callback", "admin:1", `{"callback_url":"secret"}`); w.Code != 404 {
		t.Fatalf("cancelled %d", w.Code)
	}
	w = loginRequest(mux, "POST", "/internal/login/sessions", "admin:1", "{}")
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	now = now.Add(11 * time.Minute)
	if w := loginRequest(mux, "POST", "/internal/login/sessions/"+started.ID+"/callback", "admin:1", `{"callback_url":"secret"}`); w.Code != 404 {
		t.Fatalf("expired %d", w.Code)
	}
}

func TestLoginPendingAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		err      error
		status   int
		contains string
	}{{ErrPending, 200, "pending"}, {errors.New("refresh-token-secret"), 502, "Authorization failed"}, {&PublicError{400, "Invalid callback"}, 400, "Invalid callback"}} {
		h := New(func(context.Context) (*Flow, error) {
			return &Flow{URL: "https://example.com", Mode: "poll", Complete: func(context.Context, string) (*Account, error) { return nil, tc.err }}, nil
		})
		mux := http.NewServeMux()
		h.Register(mux, func(next http.HandlerFunc) http.HandlerFunc { return next })
		w := loginRequest(mux, "POST", "/internal/login/sessions", "a", "{}")
		var s result
		if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		w = loginRequest(mux, "POST", "/internal/login/sessions/"+s.ID+"/poll", "a", "{}")
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("unexpected %d %s", w.Code, w.Body.String())
		}
	}
}
