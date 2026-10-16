package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCreditForecastRateAccelerationAndRecharge(t *testing.T) {
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	first := observeCredit(CreditForecast{}, 1000, now)
	require.Equal(t, creditRunwayUnknown, first.runwayHours())
	require.Equal(t, time.Hour, first.nextInterval())
	second := observeCredit(first, 900, now.Add(time.Hour))
	require.InDelta(t, 9, second.runwayHours(), 1e-9)
	// 第三个采样与第二个相距两小时：区间速度 100→200，加速度按中点距离 1.5h 计算。
	third := observeCredit(second, 500, now.Add(3*time.Hour))
	require.InDelta(t, 200, third.Rate, 1e-9)
	require.InDelta(t, 100/1.5, third.Acceleration, 1e-9)
	require.Less(t, third.runwayHours(), 500.0/200)
	runway := third.runwayHours()
	require.InDelta(t, third.Balance, (third.Rate+third.Acceleration*third.IntervalHours/2)*runway+third.Acceleration*runway*runway/2, 1e-8)
	require.GreaterOrEqual(t, third.nextInterval(), 15*time.Minute)
	recharged := observeCredit(third, 1200, now.Add(4*time.Hour))
	require.Equal(t, 1, recharged.Samples)
	require.Zero(t, recharged.Rate)
	require.Zero(t, recharged.Acceleration)
	require.Equal(t, creditRunwayUnknown, recharged.runwayHours())
	require.Equal(t, 15*time.Minute, (CreditForecast{Balance: 0}).nextInterval())
	idle := observeCredit(observeCredit(first, 1000, now.Add(time.Hour)), 1000, now.Add(3*time.Hour))
	require.Equal(t, 6*time.Hour, idle.nextInterval())
	require.Equal(t, 1, observeCredit(third, 400, now.Add(48*time.Hour)).Samples)
}

type creditTestRepo struct {
	accounts []Account
	updates  int
}

func (r *creditTestRepo) ListByPlatform(_ context.Context, platform string) ([]Account, error) {
	var result []Account
	for _, a := range r.accounts {
		if a.Platform == platform {
			result = append(result, a)
		}
	}
	return result, nil
}
func (r *creditTestRepo) UpdateExtra(_ context.Context, id int64, extra map[string]any) error {
	r.updates++
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			if r.accounts[i].Extra == nil {
				r.accounts[i].Extra = make(map[string]any)
			}
			r.accounts[i].Extra[creditForecastExtraKey] = extra[creditForecastExtraKey]
		}
	}
	return nil
}

func TestCreditForecastPersistsAndBacksOffFailures(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	repo := &creditTestRepo{accounts: []Account{{ID: 1, Platform: PlatformDeepseek, Status: StatusActive}}}
	calls := 0
	fail := false
	m := &CreditForecastMonitor{accounts: repo, cache: make(map[string]creditForecastState)}
	m.fetch = func(context.Context, *Account) (map[string]float64, error) {
		calls++
		if fail {
			return nil, errors.New("unavailable")
		}
		return map[string]float64{"CNY": 100 - float64(calls)*10, "USD": 0}, nil
	}
	value, ok := m.Runway(ctx, PlatformDeepseek, nil, now)
	require.True(t, ok)
	require.Equal(t, creditRunwayUnknown, value)
	_, ok = m.Runway(ctx, PlatformDeepseek, nil, now.Add(time.Minute))
	require.True(t, ok)
	require.Equal(t, 1, calls)
	// 模拟重启，丢掉内存缓存后仍遵守数据库中保存的下次采样时间。
	clear(m.cache)
	_, ok = m.Runway(ctx, PlatformDeepseek, nil, now.Add(2*time.Minute))
	require.True(t, ok)
	require.Equal(t, 1, calls)
	value, ok = m.Runway(ctx, PlatformDeepseek, nil, now.Add(time.Hour))
	require.True(t, ok)
	require.InDelta(t, 8, value, 1e-9)
	require.Contains(t, m.details, "CNY")
	require.Contains(t, m.details, "账号 #1")
	fail = true
	_, ok = m.Runway(ctx, PlatformDeepseek, nil, now.Add(2*time.Hour))
	require.False(t, ok)
	require.Equal(t, 3, calls)
	_, ok = m.Runway(ctx, PlatformDeepseek, nil, now.Add(2*time.Hour+time.Minute))
	require.False(t, ok)
	require.Equal(t, 3, calls)
	// 恢复查询后才解除未知状态，不能把网络错误当成余额耗尽或恢复。
	fail = false
	_, ok = m.Runway(ctx, PlatformDeepseek, nil, now.Add(3*time.Hour))
	require.True(t, ok)
	require.Equal(t, 4, calls)
}

func TestCreditForecastSkipsUnboundAndRespectsGroup(t *testing.T) {
	repo := &creditTestRepo{accounts: []Account{
		{ID: 1, Platform: PlatformWorkbuddy, Status: StatusActive},
		{ID: 2, Platform: PlatformWorkbuddy, Status: StatusActive, GroupIDs: []int64{2}, Extra: map[string]any{"builtin_adapter_uid": "u2"}},
		{ID: 3, Platform: PlatformWorkbuddy, Status: StatusActive, GroupIDs: []int64{3}, Extra: map[string]any{"builtin_adapter_uid": "u3"}},
	}}
	m := &CreditForecastMonitor{accounts: repo, cache: make(map[string]creditForecastState)}
	m.fetch = func(_ context.Context, a *Account) (map[string]float64, error) {
		require.Equal(t, int64(2), a.ID)
		return map[string]float64{"credits": 0}, nil
	}
	groupID := int64(2)
	value, ok := m.Runway(context.Background(), PlatformWorkbuddy, &groupID, time.Now())
	require.True(t, ok)
	require.Zero(t, value)
	require.Equal(t, 1, repo.updates)
}

