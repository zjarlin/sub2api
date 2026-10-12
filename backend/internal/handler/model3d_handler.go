package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/platform/model3d"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// model3DUsageRecorder 复用统一用量与按次计费，并允许隔离上游提交测试。
type model3DUsageRecorder interface {
	ValidateModel3DPricing(context.Context, *service.APIKey, string) error
	RecordUsage(context.Context, *service.RecordUsageInput) error
}

// model3DBillingEligibility 在发送付费请求前检查余额与平台额度。
type model3DBillingEligibility interface {
	CheckBillingEligibility(context.Context, *service.User, *service.APIKey, *service.Group, *service.UserSubscription, string) error
}

// maxModel3DAssetBytes 是网关代理下载 3D 产物时的体积上限（512MB）。
// 3D 模型远大于图片，但仍需要边界，避免上游异常地址拖垮网关。
const maxModel3DAssetBytes = 512 << 20

// Model3DHandler 暴露 3D 生成服务（Meshy / Tripo / Hi3D）的统一入口。
//
// 客户端拿到的是网关签发的任务句柄，而不是上游任务 ID；
// 句柄绑定创建者与 API Key，查询、取消、下载产物都必须用同一个 Key。
type Model3DHandler struct {
	accounts  *service.Model3DAccountPool
	gateway   model3DUsageRecorder
	billing   model3DBillingEligibility
	apiKeys   *service.APIKeyService
	enabled   bool
	secret    []byte
	handleTTL time.Duration
	client    *http.Client
}

// NewModel3DHandler 创建 3D 处理器。cfg 为 nil 时使用默认句柄有效期。
func NewModel3DHandler(cfg *config.Config) *Model3DHandler {
	handler := &Model3DHandler{
		handleTTL: 168 * time.Hour,
		client:    newModel3DAssetClient(),
	}
	if cfg != nil {
		handler.handleTTL = cfg.Gateway.Model3D.TaskHandleTTL()
		handler.enabled = cfg.Gateway.Model3D.Enabled
		handler.secret = model3d.HandleSecret(cfg.JWT.Secret)
	}
	return handler
}

// Providers 返回已注册的 3D 服务商能力与配置状态（公开端点，不含凭据）。
func (h *Model3DHandler) Providers(c *gin.Context) {
	catalog := []model3d.ProviderInfo{model3d.NewMeshyProvider(nil, 60).Info(), model3d.NewTripoProvider(nil, 60).Info(), model3d.NewHi3DProvider(nil, 60).Info()}
	for i := range catalog {
		catalog[i].Configured = false
		catalog[i].BaseURL = ""
	}
	response.Success(c, gin.H{"enabled": h != nil && h.enabled, "providers": catalog, "source": "account"})
}

// Create 创建 3D 生成任务。
//
// POST /api/v1/3d/generations
func (h *Model3DHandler) Create(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		response.Unauthorized(c, "Missing authenticated API key")
		return
	}
	if message, allowed := model3DGroupAllowed(key); !allowed {
		response.Forbidden(c, message)
		return
	}
	if h == nil || !h.enabled || h.accounts == nil {
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "3D generation is not available", "model3d_disabled", nil)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 48<<20)
	var req model3d.CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	h.createWithAccount(c, key, &req)
}

// Status 查询任务状态。
//
// GET /api/v1/3d/tasks/:task_id
func (h *Model3DHandler) Status(c *gin.Context) {
	task, _, ok := h.resolveTask(c)
	if !ok {
		return
	}
	presented := h.presentTask(c.Param("task_id"), task)
	// presentTask 只补充句柄自身信息，查询结果需要带上实时进度与产物。
	presented["status"] = task.Status
	presented["provider_status"] = task.ProviderStatus
	presented["progress"] = task.Progress
	presented["error"] = task.Error
	if task.Model != "" {
		presented["model"] = task.Model
	}
	if task.Prompt != "" {
		presented["prompt"] = task.Prompt
	}
	if task.ThumbnailURL != "" {
		presented["thumbnail_url"] = task.ThumbnailURL
	}
	if task.Credits != 0 {
		presented["credits"] = task.Credits
	}
	if task.CreatedAt != 0 {
		presented["created_at"] = task.CreatedAt
	}
	if task.FinishedAt != 0 {
		presented["finished_at"] = task.FinishedAt
	}
	presented["assets"] = h.presentAssets(c.Param("task_id"), task)
	response.Success(c, presented)
}

