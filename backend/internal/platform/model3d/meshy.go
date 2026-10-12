package model3d

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Meshy 适配 https://docs.meshy.ai 的开放接口：
//
//	POST {openapi}/v2/text-to-3d      文生 3D（preview / refine 两阶段）
//	POST {openapi}/v1/image-to-3d     图生 3D
//	GET  {openapi}/v2/text-to-3d/{id} 查询文生 3D 任务
//	GET  {openapi}/v1/image-to-3d/{id} 查询图生 3D 任务
//	DEL  .../{id}                     取消并删除任务
//	GET  {openapi}/v1/balance         查询余额
//
// 依据：官方 CLI meshy-dev/meshy-cli 冻结契约
// docs/skill-parity/endpoint-contracts.json 与 src/client/types.ts。
const (
	meshyDefaultBaseURL      = "https://api.meshy.ai/openapi"
	meshyDefaultModel        = "meshy-5"
	meshyTextTo3DPath        = "/v2/text-to-3d"
	meshyImageTo3DPath       = "/v1/image-to-3d"
	meshyBalancePath         = "/v1/balance"
	meshyDefaultTopology     = "triangle"
	meshyDefaultTargetFaces  = 30000
	meshyMaxTargetFaceCount  = 300000
	meshyTextPreviewModeName = "preview"
)

// MeshyProvider 实现 Provider。
type MeshyProvider struct {
	apiKey       string
	baseURL      string
	defaultModel string
	client       *http.Client
}

// NewMeshyProvider 创建 Meshy 适配器；apiKey 为空时视为未配置。
func NewMeshyProvider(cfg *MeshyConfig, timeoutSeconds int) *MeshyProvider {
	if cfg == nil {
		cfg = &MeshyConfig{}
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv(EnvMeshyBaseURL)), "/")
	}
	if base == "" {
		base = meshyDefaultBaseURL
	}
	model := strings.TrimSpace(cfg.DefaultModel)
	if model == "" {
		model = meshyDefaultModel
	}
	return &MeshyProvider{
		apiKey:       strings.TrimSpace(cfg.APIKey),
		baseURL:      base,
		defaultModel: model,
		client:       newHTTPClient(timeoutSeconds),
	}
}

// Key 返回服务商标识。
func (p *MeshyProvider) Key() string { return "meshy" }

// Info 返回能力快照。Meshy 同时支持文生 3D 与图生 3D。
func (p *MeshyProvider) Info() ProviderInfo {
	return ProviderInfo{
		Key:         p.Key(),
		DisplayName: "Meshy",
		Modes:       []Mode{ModeTextTo3D, ModeImageTo3D},
		Models: []ModelSpec{
			{ID: "meshy-5", DisplayName: "Meshy 5", Modes: []Mode{ModeTextTo3D, ModeImageTo3D}, Default: p.defaultModel == "meshy-5"},
			{ID: "meshy-4", DisplayName: "Meshy 4", Modes: []Mode{ModeTextTo3D, ModeImageTo3D}},
			{ID: "latest", DisplayName: "Latest", Modes: []Mode{ModeTextTo3D, ModeImageTo3D}},
		},
		Configured: p.apiKey != "",
		BaseURL:    p.baseURL,
		Notes: []string{
			"文生 3D 分 preview / refine 两阶段；refine 需要 options.preview_task_id 指向已成功的 preview 任务",
			"模型产物默认包含 glb/obj/fbx/usdz 多种格式",
		},
	}
}

// Create 创建 Meshy 任务，返回上游任务 ID。
func (p *MeshyProvider) Create(ctx context.Context, req *CreateRequest) (string, error) {
	if err := p.ensureConfigured(); err != nil {
		return "", err
	}
	if err := req.Validate(); err != nil {
		return "", err
	}
	var (
		path string
		body map[string]any
	)
	switch req.Mode {
	case ModeTextTo3D:
		path = meshyTextTo3DPath
		body = p.textTo3DBody(req)
	case ModeImageTo3D:
		path = meshyImageTo3DPath
		body = p.imageTo3DBody(req)
	default:
		return "", fmt.Errorf("%w: mode %q", ErrUnsupported, req.Mode)
	}
	var resp struct {
		Result string `json:"result"`
	}
	if err := doJSON(ctx, p.client, p.Key(), http.MethodPost, p.baseURL+path, p.headers(), body, &resp); err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Result) == "" {
		return "", &UpstreamError{Provider: p.Key(), StatusCode: http.StatusOK, Message: "missing result task id"}
	}
	return resp.Result, nil
}

