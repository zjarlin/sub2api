package handler

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestAskRoutingPrefersFreeThenWebAskExcludesDeepseek 验证 ask 打包模型组：
// 1) deepseek-v4.1-flash 从 ask 候选排除；2) 免费模型优先；3) web 问答适配器次之；
// 4) auto 链路不受影响，deepseek-v4.1-flash 仍是首选。
func TestAskRoutingPrefersFreeThenWebAskExcludesDeepseek(t *testing.T) {
	accounts := []service.Account{
		{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"}}},
		{ID: 2, Platform: service.PlatformKilo, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"kilo-auto/free": "kilo-auto/free"}}},
		{ID: 3, Platform: service.PlatformDeepseekWeb, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"deepseek-web-chat": "deepseek-web-chat"}}},
		{ID: 4, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"openrouter/free": "openrouter/free"}}},
	}
	h := newAutoModelTestHandler(accounts)
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, 71)
	require.NoError(t, err)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}

	askBody := []byte(`{"model":"ask","input":"hello"}`)
	askCtx := service.WithAutoModelRequestCapabilities(ctx, askBody)
	askRoutes, askPlan, err := h.autoModelPlan(askCtx, group, nil, "/v1/responses", askBody, models)
	require.NoError(t, err)
	require.NotEmpty(t, askRoutes, "ask should have eligible candidates")

	// deepseek-v4.1-flash 不得出现在 ask 候选里。
	for _, candidate := range askPlan {
		require.NotEqual(t, "deepseek-v4.1-flash", candidate.Model, "ask must not schedule deepseek-v4.1-flash")
	}

	// 免费模型排在 web 问答适配器之前，且都排在其他问答模型之前。
	orders := make(map[string]int)
	for i, r := range askRoutes {
		orders[r.model] = i
	}
	fmt.Printf("ask order: %v\n", orders)
	require.True(t, orders["kilo-auto/free"] < orders["deepseek-web-chat"], "free model must precede web-ask adapter")
	require.True(t, orders["openrouter/free"] < orders["deepseek-web-chat"], "free model must precede web-ask adapter")
	require.True(t, orders["kilo-auto/free"] == 0, "free model should be first among candidates")

	// auto 链路保持原样：deepseek-v4.1-flash 仍是首选。
	autoBody := []byte(`{"model":"auto","input":"hello"}`)
	ctx = service.WithAutoModelRequestCapabilities(ctx, autoBody)
	autoRoutes, _, err := h.autoModelPlan(ctx, group, nil, "/v1/responses", autoBody, models)
	require.NoError(t, err)
	require.NotEmpty(t, autoRoutes)
	require.Equal(t, "deepseek-v4.1-flash", autoRoutes[0].model, "auto routing is unchanged and still prefers deepseek-v4.1-flash")
}
