package scheduler

// checkin_retry_test.go 签到失败重试（issue:上游 5xx 导致当天积分白漏）。
//
// 背景：2026-10-08 09:00 上游 daily-checkin 返回 10001「签到失败，请稍后重试」，
// 旧的「每个整点只试一次」让该号当天积分白漏到 21 点。修复后：整点批次失败即按
// 间隔重排，直到领到或每时点预算（CheckinRetryMax）用尽。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workbuddy2api/internal/auth"
	"workbuddy2api/internal/pool"
	"workbuddy2api/internal/upstream"
)

// retryStub 前 failFirst 次 /daily-checkin 返回 500，之后返回成功。
// 用于验证「失败 → 重排 → 重试成功 → 收口」的完整闭环。
type retryStub struct {
	failFirst   int32
	checkinCall atomic.Int32
}

func (s *retryStub) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/daily-checkin"):
			if s.checkinCall.Add(1) <= s.failFirst {
				w.WriteHeader(500)
				w.Write([]byte(`{"code":10001,"msg":"签到失败，请稍后重试"}`))
				return
			}
			w.Write([]byte(`{"code":0,"msg":"ok","data":{}}`))
		case strings.HasSuffix(r.URL.Path, "/get-user-resource"):
			w.Write([]byte(`{"code":0,"data":{"Response":{"Data":{"Accounts":[{"CycleCapacitySize":1000,"CycleCapacityRemain":500,"CycleCapacityUsed":0}]}}}}`))
		case strings.HasSuffix(r.URL.Path, "/token/refresh"):
			w.Write([]byte(`{"code":0,"data":{"accessToken":"new","expiresIn":3600}}`))
		default:
			http.Error(w, "not found", 404)
		}
	}))
}

// checkinRetryWakeAt 读待重试唤醒时刻（测试内访问器：同包直读未导出字段，仍走锁，
// 不在生产代码里留测试专用的导出面）。
func checkinRetryWakeAt(s *Scheduler) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkinRetryAt.IsZero() {
		return time.Time{}, false
	}
	return s.checkinRetryAt, true
}

// fireCheckinRetry 模拟"重试 timer 到期"：真实路径里 Run 的 timer 到点后才调
// runCheckinRetry（它先核对计划确实到期）。测试不真等一小时，直接把计划唤醒时刻
// 推到过去，再走同一个入口，保持被验证的判定链路不变。
func fireCheckinRetry(s *Scheduler) {
	s.mu.Lock()
	if !s.checkinRetryAt.IsZero() {
		s.checkinRetryAt = time.Now().Add(-time.Second)
	}
	s.mu.Unlock()
	s.runCheckinRetry()
}

// checkinRetryBudget 读重试预算快照。
func checkinRetryBudget(s *Scheduler) map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.checkinRetryLeft))
	for uid, n := range s.checkinRetryLeft {
		out[uid] = n
	}
	return out
}

// newRetryS 构造带重试配置的调度器（间隔 1h：测试不依赖真实等待，靠手动推进时钟）。
func newRetryS(t *testing.T, stub *retryStub, retryMax int) (*Scheduler, *pool.Pool) {
	t.Helper()
	srv := stub.server()
	t.Cleanup(srv.Close)
	p := pool.New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999})
	up := &upstream.Client{HTTP: srv.Client(), ChatBaseCN: srv.URL, BillingBaseCN: srv.URL}
	return New(Config{
		Pool:              p,
		Upstream:          up,
		CheckinHours:      []int{9, 21},
		KeepaliveHours:    []int{22},
		CheckinRetryAfter: time.Hour,
		CheckinRetryMax:   retryMax,
	}), p
}

// TestCheckinRetryArmedOnFailure 整点批次全失败 → 排下一次重试唤醒（含预算）。
func TestCheckinRetryArmedOnFailure(t *testing.T) {
	stub := &retryStub{failFirst: 1}
	s, _ := newRetryS(t, stub, 3)

	s.RunCheckinNow()

	if at, ok := checkinRetryWakeAt(s); !ok {
		t.Fatal("签到失败后应排下一次重试唤醒")
	} else if d := time.Until(at); d <= 0 || d > time.Hour+time.Minute {
		t.Errorf("重试时刻应在 ~1h 后，got %v", d)
	}
	if left := checkinRetryBudget(s); left["u1"] != 3 {
		t.Errorf("预算应重置为 3，got %v", left)
	}
	if stub.checkinCall.Load() != 1 {
		t.Errorf("整点批次应只签到一次，got %d", stub.checkinCall.Load())
	}
}

// TestCheckinRetryRecovers 重试批次成功后收口：计划清空、不再唤醒、共打两次上游。
func TestCheckinRetryRecovers(t *testing.T) {
	stub := &retryStub{failFirst: 1}
	s, _ := newRetryS(t, stub, 3)

	s.RunCheckinNow()   // 第 1 次：fail → 排重试
	fireCheckinRetry(s) // 第 2 次：ok → 收口

	if stub.checkinCall.Load() != 2 {
		t.Errorf("checkin calls=%d want 2（失败一次 + 重试一次）", stub.checkinCall.Load())
	}
	if _, ok := checkinRetryWakeAt(s); ok {
		t.Error("重试成功后不应再排唤醒")
	}
	if left := checkinRetryBudget(s); len(left) != 0 {
		t.Errorf("重试成功后预算应清空，got %v", left)
	}
}