// textTo3DBody 组装文生 3D 请求体；options 里的原生字段会被原样合并（覆盖默认值）。
func (p *MeshyProvider) textTo3DBody(req *CreateRequest) map[string]any {
	body := map[string]any{
		"mode":             meshyTextPreviewModeName,
		"prompt":           strings.TrimSpace(req.Prompt),
		"ai_model":         p.resolveModel(req.Model),
		"topology":         firstNonEmpty(req.Topology, meshyDefaultTopology),
		"target_polycount": meshyTargetPolycount(req.FaceLimit),
		"should_remesh":    true,
		"symmetry_mode":    "auto",
	}
	if req.NegativePrompt != "" {
		body["negative_prompt"] = req.NegativePrompt
	}
	if req.PBR != nil {
		body["enable_pbr"] = *req.PBR
	}
	if req.Texture != nil && !*req.Texture {
		// Meshy 文生 3D 的 preview 阶段固定出白模，纹理在 refine 阶段生成；
		// 显式关闭纹理时提示调用方不要进入 refine。
		body["should_texture"] = false
	}
	mergeOptions(body, req.Options)
	return body
}

// imageTo3DBody 组装图生 3D 请求体。
func (p *MeshyProvider) imageTo3DBody(req *CreateRequest) map[string]any {
	body := map[string]any{
		"image_url":        strings.TrimSpace(req.ImageURL),
		"ai_model":         p.resolveModel(req.Model),
		"topology":         firstNonEmpty(req.Topology, meshyDefaultTopology),
		"target_polycount": meshyTargetPolycount(req.FaceLimit),
		"should_remesh":    true,
		"should_texture":   true,
		"symmetry_mode":    "auto",
	}
	if req.Texture != nil {
		body["should_texture"] = *req.Texture
	}
	if req.PBR != nil {
		body["enable_pbr"] = *req.PBR
	}
	mergeOptions(body, req.Options)
	return body
}

// Get 查询任务。Meshy 的文生/图生任务在不同资源路径下，按句柄里的 mode 选择。
func (p *MeshyProvider) Get(ctx context.Context, taskID string) (*Task, error) {
	return p.GetWithMode(ctx, ModeTextTo3D, taskID)
}

// GetWithMode 按输入形态查询任务；mode 为空时默认按文生 3D 查询。
func (p *MeshyProvider) GetWithMode(ctx context.Context, mode Mode, taskID string) (*Task, error) {
	if err := p.ensureConfigured(); err != nil {
		return nil, err
	}
	path := meshyTextTo3DPath
	if mode.Normalize() == ModeImageTo3D {
		path = meshyImageTo3DPath
	}
	var raw meshyTask
	if err := doJSON(ctx, p.client, p.Key(), http.MethodGet, p.baseURL+path+"/"+taskID, p.headers(), nil, &raw); err != nil {
		return nil, err
	}
	return raw.toTask(p.Key(), mode.Normalize()), nil
}

// Cancel 删除任务（Meshy 的 delete 同时起取消作用）。
func (p *MeshyProvider) Cancel(ctx context.Context, taskID string) error {
	return p.deleteWithMode(ctx, ModeTextTo3D, taskID)
}

// CancelWithMode 按输入形态删除任务。
func (p *MeshyProvider) CancelWithMode(ctx context.Context, mode Mode, taskID string) error {
	return p.deleteWithMode(ctx, mode, taskID)
}

func (p *MeshyProvider) deleteWithMode(ctx context.Context, mode Mode, taskID string) error {
	if err := p.ensureConfigured(); err != nil {
		return err
	}
	path := meshyTextTo3DPath
	if mode.Normalize() == ModeImageTo3D {
		path = meshyImageTo3DPath
	}
	return doJSON(ctx, p.client, p.Key(), http.MethodDelete, p.baseURL+path+"/"+taskID, p.headers(), nil, nil)
}

