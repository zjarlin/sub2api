package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type dailyUsageRepoStub struct {
	service.UsageLogRepository
	trend []usagestats.TrendDataPoint

	called      bool
	startTime   time.Time
	endTime     time.Time
	granularity string
	userID      int64
	apiKeyID    int64
	apiKeyIDs   []int64
}

func (s *dailyUsageRepoStub) GetUsageTrendWithFilters(
	ctx context.Context,
	startTime, endTime time.Time,
	granularity string,
	userID, apiKeyID, accountID, groupID int64,
	model string,
	requestType *int16,
	stream *bool,
	billingType *int8,
) ([]usagestats.TrendDataPoint, error) {
	s.called = true
	s.startTime = startTime
	s.endTime = endTime
	s.granularity = granularity
	s.userID = userID
	s.apiKeyID = apiKeyID
	return s.trend, nil
}

func (s *dailyUsageRepoStub) GetBatchAPIKeyUsageStats(ctx context.Context, ids []int64, startTime, endTime time.Time) (map[int64]*usagestats.BatchAPIKeyUsageStats, error) {
	s.called = true
	s.apiKeyIDs = ids
	s.startTime = startTime
	s.endTime = endTime
	return map[int64]*usagestats.BatchAPIKeyUsageStats{
		7: {APIKeyID: 7, MonthActualCost: 12.5, TodayActualCost: 0.5},
	}, nil
}

type dailyUsageAPIKeyRepoStub struct {
	service.APIKeyRepository
	keys map[int64]*service.APIKey
}

func (s *dailyUsageAPIKeyRepoStub) GetByID(ctx context.Context, id int64) (*service.APIKey, error) {
	key, ok := s.keys[id]
	if !ok {
		return nil, service.ErrAPIKeyNotFound
	}
	clone := *key
	return &clone, nil
}

func (s *dailyUsageAPIKeyRepoStub) VerifyOwnership(ctx context.Context, userID int64, ids []int64) ([]int64, error) {
	var owned []int64
	for _, id := range ids {
		if key := s.keys[id]; key != nil && key.UserID == userID {
			owned = append(owned, id)
		}
	}
	return owned, nil
}

func newDailyUsageTestRouter(usageRepo *dailyUsageRepoStub, apiKeyRepo *dailyUsageAPIKeyRepoStub, userID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	usageSvc := service.NewUsageService(usageRepo, nil, nil, nil)
	apiKeySvc := service.NewAPIKeyService(apiKeyRepo, nil, nil, nil, nil, nil, nil)
	handler := NewUsageHandler(usageSvc, apiKeySvc, nil, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})
		c.Next()
	})
	router.GET("/user/api-keys/:id/usage/daily", handler.GetMyAPIKeyDailyUsage)
	router.POST("/usage/dashboard/api-keys-usage", handler.DashboardAPIKeysUsage)
	return router
}

type dailyUsageHandlerResponse struct {
	Code int `json:"code"`
	Data struct {
		Items     []usagestats.APIKeyDailyUsagePoint `json:"items"`
		Days      int                                `json:"days"`
		Period    string                             `json:"period"`
		StartDate string                             `json:"start_date"`
		EndDate   string                             `json:"end_date"`
	} `json:"data"`
}

func TestGetMyAPIKeyDailyUsageRejectsCrossUserAccess(t *testing.T) {
	usageRepo := &dailyUsageRepoStub{}
	apiKeyRepo := &dailyUsageAPIKeyRepoStub{
		keys: map[int64]*service.APIKey{
			7: {ID: 7, UserID: 99, Status: service.StatusAPIKeyActive},
		},
	}
	router := newDailyUsageTestRouter(usageRepo, apiKeyRepo, 42)

	req := httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage/daily?days=30", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.False(t, usageRepo.called)
}

func TestGetMyAPIKeyDailyUsageRejectsInvalidDays(t *testing.T) {
	for _, path := range []string{
		"/user/api-keys/7/usage/daily?days=0",
		"/user/api-keys/7/usage/daily?days=91",
	} {
		t.Run(path, func(t *testing.T) {
			usageRepo := &dailyUsageRepoStub{}
			apiKeyRepo := &dailyUsageAPIKeyRepoStub{
				keys: map[int64]*service.APIKey{
					7: {ID: 7, UserID: 42, Status: service.StatusAPIKeyActive},
				},
			}
			router := newDailyUsageTestRouter(usageRepo, apiKeyRepo, 42)

			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.False(t, usageRepo.called)
		})
	}
}

