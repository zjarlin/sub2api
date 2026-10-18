// Package scheduler 定时任务：每日签到 + token 预刷新。
// 签到成功后重新查积分，积分 > 0 的冷却账号自动解冻。
package scheduler

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"traework2api/internal/pool"
	"traework2api/internal/upstream"
)

// Config 调度器依赖。
type Config struct {
	Pool         *pool.Pool
	Upstream     *upstream.Client
	CheckinHour  int           // 每日签到小时，默认 9
	RefreshHours []int         // token 预刷新小时，默认 [3]
	RefreshSkew  time.Duration // 预刷新窗口，默认 24h
}

// Scheduler 调度器。
type Scheduler struct {
	cfg Config

	// checkinMu 串行化签到：定时入口与手动触发互斥，避免同一时刻重复打上游签到接口。
	checkinMu sync.Mutex
}

// New 构建。
func New(cfg Config) *Scheduler {
	if cfg.CheckinHour < 0 {
		cfg.CheckinHour = 9
	}
	if len(cfg.RefreshHours) == 0 {
		cfg.RefreshHours = []int{3}
	}
	if cfg.RefreshSkew <= 0 {
		cfg.RefreshSkew = 24 * time.Hour
	}
	return &Scheduler{cfg: cfg}
}

// nextFire 返回 now 之后最近的一个整点触发时间；hours 为本地小时（0-23）。
func nextFire(now time.Time, hours []int) time.Time {
	var earliest time.Time
	for _, h := range hours {
		t := time.Date(now.Year(), now.Month(), now.Day(), h, 0, 0, 0, now.Location())
		if !t.After(now) {
			t = t.Add(24 * time.Hour)
		}
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}
	return earliest
}

// Run 主循环，阻塞直到 ctx 取消。
func (s *Scheduler) Run(ctx context.Context) {
	all := append(append([]int{}, s.cfg.RefreshHours...), s.cfg.CheckinHour)
	for {
		next := nextFire(time.Now(), all)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			h := time.Now().Hour()
			if contains(s.cfg.RefreshHours, h) {
				s.RunRefreshNow()
			}
			if s.cfg.CheckinHour == h {
				s.RunCheckinNow()
			}
		}
	}
}

func contains(hours []int, h int) bool {
	for _, v := range hours {
		if v == h {
			return true
		}
	}
	return false
}

// CheckinStatus 单账号签到结果状态（与 pool.CheckinRecord.Status 对齐）。
type CheckinStatus string

const (
	CheckinOK      CheckinStatus = "ok"      // 签到成功
	CheckinAlready CheckinStatus = "already" // 上游判定今天已签到（幂等重复，视为正常）
	CheckinFail    CheckinStatus = "fail"    // 签到 / 余额查询失败
	CheckinSkipped CheckinStatus = "skipped" // 禁用账号或无有效凭证，未参与
)

// CheckinOutcome 单账号签到结果（供手动签到回执与日志汇总）。
type CheckinOutcome struct {
	UID      string        `json:"uid"`
	Nickname string        `json:"nickname,omitempty"`
	Status   CheckinStatus `json:"status"`
	Credits  *int64        `json:"credits,omitempty"` // 签到后余额（余额查询成功才有值）
	Detail   string        `json:"detail,omitempty"`  // 失败/跳过原因（"已签到"不填）
}

// ErrBusy 已有一次签到正在执行（手动入口与定时撞车）。
var ErrBusy = errors.New("checkin already running")

// RunCheckinNow 立即对所有账号执行签到 + 积分刷新 + 解冻。
// 冷却中的账号也参与（签到就是为了解冻它们）；禁用的跳过。
func (s *Scheduler) RunCheckinNow() {
	if _, err := s.CheckinAll(); err != nil {
		log.Printf("scheduled checkin skipped: %v", err)
	}
}

// CheckinAll 全量签到：逐账号刷新 token（按需）→ status/claim → 查余额 → 解冻。
// 同一时刻只允许一次签到在跑，重复调用返回 ErrBusy。
func (s *Scheduler) CheckinAll() ([]CheckinOutcome, error) {
	return s.checkinLocked(nil)
}

// CheckinUIDs 手动签到指定上游账号（uids 为空等价于全量 CheckinAll）。
// 与定时签到共用 checkinMu：撞车返回 ErrBusy，避免同一时刻重复打上游。
// 未知 uid 直接忽略（由调用方根据结果是否为空决定 404），不凭空创建账号记录。
func (s *Scheduler) CheckinUIDs(uids []string) ([]CheckinOutcome, error) {
	return s.checkinLocked(uids)
}

