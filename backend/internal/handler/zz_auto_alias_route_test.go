package handler

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 复现线上 866：显式映射 cline-pass/deepseek-v4.1-flash，上游目录只列出
// deepseek/deepseek-v4.1-flash（前缀拼写不同），且无 verified 标记。
// 修复后 Auto 应仍把该账号视为可用并选中 deepseek-v4.1-flash。
func TestAutoModelPlanSelectsSynonymMappedAccountWithoutVerifiedMark(t *testing.T) {
	acct := service.Account{
		ID: 866, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"deepseek-v4.1-flash":            "cline-pass/deepseek-v4.1-flash",
			"cline-pass/deepseek-v4.1-flash": "cline-pass/deepseek-v4.1-flash",
		}},
	}
	// 上游目录只列出 deepseek/ 前缀拼写，且无 verified 标记。
	acct.SetUpstreamSupportedModelsSnapshot(service.UpstreamSupportedModelsSnapshot{
		Source: "upstream", SyncedAt: time.Now().UTC().Format(time.RFC3339),
		Models: []string{"deepseek/deepseek-v4.1-flash"},
	})
	accounts := []service.Account{acct}
	h := newAutoModelTestHandler(accounts)
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, 71)
	require.NoError(t, err)
	routes, plan, err := h.autoModelPlan(ctx, &service.Group{ID: 71, Platform: service.PlatformOpenAI}, nil, "/v1/responses", []byte(`{"model":"auto","input":"OK"}`), models)
	require.NoError(t, err)
	require.NotEmpty(t, routes)
	require.Equal(t, "deepseek-v4.1-flash", routes[0].model)
	require.True(t, plan[0].Eligible)
}