func TestGetMyAPIKeyDailyUsageRejectsInvalidPeriod(t *testing.T) {
	usageRepo := &dailyUsageRepoStub{}
	apiKeyRepo := &dailyUsageAPIKeyRepoStub{
		keys: map[int64]*service.APIKey{
			7: {ID: 7, UserID: 42, Status: service.StatusAPIKeyActive},
		},
	}
	router := newDailyUsageTestRouter(usageRepo, apiKeyRepo, 42)

	req := httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage/daily?period=week", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.False(t, usageRepo.called)
}

func TestGetMyAPIKeyDailyUsageReturnsEmptyData(t *testing.T) {
	usageRepo := &dailyUsageRepoStub{trend: []usagestats.TrendDataPoint{}}
	apiKeyRepo := &dailyUsageAPIKeyRepoStub{
		keys: map[int64]*service.APIKey{
			7: {ID: 7, UserID: 42, Status: service.StatusAPIKeyActive},
		},
	}
	router := newDailyUsageTestRouter(usageRepo, apiKeyRepo, 42)

	req := httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage/daily", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got dailyUsageHandlerResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, 30, got.Data.Days)
	require.Equal(t, apiKeyDailyUsagePeriodDays, got.Data.Period)
	require.Empty(t, got.Data.Items)
}

func TestGetMyAPIKeyDailyUsageReturnsCurrentMonthRange(t *testing.T) {
	usageRepo := &dailyUsageRepoStub{trend: []usagestats.TrendDataPoint{}}
	apiKeyRepo := &dailyUsageAPIKeyRepoStub{
		keys: map[int64]*service.APIKey{
			7: {ID: 7, UserID: 42, Status: service.StatusAPIKeyActive},
		},
	}
	router := newDailyUsageTestRouter(usageRepo, apiKeyRepo, 42)

	req := httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage/daily?period=month&timezone=Asia%2FShanghai", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, usageRepo.called)
	require.Equal(t, 1, usageRepo.startTime.Day())
	require.Equal(t, 0, usageRepo.startTime.Hour())
	require.Equal(t, usageRepo.startTime.Location(), usageRepo.endTime.Location())

	var got dailyUsageHandlerResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, apiKeyDailyUsagePeriodMonth, got.Data.Period)
	require.Equal(t, got.Data.Days, usageRepo.endTime.AddDate(0, 0, -1).Day())
	require.Equal(t, usageRepo.startTime.Format("2006-01-02"), got.Data.StartDate)
	require.Equal(t, usageRepo.endTime.AddDate(0, 0, -1).Format("2006-01-02"), got.Data.EndDate)
}

func TestGetMyAPIKeyDailyUsageAggregatesByDayForOwnedKey(t *testing.T) {
	usageRepo := &dailyUsageRepoStub{
		trend: []usagestats.TrendDataPoint{
			{
				Date:                "2026-05-19",
				Requests:            3,
				InputTokens:         10,
				OutputTokens:        20,
				CacheCreationTokens: 4,
				CacheReadTokens:     6,
				TotalTokens:         40,
				Cost:                0.5,
				ActualCost:          0.4,
			},
		},
	}
	apiKeyRepo := &dailyUsageAPIKeyRepoStub{
		keys: map[int64]*service.APIKey{
			7: {ID: 7, UserID: 42, Status: service.StatusAPIKeyActive},
		},
	}
	router := newDailyUsageTestRouter(usageRepo, apiKeyRepo, 42)

	req := httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage/daily?days=7", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, usageRepo.called)
	require.Equal(t, "day", usageRepo.granularity)
	require.Equal(t, int64(42), usageRepo.userID)
	require.Equal(t, int64(7), usageRepo.apiKeyID)
	require.True(t, usageRepo.startTime.Before(usageRepo.endTime))

	var got dailyUsageHandlerResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, 7, got.Data.Days)
	require.Equal(t, apiKeyDailyUsagePeriodDays, got.Data.Period)
	require.Len(t, got.Data.Items, 1)
	require.Equal(t, usagestats.APIKeyDailyUsagePoint{
		Date:             "2026-05-19",
		Requests:         3,
		InputTokens:      10,
		OutputTokens:     20,
		CacheReadTokens:  6,
		CacheWriteTokens: 4,
		TotalTokens:      40,
		Cost:             0.5,
		ActualCost:       0.4,
	}, got.Data.Items[0])
}

