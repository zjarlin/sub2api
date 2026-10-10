package translate

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// TranslationAttempt 记录链路状态及脱敏失败原因，不保存原文或密钥。
type TranslationAttempt struct {
	Provider  string  `json:"provider"`
	Status    string  `json:"status"`
	Reason    string  `json:"reason,omitempty"`
	LatencyMS int64   `json:"latency_ms"`
	Score     float64 `json:"score"`
}

// ChainError 保留失败链路，调用方可检查每个候选的尝试状态。
type ChainError struct{ Attempts []TranslationAttempt }

func (e *ChainError) Error() string {
	if len(e.Attempts) == 0 {
		return "translate: no providers configured"
	}
	return "translate: all providers failed or cooling down"
}

type providerHealth struct {
	attempts            int64
	successes           int64
	consecutiveFailures int
	latencyMS           float64
	cooldownUntil       time.Time
	retryAt             time.Time
	probing             bool
}

// ProviderHealth 是进程内健康观测；重启或配置变化后重新学习。
type ProviderHealth struct {
	Provider            string     `json:"provider"`
	Tier                int        `json:"tier"`
	Score               float64    `json:"score"`
	Attempts            int64      `json:"attempts"`
	Successes           int64      `json:"successes"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LatencyMS           float64    `json:"latency_ms"`
	CooldownUntil       *time.Time `json:"cooldown_until,omitempty"`
}

func providerTier(name string) int {
	switch name {
	case "tencent", "youdao":
		return 1
	case "baidu":
		return 2
	default:
		return 0
	}
}

// 健康分采用平滑成功率，扣除连续失败和延迟惩罚；它不是译文语义质量分。
func healthScore(h *providerHealth) float64 {
	if h == nil {
		return 100
	}
	reliability := float64(h.successes+4) / float64(h.attempts+4)
	return math.Max(0, 100*reliability-float64(h.consecutiveFailures)*15-math.Min(20, h.latencyMS/500))
}

func (a *Aggregator) rankedProviders(now time.Time) []Translator {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := append([]Translator(nil), a.translators...)
	sort.SliceStable(result, func(i, j int) bool {
		left, right := result[i].Name(), result[j].Name()
		if providerTier(left) != providerTier(right) {
			return providerTier(left) < providerTier(right)
		}
		// 冷却到期的服务优先获得一次恢复机会，避免低分永久饿死。
		recovering := func(name string) bool {
			h := a.health[name]
			return h != nil && h.consecutiveFailures > 0 && !now.Before(h.retryAt) && !h.probing
		}
		if recovering(left) != recovering(right) {
			return recovering(left)
		}
		return healthScore(a.health[left]) > healthScore(a.health[right])
	})
	return result
}

func (a *Aggregator) cooling(name string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.health[name]
	if h == nil {
		return false
	}
	if now.Before(h.cooldownUntil) || h.probing {
		return true
	}
	if h.consecutiveFailures > 0 && !now.Before(h.retryAt) {
		h.probing = true
	}
	return false
}

func (a *Aggregator) record(name string, elapsed time.Duration, success bool, now time.Time) float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.health == nil {
		a.health = make(map[string]*providerHealth)
	}
	h := a.health[name]
	if h == nil {
		h = &providerHealth{}
		a.health[name] = h
	}
	h.probing = false
	h.attempts++
	ms := float64(elapsed) / float64(time.Millisecond)
	if h.attempts == 1 {
		h.latencyMS = ms
	} else {
		h.latencyMS = 0.8*h.latencyMS + 0.2*ms
	}
	if success {
		h.successes++
		h.consecutiveFailures = 0
		h.cooldownUntil = time.Time{}
	} else {
		h.consecutiveFailures++
		h.retryAt = now.Add(30 * time.Second)
		if h.consecutiveFailures >= 3 {
			h.cooldownUntil = now.Add(30 * time.Second)
		}
	}
	return healthScore(h)
}

func validateTranslation(req *TranslateRequest, resp *TranslateResponse) error {
	if resp == nil || len(resp.Translations) != len(req.Text) {
		return fmt.Errorf("translate: incomplete results")
	}
	for i, text := range req.Text {
		if strings.TrimSpace(text) != "" && strings.TrimSpace(resp.Translations[i].Text) == "" {
			return fmt.Errorf("translate: empty result")
		}
	}
	return nil
}

// ProviderHealth 按当前自动路由顺序返回已配置服务的健康快照。
func (a *Aggregator) ProviderHealth() []ProviderHealth {
	candidates := a.rankedProviders(time.Now())
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]ProviderHealth, 0, len(candidates))
	for _, t := range candidates {
		name := t.Name()
		item := ProviderHealth{Provider: name, Tier: providerTier(name), Score: healthScore(a.health[name])}
		if h := a.health[name]; h != nil {
			item.Attempts, item.Successes, item.ConsecutiveFailures, item.LatencyMS = h.attempts, h.successes, h.consecutiveFailures, h.latencyMS
			if !h.cooldownUntil.IsZero() {
				deadline := h.cooldownUntil
				item.CooldownUntil = &deadline
			}
		}
		result = append(result, item)
	}
	return result
}

// 取消恢复探测时释放占用，不将调用方取消记为服务失败。
func (a *Aggregator) releaseProbe(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if h := a.health[name]; h != nil {
		h.probing = false
	}
}

// OrderPriority 保持组内配置顺序，免费源优先、百度最后，供配置展示复用。
func OrderPriority(names []string) []string {
	result := append([]string(nil), names...)
	sort.SliceStable(result, func(i, j int) bool { return providerTier(result[i]) < providerTier(result[j]) })
	return result
}
