package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"

	"github.com/stretchr/testify/require"
)

func TestIsSystemOneDecisionPlatform(t *testing.T) {
	require.True(t, IsSystemOneDecisionPlatform(PlatformLaya))
	require.True(t, IsSystemOneDecisionPlatform(PlatformJev))
	require.False(t, IsSystemOneDecisionPlatform(PlatformOpenAI))
	require.False(t, IsSystemOneDecisionPlatform(PlatformZcode))
}

func TestSystemOneDecisionAccountsAreDistinct(t *testing.T) {
	laya := &Account{Platform: PlatformLaya}
	jev := &Account{Platform: PlatformJev}
	require.True(t, laya.IsLaya())
	require.False(t, laya.IsJev())
	require.True(t, jev.IsJev())
	require.False(t, jev.IsLaya())
}

func TestSystemOneDecisionDefaultModels(t *testing.T) {
	require.Equal(t, "laya", DefaultLayaModel)
	// 公开名 `laya` 表示自动选检查点；显式检查点走带后缀的名字。
	require.Contains(t, DefaultLayaModelIDs(), DefaultLayaModel)
	require.Contains(t, DefaultLayaModelIDs(), "laya-multilingual")
	require.Equal(t, []string{"typesafe/jev"}, DefaultJevModelIDs())
}

func TestSystemOneDecisionCredentialsRequireBuiltinAdapter(t *testing.T) {
	// 关闭内置适配器时必须拒绝，避免账号指向不存在的上游。
	SetBuiltinAdapterConfig(nil)
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })

	err := validateSystemOneDecisionCredentials(
		PlatformLaya, AccountTypeAPIKey, map[string]any{"api_key": "k"},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "built-in")

	// 非 apikey 账号同样拒绝。
	err = validateSystemOneDecisionCredentials(PlatformLaya, AccountTypeOAuth, map[string]any{})
	require.Error(t, err)

	// 非 System One 平台直接放行，不影响其它平台的既有校验。
	require.NoError(t, validateSystemOneDecisionCredentials(
		PlatformOpenAI, AccountTypeAPIKey, map[string]any{},
	))
}

func TestSystemOneDecisionCredentialsRejectWrongProtocol(t *testing.T) {
	// 先打开内置适配器，否则会在更早的 adapter 检查处失败，测不到协议校验这一层。
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })

	err := validateSystemOneDecisionCredentials(
		PlatformJev, AccountTypeAPIKey,
		map[string]any{"api_key": "k", "api_protocol": APIProtocolChatCompletions},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "systemone")

	// 协议正确时应放行。
	require.NoError(t, validateSystemOneDecisionCredentials(
		PlatformJev, AccountTypeAPIKey,
		map[string]any{"api_key": "k", "api_protocol": APIProtocolSystemOne},
	))
}

func TestSystemOneDecisionCredentialsAcceptLayaWithoutAPIKey(t *testing.T) {
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })

	for _, platform := range []string{PlatformLaya, PlatformJev} {
		require.NoError(t, validateSystemOneDecisionCredentials(
			platform, AccountTypeAPIKey, map[string]any{"api_key": "k"},
		), "platform=%s", platform)
	}
	require.NoError(t, validateSystemOneDecisionCredentials(
		PlatformLaya, AccountTypeAPIKey, map[string]any{"api_protocol": APIProtocolSystemOne},
	))
	require.Error(t, validateSystemOneDecisionCredentials(
		PlatformJev, AccountTypeAPIKey, map[string]any{"api_key": "  "},
	))
}

func TestBuildLayaAccountWithoutAPIKeyUsesInternalAdapter(t *testing.T) {
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })

	account, err := buildAccountForCreate(&CreateAccountInput{
		Name: "Laya", Platform: PlatformLaya, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_protocol": APIProtocolSystemOne},
	}, nil)
	require.NoError(t, err)
	require.Equal(t, "http://edge-laya:18082/v1", account.Credentials["base_url"])
	require.Empty(t, account.Credentials["api_key"])
}

func TestSystemOneDecisionAccountsReportSystemOneProtocol(t *testing.T) {
	// IsCNProvider 把 laya/jev 纳入了国产供应商集合，因此 GetAPIProtocol 必须在
	// 通用 chat_completions 回退之前判定这两个平台，否则协议会被兜成 chat_completions。
	for _, platform := range []string{PlatformLaya, PlatformJev} {
		account := &Account{Platform: platform, Credentials: map[string]any{}}
		require.Equal(t, APIProtocolSystemOne, account.GetAPIProtocol(), "platform=%s", platform)
	}

	// 对照：ZCode 等内置适配器仍是 chat_completions。
	zcode := &Account{Platform: PlatformZcode, Credentials: map[string]any{}}
	require.Equal(t, APIProtocolChatCompletions, zcode.GetAPIProtocol())
}

func TestSystemOneDecisionSchedulingIgnoresUpstreamChatCatalog(t *testing.T) {
	account := &Account{
		Platform: PlatformJev,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{
			DefaultJevModel: DefaultJevModel,
		}},
	}
	account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source:   "upstream",
		SyncedAt: "2099-01-01T00:00:00Z",
		Models:   []string{"gpt-6-astra"},
	})

	svc := &GatewayService{}
	require.True(t, svc.isModelSupportedByAccount(account, DefaultJevModel))
	require.False(t, svc.isModelSupportedByAccount(account, "unknown-chat-model"))
}
