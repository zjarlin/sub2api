package weather

import (
	"math"
	"sync"
	"time"
)

// 与翻译聚合器一致的健康评分语义：反映可用性与速度，不代表数据质量。
//
//	score = max(0, 100 × (成功数+4)/(尝试数+4) − 15 × 连续失败数 − min(20, EWMA 延迟毫秒/500))
const (
	healthCooldownAfterFailures = 3
	healthCooldownDuration      = 30 * time.Second
	healthLatencyWeight         = 0.2
)

type providerHealth struct {
	successes       int
	attempts        int
	consecutiveFail int
	latencyMS       float64
	cooldownUntil   time.Time
	probing         bool
}

// score 计算当前健康分，未产生样本时按满分处理。
func (h *providerHealth) score() float64 {
	if h == nil {
		return 100
	}
	successRate := 100 * float64(h.successes+4) / float64(h.attempts+4)
	penalty := 15 * float64(h.consecutiveFail)
	latencyPenalty := math.Min(20, h.latencyMS/500)
	return math.Max(0, successRate-penalty-latencyPenalty)
}

// observe 记录一次真实上游结果：成功清除连续失败与冷却，失败累计并可能进入冷却。
func (h *providerHealth) observe(latency time.Duration, ok bool, now time.Time) float64 {
	h.attempts++
	if h.latencyMS == 0 {
		h.latencyMS = float64(latency.Milliseconds())
	} else {
		h.latencyMS = (1-healthLatencyWeight)*h.latencyMS + healthLatencyWeight*float64(latency.Milliseconds())
	}
	if ok {
		h.successes++
		h.consecutiveFail = 0
		h.cooldownUntil = time.Time{}
		return h.score()
	}
	h.consecutiveFail++
	if h.consecutiveFail >= healthCooldownAfterFailures {
		h.cooldownUntil = now.Add(healthCooldownDuration)
	}
	return h.score()
}

// cooling 判断该源是否处于冷却期。
func (h *providerHealth) cooling(now time.Time) bool {
	return h != nil && !h.cooldownUntil.IsZero() && now.Before(h.cooldownUntil)
}

// healthTracker 按数据源保存进程内健康数据，配置变化或重启后重新学习。
type healthTracker struct {
	mu      sync.Mutex
	entries map[string]*providerHealth
}

func newHealthTracker() *healthTracker {
	return &healthTracker{entries: map[string]*providerHealth{}}
}

func (t *healthTracker) get(name string) *providerHealth {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.entries[name]
	if !ok {
		entry = &providerHealth{}
		t.entries[name] = entry
	}
	return entry
}

// record 写入一次真实结果并返回最新分数。
func (t *healthTracker) record(name string, latency time.Duration, ok bool, now time.Time) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok2 := t.entries[name]
	if !ok2 {
		entry = &providerHealth{}
		t.entries[name] = entry
	}
	return entry.observe(latency, ok, now)
}

// scores 返回当前各源分数的快照。
func (t *healthTracker) scores() map[string]float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	scores := make(map[string]float64, len(t.entries))
	for name, entry := range t.entries {
		scores[name] = entry.score()
	}
	return scores
}
