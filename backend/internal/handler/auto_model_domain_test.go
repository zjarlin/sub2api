package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 回归 2026-10-07：客户端注入的 instructions 里固定包含 figma/react/css 等技能词，
// 不能凭它把每个请求都判成 UI 设计，否则 claude 会反超首选 deepseek-v4.1-flash。
func TestAutoModelRequestDomainIgnoresInjectedInstructions(t *testing.T) {
	body := []byte(`{"model":"auto","instructions":"Skills: figma, design system, react, vue, css, layout, 页面 组件 样式","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"你好"}]}]}`)
	require.Equal(t, autoModelDomainGeneral, autoModelRequestDomain(body))

	developerOnly := []byte(`{"model":"auto","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"UI 设计 figma"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"你好"}]}]}`)
	require.Equal(t, autoModelDomainGeneral, autoModelRequestDomain(developerOnly))
}

// 用户自己提出的 UI/前端/架构需求仍然生效。
func TestAutoModelRequestDomainUsesUserText(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want autoModelDomain
	}{
		{name: "ui design", body: `{"model":"auto","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"帮我做 UI 设计，降低卡片感的 AI 味"}]}]}`, want: autoModelDomainUIDesign},
		{name: "frontend", body: `{"model":"auto","input":"调整这个 Vue 组件的样式"}`, want: autoModelDomainFrontend},
		{name: "reasoning", body: `{"model":"auto","messages":[{"role":"user","content":"重构这个分布式事务的数据库迁移"}]}`, want: autoModelDomainReasoning},
		{name: "general", body: `{"model":"auto","input":"修复这个空指针"}`, want: autoModelDomainGeneral},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, autoModelRequestDomain([]byte(tc.body)))
		})
	}
}

// 工具循环中最后一项是工具结果时，回溯到仍在历史里的用户请求。
func TestAutoModelRequestDomainLooksBackPastToolOutput(t *testing.T) {
	body := []byte(`{"model":"auto","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"帮我做 UI 设计"}]},{"type":"function_call_output","call_id":"c1","output":"ok"}]}`)
	require.Equal(t, autoModelDomainUIDesign, autoModelRequestDomain(body))
}

// 顶层 input_image 仍是 multimodal，但 injected instructions 不参与分类。
func TestAutoModelRequestDomainKeepsImageDetection(t *testing.T) {
	body := []byte(`{"model":"auto","instructions":"figma design system","input":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"},{"type":"input_text","text":"看这张图"}]}`)
	require.Equal(t, autoModelDomainMultimodal, autoModelRequestDomain(body))
}

// Auto 首选仍是 deepseek-v4.1-flash，即使请求带有包含技能词的 instructions。
func TestAutoModelPlanKeepsDeepSeekFirstDespiteInjectedInstructions(t *testing.T) {
	accounts := []service.Account{{
		ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"deepseek-v4.1-flash": "deepseek-v4.1-flash",
			"claude-opus-4-7":     "claude-opus-4-7",
			"glm-5.3":             "glm-5.3",
		}},
	}}
	h := newAutoModelTestHandler(accounts)
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, 71)
	require.NoError(t, err)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	body := []byte(`{"model":"auto","instructions":"Skills: figma, design system, react, vue, css, layout","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"修复这个空指针"}]}]}`)
	routes, _, err := h.autoModelPlan(ctx, group, nil, "/v1/responses", body, models)
	require.NoError(t, err)
	require.NotEmpty(t, routes)
	require.Equal(t, "deepseek-v4.1-flash", routes[0].model)
}

// 高级任务领域：旗舰 gpt-6-astra / gpt-6-sol / gpt-6.1-sol 优先，claude 不做领域加成。
func TestAutoModelDomainPriorityFlagshipThenDemotesClaude(t *testing.T) {
	for _, domain := range []autoModelDomain{
		autoModelDomainUIDesign, autoModelDomainFrontend, autoModelDomainReasoning,
	} {
		flagship, ok := autoModelDomainPriority(domain, "gpt-6-astra")
		require.True(t, ok, domain)
		require.Equal(t, 0, flagship)

		for _, model := range []string{"gpt-6.1-sol", "gpt-6-sol"} {
			rank, ok := autoModelDomainPriority(domain, model)
			require.True(t, ok, model)
			require.Equal(t, 0, rank, model)
		}

		gpt6, ok := autoModelDomainPriority(domain, "gpt-6-luna")
		require.True(t, ok)
		require.Greater(t, gpt6, flagship, "本站 GPT-6 家族排在旗舰之后")

		economy, ok := autoModelDomainPriority(domain, "glm-5.3")
		require.True(t, ok)
		require.Greater(t, economy, gpt6, "经济型编码模型排在本站 GPT-6 之后")

		// claude 系列不再参与领域加成，由通用档位顺序排到后面。
		for _, model := range []string{"claude-opus-4-7", "claude-sonnet-4-6", "claude-opus-5", "claude-sonnet-5"} {
			_, ok := autoModelDomainPriority(domain, model)
			require.False(t, ok, model)
		}
	}

	// 通用任务与仅附图的多模态不做领域加成，保持 deepseek-v4.1-flash 首选。
	for _, domain := range []autoModelDomain{autoModelDomainGeneral, autoModelDomainMultimodal} {
		_, ok := autoModelDomainPriority(domain, "gpt-6-astra")
		require.False(t, ok, domain)
		_, ok = autoModelDomainPriority(domain, "claude-opus-4-7")
		require.False(t, ok, domain)
	}
}
