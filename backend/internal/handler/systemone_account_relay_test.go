package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/stretchr/testify/require"
)

func TestSystemOnePlatformForModel(t *testing.T) {
	// model 名同时充当平台选择器，与 /v1/systemone 的既有约定一致。
	// 取值集合必须与 jev_api.IsKnownSystemOneModel 保持一致。
	layaModels := []string{"laya", "laya-english", "laya-multilingual"}
	for _, model := range layaModels {
		require.Equal(t, service.PlatformLaya, systemOnePlatformForModel(model), "model=%s", model)
	}
	require.Equal(t, service.PlatformJev, systemOnePlatformForModel("typesafe/jev"))
}

func TestSystemOnePlatformForModelNonLayaFallsBackToJev(t *testing.T) {
	// 非 laya 前缀一律按 JEV 处理；未知模型名已由 jev_api.ReadModel 在上游拒绝，
	// 这里不再重复判断，避免两处白名单漂移。
	require.Equal(t, service.PlatformJev, systemOnePlatformForModel("jev-1"))
}
