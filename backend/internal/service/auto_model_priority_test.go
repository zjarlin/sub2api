//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 复现 2026-10-04 的 Auto 选模偏差：通用档的 glm-5.2 不应反超主力档的可用模型。
// 同档内 GLM 仍先于 Terra，deepseek-v4.1-flash 仍为全池首选。
func TestAutoModelPriorityKeepsTierAboveBrandPreference(t *testing.T) {
	repo := newMockSettingRepo()
	settings := NewSettingService(repo, nil)
	require.NoError(t, settings.SetModelFallbackPolicy(context.Background(), &ModelFallbackPolicy{Enabled: true, Tiers: []ModelCapabilityTier{
		{Name: "旗舰", Models: []string{"gpt-6-astra"}},
		{Name: "主力编码", Models: []string{"deepseek-v4.1-flash", "glm-5.3", "gpt-5.5", "gpt-5.6-terra"}},
		{Name: "通用编码", Models: []string{"glm-5.2", "gpt-5.4-mini"}},
	}}))
	ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)

	// 更高能力档位必须排在低档之前，即使低档是 GLM。
	higherTier, _ := AutoModelPriority(ctx, "gpt-5.5")
	lowerTier, _ := AutoModelPriority(ctx, "glm-5.2")
	require.Less(t, higherTier, lowerTier, "higher capability tier must sort before a lower-tier GLM")

	// 同一档位内 GLM 仍先于 Terra（档位内填写顺序生效）。
	glmTier, _ := AutoModelPriority(ctx, "glm-5.3")
	terraTier, _ := AutoModelPriority(ctx, "gpt-5.6-terra")
	require.Equal(t, glmTier/1000, terraTier/1000, "glm-5.3 and gpt-5.6-terra share one tier")
	require.Less(t, glmTier, terraTier, "GLM must precede Terra within the same tier")

	// 未评级模型没有档位时，品牌性价比顺序仍作为次序。
	unratedGLM, glmFamily := AutoModelPriority(ctx, "glm-unrated")
	unratedGPT, gptFamily := AutoModelPriority(ctx, "gpt-unrated")
	require.Equal(t, unratedGLM, unratedGPT)
	require.Less(t, glmFamily, gptFamily, "unrated GLM must precede unrated GPT models")

	// 首选模型无视档位，仍然最靠前。
	first, _ := AutoModelPriority(ctx, "deepseek-v4.1-flash")
	require.Less(t, first, higherTier, "deepseek-v4.1-flash remains the overall first choice")
}
