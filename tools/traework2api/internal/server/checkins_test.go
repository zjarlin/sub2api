package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"traework2api/internal/auth"
	"traework2api/internal/pool"
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
