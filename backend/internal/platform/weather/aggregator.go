package weather

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

// ErrProviderUnavailable 表示指定数据源未注册。
var ErrProviderUnavailable = errors.New("weather: provider is not available")

// defaultPriority 是内置免密钥源的默认尝试顺序：先全球通用源，再用国内源补历史。
var defaultPriority = []string{"openmeteo", "wttr", "chinasky", "tianqi2345"}

// attemptBudget 是单个候选的最长等待时间，为后续候选留出回退余量。
const attemptBudget = 10 * time.Second

// Aggregator 按优先级调用多个 Provider，失败自动回退。
type Aggregator struct {
	providers []Provider
	tracker   *healthTracker
	enabled   bool
}

// NewAggregator 按配置注册内置免密钥数据源。
func NewAggregator(cfg *Config) *Aggregator {
	if cfg == nil {
		cfg = &Config{EnableFreeProviders: true}
	}
	aggregator := &Aggregator{tracker: newHealthTracker()}
	if !cfg.EnableFreeProviders {
		return aggregator
	}
	aggregator.enabled = true
	registered := []Provider{
		NewOpenMeteoProvider(),
		NewWttrProvider(),
		NewChinaSkyProvider(),
		NewTianqi2345Provider(),
	}
	rank := make(map[string]int, len(cfg.Priority))
	for index, name := range cfg.Priority {
		rank[name] = index
	}
	priority := func(name string) int {
		if value, ok := rank[name]; ok {
			return value
		}
		return len(cfg.Priority) + indexOf(defaultPriority, name)
	}
	sort.SliceStable(registered, func(i, j int) bool { return priority(registered[i].Name()) < priority(registered[j].Name()) })
	for _, provider := range registered {
		log.Printf("[weather] registered provider: %s", provider.Name())
	}
	aggregator.providers = registered
	return aggregator
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return len(values)
}

// ChainError 表示所有候选都失败或被跳过，携带逐候选状态便于客户端展开。
type ChainError struct {
	Attempts []Attempt
}

func (e *ChainError) Error() string {
	return fmt.Sprintf("weather: no provider completed the request (%d attempts)", len(e.Attempts))
}

// ProviderInfo 描述数据源能力与当前健康分。
type ProviderInfo struct {
	Name     string  `json:"name"`
	Current  bool    `json:"current"`
	Forecast bool    `json:"forecast"`
	History  bool    `json:"history"`
	Score    float64 `json:"score"`
}

// AvailableProviders 返回按当前健康分排序的数据源名称。
func (a *Aggregator) AvailableProviders() []string {
	infos := a.Providers()
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		names = append(names, info.Name)
	}
	return names
}

// Providers 返回数据源能力与健康分，用于发现端点。
func (a *Aggregator) Providers() []ProviderInfo {
	infos := make([]ProviderInfo, 0, len(a.providers))
	for _, provider := range a.providers {
		capabilities := provider.Capabilities()
		score := a.tracker.get(provider.Name()).score()
		infos = append(infos, ProviderInfo{Name: provider.Name(), Current: capabilities.Current, Forecast: capabilities.Forecast, History: capabilities.History, Score: score})
	}
	return infos
}

// Enabled 表示是否有可用数据源。
func (a *Aggregator) Enabled() bool { return a.enabled && len(a.providers) > 0 }

// Fetch 按优先级尝试各数据源，首个成功即返回。
func (a *Aggregator) Fetch(ctx context.Context, req *WeatherRequest) (*WeatherResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("weather: request is required")
	}
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	candidates, err := a.candidates(req)
	if err != nil {
		return nil, err
	}
	attempts := make([]Attempt, 0, len(candidates))
	now := time.Now()
	for _, provider := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if req.Provider == "" && a.tracker.get(provider.Name()).cooling(now) {
			attempts = append(attempts, Attempt{Provider: provider.Name(), Status: "cooldown", Reason: "temporarily unavailable after previous failures; recovery pending"})
			continue
		}
		started := time.Now()
		attemptCtx, cancel := context.WithTimeout(ctx, budgetWithin(ctx))
		response, fetchErr := provider.Fetch(attemptCtx, req)
		cancel()
		elapsed := time.Since(started)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(fetchErr, ErrLocationUnsupported) {
			attempts = append(attempts, Attempt{Provider: provider.Name(), Status: "skipped", Reason: "this provider does not accept the requested location form"})
			continue
		}
		if fetchErr == nil {
			fetchErr = validateResponse(req, response)
		}
		score := a.tracker.record(provider.Name(), elapsed, fetchErr == nil, time.Now())
		status := "success"
		if fetchErr != nil {
			status = "failed"
		}
		attempts = append(attempts, Attempt{Provider: provider.Name(), Status: status, LatencyMS: elapsed.Milliseconds(), Score: score, Reason: failureReason(fetchErr, req.Location)})
		if fetchErr != nil {
			log.Printf("[weather] provider=%s status=failed latency_ms=%d score=%.1f", provider.Name(), elapsed.Milliseconds(), score)
			continue
		}
		response.Provider = provider.Name()
		response.Attempts = attempts
		return response, nil
	}
	return nil, &ChainError{Attempts: attempts}
}

