//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// TestBuiltinAdapterCheckinOverviewMergesStatusAndHistory 校验 relay 只从服务端配置取
// 目标与密钥，并把 /status 的积分与 /checkins 的历史合并为账号页可用结构。
func TestBuiltinAdapterCheckinOverviewMergesStatusAndHistory(t *testing.T) {
	var gotStatusAuth, gotCheckinsAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer builtin-workbuddy-key", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/status":
			require.Equal(t, "true", r.URL.Query().Get("refresh_credits"))
			gotStatusAuth = true
			require.Empty(t, r.Header.Get("X-Login-Owner"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accounts": []map[string]any{
					{"uid": "u1", "nickname": "n1", "credits": 79.75, "checkin": map[string]any{"at": "2026-10-04T09:00:00Z", "status": "ok", "credits": 1450, "delta": 100}},
					{"uid": "u2", "nickname": "n2", "credits": 800},
				},
				"total": 2,
			})
		case "/checkins":
			gotCheckinsAuth = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"checkins": map[string]any{
					"u1": []map[string]any{
						{"at": "2026-10-04T09:00:00Z", "status": "ok", "credits": 1450, "delta": 100},
						{"at": "2026-10-03T09:00:00Z", "status": "ok", "credits": 1350, "delta": 100},
					},
				},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, WorkbuddyURL: server.URL + "/v1", WorkbuddyKey: "builtin-workbuddy-key"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })

	overview, err := BuiltinAdapterCheckinOverviewForPlatform(context.Background(), PlatformWorkbuddy)
	require.NoError(t, err)
	require.True(t, gotStatusAuth)
	require.True(t, gotCheckinsAuth)
	require.Equal(t, PlatformWorkbuddy, overview.Platform)
	require.Equal(t, 879.75, overview.TotalCredits)
	require.Len(t, overview.Accounts, 2)
	require.Equal(t, "u1", overview.Accounts[0].UID)
	require.Equal(t, 79.75, overview.Accounts[0].Credits)
	require.NotNil(t, overview.Accounts[0].Checkin)
	require.Equal(t, int64(100), overview.Accounts[0].Checkin.Delta)
	require.Len(t, overview.Accounts[0].Checkins, 2)
	// 无历史账号返回空切片而非 nil（前端免判空）。
	require.Empty(t, overview.Accounts[1].Checkins)
	require.NotNil(t, overview.Accounts[1].Checkins)
}

// 手动签到必须把绑定的 uid 传给 sidecar，并回读该账号的最新积分与历史；
// 刷新 /status 时带 uid，避免把所有账号的余额都重查一遍。
func TestBuiltinAdapterManualCheckinTriggersSidecarAndReturnsFreshOverview(t *testing.T) {
	var gotBody builtinAdapterCheckinRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer builtin-workbuddy-key", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/checkin":
			require.Equal(t, http.MethodPost, r.Method)
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"uid": "mine", "status": "ok"}}})
		case "/status":
			require.Equal(t, "true", r.URL.Query().Get("refresh_credits"))
			require.Equal(t, "mine", r.URL.Query().Get("uid"))
			_ = json.NewEncoder(w).Encode(map[string]any{"accounts": []map[string]any{
				{"uid": "mine", "nickname": "n1", "credits": 99.5, "checkin": map[string]any{"at": "2026-10-18T09:00:00Z", "status": "ok", "credits": 99, "delta": 9}},
			}})
		case "/checkins":
			_ = json.NewEncoder(w).Encode(map[string]any{"checkins": map[string]any{
				"mine": []map[string]any{{"at": "2026-10-18T09:00:00Z", "status": "ok", "credits": 99, "delta": 9}},
			}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, WorkbuddyURL: server.URL + "/v1", WorkbuddyKey: "builtin-workbuddy-key"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })

	account := &Account{Platform: PlatformWorkbuddy, Extra: map[string]any{"builtin_adapter_uid": "mine"}}
	overview, err := BuiltinAdapterManualCheckinForAccount(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{"mine"}, gotBody.UIDs)
	require.Equal(t, 99.5, overview.TotalCredits)
	require.Len(t, overview.Accounts, 1)
	require.NotNil(t, overview.Accounts[0].Checkin)
	require.Equal(t, int64(9), overview.Accounts[0].Checkin.Delta)
}

// 平台级签到：不带 uids 触发 sidecar 全池签到，并回读该平台的最新概览。
func TestBuiltinAdapterPlatformCheckinTriggersPoolAndReturnsOverview(t *testing.T) {
	var gotBody builtinAdapterCheckinRequest
	var checkinCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/checkin":
			require.Equal(t, http.MethodPost, r.Method)
			checkinCalls++
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
				{"uid": "u1", "status": "ok"}, {"uid": "u2", "status": "already"},
			}})
		case "/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"accounts": []map[string]any{
				{"uid": "u1", "credits": 10},
				{"uid": "u2", "credits": 20},
			}})
		case "/checkins":
			_ = json.NewEncoder(w).Encode(map[string]any{"checkins": map[string]any{}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, WorkbuddyURL: server.URL, WorkbuddyKey: "key"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })

	overview, err := BuiltinAdapterPlatformCheckin(context.Background(), PlatformWorkbuddy)
	require.NoError(t, err)
	require.Equal(t, 1, checkinCalls)
	// 平台级签到必须保持「全池」语义：uids 为空，不能顺手退化成单账号。
	require.Empty(t, gotBody.UIDs)
	require.Equal(t, 30.0, overview.TotalCredits)
	require.Len(t, overview.Accounts, 2)
}

