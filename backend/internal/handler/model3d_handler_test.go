package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/platform/model3d"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newModel3DTestHandler 用模拟上游构建处理器，避免测试触达真实服务商。
func newModel3DTestHandler(t *testing.T, upstreamURL string) *Model3DHandler {
	t.Helper()
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "test-handle-secret"}}
	cfg.Gateway.Model3D.Enabled = true
	cfg.Gateway.Model3D.TaskHandleTTLHours = 1
	h := NewModel3DHandler(cfg)
	repo := &model3DHandlerRepo{account: service.Account{ID: 42, Platform: service.PlatformModel3D, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{9}, Credentials: map[string]any{"model3d_provider": "meshy", "api_key": "private-meshy-key", "base_url": upstreamURL, "default_model": "meshy-5"}}}
	h.accounts = service.NewModel3DAccountPool(repo, nil)
	h.billing = &model3DBillingStub{}
	h.gateway = &model3DUsageStub{}
	return h
}

// newModel3DRequest 构造带 API Key 的测试上下文，密钥归属由调用方指定。
func newModel3DRequest(method, path, body string, key *service.APIKey) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set(string(middleware.ContextKeyAPIKey), key)
	return ctx, recorder
}

// meshyTestAPIKey 返回可用于生成调用的密钥，分组已开启媒体生成权限。
func meshyTestAPIKey(id, userID int64) *service.APIKey {
	groupID := int64(9)
	return &service.APIKey{
		ID:      id,
		UserID:  userID,
		User:    &service.User{ID: userID, Concurrency: 1},
		GroupID: &groupID,
		Group:   &service.Group{ID: groupID, Platform: service.PlatformModel3D, AllowImageGeneration: true},
	}
}

// meshyUpstreamStub 提供 Meshy 文生 3D 的创建与查询响应。
func meshyUpstreamStub(t *testing.T, createStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v2/text-to-3d"):
			if createStatus != http.StatusOK {
				w.WriteHeader(createStatus)
				_, _ = w.Write([]byte(`{"message":"rate limited"}`))
				return
			}
			_, _ = w.Write([]byte(`{"result":"msy_1"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/v2/text-to-3d/msy_1"):
			_, _ = w.Write([]byte(`{"id":"msy_1","type":"text-to-3d-preview","status":"SUCCEEDED","progress":100,"model_urls":{"glb":"https://cdn.test/model.glb"},"prompt":"a house"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"unexpected path"}`))
		}
	}))
}

// TestModel3DCreateScopesHandleToAPIKey 确认任务句柄绑定创建者，跨密钥查询被拒绝。
func TestModel3DCreateScopesHandleToAPIKey(t *testing.T) {
	upstream := meshyUpstreamStub(t, http.StatusOK)
	defer upstream.Close()
	h := newModel3DTestHandler(t, upstream.URL)

	owner := meshyTestAPIKey(11, 7)
	create, recorder := newModel3DRequest(http.MethodPost, "/api/v1/3d/generations", `{"mode":"text-to-3d","prompt":"a small house"}`, owner)
	h.Create(create)
	require.Equal(t, http.StatusAccepted, recorder.Code, recorder.Body.String())

	var created struct {
		Data struct {
			TaskID         string `json:"task_id"`
			UpstreamTaskID string `json:"upstream_task_id"`
			Provider       string `json:"provider"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &created))
	require.NotEmpty(t, created.Data.TaskID)
	require.Equal(t, "msy_1", created.Data.UpstreamTaskID)
	require.Equal(t, "meshy", created.Data.Provider)

	status, statusRecorder := newModel3DRequest(http.MethodGet, "/api/v1/3d/tasks/"+created.Data.TaskID, "", owner)
	status.Params = gin.Params{{Key: "task_id", Value: created.Data.TaskID}}
	h.Status(status)
	require.Equal(t, http.StatusOK, statusRecorder.Code, statusRecorder.Body.String())
	require.Contains(t, statusRecorder.Body.String(), `"status":"succeeded"`)
	require.Contains(t, statusRecorder.Body.String(), "gateway_path")

	foreign, foreignRecorder := newModel3DRequest(http.MethodGet, "/api/v1/3d/tasks/"+created.Data.TaskID, "", meshyTestAPIKey(12, 8))
	foreign.Params = gin.Params{{Key: "task_id", Value: created.Data.TaskID}}
	h.Status(foreign)
	require.Equal(t, http.StatusForbidden, foreignRecorder.Code, foreignRecorder.Body.String())

	tampered, tamperedRecorder := newModel3DRequest(http.MethodGet, "/api/v1/3d/tasks/tampered", "", owner)
	tampered.Params = gin.Params{{Key: "task_id", Value: "tampered"}}
	h.Status(tampered)
	require.Equal(t, http.StatusForbidden, tamperedRecorder.Code, tamperedRecorder.Body.String())
}

