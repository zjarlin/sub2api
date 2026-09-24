//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVisionPerCallBillingUsesGroupPrice(t *testing.T) {
	svc := &GatewayService{billingService: &BillingService{}}
	group := &Group{}
	key := &APIKey{Group: group}
	result := &ForwardResult{Model: "edge-vision-detect", VisionCount: 1}
	cost := svc.calculateRecordUsageCost(context.Background(), result, key, result.Model, 3, 2, time.Time{})
	require.Equal(t, string(BillingModePerRequest), cost.BillingMode)
	require.InDelta(t, 0.002, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.004, cost.ActualCost, 1e-12)

	price := 0.005
	group.VisionPricePerCall = &price
	cost = svc.calculateRecordUsageCost(context.Background(), result, key, result.Model, 3, 2, time.Time{})
	require.InDelta(t, 0.005, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.01, cost.ActualCost, 1e-12)

	price = 0
	cost = svc.calculateRecordUsageCost(context.Background(), result, key, result.Model, 3, 2, time.Time{})
	require.Zero(t, cost.TotalCost)
	require.Zero(t, cost.ActualCost)
}

func TestVisionRequestIDBypassesClientIDDedup(t *testing.T) {
	require.True(t, isForcedUsageBillingRequestID("vision:unique-call"))
}
