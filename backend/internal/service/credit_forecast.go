package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const creditForecastExtraKey = "credit_alert_forecast"

// 没有消耗趋势时使用有限哨兵值，避免 JSON 中出现无穷大。
const creditRunwayUnknown = 1e9

// CreditForecast 保存同一来源、同一单位的采样，重启和集群换主后继续使用。
type CreditForecast struct {
	Balance       float64   `json:"balance"`
	At            time.Time `json:"at"`
	Rate          float64   `json:"rate"`
	Acceleration  float64   `json:"acceleration"`
	IntervalHours float64   `json:"interval_hours"`
	Samples       int       `json:"samples"`
}

// observeCredit 不把充值、签到增长或跨很久的旧数据当成负消耗。
func observeCredit(previous CreditForecast, balance float64, now time.Time) CreditForecast {
	next := CreditForecast{Balance: balance, At: now, Samples: 1}
	dt := now.Sub(previous.At).Hours()
	if previous.At.IsZero() || dt <= 0 || dt > 24 || balance > previous.Balance {
		return next
	}
	next.Samples = previous.Samples + 1
	next.IntervalHours = dt
	next.Rate = (previous.Balance - balance) / dt
	if previous.Samples >= 2 && previous.IntervalHours > 0 {
		// 相邻消耗速率位于各自采样区间的中点，非等距采样也按真实时差计算。
		next.Acceleration = (next.Rate - previous.Rate) / ((dt + previous.IntervalHours) / 2)
	}
	return next
}

func (f CreditForecast) runwayHours() float64 {
	if f.Balance <= 0 {
		return 0
	}
	if f.Rate <= 0 {
		return creditRunwayUnknown
	}
	// 减速不用于放宽告警；加速时解 B = vt + at²/2，使用数值稳定的正根。
	a := math.Max(0, f.Acceleration)
	if a == 0 {
		return math.Min(creditRunwayUnknown, f.Balance/f.Rate)
	}
	// 区间平均速度位于中点，先外推到最后采样时刻再预测耗尽。
	rate := f.Rate + a*f.IntervalHours/2
	return math.Min(creditRunwayUnknown, 2*f.Balance/(rate+math.Sqrt(rate*rate+2*a*f.Balance)))
}

func (f CreditForecast) nextInterval() time.Duration {
	if f.Balance <= 0 {
		return 15 * time.Minute
	}
	if f.Samples < 2 {
		return time.Hour
	}
	if f.Rate == 0 {
		if f.Samples < 3 {
			return 2 * time.Hour
		}
		return 6 * time.Hour
	}
	hours := math.Max(0.25, math.Min(6, f.runwayHours()/8))
	return time.Duration(hours * float64(time.Hour))
}

type creditForecastState struct {
	Source    string                    `json:"source"`
	NextCheck time.Time                 `json:"next_check"`
	Failed    bool                      `json:"failed"`
	Units     map[string]CreditForecast `json:"units"`
}

type creditForecastAccountRepo interface {
	ListByPlatform(context.Context, string) ([]Account, error)
	UpdateExtra(context.Context, int64, map[string]any) error
}

// CreditForecastMonitor 复用账号余额查询和运维告警，只在到期时访问上游。
type CreditForecastMonitor struct {
	accounts creditForecastAccountRepo
	balances monitorCNBalanceSource
	fetch    func(context.Context, *Account) (map[string]float64, error)
	// 同轮多个严重级别共享采样，数据库失败时也不会在一分钟内重复请求。
	cache   map[string]creditForecastState
	details string
}

func NewCreditForecastMonitor(accounts AccountRepository, balances *CNProviderBalanceService) *CreditForecastMonitor {
	m := &CreditForecastMonitor{accounts: accounts, balances: balances, cache: make(map[string]creditForecastState)}
	m.fetch = m.fetchBalances
	return m
}

func (m *CreditForecastMonitor) fetchBalances(ctx context.Context, account *Account) (map[string]float64, error) {
	switch account.Platform {
	case PlatformWorkbuddy, PlatformTraework:
		uid := strings.TrimSpace(account.GetExtraString("builtin_adapter_uid"))
		base, key := builtinAdapterBaseURL(account.Platform), builtinAdapterAPIKey(account.Platform)
		if uid == "" || base == "" || key == "" {
			return nil, fmt.Errorf("积分账号未绑定或适配器未配置")
		}
		endpoint := strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1") + "/status?refresh_credits=true&uid=" + url.QueryEscape(uid)
		var status struct {
			Refreshed bool `json:"credits_refreshed"`
			Accounts  []struct {
				UID     string   `json:"uid"`
				Credits *float64 `json:"credits"`
			} `json:"accounts"`
		}
		if err := fetchBuiltinAdapterJSON(ctx, endpoint, key, &status); err != nil {
			return nil, err
		}
		if !status.Refreshed {
			return nil, fmt.Errorf("适配器未确认实时刷新积分，需要更新适配器")
		}
		for _, item := range status.Accounts {
			if item.UID == uid && item.Credits != nil {
				return map[string]float64{"credits": *item.Credits}, nil
			}
		}
		return nil, fmt.Errorf("上游未返回绑定账号的积分")
	case PlatformKimi, PlatformDeepseek:
		if m.balances == nil {
			return nil, fmt.Errorf("余额查询未配置")
		}
		result, err := m.balances.QueryBalanceForAccount(ctx, account)
		if err != nil {
			return nil, err
		}
		if result == nil || !result.Success {
			return nil, fmt.Errorf("上游余额查询失败")
		}
		values := make(map[string]float64)
		for _, entry := range result.Balances {
			values[entry.Currency] = entry.Balance
		}
		if len(values) == 0 {
			values[result.Currency] = result.Balance
		}
		return values, nil
	}
	return nil, fmt.Errorf("平台不支持积分或余额查询")
}

