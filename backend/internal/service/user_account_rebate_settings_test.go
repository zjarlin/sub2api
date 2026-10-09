//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 默认值：总开关关、比例 100%、仅共享、不含号主自用。
func TestUserAccountRebateParseDefaults(t *testing.T) {
	svc := NewSettingService(newMockSettingRepo(), &config.Config{})
	got := svc.parseSettings(map[string]string{})

	require.False(t, got.UserAccountRebateEnabled)
	require.Equal(t, AccountRebateRateDefault, got.UserAccountRebateRate)
	require.True(t, got.UserAccountRebateSharedOnly)
	require.False(t, got.UserAccountRebateIncludeOwner)
}

// 解析：显式值生效，比例越界被 100% 夹取。
func TestUserAccountRebateParseOverrides(t *testing.T) {
	svc := NewSettingService(newMockSettingRepo(), &config.Config{})
	got := svc.parseSettings(map[string]string{
		SettingKeyUserAccountRebateEnabled:      "true",
		SettingKeyUserAccountRebateRate:         "250",
		SettingKeyUserAccountRebateSharedOnly:   "false",
		SettingKeyUserAccountRebateIncludeOwner: "true",
	})

	require.True(t, got.UserAccountRebateEnabled)
	require.Equal(t, AccountRebateRateMax, got.UserAccountRebateRate)
	require.False(t, got.UserAccountRebateSharedOnly)
	require.True(t, got.UserAccountRebateIncludeOwner)
}

// 读取器：缺失回退安全默认；比例非法时回退 100。
func TestUserAccountRebateSettingGetters(t *testing.T) {
	empty := NewSettingService(newMockSettingRepo(), &config.Config{})
	ctx := context.Background()
	require.False(t, empty.IsUserAccountRebateEnabled(ctx))
	require.Equal(t, AccountRebateRateDefault, empty.GetUserAccountRebateRatePercent(ctx))
	require.True(t, empty.IsUserAccountRebateSharedOnly(ctx))
	require.False(t, empty.IsUserAccountRebateIncludeOwnerUsage(ctx))

	repo := newMockSettingRepo()
	repo.data[SettingKeyUserAccountRebateEnabled] = "true"
	repo.data[SettingKeyUserAccountRebateRate] = "not-a-number"
	repo.data[SettingKeyUserAccountRebateSharedOnly] = "false"
	repo.data[SettingKeyUserAccountRebateIncludeOwner] = "true"
	svc := NewSettingService(repo, &config.Config{})

	require.True(t, svc.IsUserAccountRebateEnabled(ctx))
	require.Equal(t, AccountRebateRateDefault, svc.GetUserAccountRebateRatePercent(ctx))
	require.False(t, svc.IsUserAccountRebateSharedOnly(ctx))
	require.True(t, svc.IsUserAccountRebateIncludeOwnerUsage(ctx))
}

// 写入：UpdateSettings 必须把四个开关落库并夹取比例。
func TestUserAccountRebateUpdatePersistsClampedRate(t *testing.T) {
	repo := newMockSettingRepo()
	svc := NewSettingService(repo, &config.Config{})

	settings := svc.parseSettings(map[string]string{})
	settings.UserAccountRebateEnabled = true
	settings.UserAccountRebateRate = 500
	settings.UserAccountRebateSharedOnly = false
	settings.UserAccountRebateIncludeOwner = true
	require.NoError(t, svc.UpdateSettings(context.Background(), settings))

	require.Equal(t, "true", repo.data[SettingKeyUserAccountRebateEnabled])
	require.Equal(t, "false", repo.data[SettingKeyUserAccountRebateSharedOnly])
	require.Equal(t, "true", repo.data[SettingKeyUserAccountRebateIncludeOwner])
	// 500 被夹到 100 后写入
	require.Equal(t, "100.00000000", repo.data[SettingKeyUserAccountRebateRate])
}
