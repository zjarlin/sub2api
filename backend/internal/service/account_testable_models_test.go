package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func testableAuthProbeService(upstream HTTPUpstream) *AccountTestService {
	return &AccountTestService{
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream: upstream,
	}
}

func testableAuthProbeAccount() *Account {
	return &Account{
		ID:          12345,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "sk-invalid",
			"base_url":      "https://tokenhub.example/v1",
			"model_mapping": map[string]any{"hy3": "hy3"},
		},
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// 凭证被上游拒绝（401）时，测试弹窗的模型列表整体报错，而不是把无法调用的模型
// 展示为可测。
func TestFilterTestableOpenAIModelsRejectsInvalidCredentials(t *testing.T) {
	var probed bool
	svc := testableAuthProbeService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		probed = true
		return jsonResponse(http.StatusUnauthorized, `{"error":{"type":"authentication_error"}}`), nil
	}})
	account := testableAuthProbeAccount()
	accountTestableAuthCache.Delete(account.ID)
	t.Cleanup(func() { accountTestableAuthCache.Delete(account.ID) })

	models, err := svc.filterTestableOpenAIModels(context.Background(), account, []openai.Model{
		{ID: "hy3"}, {ID: "glm-5.3"},
	})
	require.Error(t, err)
	require.Nil(t, models)
	var invalid *AccountCredentialsInvalidError
	require.True(t, errors.As(err, &invalid), "auth failure must be a typed error")
	require.True(t, probed, "the probe must actually reach the upstream")
	require.Contains(t, err.Error(), "401")
}

// 403 是访问范围/权限问题而非凭证失效，不得据此封掉整个列表。
func TestFilterTestableOpenAIModelsAllowsForbiddenScope(t *testing.T) {
	svc := testableAuthProbeService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, `{"error":{"code":"403002","type":"permission_error"}}`), nil
	}})
	account := testableAuthProbeAccount()
	accountTestableAuthCache.Delete(account.ID)
	t.Cleanup(func() { accountTestableAuthCache.Delete(account.ID) })

	models, err := svc.filterTestableOpenAIModels(context.Background(), account, []openai.Model{{ID: "glm-5.3"}})
	require.NoError(t, err)
	require.Len(t, models, 1)
}

// EnsureAccountTestableCredentials 在目录不可得时仍能拦住失效凭证。
func TestEnsureAccountTestableCredentialsRejects401(t *testing.T) {
	svc := testableAuthProbeService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		return jsonResponse(http.StatusUnauthorized, `{"error":{"type":"authentication_error"}}`), nil
	}})
	account := testableAuthProbeAccount()
	accountTestableAuthCache.Delete(account.ID)
	t.Cleanup(func() { accountTestableAuthCache.Delete(account.ID) })

	err := svc.EnsureAccountTestableCredentials(context.Background(), account)
	require.Error(t, err)
	var invalid *AccountCredentialsInvalidError
	require.True(t, errors.As(err, &invalid))
}

// 凭证有效（含 403 范围问题）时 EnsureAccountTestableCredentials 放行。
func TestEnsureAccountTestableCredentialsAllowsValid(t *testing.T) {
	svc := testableAuthProbeService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, `{"error":{"code":"403002"}}`), nil
	}})
	account := testableAuthProbeAccount()
	accountTestableAuthCache.Delete(account.ID)
	t.Cleanup(func() { accountTestableAuthCache.Delete(account.ID) })

	require.NoError(t, svc.EnsureAccountTestableCredentials(context.Background(), account))
}

// 凭证有效时保留候选模型，并剔除已有失败证据的模型。
func TestFilterTestableOpenAIModelsKeepsValidAndDropsFailed(t *testing.T) {
	svc := testableAuthProbeService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{}`), nil
	}})
	account := testableAuthProbeAccount()
	accountTestableAuthCache.Delete(account.ID)
	t.Cleanup(func() { accountTestableAuthCache.Delete(account.ID) })
	account.Extra = map[string]any{
		UnsupportedModelsExtraKey: map[string]any{"glm-5.3": map[string]any{}},
	}

	models, err := svc.filterTestableOpenAIModels(context.Background(), account, []openai.Model{
		{ID: "hy3"}, {ID: "glm-5.3"},
	})
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "hy3", models[0].ID)
}

// 网络错误不代表凭证无效，必须保守放行，避免抖动误伤列表。
func TestFilterTestableOpenAIModelsFailsOpenOnTransportError(t *testing.T) {
	svc := testableAuthProbeService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		return nil, errors.New("connection reset")
	}})
	account := testableAuthProbeAccount()
	accountTestableAuthCache.Delete(account.ID)
	t.Cleanup(func() { accountTestableAuthCache.Delete(account.ID) })

	models, err := svc.filterTestableOpenAIModels(context.Background(), account, []openai.Model{{ID: "hy3"}})
	require.NoError(t, err)
	require.Len(t, models, 1)
}

// 缺少探测依赖时无法验证，放行而不是误伤。
func TestFilterTestableOpenAIModelsFailsOpenWithoutDependencies(t *testing.T) {
	svc := &AccountTestService{}
	account := testableAuthProbeAccount()
	accountTestableAuthCache.Delete(account.ID)
	t.Cleanup(func() { accountTestableAuthCache.Delete(account.ID) })

	models, err := svc.filterTestableOpenAIModels(context.Background(), account, []openai.Model{{ID: "hy3"}})
	require.NoError(t, err)
	require.Len(t, models, 1)
}

// 鉴权探测结论在缓存窗口内只发一次请求。
func TestProbeAccountAuthVerdictCachesWithinTTL(t *testing.T) {
	var calls int
	svc := testableAuthProbeService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		calls++
		return jsonResponse(http.StatusUnauthorized, `{"error":"bad key"}`), nil
	}})
	account := testableAuthProbeAccount()
	accountTestableAuthCache.Delete(account.ID)
	t.Cleanup(func() { accountTestableAuthCache.Delete(account.ID) })

	for i := 0; i < 3; i++ {
		verdict := svc.probeAccountAuthVerdict(context.Background(), account)
		require.False(t, verdict.ok)
	}
	require.Equal(t, 1, calls, "cached verdict must not re-probe within TTL")
}

// 失效缓存后重新探测。
func TestInvalidateAccountTestableAuthCache(t *testing.T) {
	var calls int
	svc := testableAuthProbeService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		calls++
		return jsonResponse(http.StatusOK, `{}`), nil
	}})
	account := testableAuthProbeAccount()
	accountTestableAuthCache.Delete(account.ID)
	t.Cleanup(func() { accountTestableAuthCache.Delete(account.ID) })

	svc.probeAccountAuthVerdict(context.Background(), account)
	invalidateAccountTestableAuthCache(account.ID)
	svc.probeAccountAuthVerdict(context.Background(), account)
	require.Equal(t, 2, calls)
}