// Cancel 取消任务；服务商不支持取消时返回 501。
//
// DELETE /api/v1/3d/tasks/:task_id
func (h *Model3DHandler) Cancel(c *gin.Context) {
	handle, key, ok := h.decodeOwned(c)
	if !ok {
		return
	}
	if !h.enabled || h.accounts == nil {
		response.ErrorWithDetails(c, 503, "3D generation is unavailable", "model3d_disabled", nil)
		return
	}
	_, provider, err := h.accounts.Resolve(c.Request.Context(), handle, key)
	if err != nil {
		h.writeError(c, err)
		return
	}
	if err := service.CancelModel3DAccountTask(c.Request.Context(), provider, handle); err != nil {
		h.writeError(c, err)
		return
	}
	response.Success(c, gin.H{"task_id": c.Param("task_id"), "provider": handle.Provider, "status": model3d.StatusCancelled})
}

// Asset 代理下载任务产物，避免浏览器因跨域无法读取服务商 CDN。
//
// GET /api/v1/3d/tasks/:task_id/assets/:index
func (h *Model3DHandler) Asset(c *gin.Context) {
	task, _, ok := h.resolveTask(c)
	if !ok {
		return
	}
	index, err := strconv.Atoi(strings.TrimSpace(c.Param("index")))
	if err != nil || index < 0 {
		response.BadRequest(c, "Invalid asset index")
		return
	}
	if task.Status != model3d.StatusSucceeded {
		response.ErrorWithDetails(c, http.StatusConflict, "Task has not succeeded yet", "model3d_task_not_ready", map[string]string{"status": string(task.Status)})
		return
	}
	if index >= len(task.Assets) {
		response.NotFound(c, "Asset index out of range")
		return
	}
	asset := task.Assets[index]
	if !isHTTPURL(asset.URL) {
		response.ErrorWithDetails(c, http.StatusBadGateway, "Upstream asset URL is not usable", "model3d_asset_unavailable", nil)
		return
	}
	format := asset.Format
	if format == "" {
		format = "glb"
	}
	if err := h.streamAsset(c, asset.URL, format); err != nil {
		response.ErrorWithDetails(c, http.StatusBadGateway, "Unable to fetch the generated asset", "model3d_asset_fetch_failed", map[string]string{"reason": err.Error()})
	}
}

// Balance 查询指定服务商的账户余额。
//
// GET /api/v1/3d/balance?provider=meshy
func (h *Model3DHandler) Balance(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		response.Unauthorized(c, "Missing authenticated API key")
		return
	}
	if message, allowed := model3DGroupAllowed(key); !allowed {
		response.Forbidden(c, message)
		return
	}
	if h == nil || !h.enabled || h.accounts == nil {
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "3D generation is not available", "model3d_disabled", nil)
		return
	}
	account, provider, release, err := h.accounts.Select(c.Request.Context(), key, &model3d.CreateRequest{Mode: model3d.ModeImageTo3D, Provider: c.Query("provider")})
	if err != nil {
		h.writeError(c, err)
		return
	}
	defer release()
	balance, err := provider.Balance(c.Request.Context())
	if err != nil {
		h.writeError(c, err)
		return
	}
	response.Success(c, gin.H{"provider": provider.Key(), "account_id": account.ID, "balance": balance})
	return
}

// resolveTask 解析并校验任务句柄，再向原服务商查询任务。
func (h *Model3DHandler) resolveTask(c *gin.Context) (*model3d.Task, model3d.TaskHandle, bool) {
	handle, key, ok := h.decodeOwned(c)
	if !ok {
		return nil, model3d.TaskHandle{}, false
	}
	if h == nil || !h.enabled || h.accounts == nil {
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "3D generation is not available", "model3d_disabled", nil)
		return nil, model3d.TaskHandle{}, false
	}
	_, provider, err := h.accounts.Resolve(c.Request.Context(), handle, key)
	if err != nil {
		h.writeError(c, err)
		return nil, handle, false
	}
	task, err := service.GetModel3DAccountTask(c.Request.Context(), provider, handle)
	if err != nil {
		h.writeError(c, err)
		return nil, handle, false
	}
	return task, handle, true
}

