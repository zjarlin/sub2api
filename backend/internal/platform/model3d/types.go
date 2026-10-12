// Package model3d 聚合第三方「文生 3D / 图生 3D」服务商（Meshy、Tripo、Hi3D），
// 把各家的异步任务协议归一成统一的创建、查询、取消、产物下载与余额查询接口。
//
// 与 translate 聚合器一致：凭据来自部署环境（环境变量或 config.yaml），
// 由网关统一鉴权、限权后转发，服务商密钥不下发到客户端。
package model3d

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Mode 是 3D 生成任务的输入形态。
type Mode string

const (
	// ModeTextTo3D 文本提示词生成 3D 模型。
	ModeTextTo3D Mode = "text-to-3d"
	// ModeImageTo3D 单图（或多视图）生成 3D 模型。
	ModeImageTo3D Mode = "image-to-3d"
)

// Normalize 归一化外部传入的 mode 别名，未知形态返回空字符串。
func (m Mode) Normalize() Mode {
	switch strings.ToLower(strings.TrimSpace(string(m))) {
	case "text-to-3d", "text_to_3d", "text2d3d", "text":
		return ModeTextTo3D
	case "image-to-3d", "image_to_3d", "image2d3d", "image", "img2model":
		return ModeImageTo3D
	default:
		return ""
	}
}

// Status 是归一化后的任务状态。各服务商原始状态保留在 Task.ProviderStatus。
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Terminal 判断是否为终态。终态任务不再变化，可以停止轮询。
func (s Status) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled
}

// Asset 是任务产出的模型文件或预览图。
type Asset struct {
	// Format 是文件扩展名（glb/obj/fbx/usdz/stl/3mf/png...），小写、不含点。
	Format string `json:"format"`
	// URL 是服务商返回的临时下载地址，有效期由服务商决定。
	URL string `json:"url"`
	// Name 是可选的展示名，例如 Meshy 的 model_urls 键名。
	Name string `json:"name,omitempty"`
}

// Task 是归一化后的 3D 生成任务。
type Task struct {
	ID             string  `json:"id"`              // 服务商任务 ID
	Mode           Mode    `json:"mode"`            // 输入形态
	Provider       string  `json:"provider"`        // 服务商 key
	Model          string  `json:"model,omitempty"` // 服务商模型/版本
	ProviderStatus string  `json:"provider_status"` // 服务商原始状态字符串
	Status         Status  `json:"status"`          // 归一化状态
	Progress       int     `json:"progress"`        // 0-100，未知时为 0
	Assets         []Asset `json:"assets,omitempty"`
	ThumbnailURL   string  `json:"thumbnail_url,omitempty"`
	Prompt         string  `json:"prompt,omitempty"`
	Error          string  `json:"error,omitempty"`
	// Credits 是服务商上报的本任务消耗积分；未知时为 0。
	Credits float64 `json:"credits,omitempty"`
	// CreatedAt/FinishedAt 是 Unix 秒级时间戳；未知时为 0。
	CreatedAt  int64 `json:"created_at,omitempty"`
	FinishedAt int64 `json:"finished_at,omitempty"`
}

// CreateRequest 是统一的任务创建请求。
type CreateRequest struct {
	// AccountID 仅由网关解析精修句柄后设置，不接受外部 JSON 指定。
	AccountID int64 `json:"-"`
	// Mode 必填，text-to-3d 或 image-to-3d。
	Mode Mode `json:"mode"`
	// Prompt 文生 3D 的提示词。
	Prompt string `json:"prompt,omitempty"`
	// ImageURL 图生 3D 的输入图片地址（公网可访问的 http/https URL）。
	ImageURL string `json:"image_url,omitempty"`
	// ImageBase64 图生 3D 的内联图片（PNG/JPEG/WebP），支持 data URI 或裸 base64。
	ImageBase64 string `json:"image_base64,omitempty"`
	// ImageURLs 多视图图生 3D 的输入地址（最多 4 张，顺序：前、后、左、右）。
	ImageURLs []string `json:"image_urls,omitempty"`
	// Provider 指定服务商；留空按聚合器优先级自动选择。
	Provider string `json:"provider,omitempty"`
	// Model 指定服务商模型/版本；留空使用服务商默认值。
	Model string `json:"model,omitempty"`
	// NegativePrompt 部分服务商支持的反向提示词。
	NegativePrompt string `json:"negative_prompt,omitempty"`
	// Texture 是否生成纹理；nil 表示使用服务商默认值。
	Texture *bool `json:"texture,omitempty"`
	// PBR 是否生成 PBR 材质；nil 表示使用服务商默认值。
	PBR *bool `json:"pbr,omitempty"`
	// FaceLimit 目标面数上限；0 表示使用服务商默认值。
	FaceLimit int `json:"face_limit,omitempty"`
	// Topology 拓扑："quad"/"triangle"，由具体服务商解释。
	Topology string `json:"topology,omitempty"`
	// Format 期望的产物格式（glb/obj/fbx/usdz/stl/3mf）；留空使用服务商默认值。
	Format string `json:"format,omitempty"`
	// Options 服务商原生扩展参数，原样合并进上游请求体（白名单外的键也会被传递）。
	Options map[string]any `json:"options,omitempty"`
}

