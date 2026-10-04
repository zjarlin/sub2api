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
			gotStatusAuth = true
			require.Empty(t, r.Header.Get("X-Login-Owner"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accounts": []map[string]any{
					{"uid": "u1", "nickname": "n1", "credits": 1450, "checkin": map[string]any{"at": "2026-10-04T09:00:00Z", "status": "ok", "credits": 1450, "delta": 100}},
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
	require.Equal(t, int64(2250), overview.TotalCredits)
	require.Len(t, overview.Accounts, 2)
	require.Equal(t, "u1", overview.Accounts[0].UID)
	require.Equal(t, int64(1450), overview.Accounts[0].Credits)
	require.NotNil(t, overview.Accounts[0].Checkin)
	require.Equal(t, int64(100), overview.Accounts[0].Checkin.Delta)
	require.Len(t, overview.Accounts[0].Checkins, 2)
	// 无历史账号返回空切片而非 nil（前端免判空）。
	require.Empty(t, overview.Accounts[1].Checkins)
	require.NotNil(t, overview.Accounts[1].Checkins)
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