func budgetWithin(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < attemptBudget {
			return remaining
		}
	}
	return attemptBudget
}

// candidates 返回满足请求能力的数据源；指定 Provider 时只返回该源。
func (a *Aggregator) candidates(req *WeatherRequest) ([]Provider, error) {
	if req.Provider != "" {
		for _, provider := range a.providers {
			if provider.Name() == req.Provider {
				return []Provider{provider}, nil
			}
		}
		return nil, ErrProviderUnavailable
	}
	candidates := make([]Provider, 0, len(a.providers))
	for _, provider := range a.providers {
		capabilities := provider.Capabilities()
		if req.History {
			if !capabilities.History {
				continue
			}
			// 历史查询同时要求当前或预报能力，避免只取历史时其它字段为空。
			if req.Days > 0 && !capabilities.Forecast && !capabilities.Current {
				continue
			}
		} else if !capabilities.Current && !capabilities.Forecast {
			continue
		}
		candidates = append(candidates, provider)
	}
	if len(candidates) == 0 {
		return nil, ErrProviderUnavailable
	}
	// 高健康分优先；分数相同时保留配置顺序。
	tracker := a.tracker
	sort.SliceStable(candidates, func(i, j int) bool {
		return tracker.get(candidates[i].Name()).score() > tracker.get(candidates[j].Name()).score()
	})
	return candidates, nil
}

func validateRequest(req *WeatherRequest) error {
	if trimmed(req.Location) == "" && (req.Latitude == 0 || req.Longitude == 0) {
		return fmt.Errorf("weather: location is required")
	}
	if req.Days < 0 || req.Days > 16 {
		return fmt.Errorf("weather: days must be between 0 and 16")
	}
	if req.History && (trimmed(req.Start) == "" || trimmed(req.End) == "") {
		// 未指定区间时允许数据源回退到默认区间。
		return nil
	}
	if req.Start != "" {
		if _, err := time.Parse("2006-01-02", trimmed(req.Start)); err != nil {
			return fmt.Errorf("weather: start must be YYYY-MM-DD")
		}
	}
	if req.End != "" {
		if _, err := time.Parse("2006-01-02", trimmed(req.End)); err != nil {
			return fmt.Errorf("weather: end must be YYYY-MM-DD")
		}
	}
	return nil
}

// validateResponse 拒绝空响应，保证回退链不会把空数据当成功。
func validateResponse(req *WeatherRequest, response *WeatherResponse) error {
	if response == nil {
		return fmt.Errorf("weather: provider returned no response")
	}
	hasData := response.Current != nil || len(response.Forecast) > 0 || len(response.History) > 0
	if !hasData {
		return fmt.Errorf("weather: provider returned no weather data")
	}
	if req.History && len(response.History) == 0 && response.Current == nil && len(response.Forecast) == 0 {
		return fmt.Errorf("weather: provider returned no history rows")
	}
	return nil
}

// failureReason 去掉 URL 与凭据，只保留有限长度的类别信息。
func failureReason(err error, location string) string {
	if err == nil {
		return ""
	}
	var requestError interface{ Unwrap() error }
	reason := err.Error()
	if errors.As(err, &requestError) && requestError.Unwrap() != nil {
		reason = requestError.Unwrap().Error()
	}
	if location != "" {
		reason = strings.ReplaceAll(reason, location, "[location redacted]")
	}
	runes := []rune(reason)
	if len(runes) > 300 {
		return string(runes[:300]) + "…"
	}
	return reason
}