// decodeOwned 校验句柄签名与创建者归属，越权或篡改一律 403。
func (h *Model3DHandler) decodeOwned(c *gin.Context) (model3d.TaskHandle, *service.APIKey, bool) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		response.Unauthorized(c, "Missing authenticated API key")
		return model3d.TaskHandle{}, nil, false
	}
	handle, err := model3d.DecodeTaskHandle(h.secret, c.Param("task_id"), time.Now())
	if err != nil {
		if errors.Is(err, model3d.ErrTaskExpired) {
			response.ErrorWithDetails(c, http.StatusGone, "Task handle expired; create a new task", "model3d_task_handle_expired", nil)
			return model3d.TaskHandle{}, nil, false
		}
		response.Forbidden(c, "Invalid task handle")
		return model3d.TaskHandle{}, nil, false
	}
	if handle.UserID != key.UserID || handle.KeyID != key.ID {
		response.Forbidden(c, "Task belongs to another API key")
		return model3d.TaskHandle{}, nil, false
	}
	return handle, key, true
}

// presentTask 组装对外响应；task_id 始终是网关句柄，上游 ID 单独放在 upstream_task_id。
func (h *Model3DHandler) presentTask(handleToken string, task *model3d.Task) gin.H {
	payload := gin.H{
		"task_id":          handleToken,
		"upstream_task_id": task.ID,
		"provider":         task.Provider,
		"mode":             task.Mode,
		"status":           task.Status,
		"provider_status":  task.ProviderStatus,
		"progress":         task.Progress,
	}
	if task.Model != "" {
		payload["model"] = task.Model
	}
	if h.handleTTL > 0 {
		if decoded, err := model3d.DecodeTaskHandle(h.secret, handleToken, time.Now()); err == nil {
			payload["handle_expires_at"] = decoded.ExpiresAt
		}
	}
	return payload
}

// presentAssets 输出产物列表：上游直链 + 同源网关路径（浏览器必须走网关）。
func (h *Model3DHandler) presentAssets(handleToken string, task *model3d.Task) []gin.H {
	assets := make([]gin.H, 0, len(task.Assets))
	for index, asset := range task.Assets {
		assets = append(assets, gin.H{
			"format":       asset.Format,
			"name":         asset.Name,
			"url":          asset.URL,
			"gateway_path": fmt.Sprintf("/api/v1/3d/tasks/%s/assets/%d", url.PathEscape(handleToken), index),
		})
	}
	return assets
}

// writeError 把聚合器错误分级映射成 HTTP 状态，保留上游错误码便于排查。
func (h *Model3DHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model3d.ErrInvalidRequest):
		response.BadRequest(c, err.Error())
	case errors.Is(err, model3d.ErrTaskMismatch):
		response.Forbidden(c, err.Error())
	case errors.Is(err, model3d.ErrTaskExpired):
		response.ErrorWithDetails(c, http.StatusGone, err.Error(), "model3d_task_handle_expired", nil)
	case errors.Is(err, model3d.ErrUnsupported):
		response.ErrorWithDetails(c, http.StatusNotImplemented, err.Error(), "model3d_unsupported", nil)
	case errors.Is(err, model3d.ErrProviderUnavailable):
		response.BadRequest(c, err.Error())
	case errors.Is(err, service.ErrNoAvailableAccounts):
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "No available 3D account in this group", "model3d_no_account", nil)
	case errors.Is(err, model3d.ErrNoProvider):
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, err.Error(), "model3d_no_provider", nil)
	default:
		var upstream *model3d.UpstreamError
		if errors.As(err, &upstream) {
			status, reason := model3DUpstreamStatus(upstream)
			response.ErrorWithDetails(c, status, upstream.Error(), reason, model3DRedactedMetadata(upstream))
			return
		}
		response.InternalError(c, err.Error())
	}
}

