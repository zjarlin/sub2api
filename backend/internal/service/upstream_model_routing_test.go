package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type upstreamModelRefreshRepoStub struct {
	AccountRepository
	accounts []Account
	updates  map[string]any
}

func (r *upstreamModelRefreshRepoStub) ListActive(context.Context) ([]Account, error) {
	return r.accounts, nil
}

func (r *upstreamModelRefreshRepoStub) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return r.accounts, nil
}

func (r *upstreamModelRefreshRepoStub) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.updates = updates
	for i := range r.accounts {
		if r.accounts[i].ID != id {
			continue
		}
		if r.accounts[i].Extra == nil {
			r.accounts[i].Extra = make(map[string]any)
		}
		for key, value := range updates {
			r.accounts[i].Extra[key] = value
		}
	}
	return nil
}

func TestAccountIsModelSupportedUsesFreshUpstreamCatalog(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"public-good": "upstream-good",
			"public-bad":  "upstream-bad",
		}},
		Extra: map[string]any{UpstreamSupportedModelsExtraKey: UpstreamSupportedModelsSnapshot{
			Source:   "upstream",
			SyncedAt: now.Format(time.RFC3339),
			Models:   []string{"upstream-good", "public-new"},
		}},
	}

	require.True(t, account.IsModelSupported("public-good"))
	require.False(t, account.IsModelSupported("public-new"), "上游目录不能扩大显式白名单")
	require.False(t, account.IsModelSupported("public-bad"))

	account.Extra[UpstreamSupportedModelsExtraKey] = UpstreamSupportedModelsSnapshot{
		Source:   "upstream",
		SyncedAt: now.Add(-upstreamSupportedModelsFreshness - time.Minute).Format(time.RFC3339),
		Models:   []string{"upstream-good", "public-new"},
	}
	require.False(t, account.IsModelSupported("public-new"), "过期目录同样不能扩大显式白名单")
	require.True(t, account.IsModelSupported("public-good"), "过期目录中的已配置模型仍可使用")
	require.True(t, account.IsModelSupported("public-bad"), "stale catalogs must fail open")
	require.False(t, account.IsModelSupported("public-unknown"), "stale absence must fall back to the configured mapping")
}

func TestUpstreamModelRefreshPersistsPositiveCapability(t *testing.T) {
	account := Account{
		ID:          42,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"api_key": "key", "base_url": "https://provider.example/v1"},
	}
	repo := &upstreamModelRefreshRepoStub{accounts: []Account{account}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(`{"models":[{
			"id":"gpt-supported","reasoning":false,"input_modalities":["text"],"context_window":128000
		}]}`)),
	}}
	syncer := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
	refresher := NewUpstreamModelRefreshService(repo, syncer)

	refresher.refresh(context.Background())

	raw, ok := repo.updates[UpstreamSupportedModelsExtraKey]
	require.True(t, ok)
	snapshot, ok := raw.(UpstreamSupportedModelsSnapshot)
	require.True(t, ok)
	require.Equal(t, []string{"gpt-supported"}, snapshot.Models)
}

func TestOpenAIModelSuccessAffinityIsModelScoped(t *testing.T) {
	stats := newOpenAIAccountRuntimeStats()
	for range 8 {
		stats.reportModel(7, "gpt-good", true, nil)
		stats.reportModel(7, "gpt-bad", false, nil)
	}

	goodErrorRate, _, _, goodSamples := stats.snapshotModel(7, "gpt-good")
	badErrorRate, _, _, badSamples := stats.snapshotModel(7, "gpt-bad")
	require.Equal(t, int64(8), goodSamples)
	require.Equal(t, int64(8), badSamples)
	require.Greater(t,
		openAIModelSuccessAffinity(goodErrorRate, goodSamples),
		openAIModelSuccessAffinity(badErrorRate, badSamples),
	)
	require.Equal(t, 0.5, openAIModelSuccessAffinity(0, 0), "unknown accounts must remain neutral")
}

