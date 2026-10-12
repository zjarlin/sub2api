package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestModel3DPricingRequiresExplicitRequestPrice 未定价不能提交上游，显式零价可用。
func TestModel3DPricingRequiresExplicitRequestPrice(t *testing.T) {
	s := &GatewayService{resolver: NewModelPricingResolver(nil, NewBillingService(nil, nil))}
	key := &APIKey{Group: &Group{ID: 9}}
	require.Error(t, s.ValidateModel3DPricing(context.Background(), key, "meshy-5"))
	zero := 0.0
	key.Group.ModelPricing = []ChannelModelPricing{{Models: []string{"meshy-5"}, BillingMode: BillingModePerRequest, PerRequestPrice: &zero}}
	require.NoError(t, s.ValidateModel3DPricing(context.Background(), key, "meshy-5"))
	key.Group.ModelPricing[0].PerRequestPrice = nil
	require.Error(t, s.ValidateModel3DPricing(context.Background(), key, "meshy-5"))
}

// TestModel3DPerRequestBillingChargesOneSubmission 按次价格与倍率真实进入统一费用计算。
func TestModel3DPerRequestBillingChargesOneSubmission(t *testing.T) {
	price := 0.35
	billing := NewBillingService(nil, nil)
	resolver := NewModelPricingResolver(nil, billing)
	group := &Group{ID: 9, ModelPricing: []ChannelModelPricing{{Models: []string{"meshy-5"}, BillingMode: BillingModePerRequest, PerRequestPrice: &price}}}
	ctx := context.Background()
	cost, err := billing.CalculateTokenCostForRequest(TokenCostRequest{Ctx: ctx, Model: "meshy-5", Group: group, Resolver: resolver, RateMultiplier: 1.5})
	require.NoError(t, err)
	require.InDelta(t, 0.35, cost.TotalCost, 1e-8)
	require.InDelta(t, 0.525, cost.ActualCost, 1e-8)
}
