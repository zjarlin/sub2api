package model3d

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// Tripo 适配 https://platform.tripo3d.ai/docs 的开放接口：
//
//	POST {base}/task               创建任务（text_to_model / image_to_model / multiview_to_model）
//	GET  {base}/task/{id}          查询任务
//	POST {base}/task/{id}/cancel   取消任务
//	POST {base}/upload             上传本地图片换 file_token
//	GET  {base}/user/balance       查询余额
//
// 依据：官方 Python SDK tripo3d 0.4.2（client.py / models.py / client_impl）。
const (
	tripoDefaultBaseURL  = "https://api.tripo3d.ai/v2/openapi"
	tripoDefaultModel    = "v2.5-20250123"
	tripoUploadPath      = "/upload"
	tripoTaskPath        = "/task"
	tripoBalancePath     = "/user/balance"
	tripoTextTaskType    = "text_to_model"
	tripoImageTaskType   = "image_to_model"
	tripoMultiTaskType   = "multiview_to_model"
	tripoModelVersionKey = "model_version"
)

// TripoProvider 实现 Provider。
type TripoProvider struct {
	apiKey       string
	baseURL      string
	defaultModel string
	client       *http.Client
}

// NewTripoProvider 创建 Tripo 适配器；apiKey 为空时视为未配置。
func NewTripoProvider(cfg *TripoConfig, timeoutSeconds int) *TripoProvider {
	if cfg == nil {
		cfg = &TripoConfig{}
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv(EnvTripoBaseURL)), "/")
	}
	if base == "" {
		base = tripoDefaultBaseURL
	}
	model := strings.TrimSpace(cfg.DefaultModel)
	if model == "" {
		model = tripoDefaultModel
	}
	return &TripoProvider{
		apiKey:       strings.TrimSpace(cfg.APIKey),
		baseURL:      base,
		defaultModel: model,
		client:       newHTTPClient(timeoutSeconds),
	}
}

// Key 返回服务商标识。
func (p *TripoProvider) Key() string { return "tripo" }

// Info 返回能力快照。
func (p *TripoProvider) Info() ProviderInfo {
	models := []string{
		"P1-20260311", "v3.1-20260211", "v3.0-20250812",
		"v2.5-20250123", "Turbo-v1.0-20250506", "v2.0-20240919", "v1.4-20240625",
	}
	specs := make([]ModelSpec, 0, len(models))
	for _, model := range models {
		specs = append(specs, ModelSpec{
			ID:      model,
			Modes:   []Mode{ModeTextTo3D, ModeImageTo3D},
			Default: model == p.defaultModel,
		})
	}
	return ProviderInfo{
		Key:         p.Key(),
		DisplayName: "Tripo",
		Modes:       []Mode{ModeTextTo3D, ModeImageTo3D},
		Models:      specs,
		Configured:  p.apiKey != "",
		BaseURL:     p.baseURL,
		Notes: []string{
			"产物固定为 glb（output.model / output.pbr_model / output.base_model）",
			"image_base64 会先调用 /upload 换成 file_token 再创建任务",
		},
	}
}

// tripoEnvelope 是所有 Tripo 接口的通用信封。
type tripoEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// Create 创建 Tripo 任务。
func (p *TripoProvider) Create(ctx context.Context, req *CreateRequest) (string, error) {
	if err := p.ensureConfigured(); err != nil {
		return "", err
	}
	if err := req.Validate(); err != nil {
		return "", err
	}
	body, err := p.taskBody(ctx, req)
	if err != nil {
		return "", err
	}
	var envelope tripoEnvelope
	if err := doJSON(ctx, p.client, p.Key(), http.MethodPost, p.baseURL+tripoTaskPath, p.headers(), body, &envelope); err != nil {
		return "", err
	}
	if err := p.envelopeError(envelope); err != nil {
		return "", err
	}
	var data struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return "", &UpstreamError{Provider: p.Key(), Message: "decode task id: " + err.Error()}
	}
	if strings.TrimSpace(data.TaskID) == "" {
		return "", &UpstreamError{Provider: p.Key(), Message: "missing task_id"}
	}
	return data.TaskID, nil
}

