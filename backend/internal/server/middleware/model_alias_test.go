package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// modelAliasToggleRepo 可切换读失败的设置仓储，用于复现 DB 瞬时故障。
type modelAliasToggleRepo struct {
	mu      sync.Mutex
	values  map[string]string
	failing bool
}

func (r *modelAliasToggleRepo) Get(_ context.Context, key string) (*service.Setting, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failing {
		return nil, errors.New("the database system is in recovery mode")
	}
	value, ok := r.values[key]
	if !ok {
		return nil, service.ErrSettingNotFound
	}
	return &service.Setting{Key: key, Value: value}, nil
}

func (r *modelAliasToggleRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failing {
		return "", errors.New("the database system is in recovery mode")
	}
	value, ok := r.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (r *modelAliasToggleRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func (r *modelAliasToggleRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *modelAliasToggleRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func (r *modelAliasToggleRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func (r *modelAliasToggleRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
	return nil
}

func (r *modelAliasToggleRepo) setFailing(failing bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failing = failing
}

// DB 瞬时故障时文本入口必须继续服务：用最近一次成功读取的别名快照替代 503。
// 回归自 2026-10-17 06:37 的 "Model alias settings unavailable" 事故
// （Postgres 被 OOM 击杀后恢复期间，别名中间件把读库失败直接放大成网关 503）。
func TestGlobalModelAliasesSurvivesSettingsReadFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &modelAliasToggleRepo{values: map[string]string{
		service.SettingKeyModelAliases: `{"groups":[{"canonical":"deepseek-v4.1-flash","aliases":["deepseek/deepseek-v4.1-flash"]}]}`,
	}}
	settings := service.NewSettingService(repo, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}})
		c.Next()
	})
	router.Use(GlobalModelAliases(settings))
	router.POST("/v1/responses", func(c *gin.Context) {
		require.NotNil(t, service.ModelAliasesFromContext(c.Request.Context()))
		c.Status(http.StatusOK)
	})

	// 预热请求：成功读取并写下进程内快照。
	warm := httptest.NewRecorder()
	router.ServeHTTP(warm, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusOK, warm.Code)

	// DB 瞬时不可用：必须降级为 200 + 快照，而不是 503。
	repo.setFailing(true)
	degraded := httptest.NewRecorder()
	router.ServeHTTP(degraded, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusOK, degraded.Code, degraded.Body.String())
	require.NotContains(t, degraded.Body.String(), "Model alias settings unavailable")

	// DB 恢复后继续正常服务。
	repo.setFailing(false)
	recovered := httptest.NewRecorder()
	router.ServeHTTP(recovered, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusOK, recovered.Code)
}

// 冷启动且 DB 不可用时仍必须 503，保证不会在没有任何配置依据时放行请求。
func TestGlobalModelAliasesColdStartFailureStillReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &modelAliasToggleRepo{failing: true}
	settings := service.NewSettingService(repo, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}})
		c.Next()
	})
	router.Use(GlobalModelAliases(settings))
	router.POST("/v1/responses", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Model alias settings unavailable")
}

func TestGlobalModelAliasesLoadsCompositeCatalogPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	policy := &service.ModelAliasPolicy{Groups: []service.ModelAliasGroup{{
		Canonical: "deepseek-v4.1-flash", Aliases: []string{"deepseek/deepseek-v4.1-flash"},
	}}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}})
		c.Request = c.Request.WithContext(service.WithModelAliases(c.Request.Context(), policy))
		c.Next()
	})
	router.Use(GlobalModelAliases(&service.SettingService{}))
	assertPolicy := func(c *gin.Context) {
		require.Same(t, policy, service.ModelAliasesFromContext(c.Request.Context()))
		c.Status(http.StatusOK)
	}
	router.GET("/v1/models", func(c *gin.Context) {
		assertPolicy(c)
		require.Empty(t, c.GetHeader("If-None-Match"))
		require.Equal(t, `"old-etag"`, c.GetString("model_alias_if_none_match"))
	})
	router.POST("/v1/responses", assertPolicy)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("If-None-Match", `"old-etag"`)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
}