// TestCheckinRetryBudgetExhausts 一直失败：重试次数用尽后不再排唤醒（有界收口）。
func TestCheckinRetryBudgetExhausts(t *testing.T) {
	stub := &retryStub{failFirst: 99} // 永远失败
	s, _ := newRetryS(t, stub, 2)     // 每时点 2 次

	s.RunCheckinNow()   // 第 1 次：fail，预算 2
	fireCheckinRetry(s) // 第 2 次：fail，预算 2-1=1
	fireCheckinRetry(s) // 第 3 次：fail，预算 1-1=0 → 出局
	if _, ok := checkinRetryWakeAt(s); ok {
		t.Error("预算耗尽后不应再排唤醒")
	}
	if stub.checkinCall.Load() != 3 {
		t.Errorf("checkin calls=%d want 3（1 整点 + 2 重试）", stub.checkinCall.Load())
	}

	// 下一次整点批次重新给满预算（09 点用完不影响 21 点）。
	s.RunCheckinNow()
	if left := checkinRetryBudget(s); left["u1"] != 2 {
		t.Errorf("新时点应重新给满预算，got %v", left)
	}
}

// TestCheckinRetryBudgetResetAtEverySlot 前一时点的重试计划被新整点批次清掉：
// 09 点失败留下的唤醒时刻不得"穿越"到 21 点之后才触发。
func TestCheckinRetryBudgetResetAtEverySlot(t *testing.T) {
	stub := &retryStub{failFirst: 99}
	s, _ := newRetryS(t, stub, 3)

	s.RunCheckinNow() // 09 点批次：fail → 排重试
	if _, ok := checkinRetryWakeAt(s); !ok {
		t.Fatal("应先排上重试")
	}
	s.RunCheckinNow() // 21 点批次：重置计划后又是一次 fail → 排新的重试
	if left := checkinRetryBudget(s); left["u1"] != 3 {
		t.Errorf("新整点批次应重置为满预算，got %v", left)
	}
}

// TestCheckinRetryDisabledNoWake 重试关闭（零值 Config）时行为与引入前逐字一致：
// 失败不留任何唤醒计划。
func TestCheckinRetryDisabledNoWake(t *testing.T) {
	stub := &retryStub{failFirst: 99}
	srv := stub.server()
	defer srv.Close()
	p := pool.New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999})
	up := &upstream.Client{HTTP: srv.Client(), ChatBaseCN: srv.URL, BillingBaseCN: srv.URL}
	s := New(Config{Pool: p, Upstream: up, CheckinHours: []int{9, 21}, KeepaliveHours: []int{22}})

	s.RunCheckinNow()

	if _, ok := checkinRetryWakeAt(s); ok {
		t.Error("重试关闭时不应排唤醒")
	}
	if stub.checkinCall.Load() != 1 {
		t.Errorf("重试关闭时应只签一次，got %d", stub.checkinCall.Load())
	}
}

// TestNextWakeIncludesCheckinRetry 重试时刻与整点槽位同权参与 nextWake 取最近者。
func TestNextWakeIncludesCheckinRetry(t *testing.T) {
	stub := &retryStub{failFirst: 99}
	s, _ := newRetryS(t, stub, 3)
	s.RunCheckinNow() // 排一次重试

	// 从"现在"看，最近的可等时刻应为重试（1h 后）而非下一个整点。
	now := time.Now()
	at, kinds := s.nextWake(now)
	if !hasKind(kinds, taskCheckinRetry) {
		t.Errorf("nextWake kinds=%v 应含 taskCheckinRetry", kinds)
	}
	if at.IsZero() {
		t.Fatal("nextWake 不应返回零时刻")
	}
}

// TestCheckinRetrySkippedNotRetried already/skipped 不进重试计划：今天已签到、
// 被禁用、无凭证的账号都不该被重试唤醒拖着再打一次上游。
func TestCheckinRetrySkippedNotRetried(t *testing.T) {
	stub := &retryStub{failFirst: 99}
	srv := stub.server()
	defer srv.Close()

	p := pool.New("")
	p.Add(&auth.Auth{UID: "ok", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999})
	p.Add(&auth.Auth{UID: "dis", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999})
	p.Disable("dis", "test")
	up := &upstream.Client{HTTP: srv.Client(), ChatBaseCN: srv.URL, BillingBaseCN: srv.URL}
	s := New(Config{Pool: p, Upstream: up, CheckinRetryAfter: time.Hour, CheckinRetryMax: 3})

	out, err := s.CheckinAll()
	if err != nil {
		t.Fatalf("CheckinAll: %v", err)
	}
	s.armCheckinRetry(out, true)

	left := checkinRetryBudget(s)
	if _, ok := left["dis"]; ok {
		t.Error("禁用账号不应进重试计划")
	}
	if _, ok := left["ok"]; !ok {
		t.Error("上游 500 的账号应进重试计划")
	}
}

// TestCheckinRetryStaleWakeIgnored 计划被整点批次重置后，旧的唤醒时刻（timer 早已
// 按上一个计划定好）不得再触发一次签到——否则重试间隔跨过整点时会多打一轮上游。
func TestCheckinRetryStaleWakeIgnored(t *testing.T) {
	stub := &retryStub{failFirst: 99}
	s, _ := newRetryS(t, stub, 3)

	s.RunCheckinNow() // 排一次重试（计划在 1h 后）
	callsAfterSlot := stub.checkinCall.Load()

	// 不推进时钟直接触发重试唤醒：计划未到期 → 应被丢弃，不用真打上游。
	s.runCheckinRetry()
	if stub.checkinCall.Load() != callsAfterSlot {
		t.Errorf("未到期的重试唤醒不应触发签到：calls=%d want %d",
			stub.checkinCall.Load(), callsAfterSlot)
	}
}
