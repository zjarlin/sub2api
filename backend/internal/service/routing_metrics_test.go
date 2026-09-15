package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

type routingMetricsRepo struct {
	channelMonitorV2RepoStub
	received ChannelMonitorV2Config
	filter   ChannelMonitorV2Filter
}

func (r *routingMetricsRepo) GetModels(_ context.Context, f ChannelMonitorV2Filter, cfg ChannelMonitorV2Config, admin bool) (*ChannelMonitorV2List[ChannelMonitorV2ModelRow], error) {
	r.received = cfg
	r.filter = f
	r.admin = admin
	return &ChannelMonitorV2List[ChannelMonitorV2ModelRow]{}, nil
}
func TestRoutingModelsKeepsRawNamesAndCredentialScope(t *testing.T) {
	r := &routingMetricsRepo{channelMonitorV2RepoStub: channelMonitorV2RepoStub{config: ChannelMonitorV2Config{GroupIDs: []int64{99}, Platforms: []ChannelMonitorV2PlatformConfig{{Platform: "openai", Enabled: false, Models: []string{"only-dashboard-model"}}}}}}
	_, err := NewChannelMonitorV2Service(r).RoutingModels(context.Background(), ChannelMonitorV2Filter{GroupIDs: []int64{6}})
	require.NoError(t, err)
	require.Equal(t, []int64{6}, r.filter.GroupIDs)
	require.Empty(t, r.received.GroupIDs)
	require.Empty(t, r.received.Platforms[0].Models)
	require.True(t, r.received.Platforms[0].Enabled)
	require.True(t, r.admin)
	require.Equal(t, []string{"only-dashboard-model"}, r.config.Platforms[0].Models)
	require.False(t, r.config.Platforms[0].Enabled)
}
