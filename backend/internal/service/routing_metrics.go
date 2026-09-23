package service

import "context"

// RoutingModels preserves raw model identities independently of dashboard display filters.
// This read-only view is exposed only through the dedicated, group-scoped metrics key.
func (s *ChannelMonitorV2Service) RoutingModels(ctx context.Context, filter ChannelMonitorV2Filter) (*ChannelMonitorV2List[ChannelMonitorV2ModelRow], error) {
	stored, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, ErrChannelMonitorDisabled
	}
	cfg := *stored
	cfg.GroupIDs = nil
	cfg.Platforms = append([]ChannelMonitorV2PlatformConfig(nil), stored.Platforms...)
	for i := range cfg.Platforms {
		cfg.Platforms[i].Enabled = true
		cfg.Platforms[i].Models = nil
	}
	return s.repo.GetModels(ctx, filter, cfg, true)
}
