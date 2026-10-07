//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 上游按调用来源国家/地区拦截时回结构化 403（opencode zen/go 实测：
// code=unsupported_country_region_territory / type=request_forbidden）。这与
// HTML 403 同属端点/链路级响应：不构成账号凭据失效的证据，不能参与账号级处罚。
const openAI403RegionRestrictedBody = `{"error":{"code":"unsupported_country_region_territory",` +
	`"message":"Country, region, or territory not supported","param":null,` +
	`"type":"request_forbidden"},"model":"gpt-6-luna"}`

func TestHandleUpstreamError_OpenAIRegionRestricted403DoesNotPenalizeAccount(t *testing.T) {
	h := newOpenAI403TestHarness(t, 501, 1)

	shouldDisable := h.handle(openAI403RegionRestrictedBody)

	require.False(t, shouldDisable, "地区受限 403 不得判定账号应下线")
	h.requireNoAccountPenalty(t)
}

// 重复的地区受限 403 不能踩满连续 403 阈值把账号永久禁用（#region-403 事故根因）。
func TestHandleUpstreamError_OpenAIRegionRestricted403RepeatedNeverEscalates(t *testing.T) {
	h := newOpenAI403TestHarness(t, 502, 1, 2, 3, 4, 5)

	for i := 0; i < openAI403DisableThreshold+2; i++ {
		require.False(t, h.handle(openAI403RegionRestrictedBody),
			"第 %d 次地区受限 403 仍不得判定账号应下线", i+1)
	}

	h.requireNoAccountPenalty(t)
}

// opencode_go 通过 IsOpenCodeGo() 走进 handleOpenAI403，同样必须豁免。
func TestHandleUpstreamError_OpenCodeGoRegionRestricted403DoesNotPenalizeAccount(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &countingOpenAI403CounterCache{}
	blocker := &runtimeBlockRecorder{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc.SetOpenAI403CounterCache(counter)
	svc.SetAccountRuntimeBlocker(blocker)
	account := &Account{ID: 856, Platform: PlatformOpenCodeGo, Type: AccountTypeAPIKey}

	shouldDisable := svc.HandleUpstreamError(
		context.Background(), account, http.StatusForbidden, http.Header{},
		[]byte(openAI403RegionRestrictedBody),
	)

	require.False(t, shouldDisable)
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, 0, repo.tempCalls)
	require.Empty(t, blocker.accounts)
	require.Equal(t, 0, counter.increments)
}

// 不变式守卫：真正的账号级结构化 403（无地区编码）必须原样保留处罚链路，
// 避免上面的放行逻辑写宽后放过真实封号。
func TestHandleUpstreamError_NonRegionStructured403StillPenalizes(t *testing.T) {
	h := newOpenAI403TestHarness(t, 503, 1)

	require.True(t, h.handle(`{"error":{"message":"Your account is not authorized"}}`))
	require.Equal(t, 1, h.counter.increments)
	require.Equal(t, 1, h.repo.tempCalls)
	require.Equal(t, 0, h.repo.setErrorCalls)
}

func TestIsOpenAIRegionRestricted403(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		body string
		want bool
	}{
		{"structured_code", "", openAI403RegionRestrictedBody, true},
		{"response_wrapped_code", "", `{"response":{"error":{"code":"unsupported_country_region_territory"}}}`, true},
		{"message_only_upper", "Country, Region, Or Territory Not Supported", `{}`, true},
		{"other_structured_403", "", `{"error":{"message":"Your account is not authorized"}}`, false},
		{"html_body", "", openAI403HTMLBody, false},
		{"plain_forbidden", "Forbidden", "Forbidden", false},
		{"empty", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isOpenAIRegionRestricted403(tc.msg, []byte(tc.body)))
		})
	}
}
