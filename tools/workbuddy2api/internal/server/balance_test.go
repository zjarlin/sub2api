package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workbuddy2api/internal/auth"
	"workbuddy2api/internal/upstream"
)

func TestStatusRefreshCreditsUsesOfficialBalance(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"Response":{"Data":{"Accounts":[{"CycleCapacitySize":100,"CycleCapacityRemain":79,"CycleCapacityRemainPrecise":"79.75"}]}}}}`))
	}))
	defer up.Close()
	p := testPoolWith(&auth.Auth{UID: "u1", Nickname: "🤔", AccessToken: "secret"})
	p.SetCredits("u1", 46)
	c := upstream.New()
	c.BillingBaseCN = up.URL
	h := NewHandler(Config{Pool: p, Upstream: c, APIKey: "key"})
	r := httptest.NewRequest(http.MethodGet, "/status?refresh_credits=true", nil)
	r.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"credits":79.75`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("余额响应泄露凭证")
	}
	status, _ := p.Status("u1")
	if status.Credits != 46 {
		t.Fatal("展示查询不应改写调度池")
	}
}

func TestStatusRefreshCreditsDoesNotFallBackToEstimate(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer up.Close()
	p := testPoolWith(&auth.Auth{UID: "u1", AccessToken: "secret"})
	p.SetCredits("u1", 46)
	c := upstream.New()
	c.BillingBaseCN = up.URL
	h := NewHandler(Config{Pool: p, Upstream: c})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/status?refresh_credits=true", nil))
	if w.Code != 502 || strings.Contains(w.Body.String(), `"credits":46`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
