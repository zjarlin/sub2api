//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 模拟 Redis 已写入槽位，但客户端未收到成功响应的故障。
type uncertainAcquireCache struct {
	ConcurrencyCache
	slots             map[string]bool
	fail              bool
	cancel            context.CancelFunc
	acquiredID        string
	releasedID        string
	releaseContextErr error
}

func (c *uncertainAcquireCache) acquire(id string) (bool, error) {
	if len(c.slots) >= 1 {
		return false, nil
	}
	c.slots[id] = true
	c.acquiredID = id
	if c.fail {
		c.cancel()
		return false, context.DeadlineExceeded
	}
	return true, nil
}

func (c *uncertainAcquireCache) release(ctx context.Context, id string) error {
	c.releasedID = id
	c.releaseContextErr = ctx.Err()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	delete(c.slots, id)
	return nil
}

func (c *uncertainAcquireCache) AcquireAccountSlot(_ context.Context, _ int64, _ int, id string) (bool, error) {
	return c.acquire(id)
}
func (c *uncertainAcquireCache) AcquireUserSlot(_ context.Context, _ int64, _ int, id string) (bool, error) {
	return c.acquire(id)
}
func (c *uncertainAcquireCache) ReleaseAccountSlot(ctx context.Context, _ int64, id string) error {
	return c.release(ctx, id)
}
func (c *uncertainAcquireCache) ReleaseUserSlot(ctx context.Context, _ int64, id string) error {
	return c.release(ctx, id)
}

func TestAcquireSlotTimeoutDoesNotLeakCapacity(t *testing.T) {
	for _, slotType := range []string{"account", "user"} {
		t.Run(slotType, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cache := &uncertainAcquireCache{slots: map[string]bool{}, fail: true, cancel: cancel}
			svc := NewConcurrencyService(cache)
			acquire := svc.AcquireAccountSlot
			if slotType == "user" {
				acquire = svc.AcquireUserSlot
			}
			result, err := acquire(ctx, 42, 1)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Nil(t, result)
			require.NoError(t, cache.releaseContextErr)
			require.Equal(t, cache.acquiredID, cache.releasedID)
			require.Empty(t, cache.slots)
			cache.fail = false
			result, err = acquire(context.Background(), 42, 1)
			require.NoError(t, err)
			require.True(t, result.Acquired)
			result.ReleaseFunc()
			require.Empty(t, cache.slots)
		})
	}
}
