package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"workbuddy2api/internal/auth"
	"workbuddy2api/internal/pool"
)

func decodeCheckins(t *testing.T, body []byte) map[string][]map[string]any {
	t.Helper()
	var out struct {
		Checkins map[string][]map[string]any `json:"checkins"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode checkins: %v body=%s", err, body)
	}
	return out.Checkins
}

// TestCheckinsAllAndSingle 校验全池与单账号两种返回形态，且历史来自 pool 记录。
func TestCheckinsAllAndSingle(t *testing.T) {
	p := testPoolWith(&auth.Auth{UID: "u1"}, &auth.Auth{UID: "u2"})
	at := time.Now().Round(time.Second)
	p.RecordCheckin("u1", pool.CheckinRecord{At: at, Status: "ok", Credits: 1500, Delta: 100})
	h := NewHandler(Config{Pool: p, APIKey: "k"})

	// 全池
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/checkins", nil)
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	all := decodeCheckins(t, rec.Body.Bytes())
	if len(all["u1"]) != 1 {
		t.Fatalf("u1 应有 1 条历史: %+v", all)
	}
	if _, ok := all["u2"]; ok {
		t.Fatalf("无历史账号不应出现: %+v", all)
	}

	// 单账号
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/checkins?uid=u1", nil)
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("single code=%d", rec.Code)
	}
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

	// 未知 uid → 空数组（非 404）
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/checkins?uid=ghost", nil)
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(rec, req)
	var ghost struct {
		Checkins []map[string]any `json:"checkins"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ghost); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || len(ghost.Checkins) != 0 {
		t.Fatalf("未知 uid 应 200 空: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCheckinsRequiresAuth /checkins 与 /status 共用鉴权。
func TestCheckinsRequiresAuth(t *testing.T) {
	p := testPoolWith(&auth.Auth{UID: "u1"})
	h := NewHandler(Config{Pool: p, APIKey: "secret"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/checkins", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 key 应 401: %d", rec.Code)
	}
}
