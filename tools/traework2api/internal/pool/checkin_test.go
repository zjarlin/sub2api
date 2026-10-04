// checkin_test.go 签到历史测试：环形裁剪、落盘/恢复往返、零值剔除。
package pool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"traework2api/internal/auth"
)

func TestRecordCheckinAppendsAndRings(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	for i := 0; i < CheckinHistoryLimit+5; i++ {
		p.RecordCheckin("u1", CheckinRecord{At: time.Unix(int64(i), 0), Status: "ok", Credits: int64(i), Delta: 1})
	}
	hist := p.CheckinHistory("u1")
	if len(hist) != CheckinHistoryLimit {
		t.Fatalf("环形裁剪: len=%d want %d", len(hist), CheckinHistoryLimit)
	}
	if hist[len(hist)-1].Credits != int64(CheckinHistoryLimit+4) {
		t.Fatalf("末条应为最新: %+v", hist[len(hist)-1])
	}
}

func TestRecordCheckinIgnoresUnknownUID(t *testing.T) {
	p := New("")
	p.RecordCheckin("ghost", CheckinRecord{At: time.Now()})
	if got := p.CheckinHistory("ghost"); got != nil {
		t.Fatalf("未知 uid 记录应被忽略: %+v", got)
	}
}

func TestCheckinHistoryPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "state.json")
	p := New(fp)
	p.Add(&auth.Auth{UID: "u1"})
	at := time.Now().Round(time.Second)
	p.RecordCheckin("u1", CheckinRecord{At: at, Status: "ok", Credits: 900, Delta: 50})
	p.RecordCheckin("u1", CheckinRecord{At: at.Add(time.Hour), Status: "already", Credits: 880, Delta: 0})

	p2 := New(fp)
	p2.Add(&auth.Auth{UID: "u1"})
	hist := p2.CheckinHistory("u1")
	if len(hist) != 2 {
		t.Fatalf("恢复后历史条数: %d want 2", len(hist))
	}
	if hist[0].Credits != 900 || hist[0].Delta != 50 {
		t.Fatalf("首条往返不一致: %+v", hist[0])
	}
	st, _ := p2.Status("u1")
	if st.Checkin == nil || st.Checkin.Status != "already" {
		t.Fatalf("Status.Checkin 应取末条: %+v", st.Checkin)
	}
}

func TestCheckinRestoreDropsZeroAt(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "state.json")
	state := map[string]any{
		"accounts": map[string]any{
			"u1": map[string]any{
				"credits": 100,
				"checkins": []map[string]any{
					{"status": "ok", "credits": 120},
					{"at": time.Now().Format(time.RFC3339Nano), "status": "ok", "credits": 130, "delta": 10},
				},
			},
		},
	}
	raw, _ := json.Marshal(state)
	if err := os.WriteFile(fp, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(fp)
	p.Add(&auth.Auth{UID: "u1"})
	hist := p.CheckinHistory("u1")
	if len(hist) != 1 || hist[0].Credits != 130 {
		t.Fatalf("零 at 条目应剔除: %+v", hist)
	}
}
