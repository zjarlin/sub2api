package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"traework2api/internal/auth"
)

func TestStatusRefreshCreditsQueriesOnlyBoundAccount(t *testing.T) {
	calls := 0
	up := newFakeUpstream(t, func(token string) (int, string, bool) {
		calls++
		if token != "Cloud-IDE-JWT target-token" {
			t.Fatalf("unexpected upstream account: %s", token)
		}
		return 200, `{"usage_summary":{"total_amount":100,"consumed_amount":95}}`, false
	})
	p := testPoolWith(&auth.Auth{UID: "target", AccessToken: "target-token"}, &auth.Auth{UID: "other", AccessToken: "other-token"})
	h := NewHandler(Config{Pool: p, Upstream: up})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/status?refresh_credits=true&uid=target", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"credits":5`) || !strings.Contains(w.Body.String(), `"credits_refreshed":true`) || strings.Contains(w.Body.String(), "other") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	if status, _ := p.Status("target"); status.Credits != 1000 {
		t.Fatal("查询不应改写调度状态")
	}
}

func TestStatusRefreshCreditsRejectsUnknownBalance(t *testing.T) {
	for _, body := range []string{`{}`, `{"code":123,"message":"failure"}`} {
		t.Run(body, func(t *testing.T) {
			up := newFakeUpstream(t, func(string) (int, string, bool) { return 200, body, false })
			h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "u1", AccessToken: "secret"}), Upstream: up})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/status?refresh_credits=true", nil))
			if w.Code != 502 || strings.Contains(w.Body.String(), `"credits":`) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
