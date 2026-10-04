// 签到历史：单账号维度的签到结果环形记录（供面板展示"领了多少 / 还剩多少"）。
//
// 与 workbuddy2api 同构：历史由 scheduler 在签到时产生，持久化统一走 pool 的
// state.json（单一事实来源，跨重启不失忆）。scheduler → pool 是既有单向依赖，
// 历史存储落在 pool 不引入反向依赖（pool 不 import scheduler）。
package pool

import "time"

// CheckinHistoryLimit 单账号保留的签到历史条数（环形，越新越靠后）。
const CheckinHistoryLimit = 30

// CheckinRecord 单次签到结果（供面板展示：本次领取、领完剩余）。
// Status 取值："ok"（本次签到成功）/ "already"（今天已签）/ "fail" / "skipped"。
type CheckinRecord struct {
	At      time.Time `json:"at"`
	Status  string    `json:"status"`
	Credits int64     `json:"credits"` // 签到后积分（查询成功才有意义，未知为 0）
	Delta   int64     `json:"delta"`   // 本次领取积分（签到后 - 签到前；already 视为 0）
	Detail  string    `json:"detail,omitempty"`
}

// RecordCheckin 记录一次签到结果：追加到账号历史、环形裁剪、落盘。
func (p *Pool) RecordCheckin(uid string, rec CheckinRecord) {
	if uid == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.byUID[uid]
	if e == nil {
		return
	}
	e.checkins = append(e.checkins, rec)
	if len(e.checkins) > CheckinHistoryLimit {
		e.checkins = e.checkins[len(e.checkins)-CheckinHistoryLimit:]
	}
	p.saveLocked()
}

// CheckinHistory 返回单账号的签到历史副本（越新越靠后）。uid 不存在返回 nil。
func (p *Pool) CheckinHistory(uid string) []CheckinRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()
	e := p.byUID[uid]
	if e == nil || len(e.checkins) == 0 {
		return nil
	}
	out := make([]CheckinRecord, len(e.checkins))
	copy(out, e.checkins)
	return out
}

// lastCheckinLocked 返回最近一条签到记录（无则 nil）。调用方必须已持 p.mu。
func lastCheckinLocked(e *entry) *CheckinRecord {
	if e == nil || len(e.checkins) == 0 {
		return nil
	}
	last := e.checkins[len(e.checkins)-1]
	return &last
}

// toStateCheckins 运行态 → 持久化镜像（落盘用，nil 保持 nil）。
func toStateCheckins(in []CheckinRecord) []stateCheckin {
	if len(in) == 0 {
		return nil
	}
	out := make([]stateCheckin, len(in))
	for i, r := range in {
		out[i] = stateCheckin{At: r.At, Status: r.Status, Credits: r.Credits, Delta: r.Delta, Detail: r.Detail}
	}
	return out
}

// fromStateCheckins 持久化镜像 → 运行态（恢复用，环形裁剪 + 零值剔除）。
func fromStateCheckins(in []stateCheckin) []CheckinRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]CheckinRecord, 0, len(in))
	for _, r := range in {
		if r.At.IsZero() {
			continue
		}
		out = append(out, CheckinRecord{At: r.At, Status: r.Status, Credits: r.Credits, Delta: r.Delta, Detail: r.Detail})
	}
	if len(out) > CheckinHistoryLimit {
		out = out[len(out)-CheckinHistoryLimit:]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
