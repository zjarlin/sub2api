package service

import (
	"context"
	"fmt"
	"math"
)

// ValidateModel3DPricing 在提交付费上游前要求显式按次定价，零价必须明确配置。
func (s *GatewayService) ValidateModel3DPricing(ctx context.Context, key *APIKey, model string) error {
	if s == nil || s.resolver == nil || key == nil || key.Group == nil {
		return fmt.Errorf("3D pricing is unavailable")
	}
	groupID := key.Group.ID
	pricing := s.resolver.Resolve(ctx, PricingInput{Model: model, GroupID: &groupID, Group: key.Group})
	if pricing == nil || pricing.Mode != BillingModePerRequest || (pricing.Source != PricingSourceGroup && pricing.Source != PricingSourceChannel) {
		return fmt.Errorf("configure explicit per_request pricing for 3D model %s", model)
	}
	if pricing.channelPricing == nil || pricing.channelPricing.PerRequestPrice == nil {
		return fmt.Errorf("configure a default per_request price for 3D model %s", model)
	}
	if math.IsNaN(pricing.DefaultPerRequestPrice) || math.IsInf(pricing.DefaultPerRequestPrice, 0) || pricing.DefaultPerRequestPrice < 0 {
		return fmt.Errorf("invalid 3D request price")
	}
	return nil
}