// Balance 查询 Meshy 账户剩余额度。
func (p *MeshyProvider) Balance(ctx context.Context) (float64, error) {
	if err := p.ensureConfigured(); err != nil {
		return 0, err
	}
	var resp struct {
		Balance float64 `json:"balance"`
	}
	if err := doJSON(ctx, p.client, p.Key(), http.MethodGet, p.baseURL+meshyBalancePath, p.headers(), nil, &resp); err != nil {
		return 0, err
	}
	return resp.Balance, nil
}

func (p *MeshyProvider) ensureConfigured() error {
	if p.apiKey == "" {
		return fmt.Errorf("%w: meshy api key is not configured", ErrProviderUnavailable)
	}
	return nil
}

func (p *MeshyProvider) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + p.apiKey}
}

func (p *MeshyProvider) resolveModel(requested string) string {
	if model := strings.TrimSpace(requested); model != "" {
		return model
	}
	return p.defaultModel
}

// meshyTask 是 Meshy 任务响应；时间戳为毫秒。
type meshyTask struct {
	ID              string             `json:"id"`
	Type            string             `json:"type"`
	Status          string             `json:"status"`
	Progress        int                `json:"progress"`
	ModelURLs       map[string]*string `json:"model_urls"`
	ThumbnailURL    string             `json:"thumbnail_url"`
	Prompt          string             `json:"prompt"`
	CreatedAt       int64              `json:"created_at"`
	FinishedAt      int64              `json:"finished_at"`
	ConsumedCredits float64            `json:"consumed_credits"`
	TaskError       *struct {
		Message string `json:"message"`
	} `json:"task_error"`
}

func (t meshyTask) toTask(provider string, mode Mode) *Task {
	task := &Task{
		ID:             t.ID,
		Mode:           mode,
		Provider:       provider,
		ProviderStatus: t.Status,
		Status:         normalizeMeshyStatus(t.Status),
		Progress:       clampProgress(t.Progress),
		ThumbnailURL:   t.ThumbnailURL,
		Prompt:         t.Prompt,
		Credits:        t.ConsumedCredits,
		CreatedAt:      millisToSeconds(t.CreatedAt),
		FinishedAt:     millisToSeconds(t.FinishedAt),
		Assets:         meshyAssets(t.ModelURLs),
	}
	if t.TaskError != nil {
		task.Error = strings.TrimSpace(t.TaskError.Message)
	}
	return task
}

func normalizeMeshyStatus(status string) Status {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PENDING":
		return StatusQueued
	case "IN_PROGRESS":
		return StatusRunning
	case "SUCCEEDED":
		return StatusSucceeded
	case "FAILED":
		return StatusFailed
	case "CANCELED", "CANCELLED":
		return StatusCancelled
	default:
		return StatusQueued
	}
}

// meshyAssets 把 model_urls 映射成有序产物列表，glb 优先，便于直接预览。
func meshyAssets(urls map[string]*string) []Asset {
	if len(urls) == 0 {
		return nil
	}
	order := []string{"glb", "usdz", "fbx", "obj", "blend", "stl", "3mf"}
	seen := map[string]bool{}
	assets := make([]Asset, 0, len(urls))
	for _, format := range order {
		url, ok := urls[format]
		if !ok || url == nil || strings.TrimSpace(*url) == "" {
			continue
		}
		seen[format] = true
		assets = append(assets, Asset{Format: format, URL: *url, Name: format})
	}
	for format, url := range urls {
		if seen[format] || url == nil || strings.TrimSpace(*url) == "" {
			continue
		}
		assets = append(assets, Asset{Format: strings.ToLower(format), URL: *url, Name: format})
	}
	return assets
}

func meshyTargetPolycount(faceLimit int) int {
	if faceLimit <= 0 {
		return meshyDefaultTargetFaces
	}
	if faceLimit > meshyMaxTargetFaceCount {
		return meshyMaxTargetFaceCount
	}
	return faceLimit
}

func millisToSeconds(value int64) int64 {
	if value <= 0 {
		return 0
	}
	return time.UnixMilli(value).Unix()
}

func clampProgress(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
