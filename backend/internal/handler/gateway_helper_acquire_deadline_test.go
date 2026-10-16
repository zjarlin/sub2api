package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 阻塞发生在 Redis 抢槽调用内，而不是外层等待定时器上。
type blockingSlotCache struct {
	helperConcurrencyCacheStub
	calls       int
	lateSuccess bool
	acquireErr  error
}

func (s *blockingSlotCache) AcquireAccountSlot(ctx context.Context, _ int64, _ int, _ string) (bool, error) {
	s.calls++
	<-ctx.Done()
	if s.lateSuccess {
		return true, nil
	}
	if s.acquireErr != nil {
		return false, s.acquireErr
	}
	return false, ctx.Err()
}

func (s *blockingSlotCache) AcquireUserSlot(ctx context.Context, id int64, maxConcurrency int, requestID string) (bool, error) {
	return s.AcquireAccountSlot(ctx, id, maxConcurrency, requestID)
}

func TestWaitForSlotDeadlineDuringAcquire(t *testing.T) {
	for _, immediate := range []bool{true, false} {
		t.Run(map[bool]string{true: "immediate", false: "poll"}[immediate], func(t *testing.T) {
			cache := &blockingSlotCache{}
			helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, time.Second)
			c, _ := newHelperTestContext(http.MethodPost, "/v1/responses")
			started := false
			release, err := helper.waitForSlotWithPingTimeout(c, "account", 42, 1, time.Second, false, &started, immediate)
			require.Nil(t, release)
			var limit *ConcurrencyError
			require.ErrorAs(t, err, &limit)
			require.True(t, limit.IsTimeout)
			require.Equal(t, "account", limit.SlotType)
			require.Equal(t, 1, cache.calls)
		})
	}
}

func TestWaitForSlotParentDeadlineDuringAcquire(t *testing.T) {
	cache := &blockingSlotCache{}
	helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, time.Second)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/responses")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Millisecond)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	started := false
	_, err := helper.waitForSlotWithPingTimeout(c, "account", 42, 1, time.Second, false, &started, true)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	var limit *ConcurrencyError
	require.NotErrorAs(t, err, &limit)
}

func TestWaitForSlotLateSuccessReleasesCapacity(t *testing.T) {
	for _, slotType := range []string{"account", "user"} {
		t.Run(slotType, func(t *testing.T) {
			cache := &blockingSlotCache{lateSuccess: true}
			helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, time.Second)
			c, _ := newHelperTestContext(http.MethodPost, "/v1/responses")
			started := false
			release, err := helper.waitForSlotWithPingTimeout(c, slotType, 42, 1, 20*time.Millisecond, false, &started, true)
			require.Nil(t, release)
			var limit *ConcurrencyError
			require.ErrorAs(t, err, &limit)
			require.Equal(t, 1, cache.accountReleaseCalls+cache.userReleaseCalls)
		})
	}
}

func TestWaitForSlotExpiredBudgetPreservesRedisError(t *testing.T) {
	errRedis := errors.New("redis unavailable")
	cache := &blockingSlotCache{acquireErr: errRedis}
	helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, time.Second)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/responses")
	started := false
	_, err := helper.waitForSlotWithPingTimeout(c, "account", 42, 1, 20*time.Millisecond, false, &started, true)
	require.ErrorIs(t, err, errRedis)
	var limit *ConcurrencyError
	require.NotErrorAs(t, err, &limit)
}