// taskBody 组装创建请求体。图片既支持公网 URL（file.url），
// 也支持内联 base64（先上传换 file_token）。
func (p *TripoProvider) taskBody(ctx context.Context, req *CreateRequest) (map[string]any, error) {
	body := map[string]any{tripoModelVersionKey: p.resolveModel(req.Model)}
	if req.FaceLimit > 0 {
		body["face_limit"] = req.FaceLimit
	}
	if req.Texture != nil {
		body["texture"] = *req.Texture
	}
	if req.PBR != nil {
		body["pbr"] = *req.PBR
	}
	if req.NegativePrompt != "" {
		body["negative_prompt"] = req.NegativePrompt
	}
	switch req.Mode {
	case ModeTextTo3D:
		body["type"] = tripoTextTaskType
		body["prompt"] = strings.TrimSpace(req.Prompt)
	case ModeImageTo3D:
		file, err := p.fileContent(ctx, req)
		if err != nil {
			return nil, err
		}
		body["type"] = tripoImageTaskType
		body["file"] = file
	default:
		return nil, fmt.Errorf("%w: mode %q", ErrUnsupported, req.Mode)
	}
	mergeOptions(body, req.Options)
	return body, nil
}

// fileContent 生成 Tripo 的 file 字段；多视图时返回 files 数组。
func (p *TripoProvider) fileContent(ctx context.Context, req *CreateRequest) (any, error) {
	if len(req.ImageURLs) > 0 {
		files := make([]map[string]any, 0, len(req.ImageURLs))
		for _, url := range req.ImageURLs {
			if strings.TrimSpace(url) == "" {
				continue
			}
			files = append(files, map[string]any{"type": "jpg", "url": strings.TrimSpace(url)})
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("%w: image_urls is empty", ErrInvalidRequest)
		}
		return files, nil
	}
	if url := strings.TrimSpace(req.ImageURL); url != "" {
		return map[string]any{"type": "jpg", "url": url}, nil
	}
	data, contentType, err := decodeInlineImage(req.ImageBase64)
	if err != nil {
		return nil, err
	}
	token, err := p.uploadImage(ctx, data, contentType)
	if err != nil {
		return nil, err
	}
	return map[string]any{"type": "jpg", "file_token": token}, nil
}

// uploadImage 把内联图片上传到 Tripo，返回 file_token。
func (p *TripoProvider) uploadImage(ctx context.Context, data []byte, contentType string) (string, error) {
	var envelope tripoEnvelope
	files := []multipartFile{{
		Field:       "file",
		Filename:    "image" + imageFileExtension(contentType),
		ContentType: contentType,
		Data:        data,
	}}
	if err := postMultipart(ctx, p.client, p.Key(), p.baseURL+tripoUploadPath, p.headers(), nil, files, &envelope); err != nil {
		return "", err
	}
	if err := p.envelopeError(envelope); err != nil {
		return "", err
	}
	var data2 struct {
		ImageToken string `json:"image_token"`
		FileToken  string `json:"file_token"`
	}
	if err := json.Unmarshal(envelope.Data, &data2); err != nil {
		return "", &UpstreamError{Provider: p.Key(), Message: "decode upload token: " + err.Error()}
	}
	token := firstNonEmpty(data2.ImageToken, data2.FileToken)
	if token == "" {
		return "", &UpstreamError{Provider: p.Key(), Message: "missing upload token"}
	}
	return token, nil
}

// Get 查询任务。
func (p *TripoProvider) Get(ctx context.Context, taskID string) (*Task, error) {
	if err := p.ensureConfigured(); err != nil {
		return nil, err
	}
	var envelope tripoEnvelope
	if err := doJSON(ctx, p.client, p.Key(), http.MethodGet, p.baseURL+tripoTaskPath+"/"+taskID, p.headers(), nil, &envelope); err != nil {
		return nil, err
	}
	if err := p.envelopeError(envelope); err != nil {
		return nil, err
	}
	var raw tripoTask
	if err := json.Unmarshal(envelope.Data, &raw); err != nil {
		return nil, &UpstreamError{Provider: p.Key(), Message: "decode task: " + err.Error()}
	}
	task := raw.toTask(p.Key())
	if task.Mode == "" {
		task.Mode = tripoModeFromType(raw.Type)
	}
	return task, nil
}

// Cancel 取消任务。
func (p *TripoProvider) Cancel(ctx context.Context, taskID string) error {
	if err := p.ensureConfigured(); err != nil {
		return err
	}
	var envelope tripoEnvelope
	if err := doJSON(ctx, p.client, p.Key(), http.MethodPost, p.baseURL+tripoTaskPath+"/"+taskID+"/cancel", p.headers(), map[string]any{}, &envelope); err != nil {
		return err
	}
	return p.envelopeError(envelope)
}