func readCreditForecast(account *Account) creditForecastState {
	var state creditForecastState
	raw, err := json.Marshal(account.Extra[creditForecastExtraKey])
	if err == nil {
		_ = json.Unmarshal(raw, &state)
	}
	return state
}

func creditForecastSource(account *Account) string {
	return account.Platform + ":" + account.GetBaseURL() + ":" + builtinAdapterBaseURL(account.Platform) + ":" + account.GetExtraString("builtin_adapter_uid")
}

func (m *CreditForecastMonitor) sample(ctx context.Context, account *Account, now time.Time) (creditForecastState, bool) {
	source := creditForecastSource(account)
	cacheKey := fmt.Sprintf("%d:%s", account.ID, source)
	if account.Platform == PlatformWorkbuddy || account.Platform == PlatformTraework {
		cacheKey = source
	}
	state, ok := m.cache[cacheKey]
	if !ok || state.Source != source {
		state = readCreditForecast(account)
	}
	if state.Source != source {
		state = creditForecastState{Source: source}
	}
	if now.Before(state.NextCheck) {
		return state, !state.Failed && len(state.Units) > 0
	}
	if ctx.Err() != nil {
		return state, false
	}
	values, err := m.fetch(ctx, account)
	if err == nil && len(values) == 0 {
		err = fmt.Errorf("上游未返回余额单位")
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			err = fmt.Errorf("上游余额数值无效")
		}
	}
	state.NextCheck = now.Add(time.Hour)
	state.Failed = err != nil
	if err != nil {
		logger.LegacyPrintf("service.credit_forecast", "[CreditForecast] account=%d: %v", account.ID, err)
	} else {
		units := make(map[string]CreditForecast, len(values))
		interval := time.Duration(0)
		for unit, balance := range values {
			forecast := observeCredit(state.Units[unit], balance, now)
			units[unit] = forecast
			if candidate := forecast.nextInterval(); candidate > interval {
				interval = candidate
			}
		}
		state.Units = units
		state.NextCheck = now.Add(interval)
	}
	m.cache[cacheKey] = state
	if err := m.accounts.UpdateExtra(ctx, account.ID, map[string]any{creditForecastExtraKey: state}); err != nil {
		logger.LegacyPrintf("service.credit_forecast", "[CreditForecast] persist account=%d: %v", account.ID, err)
		return state, false
	}
	return state, !state.Failed
}

// Runway 返回最紧张账号的预计小时数；多币种不相加，任一币种可用就不判为耗尽。
func (m *CreditForecastMonitor) Runway(ctx context.Context, platform string, groupID *int64, now time.Time) (float64, bool) {
	if m == nil || m.accounts == nil {
		return 0, false
	}
	m.details = ""
	platforms := []string{PlatformWorkbuddy, PlatformTraework, PlatformKimi, PlatformDeepseek}
	if platform != "" {
		if !slices.Contains(platforms, platform) {
			return 0, false
		}
		platforms = []string{platform}
	}
	minimum, complete := creditRunwayUnknown, true
	for _, p := range platforms {
		accounts, err := m.accounts.ListByPlatform(ctx, p)
		if err != nil {
			return 0, false
		}
		for i := range accounts {
			a := &accounts[i]
			if !a.IsActive() || a.IsCodingPlan() || IsOllamaCloudUsageAccount(a) {
				continue
			}
			if groupID != nil && !slices.Contains(a.GroupIDs, *groupID) {
				continue
			}
			if (p == PlatformWorkbuddy || p == PlatformTraework) && a.GetExtraString("builtin_adapter_uid") == "" {
				continue
			}
			state, ok := m.sample(ctx, a, now)
			if !ok {
				complete = false
				continue
			}
			runway := 0.0
			detail := ""
			for unit, f := range state.Units {
				hours := f.runwayHours()
				if hours < creditRunwayUnknown {
					hours = math.Max(0, hours-now.Sub(f.At).Hours())
				}
				if hours >= runway {
					runway = hours
					detail = fmt.Sprintf("账号 #%d（%s），余额 %.4g %s，消耗 %.4g/小时，加速度 %.4g/小时²，采样 %s，下次查询 %s", a.ID, p, f.Balance, unit, f.Rate, f.Acceleration, f.At.UTC().Format(time.RFC3339), state.NextCheck.UTC().Format(time.RFC3339))
				}
			}
			if runway < minimum {
				minimum, m.details = runway, detail
			}
		}
	}
	// 查询失败时不把不完整结果当成恢复，也不把失败当零余额。
	return minimum, complete
}