func TestGetMyAPIKeyDailyUsageSelectedMonth(t *testing.T) {
	for _, tc := range []struct {
		month, zone, end string
		days             int
	}{
		{"2024-02", "Asia/Shanghai", "2024-02-29", 29},
		{"2025-02", "Asia/Shanghai", "2025-02-28", 28},
		{"2024-12", "UTC", "2024-12-31", 31},
		{"2024-03", "America/New_York", "2024-03-31", 31},
	} {
		t.Run(tc.month+tc.zone, func(t *testing.T) {
			usageRepo := &dailyUsageRepoStub{}
			apiKeyRepo := &dailyUsageAPIKeyRepoStub{keys: map[int64]*service.APIKey{7: {ID: 7, UserID: 42}}}
			router := newDailyUsageTestRouter(usageRepo, apiKeyRepo, 42)
			req := httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage/daily?month="+tc.month+"&timezone="+url.QueryEscape(tc.zone), nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
			var got dailyUsageHandlerResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			require.Equal(t, tc.month+"-01", got.Data.StartDate)
			require.Equal(t, tc.end, got.Data.EndDate)
			require.Equal(t, tc.days, got.Data.Days)
			require.Equal(t, "month", got.Data.Period)
			require.Equal(t, tc.zone, usageRepo.startTime.Location().String())
			require.Equal(t, 1, usageRepo.endTime.Day())
			require.Equal(t, 0, usageRepo.endTime.Hour())
		})
	}
}

func TestAPIKeyUsageRejectsInvalidMonth(t *testing.T) {
	for _, month := range []string{"2024-2", "2024-13", "2024-00", "2024-02-01", "0000-01", "invalid", "9999-01"} {
		t.Run(month, func(t *testing.T) {
			usageRepo := &dailyUsageRepoStub{}
			apiKeyRepo := &dailyUsageAPIKeyRepoStub{keys: map[int64]*service.APIKey{7: {ID: 7, UserID: 42}}}
			router := newDailyUsageTestRouter(usageRepo, apiKeyRepo, 42)
			body, err := json.Marshal(BatchAPIKeysUsageRequest{APIKeyIDs: []int64{7}, Month: month})
			require.NoError(t, err)
			requests := []*http.Request{
				httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage/daily?month="+month, nil),
				httptest.NewRequest(http.MethodPost, "/usage/dashboard/api-keys-usage", bytes.NewReader(body)),
			}
			for _, req := range requests {
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.False(t, usageRepo.called)
			}
		})
	}
}

func TestDashboardAPIKeysUsageMatchesDailyMonthAndFiltersOwnership(t *testing.T) {
	usageRepo := &dailyUsageRepoStub{}
	apiKeyRepo := &dailyUsageAPIKeyRepoStub{keys: map[int64]*service.APIKey{
		7: {ID: 7, UserID: 42},
		8: {ID: 8, UserID: 99},
	}}
	router := newDailyUsageTestRouter(usageRepo, apiKeyRepo, 42)
	for _, month := range []string{"2024-02", time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01"), ""} {
		t.Run(month, func(t *testing.T) {
			daily := httptest.NewRecorder()
			router.ServeHTTP(daily, httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage/daily?period=month&month="+month+"&timezone=Asia%2FShanghai", nil))
			require.Equal(t, http.StatusOK, daily.Code)
			start, end := usageRepo.startTime, usageRepo.endTime
			body, err := json.Marshal(BatchAPIKeysUsageRequest{APIKeyIDs: []int64{7, 8, 999}, Month: month, Timezone: "Asia/Shanghai"})
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/usage/dashboard/api-keys-usage", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, []int64{7}, usageRepo.apiKeyIDs)
			require.Equal(t, start, usageRepo.startTime)
			require.Equal(t, end, usageRepo.endTime)
			require.JSONEq(t, `{"code":0,"message":"success","data":{"stats":{"7":{"api_key_id":7,"today_actual_cost":0.5,"month_actual_cost":12.5,"total_actual_cost":0}}}}`, rec.Body.String())
		})
	}
}