// Validate 做与服务商无关的基础校验，避免把明显非法的请求打到上游。
func (r *CreateRequest) Validate() error {
	if r == nil {
		return errors.New("empty request")
	}
	r.Mode = r.Mode.Normalize()
	if r.Mode == "" {
		return fmt.Errorf("%w: mode must be text-to-3d or image-to-3d", ErrInvalidRequest)
	}
	switch r.Mode {
	case ModeTextTo3D:
		if strings.TrimSpace(r.Prompt) == "" && len(r.ImageURLs) == 0 {
			return fmt.Errorf("%w: prompt is required for text-to-3d", ErrInvalidRequest)
		}
	case ModeImageTo3D:
		if strings.TrimSpace(r.ImageURL) == "" && strings.TrimSpace(r.ImageBase64) == "" && len(r.ImageURLs) == 0 {
			return fmt.Errorf("%w: image_url, image_base64 or image_urls is required for image-to-3d", ErrInvalidRequest)
		}
	}
	return nil
}

// ModelSpec 是服务商可选模型/版本的描述。
type ModelSpec struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Modes       []Mode `json:"modes"`
	Default     bool   `json:"default,omitempty"`
}

// ProviderInfo 是服务商能力快照，用于 /providers 与边缘控制台展示。
type ProviderInfo struct {
	Key         string      `json:"key"`
	DisplayName string      `json:"display_name"`
	Modes       []Mode      `json:"modes"`
	Models      []ModelSpec `json:"models,omitempty"`
	Configured  bool        `json:"configured"`
	Default     bool        `json:"default,omitempty"`
	BaseURL     string      `json:"base_url,omitempty"`
	// Notes 记录能力边界，例如 Hi3D 只支持图生 3D。
	Notes []string `json:"notes,omitempty"`
}

// Supports 判断服务商是否支持某个输入形态。
func (p ProviderInfo) Supports(mode Mode) bool {
	for _, m := range p.Modes {
		if m == mode {
			return true
		}
	}
	return false
}

// Provider 是单个 3D 服务商适配器。
type Provider interface {
	// Key 返回稳定的小写服务商标识，例如 "meshy"。
	Key() string
	// Info 返回该服务商的能力与配置状态。
	Info() ProviderInfo
	// Create 创建异步任务并返回上游任务 ID。
	Create(ctx context.Context, req *CreateRequest) (string, error)
	// Get 查询任务；返回的 Task.Provider 由调用方补齐。
	Get(ctx context.Context, taskID string) (*Task, error)
	// Cancel 取消任务；服务商不支持时返回 ErrUnsupported。
	Cancel(ctx context.Context, taskID string) error
	// Balance 查询服务商账户余额；不支持时返回 ErrUnsupported。
	Balance(ctx context.Context) (float64, error)
}

// 聚合器与适配器对外暴露的稳定错误。
var (
	// ErrProviderUnavailable 表示请求指定的服务商不存在或未配置凭据。
	ErrProviderUnavailable = errors.New("model3d: provider unavailable")
	// ErrInvalidRequest 表示请求缺少必要字段或字段非法。
	ErrInvalidRequest = errors.New("model3d: invalid request")
	// ErrUnsupported 表示服务商不支持该能力（例如 Hi3D 不支持文生 3D）。
	ErrUnsupported = errors.New("model3d: unsupported by provider")
	// ErrNoProvider 表示没有任何已配置的服务商可以处理该请求。
	ErrNoProvider = errors.New("model3d: no provider configured")
	// ErrTaskExpired 表示任务句柄已过期，无法再映射到上游任务。
	ErrTaskExpired = errors.New("model3d: task handle expired")
	// ErrTaskMismatch 表示任务句柄校验失败（越权、篡改或跨用户）。
	ErrTaskMismatch = errors.New("model3d: task handle rejected")
)

// UpstreamError 保留上游返回的状态码与业务错误码，便于网关原样分级返回。
type UpstreamError struct {
	Provider   string
	StatusCode int
	Code       string
	Message    string
}

func (e *UpstreamError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return fmt.Sprintf("%s upstream error %d (%s): %s", e.Provider, e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("%s upstream error %d: %s", e.Provider, e.StatusCode, e.Message)
}

// ChainError 记录自动选择服务商时的全部尝试，便于排查为什么整体失败。
type ChainError struct {
	Attempts []Attempt
}

// Attempt 是一次服务商尝试的结果。
type Attempt struct {
	Provider string `json:"provider"`
	Error    string `json:"error,omitempty"`
	OK       bool   `json:"ok"`
}

func (e *ChainError) Error() string {
	if e == nil || len(e.Attempts) == 0 {
		return "model3d: all providers failed"
	}
	parts := make([]string, 0, len(e.Attempts))
	for _, a := range e.Attempts {
		parts = append(parts, fmt.Sprintf("%s: %s", a.Provider, a.Error))
	}
	return "model3d: all providers failed (" + strings.Join(parts, "; ") + ")"
}