// model3DUpstreamStatus 把上游状态映射成网关状态：客户端输入错误直接透传，
// 鉴权/额度/服务端问题统一按上游依赖故障返回 502，避免误导调用方以为是自己写错了。
func model3DUpstreamStatus(err *model3d.UpstreamError) (int, string) {
	switch {
	case err == nil:
		return http.StatusBadGateway, "model3d_upstream_error"
	case err.StatusCode == http.StatusTooManyRequests:
		return http.StatusTooManyRequests, "model3d_upstream_rate_limited"
	case err.StatusCode == http.StatusBadRequest, err.StatusCode == http.StatusUnprocessableEntity:
		return http.StatusBadRequest, "model3d_upstream_rejected"
	default:
		return http.StatusBadGateway, "model3d_upstream_error"
	}
}

// model3DRedactedMetadata 只暴露上游状态码与业务码，不泄露请求体或凭据。
func model3DRedactedMetadata(err *model3d.UpstreamError) map[string]string {
	metadata := map[string]string{"provider": err.Provider}
	if err.StatusCode != 0 {
		metadata["upstream_status"] = strconv.Itoa(err.StatusCode)
	}
	if err.Code != "" {
		metadata["upstream_code"] = err.Code
	}
	return metadata
}

// streamAsset 把服务商 CDN 上的产物流式转发给客户端。
func (h *Model3DHandler) streamAsset(c *gin.Context, assetURL, format string) error {
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, assetURL, nil)
	if err != nil {
		return err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	if resp.ContentLength > maxModel3DAssetBytes {
		return fmt.Errorf("asset exceeds %d bytes", maxModel3DAssetBytes)
	}
	contentType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = model3DContentType(format)
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "model."+format))
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusOK)
	_, err = io.Copy(c.Writer, io.LimitReader(resp.Body, maxModel3DAssetBytes))
	return err
}

// model3DContentType 按产物格式返回 MIME 类型。
func model3DContentType(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "glb":
		return "model/gltf-binary"
	case "gltf":
		return "model/gltf+json"
	case "obj":
		return "model/obj"
	case "stl":
		return "model/stl"
	case "usdz":
		return "model/vnd.usdz+zip"
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// model3DGroupAllowed 要求 API Key 归属分组且该分组开启了媒体生成权限。
// 3D 生成会真实消耗运维侧的服务商额度，必须与图片/视频生成使用同一道权限闸门。
func model3DGroupAllowed(key *service.APIKey) (string, bool) {
	if key == nil || key.Group == nil {
		return "3D generation requires an API key bound to a group", false
	}
	if !key.Group.AllowImageGeneration {
		return "3D generation is not enabled for this group", false
	}
	return "", true
}

// newModel3DAssetClient 创建下载产物用的客户端，禁止访问内网地址（SSRF 兜底）。
func newModel3DAssetClient() *http.Client { return model3d.NewDownloadClient() }

func isHTTPURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")
}

