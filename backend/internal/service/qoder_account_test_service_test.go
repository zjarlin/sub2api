//go:build unit

package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestQoderAccountTestUsesOAuthTokenAndSelectedMode(t *testing.T) {
	for _, mode := range []string{AccountTestModeQoderCommitMessage, AccountTestModeDefault} {
		t.Run(mode, func(t *testing.T) {
			account := &Account{
				ID: 855, Platform: PlatformQoder, Type: AccountTypeOAuth, Concurrency: 1,
				Credentials: map[string]any{
					"access_token": "device-token", "api_protocol": APIProtocolChatCompletions,
					"model_mapping": map[string]any{"qwen3.8-max": "qmodel_38max"},
				},
			}
			svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
			ctx, recorder := newTestContext()
			err := svc.TestAccountConnection(ctx, account.ID, "qwen3.8-max", "", mode)
			require.NoError(t, err)
			require.Len(t, upstream.requests, 1)
			req := upstream.requests[0]
			require.Equal(t, QoderChatCompletionsURL(), req.URL.String())
			require.Equal(t, "Bearer device-token", req.Header.Get("Authorization"))
			require.Equal(t, "qmodel_38max", gjson.GetBytes(upstream.lastBody, "model").String())
			require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
			if mode == AccountTestModeQoderCommitMessage {
				require.NotEmpty(t, req.Header.Get("X-Request-ID"))
				require.NotEmpty(t, req.Header.Get("X-Session-ID"))
				require.Equal(t, "qoder-ide", gjson.GetBytes(upstream.lastBody, "metadata.context.client_type").String())
				require.Contains(t, gjson.GetBytes(upstream.lastBody, "messages.1.content").String(), QoderCommitMessageSampleDiff)
			}
		})
	}
}

func TestQoderAccountTestSurfacesUpstreamQuotaError(t *testing.T) {
	account := &Account{ID: 855, Platform: PlatformQoder, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "device-token"}}
	svc, upstream := adaptiveCNAccountTestService(account, newJSONResponse(402, `{"code":116,"error":"quota exceeded"}`))
	ctx, recorder := newTestContext()
	err := svc.TestAccountConnection(ctx, account.ID, "qmodel_38max", "", AccountTestModeQoderCommitMessage)
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
	require.Contains(t, recorder.Body.String(), "returned 402")
	require.Contains(t, recorder.Body.String(), "quota exceeded")
	require.NotContains(t, recorder.Body.String(), "No API key available")
	require.NotContains(t, recorder.Body.String(), `"type":"test_complete"`)
}

func TestQoderBuiltinModelCatalogReturnsOfficialIDsAndWarns(t *testing.T) {
	account := &Account{
		ID: 855, Platform: PlatformQoder, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "device-token"},
	}
	account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source: "upstream", SyncedAt: "2026-09-28T01:50:22Z",
		Models: []string{"qmodel_38max", "qfmodel"},
	})
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Source: "upstream",
		Models: map[string]UpstreamModelMetadata{
			"qmodel_38max": {ID: "qmodel_38max", DisplayName: "Qwen3.8-Max"},
		},
	})

	catalog := QoderBuiltinModelCatalog(account)
	// 已有上游目录时，模型列表严格以目录为准，内置候选不得扩大可用范围。
	require.Equal(t, []string{"qmodel_38max", "qfmodel"}, catalog.Models)
	require.Equal(t, "Qwen3.8-Max", catalog.Metadata["qmodel_38max"].DisplayName)
	require.Len(t, catalog.Warnings, 1)
	require.Equal(t, QoderBuiltinCatalogWarningCode, catalog.Warnings[0].Code)

	ids := QoderAccountModelIDs(account)
	require.Equal(t, []string{"qmodel_38max", "qfmodel"}, ids)
}

func TestQoderBuiltinModelCatalogWithoutSnapshotKeepsOfficialDefaults(t *testing.T) {
	account := &Account{ID: 855, Platform: PlatformQoder, Type: AccountTypeOAuth}
	catalog := QoderBuiltinModelCatalog(account)
	require.Equal(t, DefaultQoderModelIDs(), catalog.Models)
	// 无快照时内置目录自带给定的展示元数据，前端仍能显示可读的模型名。
	require.Len(t, catalog.Metadata, len(DefaultQoderModelIDs()))
	require.Equal(t, "Qwen3.8-Max", catalog.Metadata["qmodel_38max"].DisplayName)
	require.Equal(t, QoderBuiltinCatalogWarningCode, catalog.Warnings[0].Code)
}

func TestQoderQuotaDiagnosisExplainsPlanAndQuota(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v3/user/status", r.URL.Path)
		require.Equal(t, "Bearer device-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"userType":"personal_standard","plan":"PLAN_TIER_FREE","userTag":"Free","quota":0,"isQuotaExceeded":true}`))
	}))
	defer server.Close()
	t.Setenv("QODER_OPENAPI_BASE_URL", server.URL)

	diagnosis := qoderQuotaDiagnosis(t.Context(), "device-token")
	require.Contains(t, diagnosis, "plan=PLAN_TIER_FREE")
	require.Contains(t, diagnosis, "tag=Free")
	require.Contains(t, diagnosis, "quota exhausted")
}

func TestQoderQuotaDiagnosisIsSilentWhenStatusUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("QODER_OPENAPI_BASE_URL", server.URL)

	// 诊断失败不能改变原始 402 语义，也不能拼接空诊断。
	require.Empty(t, qoderQuotaDiagnosis(t.Context(), "device-token"))
}
