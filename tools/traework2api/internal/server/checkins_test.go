package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"traework2api/internal/auth"
	"traework2api/internal/pool"
	"traework2api/internal/scheduler"
	"traework2api/internal/upstream"
)

func TestCheckinsAllAndSingle(t *testing.T) {
	p := testPoolWith(&auth.Auth{UID: "u1"}, &auth.Auth{UID: "u2"})
	at := time.Now().Round(time.Second)
	p.RecordCheckin("u1", pool.CheckinRecord{At: at, Status: "ok", Credits: 700, Delta: 50})
	h := NewHandler(Config{Pool: p, APIKey: "k"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/checkins", nil)
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var all struct {
		Checkins map[string][]map[string]any `json:"checkins"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatal(err)
	}
	if len(all.Checkins["u1"]) != 1 {
		t.Fatalf("u1 应有 1 条历史: %+v", all)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/checkins?uid=u1", nil)
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(rec, req)
	var single struct {
		UID      string           `json:"uid"`
		Checkins []map[string]any `json:"checkins"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &single); err != nil {
		t.Fatal(err)
	}
	if single.UID != "u1" || len(single.Checkins) != 1 {
		t.Fatalf("单账号返回不符: %+v", single)
	}
}

func TestCheckinsRequiresAuth(t *testing.T) {
	p := testPoolWith(&auth.Auth{UID: "u1"})
	h := NewHandler(Config{Pool: p, APIKey: "secret"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/checkins", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 key 应 401: %d", rec.Code)
	}
}

// newCheckinHandler 构造带 scheduler 的 handler：手动签到需要 Scheduler 才能工作。
func newCheckinHandler(t *testing.T, p *pool.Pool) *Handler {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/checkin_credits/status"):
			_, _ = w.Write([]byte(`{"checked_in":false,"credits":200,"enable":true}`))
		case strings.HasSuffix(r.URL.Path, "/checkin_credits/claim"):
			_, _ = w.Write([]byte(`{"code":0,"message":"success"}`))
		case strings.HasSuffix(r.URL.Path, "/ide_user_ent_usage"):
			_, _ = w.Write([]byte(`{"is_credits_billing":true,"user_entitlement_pack_list":[{"entitlement_base_info":{"quota":{"credits_limit":900}},"usage":{"credits_amount":0}}]}`))
		default:
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	up := &upstream.Client{HTTP: srv.Client(), AgentHost: srv.URL, UgHost: srv.URL, OAuthHost: srv.URL, ClientID: upstream.ClientID}
	return NewHandler(Config{AuthDir: t.TempDir(), Pool: p, Upstream: up, APIKey: "k",
		Scheduler: scheduler.New(scheduler.Config{Pool: p, Upstream: up})})
}

// TestCheckinHTTPRequiresAuth /checkin 与其它端点一样要求 bearer key。
func TestCheckinHTTPRequiresAuth(t *testing.T) {
	h := NewHandler(Config{Pool: testPoolWith(), APIKey: "k"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/checkin", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rec.Code)
	}
}

// TestCheckinHTTPWithoutSchedulerUnavailable 未接入调度器时如实回 503，而不是假装成功。
func TestCheckinHTTPWithoutSchedulerUnavailable(t *testing.T) {
	h := NewHandler(Config{Pool: testPoolWith(&auth.Auth{UID: "u1"}), APIKey: "k"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/checkin", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !assertJSONErrorCode(t, rec.Body.String(), "checkin_unavailable") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

// TestCheckinHTTPUnknownUIDNotFound 指定 uid 无匹配账号 → 404，不落到全池签到。
func TestCheckinHTTPUnknownUIDNotFound(t *testing.T) {
	p := testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999})
	h := newCheckinHandler(t, p)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/checkin", strings.NewReader(`{"uids":["missing"]}`))
	req.Header.Set("Authorization", "Bearer k")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !assertJSONErrorCode(t, rec.Body.String(), "account_not_found") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

// TestCheckinHTTPSingleAndPool 空 body = 全池；带 uids = 只签指定账号。
func TestCheckinHTTPSingleAndPool(t *testing.T) {
	p := testPoolWith(
		&auth.Auth{UID: "u1", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999},
		&auth.Auth{UID: "u2", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999},
	)
	h := newCheckinHandler(t, p)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/checkin", strings.NewReader(`{"uids":["u2"]}`))
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var single struct {
		Results []struct {
			UID    string `json:"uid"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &single); err != nil {
		t.Fatal(err)
	}
	if len(single.Results) != 1 || single.Results[0].UID != "u2" || single.Results[0].Status != "ok" {
		t.Fatalf("results=%+v want only u2 ok", single.Results)
	}
	if st, _ := p.Status("u1"); st.Credits != 1000 {
		t.Fatalf("u1 must stay untouched, credits=%d", st.Credits)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/checkin", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(rec, req)
	var all struct {
		Results []struct {
			UID string `json:"uid"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatal(err)
	}
	if len(all.Results) != 2 {
		t.Fatalf("pool results=%+v want 2", all.Results)
	}
}

// assertJSONErrorCode 断言响应体是 OpenAI 风格 error 信封且 code 精确等于 want。
func assertJSONErrorCode(t *testing.T, body, want string) bool {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return false
	}
	return envelope.Error.Code == want
}
