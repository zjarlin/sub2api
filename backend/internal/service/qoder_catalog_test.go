//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseQoderCatalogKeepsAvailabilityAndEfforts(t *testing.T) {
	body := []byte(`{
		"chat": [
			{"key":"qfmodel","display_name":"Qwen3.8-Flash","enable":true,"is_free":true,"is_default":true,
			 "is_reasoning":true,"is_vl":true,"max_input_tokens":180000,"price_factor":0,
			 "context_config":{"200K":{"token_count":200000,"is_default":true},"1M":{"token_count":1000000}},
			 "thinking_config":{"enabled":{"efforts":{"low":{},"max":{"is_default":true}}}}},
			{"key":"smodel","display_name":"Sonus","enable":false,"is_free":false,"price_factor":8}
		],
		"inline": [{"key":"auto","enable":false,"is_vl":true}]
	}`)
	catalog, err := parseQoderCatalog(body)
	require.NoError(t, err)
	require.Equal(t, QoderCatalogSourceSigned, catalog.Source)

	chat := catalog.ChatModels()
	require.Len(t, chat, 2)
	require.Equal(t, "qfmodel", chat[0].Key)
	require.True(t, chat[0].Enable)
	require.True(t, chat[0].Free)
	require.True(t, chat[0].Default)
	require.Equal(t, int64(180000), chat[0].ContextWindow)
	require.Equal(t, []int64{200000, 1000000}, chat[0].ContextWindows)
	require.Equal(t, []string{"low", "max"}, chat[0].Efforts)
	require.Equal(t, "max", chat[0].DefaultEffort)
	require.False(t, chat[1].Enable)

	require.Len(t, catalog.Scenes["inline"], 1)
}

func TestParseQoderCatalogRejectsEmptyPayload(t *testing.T) {
	_, err := parseQoderCatalog([]byte(`{}`))
	require.Error(t, err)
}

func TestQoderSignedCatalogPersistsEnabledAndDisabledModels(t *testing.T) {
	repo := &catalogExtraRepo{}
	svc := &AccountTestService{accountRepo: repo}
	SetQoderCatalogFetcher(stubQoderCatalogFetcher{body: []byte(`{"chat":[
		{"key":"qfmodel","display_name":"Qwen3.8-Flash","enable":true,"is_free":true,"is_default":true},
		{"key":"smodel","display_name":"Sonus","enable":false}
	]}`)})
	t.Cleanup(func() { SetQoderCatalogFetcher(nil) })

	account := &Account{ID: 855, Platform: PlatformQoder, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "device-token", "uid": "uid-1"}}
	catalog, err := svc.syncQoderSignedCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{"qfmodel", "smodel"}, catalog.Models)
	require.Equal(t, QoderCatalogSyncedWarningCode, catalog.Warnings[0].Code)
	require.Len(t, repo.updates, 1)

	require.Equal(t, []string{"qfmodel"}, QoderAccountModelIDs(account))
	models := QoderAccountModels(account)
	require.Len(t, models, 2)
	require.Equal(t, "Qwen3.8-Flash", models[0].DisplayName)
	require.Equal(t, "qfmodel", QoderDefaultTestModel(account))
}

func TestQoderSignedCatalogFallsBackWhenFetcherFails(t *testing.T) {
	SetQoderCatalogFetcher(stubQoderCatalogFetcher{err: context.DeadlineExceeded})
	t.Cleanup(func() { SetQoderCatalogFetcher(nil) })

	svc := &AccountTestService{}
	account := &Account{ID: 855, Platform: PlatformQoder, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "device-token"}}
	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err, "sidecar failure must fall back to the builtin catalog")
	require.Equal(t, DefaultQoderModelIDs(), catalog.Models)
	require.Equal(t, QoderBuiltinCatalogWarningCode, catalog.Warnings[0].Code)
}

