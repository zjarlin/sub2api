package upstream

import (
	"context"
	"math"

	"workbuddy2api/internal/auth"
)

// UserResourceBalance 查询官方精确余额，独立于调度池的整数估算余额。
func (c *Client) UserResourceBalance(ctx context.Context, a *auth.Auth) (float64, error) {
	resp, err := c.getUserResourceBodyContext(ctx, a)
	if err != nil {
		return 0, err
	}
	var total float64
	for _, account := range resp.Response.Data.Accounts {
		remain := float64(account.CapacityRemain)
		precise := account.CapacityRemainPrecise
		if account.CycleCapacitySize > 0 {
			remain = float64(account.CycleCapacityRemain)
			precise = account.CycleCapacityRemainPrecise
		}
		if precise != nil {
			remain = *precise
		}
		total += math.Max(0, remain)
	}
	return math.Round(total*100) / 100, nil
}
