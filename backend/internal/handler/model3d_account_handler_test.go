package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type model3DHandlerRepo struct {
	service.AccountRepository
	account service.Account
}

func (r *model3DHandlerRepo) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]service.Account, error) {
	return []service.Account{r.account}, nil
}
func (r *model3DHandlerRepo) GetByID(context.Context, int64) (*service.Account, error) {
	return &r.account, nil
}
func (r *model3DHandlerRepo) UpdateLastUsed(context.Context, int64) error { return nil }

type model3DBillingStub struct {
	err   error
	calls int
}

func (b *model3DBillingStub) CheckBillingEligibility(context.Context, *service.User, *service.APIKey, *service.Group, *service.UserSubscription, string) error {
	b.calls++
	return b.err
}

type model3DUsageStub struct {
	priceErr  error
	recordErr error
	records   []*service.RecordUsageInput
}

func (u *model3DUsageStub) ValidateModel3DPricing(context.Context, *service.APIKey, string) error {
	return u.priceErr
}
func (u *model3DUsageStub) RecordUsage(_ context.Context, input *service.RecordUsageInput) error {
	u.records = append(u.records, input)
	return u.recordErr
}

// TestModel3DAccountCreateBillsActualAccountOnce 查询不重复计费；扣费记录固定实际账号、Key 与分组。
func TestModel3DAccountCreateBillsActualAccountOnce(t *testing.T) {
	upstream := meshyUpstreamStub(t, http.StatusOK)
	defer upstream.Close()
	h := newModel3DTestHandler(t, upstream.URL)
	repo := &model3DHandlerRepo{account: service.Account{ID: 42, Platform: service.PlatformModel3D, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{9}, Credentials: map[string]any{"model3d_provider": "meshy", "api_key": "private", "base_url": upstream.URL, "default_model": "meshy-5"}}}
	usage := &model3DUsageStub{}
	h.accounts = service.NewModel3DAccountPool(repo, nil)
	h.billing = &model3DBillingStub{}
	h.gateway = usage
	h.enabled = true
	key := meshyTestAPIKey(1, 7)
	key.Group.Platform = service.PlatformModel3D
	key.User = &service.User{ID: 7, Concurrency: 1}
	create, w := newModel3DRequest(http.MethodPost, "/api/v1/3d/generations", `{"mode":"text-to-3d","prompt":"house"}`, key)
	h.Create(create)
	require.Equal(t, 202, w.Code, w.Body.String())
	require.Len(t, usage.records, 1)
	record := usage.records[0]
	require.Equal(t, int64(42), record.Account.ID)
	require.Equal(t, service.PlatformModel3D, record.QuotaPlatform)
	require.Equal(t, "meshy-5", record.Result.Model)
	require.Contains(t, record.Result.RequestID, "model3d:42:meshy:msy_1")
	var result struct {
		Data struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	for i := 0; i < 2; i++ {
		c, w := newModel3DRequest(http.MethodGet, "/tasks/", "", key)
		c.Params = gin.Params{{Key: "task_id", Value: result.Data.TaskID}}
		h.Status(c)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	require.Len(t, usage.records, 1)
	// 精修不能接受裸上游 ID，也不能复用其他 Key 的签名任务。
	for _, preview := range []string{"raw-upstream-id", result.Data.TaskID} {
		foreign := *key
		foreign.ID = key.ID + 1
		payload, err := json.Marshal(map[string]any{"mode": "text-to-3d", "prompt": "refine", "options": map[string]any{"preview_task_id": preview}})
		require.NoError(t, err)
		c, rejected := newModel3DRequest(http.MethodPost, "/generations", string(payload), &foreign)
		h.Create(c)
		require.Equal(t, 403, rejected.Code)
	}
	require.Len(t, usage.records, 1)
	// 无定价时拒绝提交；上游受理后的计费失败仍保留句柄，避免重新提交任务。
	usage.priceErr = errors.New("missing price")
	c, w := newModel3DRequest(http.MethodPost, "/generations", `{"mode":"text-to-3d","prompt":"house"}`, key)
	h.Create(c)
	require.Equal(t, 503, w.Code)
	require.Len(t, usage.records, 1)
	usage.priceErr = nil
	usage.recordErr = errors.New("storage unavailable")
	c, w = newModel3DRequest(http.MethodPost, "/generations", `{"mode":"text-to-3d","prompt":"house"}`, key)
	h.Create(c)
	require.Equal(t, 202, w.Code)
	require.Contains(t, w.Body.String(), `"billing_status":"pending"`)
	require.Contains(t, w.Body.String(), "task_id")
}

// TestModel3DAccountDiscoveryDoesNotExposeDeploymentCredentials 线上发现只返回静态能力目录。
func TestModel3DAccountDiscoveryDoesNotExposeDeploymentCredentials(t *testing.T) {
	h := NewModel3DHandler(nil)
	h.accounts = service.NewModel3DAccountPool(&model3DHandlerRepo{}, nil)
	h.enabled = true
	c, w := newModel3DRequest(http.MethodGet, "/providers", "", nil)
	h.Providers(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"source":"account"`)
	require.Contains(t, w.Body.String(), "hi3d")
}