func TestQoderBuiltinCatalogCoversChatScene(t *testing.T) {
	ids := DefaultQoderModelIDs()
	require.Len(t, ids, 15)
	require.Contains(t, ids, "dfmodel")
	require.Contains(t, ids, "qfmodel")
	require.Equal(t, "qfmodel", ids[6])

	account := &Account{ID: 1, Platform: PlatformQoder}
	catalog := QoderBuiltinModelCatalog(account)
	require.Equal(t, ids, catalog.Models)
	require.Equal(t, "DeepSeek-Flash", catalog.Metadata["dfmodel"].DisplayName)
	require.Equal(t, int64(1000000), catalog.Metadata["dfmodel"].ContextWindow)
}

func TestQoderDefaultTestModelPrefersFreeEnabledModel(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformQoder}
	account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source: QoderCatalogSourceSigned, Models: []string{"auto", "qmodel_38max", "dfmodel"},
	})
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Source: QoderCatalogSourceSigned,
		Models: map[string]UpstreamModelMetadata{
			"auto":         {ID: "auto", DisplayName: "Auto", Enable: qoderBoolPtr(false)},
			"qmodel_38max": {ID: "qmodel_38max", DisplayName: "Qwen3.8-Max", Enable: qoderBoolPtr(false)},
			"dfmodel":      {ID: "dfmodel", DisplayName: "DeepSeek-Flash", Enable: qoderBoolPtr(true), Free: qoderBoolPtr(true)},
		},
	})
	require.Equal(t, []string{"dfmodel"}, QoderAccountModelIDs(account))
	require.Equal(t, "dfmodel", QoderDefaultTestModel(account))
}

func TestQoderAccountModelsExposeAvailability(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformQoder}
	account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source: QoderCatalogSourceSigned, Models: []string{"auto", "qfmodel", "dfmodel"},
	})
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Source: QoderCatalogSourceSigned,
		Models: map[string]UpstreamModelMetadata{
			"auto":    {ID: "auto", DisplayName: "Auto", Enable: qoderBoolPtr(false)},
			"qfmodel": {ID: "qfmodel", DisplayName: "Qwen3.8-Flash", Enable: qoderBoolPtr(true), Free: qoderBoolPtr(true), Default: qoderBoolPtr(true)},
			"dfmodel": {ID: "dfmodel", DisplayName: "DeepSeek-Flash", Enable: qoderBoolPtr(true), Free: qoderBoolPtr(true)},
		},
	})
	models := QoderAccountModels(account)
	require.Len(t, models, 3)
	byID := make(map[string]QoderAccountModel, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}
	// 未启用的模型仍要出现在列表里（显示套餐差异），但必须带 enable=false。
	require.NotNil(t, byID["auto"].Enable)
	require.False(t, *byID["auto"].Enable)
	require.Nil(t, byID["auto"].Free)
	require.True(t, *byID["qfmodel"].Enable)
	require.True(t, *byID["qfmodel"].Free)
	require.True(t, *byID["qfmodel"].Default)
	require.Equal(t, "Qwen3.8-Flash", byID["qfmodel"].DisplayName)
	require.True(t, *byID["dfmodel"].Enable)
}

func TestQoderAccountModelsLegacySnapshotHasNoAvailabilityFlags(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformQoder}
	account.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source: "upstream", Models: []string{"qmodel_38max", "qfmodel"},
	})
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Source: "upstream",
		Models: map[string]UpstreamModelMetadata{
			"qmodel_38max": {ID: "qmodel_38max", DisplayName: "Qwen3.8-Max"},
		},
	})
	models := QoderAccountModels(account)
	require.Len(t, models, 2)
	for _, model := range models {
		// 旧快照没有 enable 字段：不能把未知状态显示成不可用。
		require.Nil(t, model.Enable)
		require.Nil(t, model.Free)
		require.Nil(t, model.Default)
	}
	require.Equal(t, "Qwen3.8-Max", models[0].DisplayName)
}

func TestQoderSidecarFetcherInvokesScript(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fetch.mjs")
	require.NoError(t, os.WriteFile(script, []byte("process.exit(0)"), 0o600))
	fetcher := &QoderCatalogSidecarFetcher{ScriptPath: script}
	_, err := fetcher.FetchCatalog(context.Background(), "token", "uid")
	require.Error(t, err, "exit without output must be reported, not silently accepted")
}

