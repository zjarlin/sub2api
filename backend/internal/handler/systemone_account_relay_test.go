package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/stretchr/testify/require"
)

func TestSystemOnePlatformForModel(t *testing.T) {
	// model 名同时充当平台选择器，与 /v1/systemone 的既有约定一致。
	// 取值集合必须与 jev_api.IsKnownSystemOneModel 保持一致。
	layaModels := []string{"laya", "laya-english", "laya-multilingual"}
	for _, model := range layaModels {
		require.Equal(t, service.SystemOneProviderLaya, systemOneProviderForModel(model), "model=%s", model)
	}
	require.Equal(t, service.SystemOneProviderJev, systemOneProviderForModel("typesafe/jev"))
}

func TestSystemOnePlatformForModelNonLayaFallsBackToJev(t *testing.T) {
	// 非 laya 前缀一律按 JEV 处理；未知模型名已由 jev_api.ReadModel 在上游拒绝，
	// 这里不再重复判断，避免两处白名单漂移。
	require.Equal(t, service.SystemOneProviderJev, systemOneProviderForModel("jev-1"))
}

func TestSystemOneSchedulingContextSelectsModelPlatform(t *testing.T) {
	for _, model := range []string{"laya", "typesafe/jev"} {
		_ = systemOneProviderForModel(model)
		ctx := systemOneSchedulingContext(context.Background())
		require.Equal(t, service.PlatformSystemOne, ctx.Value(ctxkey.ForcePlatform))
	}
}

func TestSystemOneFallbackPlatformOnlyForJev(t *testing.T) {
	// 默认 JEV 不可用时隐式回退 Laya；显式 Laya 请求永不反向回退。
	fallback, ok := systemOneFallbackProvider(service.SystemOneProviderJev)
	require.True(t, ok)
	require.Equal(t, service.SystemOneProviderLaya, fallback)

	_, ok = systemOneFallbackProvider(service.SystemOneProviderLaya)
	require.False(t, ok)
	_, ok = systemOneFallbackProvider(service.PlatformOpenAI)
	require.False(t, ok)
}

func TestSystemOneRelayFailureShouldFallback(t *testing.T) {
	// 仅 JEV 请求、且属于可用性故障时才回退。
	require.True(t, systemOneRelayFailureShouldFallback(service.SystemOneProviderJev, 0, errors.New("timeout")))
	require.True(t, systemOneRelayFailureShouldFallback(service.SystemOneProviderJev, http.StatusBadGateway, nil))
	require.True(t, systemOneRelayFailureShouldFallback(service.SystemOneProviderJev, http.StatusInternalServerError, nil))
	require.True(t, systemOneRelayFailureShouldFallback(service.SystemOneProviderJev, http.StatusServiceUnavailable, nil))
	require.True(t, systemOneRelayFailureShouldFallback(service.SystemOneProviderJev, http.StatusTooManyRequests, nil))

	// 4xx 是请求本身的问题，回退会掩盖真实错误。
	require.False(t, systemOneRelayFailureShouldFallback(service.SystemOneProviderJev, http.StatusBadRequest, nil))
	require.False(t, systemOneRelayFailureShouldFallback(service.SystemOneProviderJev, http.StatusUnauthorized, nil))
	// 显式 Laya 请求永不触发回退。
	require.False(t, systemOneRelayFailureShouldFallback(service.SystemOneProviderLaya, http.StatusBadGateway, nil))
}
