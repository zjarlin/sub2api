package scheduler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"traework2api/internal/auth"
	"traework2api/internal/pool"
	"traework2api/internal/upstream"
)

func TestNextFire(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, loc)
	next := nextFire(now, []int{9, 21})
	if next.Hour() != 21 || next.Day() != 27 {
		t.Errorf("next=%v want 21:00 same day", next)
	}
	now = time.Date(2026, 7, 27, 22, 0, 0, 0, loc)
	next = nextFire(now, []int{9, 21})
	if next.Hour() != 9 || next.Day() != 28 {
		t.Errorf("next=%v want 09:00 next day", next)
	}
	now = time.Date(2026, 7, 27, 9, 0, 0, 0, loc)
	next = nextFire(now, []int{9})
	if next.Day() != 28 {
		t.Errorf("exact match should roll to next day: %v", next)
	}
}

func TestNextFireMergesSchedules(t *testing.T) {
	now := time.Date(2026, 7, 27, 20, 0, 0, 0, time.Local)
	next := nextFire(now, []int{9, 21, 22})
	if next.Hour() != 21 {
		t.Errorf("next=%v want 21 (earliest of 21/22)", next)
	}
}

// fakeUpstream 同时模拟 checkin/ent_usage/refresh。
type fakeUpstream struct {
	checkinCalls   atomic.Int32
	claimCalls     atomic.Int32
	refreshCalls   atomic.Int32
	entUsageCalls  atomic.Int32
	resourceRemain int64
	resourceUsed   int64
}

func (f *fakeUpstream) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/checkin_credits/status"):
			f.checkinCalls.Add(1)
			w.Write([]byte(`{"checked_in":false,"credits":200,"enable":true}`))
		case strings.HasSuffix(r.URL.Path, "/checkin_credits/claim"):
			f.claimCalls.Add(1)
			w.Write([]byte(`{"code":0,"message":"success"}`))
		case strings.HasSuffix(r.URL.Path, "/ide_user_ent_usage"):
			f.entUsageCalls.Add(1)
			w.Write([]byte(`{"is_credits_billing":true,"user_entitlement_pack_list":[{"entitlement_base_info":{"quota":{"credits_limit":` +
				jsonI64(f.resourceRemain) + `}},"usage":{"credits_amount":` + jsonI64(f.resourceUsed) + `}}]}`))
		case strings.HasSuffix(r.URL.Path, "/ExchangeToken"):
			f.refreshCalls.Add(1)
			w.Write([]byte(`{"Result":{"Token":"newat","RefreshToken":"newrt","TokenExpireAt":1786805537,"TokenExpireDuration":1209600}}`))
		default:
			http.Error(w, "not found: "+r.URL.Path, 404)
		}
	}))
}

func jsonI64(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func newTestScheduler(f *fakeUpstream, p *pool.Pool, srv *httptest.Server) *Scheduler {
	up := &upstream.Client{
		HTTP:      srv.Client(),
		AgentHost: srv.URL,
		UgHost:    srv.URL,
		OAuthHost: srv.URL,
		ClientID:  upstream.ClientID,
	}
	return New(Config{
		Pool:         p,
		Upstream:     up,
		CheckinHour:  9,
		RefreshHours: []int{3},
		RefreshSkew:  time.Hour,
	})
}

func TestRunCheckinReenablesCoolingAccount(t *testing.T) {
	f := &fakeUpstream{resourceRemain: 500}
	srv := f.server()
	defer srv.Close()

	p := pool.New("")
	a := &auth.Auth{UID: "u1", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999}
	p.Add(a)
	p.Cooldown("u1", pool.CoolPlan, time.Hour, "plan limit")

	s := newTestScheduler(f, p, srv)
	s.RunCheckinNow()
	if f.checkinCalls.Load() != 1 {
		t.Errorf("checkin status calls=%d", f.checkinCalls.Load())
	}
	if f.claimCalls.Load() != 1 {
		t.Errorf("claim calls=%d", f.claimCalls.Load())
	}
	st, _ := p.Status("u1")
	if st.Cooling {
		t.Errorf("account should be reenabled after checkin with credits: %+v", st)
	}
	if st.Credits != 500 {
		t.Errorf("credits=%d want 500", st.Credits)
	}
}

// TestCheckinUIDsOnlyChecksRequestedAccount 手动签到只打指定账号，不触碰池内其他账号。
func TestCheckinUIDsOnlyChecksRequestedAccount(t *testing.T) {
	f := &fakeUpstream{resourceRemain: 500}
	srv := f.server()
	defer srv.Close()

	p := pool.New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999})
	p.Add(&auth.Auth{UID: "u2", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999})

	s := newTestScheduler(f, p, srv)
	out, err := s.CheckinUIDs([]string{"u2"})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(out) != 1 || out[0].UID != "u2" || out[0].Status != CheckinOK {
		t.Fatalf("out=%+v want only u2 ok", out)
	}
	if out[0].Credits == nil || *out[0].Credits != 500 {
		t.Errorf("credits=%v want 500", out[0].Credits)
	}
	if f.checkinCalls.Load() != 1 || f.claimCalls.Load() != 1 {
		t.Errorf("status=%d claim=%d want 1/1", f.checkinCalls.Load(), f.claimCalls.Load())
	}
	if st, _ := p.Status("u1"); st.Credits != 0 {
		t.Errorf("u1 must stay untouched, credits=%d", st.Credits)
	}
}