func TestCreditForecastSharesBoundUIDAndPreservesOtherCurrency(t *testing.T) {
	repo := &creditTestRepo{accounts: []Account{
		{ID: 1, Platform: PlatformWorkbuddy, Status: StatusActive, Extra: map[string]any{"builtin_adapter_uid": "shared"}},
		{ID: 2, Platform: PlatformWorkbuddy, Status: StatusActive, Extra: map[string]any{"builtin_adapter_uid": "shared"}},
	}}
	calls := 0
	m := &CreditForecastMonitor{accounts: repo, cache: make(map[string]creditForecastState)}
	m.fetch = func(context.Context, *Account) (map[string]float64, error) {
		calls++
		return map[string]float64{"credits": 100}, nil
	}
	_, ok := m.Runway(context.Background(), PlatformWorkbuddy, nil, time.Now())
	require.True(t, ok)
	require.Equal(t, 1, calls)
	// 多条路由账号关联同一上游时共用一次实时查询。
	require.WithinDuration(t, time.Now().Add(time.Hour), m.cache[creditForecastSource(&repo.accounts[0])].NextCheck, time.Second)
}

func TestCreditForecastRequiresFreshBoundBalance(t *testing.T) {
	for _, platform := range []string{PlatformWorkbuddy, PlatformTraework} {
		t.Run(platform, func(t *testing.T) {
			body := `{"credits_refreshed":true,"accounts":[{"uid":"bound","credits":79.75},{"uid":"other","credits":9999}]}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/status", r.URL.Path)
				require.Equal(t, "bound", r.URL.Query().Get("uid"))
				require.Equal(t, "true", r.URL.Query().Get("refresh_credits"))
				require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, WorkbuddyURL: server.URL + "/v1", WorkbuddyKey: "test-key", TraeworkURL: server.URL + "/v1", TraeworkKey: "test-key"})
			t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
			m := &CreditForecastMonitor{}
			a := &Account{Platform: platform, Extra: map[string]any{"builtin_adapter_uid": "bound"}}
			values, err := m.fetchBalances(context.Background(), a)
			require.NoError(t, err)
			require.Equal(t, map[string]float64{"credits": 79.75}, values)
			for _, invalid := range []string{
				`{"accounts":[{"uid":"bound","credits":79.75}]}`,
				`{"credits_refreshed":true,"accounts":[{"uid":"bound"}]}`,
				`{"credits_refreshed":true,"accounts":[{"uid":"other","credits":9999}]}`,
			} {
				body = invalid
				_, err = m.fetchBalances(context.Background(), a)
				require.Error(t, err)
			}
		})
	}
}

func TestCreditForecastEmptyCurrencyDoesNotForceFrequentChecks(t *testing.T) {
	repo := &creditTestRepo{accounts: []Account{{ID: 1, Platform: PlatformDeepseek, Status: StatusActive}}}
	m := &CreditForecastMonitor{accounts: repo, cache: make(map[string]creditForecastState)}
	m.fetch = func(context.Context, *Account) (map[string]float64, error) {
		return map[string]float64{"CNY": 100, "USD": 0}, nil
	}
	now := time.Now()
	value, ok := m.Runway(context.Background(), PlatformDeepseek, nil, now)
	require.True(t, ok)
	require.Equal(t, creditRunwayUnknown, value)
	state := readCreditForecast(&repo.accounts[0])
	require.WithinDuration(t, now.Add(time.Hour), state.NextCheck, 0)
}

func TestCreditForecastKnownExhaustionStillAlertsWithUnknownAccounts(t *testing.T) {
	repo := &creditTestRepo{accounts: []Account{
		{ID: 1, Platform: PlatformDeepseek, Status: StatusActive},
		{ID: 2, Platform: PlatformDeepseek, Status: StatusActive},
	}}
	m := &CreditForecastMonitor{accounts: repo, cache: make(map[string]creditForecastState)}
	m.fetch = func(_ context.Context, a *Account) (map[string]float64, error) {
		if a.ID == 2 {
			return nil, errors.New("upstream unavailable")
		}
		return map[string]float64{"CNY": 0}, nil
	}
	svc := &OpsAlertEvaluatorService{creditMonitor: m}
	rule := &OpsAlertRule{MetricType: "credit_runway_hours", Operator: "<=", Threshold: 6}
	value, ok := svc.computeRuleMetric(context.Background(), rule, nil, time.Time{}, time.Time{}, PlatformDeepseek, nil)
	require.True(t, ok)
	require.Zero(t, value)
	require.Contains(t, svc.alertDescription(rule, value, 1, PlatformDeepseek, nil), "账号 #1")
	// 只有未知账号时不可自动解除已存在的告警。
	repo.accounts = repo.accounts[1:]
	_, ok = svc.computeRuleMetric(context.Background(), rule, nil, time.Time{}, time.Time{}, PlatformDeepseek, nil)
	require.False(t, ok)
}
