package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTypeSafeRelayTargetReadsTypeSafeProfile(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	cfg := defaultContentModerationConfig()
	cfg.Engine = ContentModerationEngineTypeSafe
	cfg.TypeSafe = &ContentModerationEngineConfig{
		BaseURL: upstream.URL,
		Model:   "jev-latest",
		APIKeys: []string{"", "profile-key"},
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	svc := &ContentModerationService{
		settingRepo: &contentModerationTestSettingRepo{values: map[string]string{SettingKeyContentModerationConfig: string(raw)}},
		httpClient:  upstream.Client(),
	}
	baseURL, key, client, err := svc.TypeSafeRelayTarget(context.Background())
	require.NoError(t, err)
	require.Equal(t, upstream.URL, baseURL)
	require.Equal(t, "profile-key", key)
	require.NotNil(t, client)
}

func TestTypeSafeRelayTargetToleratesMissingConfig(t *testing.T) {
	svc := &ContentModerationService{settingRepo: &contentModerationTestSettingRepo{values: map[string]string{}}}
	_, key, _, err := svc.TypeSafeRelayTarget(context.Background())
	require.NoError(t, err)
	require.Empty(t, key)
}
