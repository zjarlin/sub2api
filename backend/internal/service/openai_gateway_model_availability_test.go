//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// OpenAI 网关的可用性诊断必须与调度一致地纳入混合调度来源平台，
// 否则 traework/workbuddy 账号服务 openai/Codex 分组时会被误判为 404 model_not_found。
func TestOpenAIGatewayDiagnoseModelAvailabilityMixedScheduling(t *testing.T) {
	groupID := int64(6)
	workbuddy := Account{
		ID: 844, Platform: PlatformWorkbuddy, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Extra:         map[string]any{"mixed_scheduling": true},
		AccountGroups: []AccountGroup{{GroupID: groupID}},
		Credentials: map[string]any{
			"base_url": "http://sub2api-workbuddy:7863/v1", "api_key": "k",
			"model_mapping": map[string]any{"cn:glm-5.2": "cn:glm-5.2"},
		},
	}
	disabled := Account{
		ID: 999, Platform: PlatformWorkbuddy, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		AccountGroups: []AccountGroup{{GroupID: groupID}},
		Credentials: map[string]any{
			"base_url": "http://sub2api-workbuddy:7863/v1", "api_key": "k",
			"model_mapping": map[string]any{"cn:glm-5.2": "cn:glm-5.2"},
		},
	}
	repo := &schedulerTestOpenAIAccountRepo{accounts: []Account{workbuddy, disabled}}
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{RunMode: config.RunModeStandard}}

	diag := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), &groupID, "cn:glm-5.2", PlatformOpenAI)
	require.True(t, diag.HasAccountsInPool, "mixed source platform must count toward the pool")
	require.True(t, diag.HasModelSupport, "cn:glm-5.2 served by workbuddy must be reported as supported")

	// 兼容来源只需绑定协议分组，旧混合调度开关不再阻止其模型参与。
	onlyDisabled := &schedulerTestOpenAIAccountRepo{accounts: []Account{disabled}}
	svc2 := &OpenAIGatewayService{accountRepo: onlyDisabled, cfg: &config.Config{RunMode: config.RunModeStandard}}
	diag2 := svc2.DiagnoseModelAvailabilityForPlatform(context.Background(), &groupID, "cn:glm-5.2", PlatformOpenAI)
	require.True(t, diag2.HasModelSupport, "compatible account must participate without mixed_scheduling")

	// 普通 openai 平台账号仍按原路径工作。
	plain := Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		AccountGroups: []AccountGroup{{GroupID: groupID}},
		Credentials:   map[string]any{"api_key": "k", "model_mapping": map[string]any{"gpt-5.6-sol": "gpt-5.6-sol"}},
	}
	svc3 := &OpenAIGatewayService{accountRepo: &schedulerTestOpenAIAccountRepo{accounts: []Account{plain}}, cfg: &config.Config{RunMode: config.RunModeStandard}}
	diag3 := svc3.DiagnoseModelAvailabilityForPlatform(context.Background(), &groupID, "gpt-5.6-sol", PlatformOpenAI)
	require.True(t, diag3.HasModelSupport)
}