// TestCheckinUIDsUnknownAccountReturnsEmpty 未知 uid 不报错、不凭空创建账号记录。
func TestCheckinUIDsUnknownAccountReturnsEmpty(t *testing.T) {
	f := &fakeUpstream{resourceRemain: 500}
	srv := f.server()
	defer srv.Close()

	p := pool.New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999})

	s := newTestScheduler(f, p, srv)
	out, err := s.CheckinUIDs([]string{"missing"})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(out) != 0 {
		t.Fatalf("out=%+v want empty", out)
	}
	if _, ok := p.Status("missing"); ok {
		t.Error("unknown uid must not be created")
	}
}

// TestCheckinUIDsBusy 手动签到与正在执行的签到撞车 → ErrBusy（不并发打上游）。
func TestCheckinUIDsBusy(t *testing.T) {
	p := pool.New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999})
	s := New(Config{Pool: p})
	s.checkinMu.Lock()
	defer s.checkinMu.Unlock()
	if _, err := s.CheckinUIDs([]string{"u1"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("err=%v want ErrBusy", err)
	}
}

func TestRunCreditsRefreshNowKeepsExhaustedAccountCooling(t *testing.T) {
	f := &fakeUpstream{resourceRemain: 8500, resourceUsed: 8500}
	srv := f.server()
	defer srv.Close()

	p := pool.New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999})
	p.ReenableIfCredits("u1", 8500)
	p.Cooldown("u1", pool.CoolPlan, time.Hour, "exceeded the quota")

	s := newTestScheduler(f, p, srv)
	s.RunCreditsRefreshNow()

	if f.entUsageCalls.Load() != 1 {
		t.Errorf("ent usage calls=%d want 1", f.entUsageCalls.Load())
	}
	if f.checkinCalls.Load() != 0 || f.claimCalls.Load() != 0 {
		t.Errorf("credits refresh must not check in: status=%d claim=%d", f.checkinCalls.Load(), f.claimCalls.Load())
	}
	st, _ := p.Status("u1")
	if st.Credits != 0 {
		t.Errorf("credits=%d want 0 for exhausted account", st.Credits)
	}
	if !st.Cooling {
		t.Errorf("exhausted account must stay cooling: %+v", st)
	}
}

func TestRunCreditsRefreshNowReenablesAccountWithCredits(t *testing.T) {
	f := &fakeUpstream{resourceRemain: 800, resourceUsed: 300}
	srv := f.server()
	defer srv.Close()

	p := pool.New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999})
	p.ReenableIfCredits("u1", 800)
	p.Cooldown("u1", pool.CoolPlan, time.Hour, "exceeded the quota")

	s := newTestScheduler(f, p, srv)
	s.RunCreditsRefreshNow()

	st, _ := p.Status("u1")
	if st.Credits != 500 {
		t.Errorf("credits=%d want 500", st.Credits)
	}
	if st.Cooling {
		t.Errorf("account with remaining credits should be reenabled: %+v", st)
	}
}

func TestRunCheckinSkipsDisabled(t *testing.T) {
	f := &fakeUpstream{}
	srv := f.server()
	defer srv.Close()

	p := pool.New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999})
	p.Disable("u1", "session dead")

	s := newTestScheduler(f, p, srv)
	s.RunCheckinNow()
	if f.checkinCalls.Load() != 0 {
		t.Errorf("disabled account should be skipped, calls=%d", f.checkinCalls.Load())
	}
}

func TestRunRefreshRefreshesTokens(t *testing.T) {
	f := &fakeUpstream{}
	srv := f.server()
	defer srv.Close()

	p := pool.New("")
	a := &auth.Auth{UID: "u1", AccessToken: "old", RefreshToken: "rt", ExpiresAt: 1, ApiHost: srv.URL}
	p.Add(a)

	s := newTestScheduler(f, p, srv)
	s.RunRefreshNow()
	if f.refreshCalls.Load() != 1 {
		t.Errorf("refresh calls=%d", f.refreshCalls.Load())
	}
	if a.AccessToken != "newat" {
		t.Errorf("token not updated: %s", a.AccessToken)
	}
}

func TestRunRefreshSkipsFreshToken(t *testing.T) {
	f := &fakeUpstream{}
	srv := f.server()
	defer srv.Close()

	p := pool.New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "fresh", RefreshToken: "rt", ExpiresAt: 9999999999, ApiHost: srv.URL})

	s := newTestScheduler(f, p, srv)
	s.RunRefreshNow()
	if f.refreshCalls.Load() != 0 {
		t.Errorf("fresh token should not refresh, calls=%d", f.refreshCalls.Load())
	}
}

func TestRunRefreshSessionDeadDisables(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"code":20101,"msg":"login required"}`))
	}))
	defer srv.Close()

	p := pool.New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "old", RefreshToken: "rt", ExpiresAt: 1, ApiHost: srv.URL})

	up := &upstream.Client{HTTP: srv.Client(), AgentHost: srv.URL, UgHost: srv.URL, OAuthHost: srv.URL, ClientID: upstream.ClientID}
	s := New(Config{Pool: p, Upstream: up})
	s.RunRefreshNow()
	st, _ := p.Status("u1")
	if !st.Disabled {
		t.Errorf("should disable session-dead account: %+v", st)
	}
}