// TestModel3DCreateRequiresMediaGroup 确认未授权分组无法消耗服务商额度。
func TestModel3DCreateRequiresMediaGroup(t *testing.T) {
	upstream := meshyUpstreamStub(t, http.StatusOK)
	defer upstream.Close()
	h := newModel3DTestHandler(t, upstream.URL)

	noGroup, recorder := newModel3DRequest(http.MethodPost, "/api/v1/3d/generations", `{"mode":"text-to-3d","prompt":"x"}`, &service.APIKey{ID: 13, UserID: 7})
	h.Create(noGroup)
	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())

	groupID := int64(9)
	restricted, restrictedRecorder := newModel3DRequest(http.MethodPost, "/api/v1/3d/generations", `{"mode":"text-to-3d","prompt":"x"}`, &service.APIKey{ID: 14, UserID: 7, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformModel3D}})
	h.Create(restricted)
	require.Equal(t, http.StatusForbidden, restrictedRecorder.Code, restrictedRecorder.Body.String())
}

// TestModel3DUpstreamRateLimitReturns429 确认限流语义透传并带上脱敏元数据。
func TestModel3DUpstreamRateLimitReturns429(t *testing.T) {
	upstream := meshyUpstreamStub(t, http.StatusTooManyRequests)
	defer upstream.Close()
	h := newModel3DTestHandler(t, upstream.URL)

	create, recorder := newModel3DRequest(http.MethodPost, "/api/v1/3d/generations", `{"mode":"text-to-3d","prompt":"a house"}`, meshyTestAPIKey(15, 7))
	h.Create(create)
	require.Equal(t, http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "model3d_upstream_rate_limited")
	require.Contains(t, recorder.Body.String(), `"provider":"meshy"`)
}

// TestModel3DDisabledReturnsServiceUnavailable 确认关闭时创建请求被拒绝。
func TestModel3DDisabledReturnsServiceUnavailable(t *testing.T) {
	h := NewModel3DHandler(&config.Config{})
	create, recorder := newModel3DRequest(http.MethodPost, "/api/v1/3d/generations", `{"mode":"text-to-3d","prompt":"x"}`, meshyTestAPIKey(16, 7))
	h.Create(create)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "model3d_disabled")
}

// TestModel3DDiscoveryNeverLeaksCredentials 确认发现接口只回显配置状态，不泄露密钥。
func TestModel3DDiscoveryNeverLeaksCredentials(t *testing.T) {
	upstream := meshyUpstreamStub(t, http.StatusOK)
	defer upstream.Close()
	h := newModel3DTestHandler(t, upstream.URL)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/3d/providers", nil)
	h.Providers(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "private-meshy-key")
	require.Contains(t, recorder.Body.String(), `"key":"meshy"`)
}

// TestModel3DGroupGate 无分组或未授权密钥不能消耗服务商额度。
func TestModel3DGroupGate(t *testing.T) {
	_, allowed := model3DGroupAllowed(nil)
	require.False(t, allowed)
	_, allowed = model3DGroupAllowed(&service.APIKey{})
	require.False(t, allowed)
	_, allowed = model3DGroupAllowed(meshyTestAPIKey(17, 7))
	require.True(t, allowed)
}

// TestModel3DUpstreamStatusMapping 保留限流语义，鉴权类错误按上游故障返回。
func TestModel3DUpstreamStatusMapping(t *testing.T) {
	code, _ := model3DUpstreamStatus(&model3d.UpstreamError{StatusCode: http.StatusTooManyRequests})
	require.Equal(t, http.StatusTooManyRequests, code)
	code, _ = model3DUpstreamStatus(&model3d.UpstreamError{StatusCode: http.StatusUnauthorized})
	require.Equal(t, http.StatusBadGateway, code)
	code, reason := model3DUpstreamStatus(&model3d.UpstreamError{StatusCode: http.StatusUnprocessableEntity})
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, "model3d_upstream_rejected", reason)
}
