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

func (r *upstreamModelRefreshRepoStub) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updates = updates
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
			Models:   []string{"upstream-good"},
		}},
	}

	require.True(t, account.IsModelSupported("public-good"))
	require.False(t, account.IsModelSupported("public-bad"))

	account.Extra[UpstreamSupportedModelsExtraKey] = UpstreamSupportedModelsSnapshot{
		Source:   "upstream",
		SyncedAt: now.Add(-upstreamSupportedModelsFreshness - time.Minute).Format(time.RFC3339),
		Models:   []string{"upstream-good"},
	}
	require.True(t, account.IsModelSupported("public-bad"), "stale catalogs must fail open")
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

func TestDeterministicUnsupportedModelAlwaysFailsOver(t *testing.T) {
	svc := &OpenAIGatewayService{}
	require.True(t, svc.shouldFailoverOpenAIUpstreamResponse(
		http.StatusBadRequest,
		"The model gpt-missing is not supported",
		[]byte(`{"error":{"code":"unsupported_model","message":"The model gpt-missing is not supported"}}`),
	))
	require.False(t, svc.shouldFailoverOpenAIUpstreamResponse(
		http.StatusBadRequest,
		"Parameter tools is not supported for this model",
		[]byte(`{"error":{"message":"Parameter tools is not supported for this model"}}`),
	))
}
