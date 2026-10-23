package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildCodexModelsManifestForGroupCorrectsOfficialAstraStaleModalities(t *testing.T) {
	t.Parallel()

	const groupID int64 = 780
	account := Account{
		ID:       1,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":       "sk-test",
			"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"},
		},
	}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Models: map[string]UpstreamModelMetadata{
			"gpt-6-astra": {ID: "gpt-6-astra", InputModalities: []string{"text"}},
		},
	})

	require.True(t, account.IsModelSupported("gpt-6-astra"))
	svc := &GatewayService{accountRepo: codexModelsVisibilityAccountRepo{
		byGroup: map[int64][]Account{groupID: {account}},
	}}

	body, err := svc.BuildCodexModelsManifestForGroup(
		context.Background(),
		&Group{ID: groupID, Platform: PlatformOpenAI},
		"",
		[]string{"gpt-6-astra"},
	)
	require.NoError(t, err)
	models := decodeCodexManifestModels(t, body)
	require.Len(t, models, 1)
	require.Equal(t, []any{"text", "image"}, models[0]["input_modalities"])
}

// 视觉助手覆盖所有可路由模型：第三方兼容端点即使快照写着 text-only，
// 目录也要声明图片输入，转发侧再决定走原生读图还是助手转描述。
func TestBuildCodexModelsManifestForGroupDeclaresImageForCompatibleAstraTextOnlyMetadata(t *testing.T) {
	t.Parallel()

	const groupID int64 = 781
	account := Account{
		ID:       2,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":       "sk-test",
			"base_url":      "https://relay.example.test/v1",
			"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"},
		},
	}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Models: map[string]UpstreamModelMetadata{
			"gpt-6-astra": {ID: "gpt-6-astra", InputModalities: []string{"text"}},
		},
	})

	require.True(t, account.IsModelSupported("gpt-6-astra"))
	svc := &GatewayService{accountRepo: codexModelsVisibilityAccountRepo{
		byGroup: map[int64][]Account{groupID: {account}},
	}}

	body, err := svc.BuildCodexModelsManifestForGroup(
		context.Background(),
		&Group{ID: groupID, Platform: PlatformOpenAI},
		"",
		[]string{"gpt-6-astra"},
	)
	require.NoError(t, err)
	models := decodeCodexManifestModels(t, body)
	require.Len(t, models, 1)
	require.Equal(t, []any{"text", "image"}, models[0]["input_modalities"])
	// 目录声明不影响请求侧判定：该账号仍需要视觉助手转描述。
	require.True(t, accountNeedsVisionFallback(&account, "gpt-6-astra"))
}