// checkinLocked 是签到批次的唯一实现：过滤目标账号后逐个执行签到与余额回查。
func (s *Scheduler) checkinLocked(uids []string) ([]CheckinOutcome, error) {
	if !s.checkinMu.TryLock() {
		return nil, ErrBusy
	}
	defer s.checkinMu.Unlock()

	statuses := s.cfg.Pool.List()
	if len(uids) > 0 {
		want := make(map[string]struct{}, len(uids))
		for _, uid := range uids {
			if uid = strings.TrimSpace(uid); uid != "" {
				want[uid] = struct{}{}
			}
		}
		filtered := make([]pool.Status, 0, len(want))
		for _, st := range statuses {
			if _, ok := want[st.UID]; ok {
				filtered = append(filtered, st)
			}
		}
		statuses = filtered
	}

	out := make([]CheckinOutcome, 0, len(statuses))
	for _, st := range statuses {
		oc := CheckinOutcome{UID: st.UID, Nickname: st.Nickname}
		if st.Disabled {
			oc.Status, oc.Detail = CheckinSkipped, "disabled"
			s.cfg.Pool.RecordCheckin(st.UID, pool.CheckinRecord{At: time.Now(), Status: string(oc.Status), Credits: st.Credits, Detail: oc.Detail})
			out = append(out, oc)
			continue
		}
		a := s.cfg.Pool.AuthByUID(st.UID)
		if a == nil || a.RefreshTokenValue() == "" {
			oc.Status, oc.Detail = CheckinSkipped, "no credentials"
			s.cfg.Pool.RecordCheckin(st.UID, pool.CheckinRecord{At: time.Now(), Status: string(oc.Status), Credits: st.Credits, Detail: oc.Detail})
			out = append(out, oc)
			continue
		}

		oc.Status = CheckinAlready
		// 签到（status → 未签到则 claim）
		checkedIn, _, enable, err := s.cfg.Upstream.CheckinStatus(a)
		if err != nil {
			oc.Status, oc.Detail = CheckinFail, "status: "+err.Error()
			log.Printf("checkin status %s: %v", st.UID, err)
		} else if !checkedIn && enable {
			if err := s.cfg.Upstream.CheckinClaim(a); err != nil {
				oc.Status, oc.Detail = CheckinFail, "claim: "+err.Error()
				log.Printf("checkin claim %s: %v", st.UID, err)
			} else {
				oc.Status = CheckinOK
				log.Printf("checkin %s: ok", st.UID)
			}
		} else if checkedIn {
			log.Printf("checkin %s: already checked in", st.UID)
		} else {
			oc.Status, oc.Detail = CheckinSkipped, "checkin disabled upstream"
		}

		// 查积分 + 解冻；即使签到失败也回查余额，让面板获得尽可能新的剩余积分。
		remain, err := s.cfg.Upstream.UserEntUsage(a)
		if err != nil {
			log.Printf("ent-usage %s: %v", st.UID, err)
			if oc.Detail != "" {
				oc.Detail += "; "
			}
			oc.Detail += "credit: " + err.Error()
			oc.Status = CheckinFail
			s.cfg.Pool.RecordCheckin(st.UID, pool.CheckinRecord{At: time.Now(), Status: string(oc.Status), Credits: st.Credits, Detail: oc.Detail})
			out = append(out, oc)
			continue
		}
		s.cfg.Pool.ReenableIfCredits(st.UID, remain)
		delta := int64(0)
		if oc.Status == CheckinOK {
			delta = remain - st.Credits
		}
		oc.Credits = &remain
		s.cfg.Pool.RecordCheckin(st.UID, pool.CheckinRecord{
			At: time.Now(), Status: string(oc.Status), Credits: remain, Delta: delta, Detail: oc.Detail,
		})
		out = append(out, oc)
	}
	return out, nil
}

// RunCreditsRefreshNow 只刷新剩余积分并按需解冻，不执行签到。
//
// 启动时调用：state.json 里保存的是上一次查询到的积分，重启后直接沿用会让面板
// 和调度都用过期数字（例如额度已用尽却仍显示有积分并解除冷却）。
func (s *Scheduler) RunCreditsRefreshNow() {
	for _, st := range s.cfg.Pool.List() {
		if st.Disabled {
			continue
		}
		a := s.cfg.Pool.AuthByUID(st.UID)
		if a == nil || a.RefreshTokenValue() == "" {
			continue
		}
		remain, err := s.cfg.Upstream.UserEntUsage(a)
		if err != nil {
			log.Printf("credits refresh %s: %v", st.UID, err)
			continue
		}
		s.cfg.Pool.ReenableIfCredits(st.UID, remain)
	}
}

// RunRefreshNow 立即对所有账号刷新 token；session 失效的自动禁用。
func (s *Scheduler) RunRefreshNow() {
	for _, st := range s.cfg.Pool.List() {
		if st.Disabled {
			continue
		}
		a := s.cfg.Pool.AuthByUID(st.UID)
		if a == nil || a.RefreshTokenValue() == "" {
			continue
		}
		if !a.NeedsRefresh(s.cfg.RefreshSkew) {
			continue
		}
		if err := s.cfg.Upstream.RefreshToken(a); err != nil {
			log.Printf("refresh %s: %v", st.UID, err)
			var ue *upstream.Error
			if errors.As(err, &ue) && ue.Kind == upstream.ErrSessionDead {
				s.cfg.Pool.Disable(st.UID, "session dead")
			}
			continue
		}
		if err := a.SaveAtomic(); err != nil {
			log.Printf("refresh %s save: %v", st.UID, err)
		}
	}
}
