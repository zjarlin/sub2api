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

// 单号余额查询失败不得拖垮整批：仍返回 200 与账号行，但必须带 balance_error，
// 且不能把调度池估算值伪造成官方余额（"credits":46 不得出现）。
func TestStatusRefreshCreditsMarksBalanceErrorWithoutEstimate(t *testing.T) {
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
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `"balance_error"`) || strings.Contains(body, `"credits":46`) || strings.Contains(body, `"credits":`) {
		t.Fatalf("status=%d body=%s", w.Code, body)
	}
	if !strings.Contains(body, `"balance_errors":1`) {
		t.Fatalf("应统计失败账号数: %s", body)
	}
}

// 一个号坏、一个号好：好的照常返回官方余额，坏的单独标记，互不影响。
func TestStatusRefreshCreditsIsolatesPerAccountFailure(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer good-token" {
			_, _ = w.Write([]byte(`{"code":0,"data":{"Response":{"Data":{"Accounts":[{"CycleCapacitySize":100,"CycleCapacityRemain":79,"CycleCapacityRemainPrecise":"79.75"}]}}}}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer up.Close()
	p := testPoolWith(&auth.Auth{UID: "good", AccessToken: "good-token"}, &auth.Auth{UID: "bad", AccessToken: "bad-token"})
	c := upstream.New()
	c.BillingBaseCN = up.URL
	h := NewHandler(Config{Pool: p, Upstream: c})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/status?refresh_credits=true", nil))
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `"credits":79.75`) || !strings.Contains(body, `"balance_errors":1`) {
		t.Fatalf("status=%d body=%s", w.Code, body)
	}
}

func TestStatusRefreshCreditsFiltersBoundAccount(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"code":0,"data":{"Response":{"Data":{"Accounts":[{"CycleCapacityRemainPrecise":"79.75"}]}}}}`))
	}))
	defer up.Close()
	p := testPoolWith(&auth.Auth{UID: "target", AccessToken: "secret"}, &auth.Auth{UID: "other", AccessToken: "other"})
	c := upstream.New()
	c.BillingBaseCN = up.URL
	h := NewHandler(Config{Pool: p, Upstream: c})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/status?refresh_credits=true&uid=target", nil))
	if w.Code != 200 || calls != 1 || strings.Contains(w.Body.String(), `"uid":"other"`) || !strings.Contains(w.Body.String(), `"credits_refreshed":true`) {
		t.Fatalf("calls=%d status=%d body=%s", calls, w.Code, w.Body.String())
	}
}