// 文件队列模式：主进程写 req.json，侧车写 resp.json。
// 用 goroutine 模拟 serve.mjs，验证请求内容、响应解析与旧响应清理。
func TestQoderSidecarFetcherFileQueue(t *testing.T) {
	dir := t.TempDir()
	// 预置一个旧响应，必须被忽略（不能把它当成本次结果）。
	require.NoError(t, os.WriteFile(filepath.Join(dir, "resp.json"), []byte(`{"ok":true,"body":"stale"}`), 0o600))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "resp.json"), old, old))

	fetcher := &QoderCatalogSidecarFetcher{ScriptPath: "unused", QueueDir: dir, Timeout: 5 * time.Second}
	go func() {
		reqFile := filepath.Join(dir, "req.json")
		for i := 0; i < 100; i++ {
			raw, err := os.ReadFile(reqFile)
			if err == nil && len(raw) > 0 {
				var req map[string]string
				if json.Unmarshal(raw, &req) != nil || req["access_token"] != "token" || req["uid"] != "uid" {
					return
				}
				_ = os.WriteFile(filepath.Join(dir, "resp.json"), []byte(`{"ok":true,"body":"{\"chat\":[]}"}`), 0o600)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	body, err := fetcher.FetchCatalog(context.Background(), "token", "uid")
	require.NoError(t, err)
	require.JSONEq(t, `{"chat":[]}`, string(body))
	_, err = os.Stat(filepath.Join(dir, "resp.json"))
	require.True(t, os.IsNotExist(err), "consumed response must be removed")
}

func TestQoderSidecarFetcherFileQueuePropagatesSidecarError(t *testing.T) {
	dir := t.TempDir()
	fetcher := &QoderCatalogSidecarFetcher{ScriptPath: "unused", QueueDir: dir, Timeout: 5 * time.Second}
	go func() {
		reqFile := filepath.Join(dir, "req.json")
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(reqFile); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "resp.json"), []byte(`{"ok":false,"error":"HTTP 403: Signature invalid"}`), 0o600)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	_, err := fetcher.FetchCatalog(context.Background(), "token", "uid")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Signature invalid")
}

func TestQoderSidecarFetcherFileQueueTimeout(t *testing.T) {
	dir := t.TempDir()
	fetcher := &QoderCatalogSidecarFetcher{ScriptPath: "unused", QueueDir: dir, Timeout: 300 * time.Millisecond}
	_, err := fetcher.FetchCatalog(context.Background(), "token", "uid")
	require.Error(t, err)
	require.Contains(t, err.Error(), "timed out")
}

func TestResolveQoderCatalogUIDPrefersCredential(t *testing.T) {
	// 已有 uid 时不应访问网络。
	original := QoderUserInfoURL
	QoderUserInfoURL = "http://127.0.0.1:1/unreachable"
	t.Cleanup(func() { QoderUserInfoURL = original })

	account := &Account{Credentials: map[string]any{"access_token": "token", "uid": "cached-uid"}}
	uid, err := (&AccountTestService{}).resolveQoderCatalogUID(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "cached-uid", uid)
}

func TestResolveQoderCatalogUIDFallsBackToUserInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer dt-token", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"id":"6406f467-abf0-4394-9703-dfdff583d40a","name":"qoder-user"}`))
	}))
	t.Cleanup(server.Close)
	original := QoderUserInfoURL
	QoderUserInfoURL = server.URL
	t.Cleanup(func() { QoderUserInfoURL = original })

	account := &Account{Credentials: map[string]any{"access_token": "dt-token"}}
	uid, err := (&AccountTestService{}).resolveQoderCatalogUID(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "6406f467-abf0-4394-9703-dfdff583d40a", uid)
	require.Equal(t, uid, qoderAccountUID(account), "uid must be cached on the account for the signature")
}

func TestResolveQoderCatalogUIDReportsUserInfoFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid token"}`))
	}))
	t.Cleanup(server.Close)
	original := QoderUserInfoURL
	QoderUserInfoURL = server.URL
	t.Cleanup(func() { QoderUserInfoURL = original })

	account := &Account{Credentials: map[string]any{"access_token": "bad"}}
	_, err := (&AccountTestService{}).resolveQoderCatalogUID(context.Background(), account)
	require.Error(t, err)
	require.Contains(t, err.Error(), "HTTP 401")
}