func TestAutoModelPassthroughFailurePolicy(t *testing.T) {
	ctx, err := (*SettingService)(nil).BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	svc := &OpenAIGatewayService{}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for _, tc := range []struct {
		name          string
		status        int
		body          string
		autoRetry     bool
		explicitRetry bool
	}{
		{"credential", http.StatusUnauthorized, `{"error":{"message":"Invalid API key"}}`, true, false},
		{"edge_access", http.StatusForbidden, "error code: 1010\n", true, false},
		{"capacity", http.StatusServiceUnavailable, `{"error":{"message":"Service unavailable"}}`, true, true},
		{"malformed", http.StatusBadRequest, `{"error":{"message":"input is required"}}`, false, false},
		{"context", http.StatusBadRequest, `{"error":{"code":"context_length_exceeded","message":"Maximum context length exceeded"}}`, false, false},
		{"cyber_policy", http.StatusServiceUnavailable, `{"error":{"code":"cyber_policy","message":"Request rejected"}}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.autoRetry, svc.shouldFailoverOpenAIPassthroughRequest(ctx, account, tc.status, []byte(tc.body)))
			require.Equal(t, tc.explicitRetry, svc.shouldFailoverOpenAIPassthroughRequest(context.Background(), account, tc.status, []byte(tc.body)))
		})
	}
}

func TestDeterministicUnsupportedModelManagedAccountFailsOver(t *testing.T) {
	svc := &OpenAIGatewayService{accountRepo: &modelNotFoundManagedAccountRepo{}}
	require.True(t, svc.shouldFailoverOpenAIUpstreamResponse(
		newOpenAIUpstreamErrorTestAccount(),
		http.StatusBadRequest,
		"The model gpt-missing is not supported",
		[]byte(`{"error":{"code":"unsupported_model","message":"The model gpt-missing is not supported"}}`),
	))
	require.False(t, svc.shouldFailoverOpenAIUpstreamResponse(
		newOpenAIUpstreamErrorTestAccount(),
		http.StatusBadRequest,
		"Parameter tools is not supported for this model",
		[]byte(`{"error":{"message":"Parameter tools is not supported for this model"}}`),
	))
	exactBody := []byte(`{"error":{"code":400,"message":"\"auto\" tool choice requires --enable-auto-tool-choice and --tool-call-parser to be set","type":"BadRequestError"}}`)
	require.True(t, isOpenAIToolCapabilityError(http.StatusBadRequest, "", exactBody))
	require.False(t, svc.shouldFailoverOpenAIUpstreamResponse(
		newOpenAIUpstreamErrorTestAccount(),
		http.StatusBadRequest,
		extractUpstreamErrorMessage(exactBody),
		exactBody,
	))
}

func TestAutoModelToolCapabilityFailureExcludesNextToolRequest(t *testing.T) {
	account := Account{
		ID:          42,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"public-tool": "upstream-tool"}},
	}
	repo := &upstreamModelRefreshRepoStub{accounts: []Account{account}}
	openAI := &OpenAIGatewayService{accountRepo: repo}
	ctx, err := (&SettingService{}).BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	toolBody := []byte(`{"model":"auto","tools":[{"type":"function","function":{"name":"shell"}}]}`)
	ctx = WithAutoModelRequestCapabilities(ctx, toolBody)
	errorBody := []byte(`{"error":{"code":400,"message":"\"auto\" tool choice requires --enable-auto-tool-choice and --tool-call-parser to be set","type":"BadRequestError"}}`)

	failover := openAI.newAutoModelCapabilityMismatchFailoverError(
		ctx,
		&account,
		"upstream-tool",
		http.StatusBadRequest,
		http.Header{},
		errorBody,
		extractUpstreamErrorMessage(errorBody),
	)

	require.NotNil(t, failover)
	key := autoModelToolCapabilityBlockKey("upstream-tool")
	require.Contains(t, repo.updates, key)
	account = repo.accounts[0]
	require.True(t, account.AutoModelToolCapabilityBlocked("public-tool", time.Now()))

	scheduler := &defaultOpenAIAccountScheduler{service: openAI}
	compatible, reason := scheduler.isAccountRequestCompatibleReason(ctx, &account, OpenAIAccountScheduleRequest{
		Platform:       PlatformOpenAI,
		RequestedModel: "public-tool",
	})
	require.False(t, compatible)
	require.Equal(t, "auto_tool_capability_blocked", reason)

	gateway := &GatewayService{accountRepo: repo}
	groupID := int64(71)
	compatible, err = gateway.AutoModelAccountCompatible(ctx, &groupID, PlatformOpenAI, "public-tool", toolBody)
	require.NoError(t, err)
	require.False(t, compatible)

	plainBody := []byte(`{"model":"auto","input":"hello"}`)
	plainCtx := WithAutoModelRequestCapabilities(ctx, plainBody)
	compatible, reason = scheduler.isAccountRequestCompatibleReason(plainCtx, &account, OpenAIAccountScheduleRequest{
		Platform:       PlatformOpenAI,
		RequestedModel: "public-tool",
	})
	require.True(t, compatible, reason)
	compatible, err = gateway.AutoModelAccountCompatible(plainCtx, &groupID, PlatformOpenAI, "public-tool", plainBody)
	require.NoError(t, err)
	require.True(t, compatible)
}

func TestAutoModelToolCapabilityBlockUsesPassthroughWireModel(t *testing.T) {
	for _, globalMapping := range []map[string]string{nil, {"public-tool": "global-tool"}} {
		account := &Account{
			Platform:           PlatformOpenAI,
			Type:               AccountTypeAPIKey,
			Credentials:        map[string]any{"model_mapping": map[string]any{"public-tool": "mapped-tool"}},
			Extra:              map[string]any{"openai_passthrough": true},
			globalModelMapping: globalMapping,
		}
		wireModel := "public-tool"
		if globalMapping != nil {
			wireModel = globalMapping[wireModel]
		}
		now := time.Now().UTC()
		key, block, ok := newAutoModelToolCapabilityBlock(wireModel, now)
		require.True(t, ok)
		account.Extra[key] = block
		require.True(t, account.AutoModelToolCapabilityBlocked("public-tool", now))
		require.False(t, account.AutoModelToolCapabilityBlocked("another-tool", now))
		require.False(t, account.AutoModelToolCapabilityBlocked("public-tool", now.Add(autoModelToolCapabilityBlockDuration)))
	}
}
