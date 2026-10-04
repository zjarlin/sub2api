package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 上游 Codex 网关（如 RawChat 公益站 https://new.sharedchat.cc/codex）会拒绝
// 请求体缺少 client_metadata.x-codex-installation-id 的 /responses 请求，
// 返回 403 codex_access_restricted。该注入只对命中主机名单的上游生效，
// 官方 api.openai.com 与其它中转站保持原样。

func probeBodyMetadata(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded), "探测请求体必须是 JSON 对象")
	metadata, _ := decoded["client_metadata"].(map[string]any)
	return metadata
}

func TestEnsureCodexProbeInstallationMetadata_OnlyScopedHost(t *testing.T) {
	account := &Account{
		ID:       871,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "oauth-token",
		},
	}

	// 命中名单主机：注入。
	scoped := map[string]any{}
	ensureCodexProbeInstallationMetadata("https://new.sharedchat.cc/codex/v1/responses", account, scoped)
	id, _ := scoped["client_metadata"].(map[string]any)["x-codex-installation-id"].(string)
	require.NotEmpty(t, id, "命中主机必须补写 x-codex-installation-id")

	// 官方 OpenAI：不注入。
	official := map[string]any{}
	ensureCodexProbeInstallationMetadata("https://api.openai.com/v1/responses", account, official)
	require.NotContains(t, official, "client_metadata", "官方 api.openai.com 不得注入")

	// 其它中转站：不注入。
	other := map[string]any{}
	ensureCodexProbeInstallationMetadata("https://api.example-relay.com/v1/responses", account, other)
	require.NotContains(t, other, "client_metadata", "其它中转站不得注入")

	// OAuth 的 chatgpt.com：不注入。
	oauth := map[string]any{}
	ensureCodexProbeInstallationMetadata(chatgptCodexAPIURL, account, oauth)
	require.NotContains(t, oauth, "client_metadata", "OAuth chatgpt.com 不得注入")
}

func TestEnsureCodexProbeInstallationMetadata_NonOpenAIUntouched(t *testing.T) {
	account := &Account{ID: 5, Platform: PlatformKimi, Type: AccountTypeAPIKey}
	payload := map[string]any{"model": "kimi-v4"}
	ensureCodexProbeInstallationMetadata("https://new.sharedchat.cc/codex/v1/responses", account, payload)
	require.NotContains(t, payload, "client_metadata", "非 OpenAI 平台不得注入 Codex 身份")
}

func TestEnsureCodexProbeInstallationMetadata_PreservesConvergedIdentity(t *testing.T) {
	seed := testCodexFingerprintSeed
	account := &Account{
		ID:       6,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"codex_fingerprint_mode":     "session",
			codexFingerprintSeedExtraKey: seed,
		},
	}
	payload := map[string]any{}
	ensureCodexProbeInstallationMetadata("https://new.sharedchat.cc/codex/v1/responses", account, payload)
	id, _ := payload["client_metadata"].(map[string]any)["x-codex-installation-id"].(string)
	require.Equal(t, resolveConvergedInstallationID(account, seed), id,
		"收敛账号探测应复用与真实转发同源的 installation_id")
}

// 端到端：默认 /responses 探测（apikey 账号，命中 scoped 主机）出站请求体必须携带
// client_metadata.x-codex-installation-id；换成官方 base_url 则不携带。
func TestAccountTestService_OpenAIProbeCarriesInstallationMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)

	account := Account{
		ID:          871,
		Name:        "sharedchat-codex",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-upstream",
			"base_url": "https://new.sharedchat.cc/codex",
		},
	}
	repo := &stubOpenAIAccountRepo{accounts: []Account{account}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(compactProbeSSESuccessBody)),
	}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/871/test", bytes.NewReader(nil))

	_ = svc.TestAccountConnection(c, account.ID, "gpt-5.6-sol", "", AccountTestModeDefault)

	require.NotNil(t, upstream.lastReq, "应向上游发出探测请求")
	metadata := probeBodyMetadata(t, upstream.lastBody)
	id, _ := metadata["x-codex-installation-id"].(string)
	require.NotEmpty(t, id, "命中主机时探测请求体必须携带 client_metadata.x-codex-installation-id")
}