// createWithAccount 先检查余额与定价，再选择账号；成功受理只扣一次提交费。
func (h *Model3DHandler) createWithAccount(c *gin.Context, key *service.APIKey, req *model3d.CreateRequest) {
	if err := req.Validate(); err != nil {
		h.writeError(c, err)
		return
	}
	if key.GroupID == nil || (key.Group.Platform != service.PlatformModel3D && key.Group.Platform != service.PlatformComposite) {
		response.Forbidden(c, "3D generation requires a 3D or composite group")
		return
	}
	if h.billing == nil || h.gateway == nil || key.User == nil {
		response.ErrorWithDetails(c, 503, "3D billing is unavailable", "model3d_billing_unavailable", nil)
		return
	}
	subscription, _ := middleware.GetSubscriptionFromContext(c)
	if err := h.billing.CheckBillingEligibility(c.Request.Context(), key.User, key, key.Group, subscription, service.PlatformModel3D); err != nil {
		status, reason, message, retry := billingErrorDetails(err)
		if retry > 0 {
			c.Header("Retry-After", strconv.Itoa(retry))
		}
		response.ErrorWithDetails(c, status, message, reason, nil)
		return
	}
	var previewHandle model3d.TaskHandle
	if preview, ok := req.Options["preview_task_id"].(string); ok && preview != "" {
		var err error
		previewHandle, err = model3d.DecodeTaskHandle(h.secret, preview, time.Now())
		if err != nil || previewHandle.KeyID != key.ID || previewHandle.UserID != key.UserID || previewHandle.GroupID != *key.GroupID || previewHandle.AccountID <= 0 || previewHandle.Provider != "meshy" || previewHandle.Mode != model3d.ModeTextTo3D || req.Mode != model3d.ModeTextTo3D {
			response.Forbidden(c, "Refinement requires a task handle owned by this API key")
			return
		}
		req.AccountID = previewHandle.AccountID
	}
	account, provider, release, err := h.accounts.Select(c.Request.Context(), key, req)
	if err != nil {
		h.writeError(c, err)
		return
	}
	defer release()
	publicModel := req.Model
	if publicModel == "" {
		publicModel = account.GetCredential("default_model")
	}
	if publicModel == "" {
		for _, model := range provider.Info().Models {
			if model.Default {
				publicModel = model.ID
				break
			}
		}
	}
	if len(account.GetModelMapping()) > 0 && !account.IsModelSupported(publicModel) {
		response.BadRequest(c, "Default 3D model is excluded by the account model whitelist")
		return
	}
	if publicModel == "" {
		response.BadRequest(c, "Configure a default 3D model for this account")
		return
	}
	if err := h.gateway.ValidateModel3DPricing(c.Request.Context(), key, publicModel); err != nil {
		response.ErrorWithDetails(c, 503, err.Error(), "model3d_pricing_unavailable", nil)
		return
	}
	req.Model = account.GetMappedModel(publicModel)
	req.Provider = provider.Key()
	// 模型只能由模型字段及账号映射决定，禁止扩展参数绕过白名单与计费。
	for _, field := range []string{"ai_model", "model_version", "model"} {
		if _, ok := req.Options[field]; ok {
			response.BadRequest(c, "Set the model field instead of overriding it in options")
			return
		}
	}
	if preview, ok := req.Options["preview_task_id"].(string); ok && preview != "" {
		previous := previewHandle
		if previous.CredentialIdentity != service.Model3DAccountIdentity(account) || previous.KeyID != key.ID || previous.UserID != key.UserID || previous.AccountID != account.ID || previous.GroupID != *key.GroupID || previous.Provider != provider.Key() {
			response.Forbidden(c, "Refinement requires a task handle owned by this API key and account")
			return
		}
		req.Options["preview_task_id"] = previous.TaskID
	}
	started := time.Now()
	taskID, err := provider.Create(c.Request.Context(), req)
	if err != nil {
		h.writeError(c, err)
		return
	}
	task := &model3d.Task{ID: taskID, Mode: req.Mode.Normalize(), Provider: provider.Key(), Model: publicModel, Status: model3d.StatusQueued, ProviderStatus: "created"}
	handle, err := model3d.EncodeTaskHandle(h.secret, model3d.TaskHandle{Provider: task.Provider, Mode: task.Mode, TaskID: task.ID, AccountID: account.ID, GroupID: *key.GroupID, Model: publicModel, CredentialIdentity: service.Model3DAccountIdentity(account), UserID: key.UserID, KeyID: key.ID, IssuedAt: started.Unix(), ExpiresAt: started.Add(h.handleTTL).Unix()})
	if err != nil {
		response.InternalError(c, "Unable to sign task handle")
		return
	}
	// 受理成功后即使客户端断开也完成用量写入；失败仍返回句柄，禁止客户端重新提交生成。
	billingCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 30*time.Second)
	defer cancel()
	err = h.gateway.RecordUsage(billingCtx, &service.RecordUsageInput{Result: &service.ForwardResult{RequestID: fmt.Sprintf("model3d:%d:%s:%s", account.ID, task.Provider, task.ID), Model: publicModel, UpstreamModel: req.Model, Duration: time.Since(started)}, APIKey: key, User: key.User, Account: account, Subscription: subscription, InboundEndpoint: "/api/v1/3d/generations", UpstreamEndpoint: "/3d/generations", APIKeyService: h.apiKeys, QuotaPlatform: service.PlatformModel3D})
	if updateErr := h.accounts.MarkUsed(billingCtx, account.ID); updateErr != nil {
		slog.Warn("Unable to update 3D account usage time", "account_id", account.ID, "error", updateErr)
	}
	presented := h.presentTask(handle, task)
	presented["account_id"] = account.ID
	presented["billing_status"] = "recorded"
	if err != nil {
		presented["billing_status"] = "pending"
		slog.Error("3D task accepted but billing failed", "account_id", account.ID, "task_id", taskID, "error", err)
	}
	response.Accepted(c, presented)
}