// Balance 查询 Tripo 余额（balance 为可用额度，frozen 为冻结额度）。
func (p *TripoProvider) Balance(ctx context.Context) (float64, error) {
	if err := p.ensureConfigured(); err != nil {
		return 0, err
	}
	var envelope tripoEnvelope
	if err := doJSON(ctx, p.client, p.Key(), http.MethodGet, p.baseURL+tripoBalancePath, p.headers(), nil, &envelope); err != nil {
		return 0, err
	}
	if err := p.envelopeError(envelope); err != nil {
		return 0, err
	}
	var data struct {
		Balance float64 `json:"balance"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return 0, &UpstreamError{Provider: p.Key(), Message: "decode balance: " + err.Error()}
	}
	return data.Balance, nil
}

// envelopeError 把 Tripo 的业务错误码转成 UpstreamError（HTTP 200 也可能带非 0 code）。
func (p *TripoProvider) envelopeError(envelope tripoEnvelope) error {
	if envelope.Code == 0 {
		return nil
	}
	return &UpstreamError{
		Provider:   p.Key(),
		StatusCode: http.StatusBadGateway,
		Code:       strconv.Itoa(envelope.Code),
		Message:    firstNonEmpty(strings.TrimSpace(envelope.Message), "tripo rejected the request"),
	}
}

func (p *TripoProvider) ensureConfigured() error {
	if p.apiKey == "" {
		return fmt.Errorf("%w: tripo api key is not configured", ErrProviderUnavailable)
	}
	return nil
}

func (p *TripoProvider) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + p.apiKey}
}

func (p *TripoProvider) resolveModel(requested string) string {
	if model := strings.TrimSpace(requested); model != "" {
		return model
	}
	return p.defaultModel
}

func tripoModeFromType(taskType string) Mode {
	switch strings.ToLower(strings.TrimSpace(taskType)) {
	case tripoImageTaskType, tripoMultiTaskType, "generate_multiview_image", "edit_multiview_image", "import_model":
		return ModeImageTo3D
	default:
		return ModeTextTo3D
	}
}

// tripoTask 是 Tripo 任务数据。
type tripoTask struct {
	TaskID   string `json:"task_id"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	CreateAt int64  `json:"create_time"`
	ErrorMsg string `json:"error_msg"`
	Output   struct {
		Model         string `json:"model"`
		PBRModel      string `json:"pbr_model"`
		BaseModel     string `json:"base_model"`
		RenderedImage string `json:"rendered_image"`
	} `json:"output"`
}

func (t tripoTask) toTask(provider string) *Task {
	task := &Task{
		ID:             t.TaskID,
		Mode:           tripoModeFromType(t.Type),
		Provider:       provider,
		ProviderStatus: t.Status,
		Status:         normalizeTripoStatus(t.Status),
		Progress:       clampProgress(t.Progress),
		Error:          strings.TrimSpace(t.ErrorMsg),
		CreatedAt:      t.CreateAt,
	}
	if t.Status == string(StatusSucceeded) || task.Status == StatusSucceeded {
		if strings.TrimSpace(t.Output.RenderedImage) != "" {
			task.ThumbnailURL = t.Output.RenderedImage
		}
	}
	assets := make([]Asset, 0, 3)
	for _, item := range []struct {
		name string
		url  string
	}{
		{"model", t.Output.Model},
		{"pbr_model", t.Output.PBRModel},
		{"base_model", t.Output.BaseModel},
	} {
		if strings.TrimSpace(item.url) == "" {
			continue
		}
		assets = append(assets, Asset{Format: "glb", URL: item.url, Name: item.name})
	}
	if len(assets) > 0 {
		task.Assets = assets
	}
	if task.Status == StatusSucceeded && task.Progress == 0 {
		task.Progress = 100
	}
	return task
}

func normalizeTripoStatus(status string) Status {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "queueing", "pending":
		return StatusQueued
	case "running", "processing", "in_progress":
		return StatusRunning
	case "success", "succeeded":
		return StatusSucceeded
	case "failed", "banned", "expired", "error":
		return StatusFailed
	case "cancelled", "canceled":
		return StatusCancelled
	default:
		return StatusQueued
	}
}