// 不支持签到的平台直接拒绝，不去打上游。
func TestBuiltinAdapterPlatformCheckinRejectsUnsupportedPlatform(t *testing.T) {
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, WorkbuddyURL: "http://127.0.0.1:1", WorkbuddyKey: "k"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	_, err := BuiltinAdapterPlatformCheckin(context.Background(), PlatformOpenAI)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Equal(t, "BUILTIN_CHECKIN_UNSUPPORTED", infraerrors.Reason(err))
}

// 上游已有签到在跑时返回 409，前端应提示稍后刷新而不是当成签到失败。
func TestBuiltinAdapterManualCheckinMapsBusyToConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"checkin_busy"}}`))
	}))
	defer server.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, WorkbuddyURL: server.URL, WorkbuddyKey: "key"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })

	account := &Account{Platform: PlatformWorkbuddy, Extra: map[string]any{"builtin_adapter_uid": "mine"}}
	_, err := BuiltinAdapterManualCheckinForAccount(context.Background(), account)
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))
	require.Equal(t, "BUILTIN_CHECKIN_BUSY", infraerrors.Reason(err))
}

// 签到是写操作：未绑定 uid 时不能退化成签整个共享池。
func TestBuiltinAdapterManualCheckinRequiresBoundUID(t *testing.T) {
	_, err := BuiltinAdapterManualCheckinForAdminAccount(context.Background(), &Account{Platform: PlatformWorkbuddy})
	require.ErrorContains(t, err, "未绑定上游账号")
}

func TestBuiltinAdapterCheckinOverviewRejectsUnsupportedPlatformAndDisabledAdapter(t *testing.T) {
	SetBuiltinAdapterConfig(nil)
	_, err := BuiltinAdapterCheckinOverviewForPlatform(context.Background(), PlatformWorkbuddy)
	require.Error(t, err)

	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, WorkbuddyURL: "http://sub2api-workbuddy:7863", WorkbuddyKey: "k"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	_, err = BuiltinAdapterCheckinOverviewForPlatform(context.Background(), PlatformOpenAI)
	require.Error(t, err)
}

// TestBuiltinAdapterCheckinOverviewSurfacesUpstreamError 上游 5xx 时归一化为错误，不透传原始报文。
func TestBuiltinAdapterCheckinOverviewSurfacesUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal-token-must-not-leak"))
	}))
	defer server.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, WorkbuddyURL: server.URL, WorkbuddyKey: "k"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })

	_, err := BuiltinAdapterCheckinOverviewForPlatform(context.Background(), PlatformWorkbuddy)
	require.Error(t, err)
	require.NotContains(t, strings.ToLower(err.Error()), "must-not-leak")
}

// 账号查询必须按稳定 UID 隔离，昵称相同也不得混入其他人的余额与历史。
func TestBuiltinAdapterCheckinOverviewForAccountFiltersByUID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			_, _ = w.Write([]byte(`{"accounts":[{"uid":"mine","nickname":"same","credits":79.75},{"uid":"other","nickname":"same","credits":23050}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"checkins":{"mine":[],"other":[{"status":"ok","credits":23050}]}}`))
	}))
	defer server.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, WorkbuddyURL: server.URL, WorkbuddyKey: "key"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	account := &Account{Platform: PlatformWorkbuddy, Extra: map[string]any{"builtin_adapter_uid": "mine"}}
	overview, err := BuiltinAdapterCheckinOverviewForAccount(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, 79.75, overview.TotalCredits)
	require.Len(t, overview.Accounts, 1)
	require.Equal(t, "mine", overview.Accounts[0].UID)
	account.Extra["builtin_adapter_uid"] = "missing"
	_, err = BuiltinAdapterCheckinOverviewForAccount(context.Background(), account)
	require.Error(t, err)
}

func TestBuiltinAdapterCheckinOverviewForUnboundAccountDoesNotQueryPool(t *testing.T) {
	_, err := BuiltinAdapterCheckinOverviewForAccount(context.Background(), &Account{Platform: PlatformWorkbuddy})
	require.ErrorContains(t, err, "未绑定上游账号")
}

func TestBuiltinAdapterCheckinAdminCanReuseExistingLogins(t *testing.T) {
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			_, _ = w.Write([]byte(`{"accounts":[{"uid":"mine","credits":79.75},{"uid":"other","credits":23050}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"checkins":{}}`))
	}))
	defer adapter.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, WorkbuddyURL: adapter.URL, WorkbuddyKey: "key"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	account := &Account{Platform: PlatformWorkbuddy}

	overview, err := BuiltinAdapterCheckinOverviewForAdminAccount(context.Background(), account)
	require.NoError(t, err)
	require.True(t, overview.BindingRequired)
	require.Zero(t, overview.TotalCredits)
	require.Len(t, overview.Accounts, 2)
	// 管理员恢复关联的能力不能放宽普通用户的账号隔离。
	_, err = BuiltinAdapterCheckinOverviewForAccount(context.Background(), account)
	require.ErrorContains(t, err, "未绑定上游账号")

	account.Extra = map[string]any{"builtin_adapter_uid": "mine"}
	overview, err = BuiltinAdapterCheckinOverviewForAdminAccount(context.Background(), account)
	require.NoError(t, err)
	require.False(t, overview.BindingRequired)
	require.Len(t, overview.Accounts, 1)
	require.Equal(t, 79.75, overview.TotalCredits)
}
