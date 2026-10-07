package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/platform/translate"
	"github.com/stretchr/testify/require"
)

func TestTranslateSettingsPreserveEnvironmentAndMaskSecrets(t *testing.T) {
	t.Setenv("TRANSLATE_FREE_PROVIDERS", "false")
	t.Setenv("TRANSLATE_BAIDU_APP_ID", "test-app")
	t.Setenv("TRANSLATE_BAIDU_SECRET", "baidu-private")
	t.Setenv("TRANSLATE_HYMT_URL", "http://private-inference:8000")
	t.Setenv("TRANSLATE_HYMT_API_KEY", "hymt-private")
	s := DefaultTranslateProviderSettings()
	require.NoError(t, s.Validate())
	require.True(t, s.Baidu.Enabled)
	require.True(t, s.HyMT.Enabled)
	require.False(t, s.Caiyun.Enabled)
	data, err := json.Marshal(s.Masked())
	require.NoError(t, err)
	require.NotContains(t, string(data), "baidu-private")
	require.NotContains(t, string(data), "hymt-private")
	require.Contains(t, string(data), `"secret_set":true`)
	providers := translate.NewAggregator(s.ToTranslateConfig()).AvailableProviders()
	require.Equal(t, "baidu", providers[0])
	s.Enabled = false
	require.Empty(t, translate.NewAggregator(s.ToTranslateConfig()).AvailableProviders())
}

func TestTranslateSettingsPriorityAndValidation(t *testing.T) {
	t.Setenv("TRANSLATE_FREE_PROVIDERS", "false")
	s := DefaultTranslateProviderSettings()
	s.MyMemory.Enabled = true
	s.HyMT.Enabled = true
	s.HyMT.BaseURL = "http://inference:8000"
	s.Priority = []string{"hymt", "mymemory", "baidu", "tencent", "youdao", "libretranslate", "caiyun", "google_web"}
	require.NoError(t, s.Validate())
	providers := translate.NewAggregator(s.ToTranslateConfig()).AvailableProviders()
	require.Equal(t, "hymt", providers[0])
	require.Equal(t, "mymemory", providers[1])
	s.HyMT.BaseURL = "http://user:private@inference:8000"
	require.Error(t, s.Validate())
	s.HyMT.BaseURL = "http://inference:8000"
	s.Priority[1] = "hymt"
	require.Error(t, s.Validate())
}
