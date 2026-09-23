package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sub2api/builtinlogin"
	"workbuddy2api/internal/auth"
	"workbuddy2api/internal/pool"
)

func TestWorkbuddyEmptyPoolAllowsInitialLogin(t *testing.T) {
	p := pool.New("")
	defer p.Close()
	h := NewHandler(Config{Pool: p, APIKey: "internal-key", AuthDir: t.TempDir()})
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/livez", 200}, {"GET", "/healthz", 503}, {"POST", "/internal/login/sessions", 401},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s: got %d want %d", tc.path, w.Code, tc.status)
		}
	}
}

func TestWorkbuddyBrowserLoginPollAndRetry(t *testing.T) {
	polls, saves := 0, 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "https://www.codebuddy.cn" {
			t.Error("missing origin")
		}
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			_, _ = w.Write([]byte(`{"code":0,"data":{"state":"a+b","authUrl":"https://www.codebuddy.cn/login"}}`))
		case "/v2/plugin/auth/token":
			polls++
			if r.URL.Query().Get("state") != "a+b" {
				t.Error("state not escaped")
			}
			if polls == 1 {
				_, _ = w.Write([]byte(`{"code":11217,"msg":"11217:login ing..."}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"accessToken":"access-secret","refreshToken":"refresh-secret","expiresIn":3600,"domain":"codebuddy.cn"}}`))
		case "/v2/plugin/login/account":
			if r.Header.Get("Authorization") != "Bearer access-secret" {
				t.Error("missing bearer")
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"uid":"u123","nickname":"Tester","enterpriseId":"e1"}}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer remote.Close()
	flow, err := beginWorkbuddyLogin(context.Background(), remote.Client(), remote.URL, func(a *auth.Auth) error {
		saves++
		if a.UID != "u123" || a.RefreshToken != "refresh-secret" || a.ExpiresAt == 0 {
			t.Error("bad credentials")
		}
		if saves == 1 {
			return errors.New("disk temporarily unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := flow.Complete(context.Background(), ""); !errors.Is(err, builtinlogin.ErrPending) {
		t.Fatalf("expected pending: %v", err)
	}
	if _, err := flow.Complete(context.Background(), ""); err == nil {
		t.Fatal("save failure suppressed")
	}
	a, err := flow.Complete(context.Background(), "")
	if err != nil || a.UID != "u123" {
		t.Fatalf("retry %v %v", a, err)
	}
	if polls != 2 || saves != 2 {
		t.Fatal("retried one-time token exchange")
	}
}

func TestWorkbuddyLoginDoesNotHideErrorsAsPending(t *testing.T) {
	for _, body := range []string{`{"code":401,"msg":"token-secret expired"}`, `invalid-json`} {
		remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		var target any
		err := loginJSON(context.Background(), remote.Client(), "GET", remote.URL+"/auth/token?state=x", "", &target)
		remote.Close()
		if err == nil || errors.Is(err, builtinlogin.ErrPending) {
			t.Fatalf("unexpected pending: %v", err)
		}
	}
}
