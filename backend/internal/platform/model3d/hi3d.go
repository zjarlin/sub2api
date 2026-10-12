package model3d

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Hi3D（Hitem3D）适配 https://docs.hi3d.ai 的开放接口：
//
//	POST {base}/open-api/v1/auth/token    Basic(client_id:client_secret) 换 accessToken
//	POST {base}/open-api/v1/submit-task    multipart 图生 3D（图生 3D 浮雕/拆件/多色同理）
//	GET  {base}/open-api/v1/query-task     查询任务
//	GET  {base}/open-api/v1/balance        查询余额
//
// Hi3D 只支持图生 3D：文生 3D 请求返回 ErrUnsupported，由聚合器说明原因。
const (
	hi3dDefaultBaseURL    = "https://api.hitem3d.ai"
	hi3dDefaultModel      = "hi3dv3.0"
	hi3dDefaultResolution = "2048quality"
	hi3dDefaultShading    = "0.5"
	hi3dTokenPath         = "/open-api/v1/auth/token"
	hi3dSubmitPath        = "/open-api/v1/submit-task"
	hi3dQueryPath         = "/open-api/v1/query-task"
	hi3dBalancePath       = "/open-api/v1/balance"

	hi3dRequestTypeBoth = "3"
	hi3dRequestTypeMesh = "1"

	// hi3dTokenFallbackTTL 在 token 不是可解析的 JWT 时使用的保守有效期。
	hi3dTokenFallbackTTL = 25 * time.Minute
	// hi3dTokenSkew 提前过期，避免边界时刻刚好失效。
	hi3dTokenSkew = 60 * time.Second
)

// hi3dFormatCodes 是 Hi3D 的产物格式枚举。
var hi3dFormatCodes = map[string]string{
	"obj":  "1",
	"glb":  "2",
	"stl":  "3",
	"fbx":  "4",
	"usdz": "5",
	"3mf":  "6",
}

// Hi3DProvider 实现 Provider。
type Hi3DProvider struct {
	clientID     string
	clientSecret string
	baseURL      string
	defaultModel string
	resolution   string
	faceLimit    int
	client       *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// NewHi3DProvider 创建 Hi3D 适配器；凭据缺失时视为未配置。
func NewHi3DProvider(cfg *Hi3DConfig, timeoutSeconds int) *Hi3DProvider {
	if cfg == nil {
		cfg = &Hi3DConfig{}
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv(EnvHi3DBaseURL)), "/")
	}
	if base == "" {
		base = hi3dDefaultBaseURL
	}
	model := firstNonEmpty(cfg.DefaultModel, os.Getenv(EnvHi3DDefaultModel), hi3dDefaultModel)
	resolution := firstNonEmpty(cfg.Resolution, os.Getenv(EnvHi3DResolution), hi3dDefaultResolution)
	return &Hi3DProvider{
		clientID:     strings.TrimSpace(cfg.ClientID),
		clientSecret: strings.TrimSpace(cfg.ClientSecret),
		baseURL:      base,
		defaultModel: model,
		resolution:   resolution,
		faceLimit:    cfg.FaceLimit,
		client:       newHTTPClient(timeoutSeconds),
	}
}

// Key 返回服务商标识。
func (p *Hi3DProvider) Key() string { return "hi3d" }

// Info 返回能力快照。Hi3D 仅支持图生 3D。
func (p *Hi3DProvider) Info() ProviderInfo {
	models := []string{"hi3dv3.0", "hitem3dv2.1", "hitem3dv2.0", "hitem3dv1.5", "scene-portraitv2.1", "scene-portraitv2.0", "scene-portraitv1.5"}
	specs := make([]ModelSpec, 0, len(models))
	for _, model := range models {
		specs = append(specs, ModelSpec{
			ID:      model,
			Modes:   []Mode{ModeImageTo3D},
			Default: model == p.defaultModel,
		})
	}
	return ProviderInfo{
		Key:         p.Key(),
		DisplayName: "Hi3D",
		Modes:       []Mode{ModeImageTo3D},
		Models:      specs,
		Configured:  p.clientID != "" && p.clientSecret != "",
		BaseURL:     p.baseURL,
		Notes: []string{
			"仅支持图生 3D；文生 3D 请使用 Meshy 或 Tripo",
			"输入图片由网关下载后以 multipart 上传，单张上限 20MB",
			"任务完成后产物 URL 有效期约一小时，请及时通过网关下载",
		},
	}
}

// hi3dEnvelope 是 Hi3D 的通用响应信封；code 可能是数字或字符串。
type hi3dEnvelope struct {
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
	Msg     string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

func (e hi3dEnvelope) code() int {
	raw := strings.TrimSpace(string(e.Code))
	raw = strings.Trim(raw, `"`)
	if raw == "" {
		return 0
	}
	var value int
	if _, err := fmt.Sscanf(raw, "%d", &value); err != nil {
		return 0
	}
	return value
}

func (e hi3dEnvelope) errorMessage() string {
	return firstNonEmpty(strings.TrimSpace(e.Message), strings.TrimSpace(e.Msg), "hi3d rejected the request")
}

// Create 创建图生 3D 任务。
func (p *Hi3DProvider) Create(ctx context.Context, req *CreateRequest) (string, error) {
	if err := p.ensureConfigured(); err != nil {
		return "", err
	}
	if err := req.Validate(); err != nil {
		return "", err
	}
	if req.Mode.Normalize() != ModeImageTo3D {
		return "", fmt.Errorf("%w: hi3d only accepts image-to-3d", ErrUnsupported)
	}
	images, err := p.images(ctx, req)
	if err != nil {
		return "", err
	}
	fields := map[string]string{
		"request_type": hi3dRequestTypeBoth,
		"model":        firstNonEmpty(req.Model, p.defaultModel),
		"resolution":   p.resolution,
		"pbr":          "1",
		"rmbg":         "1",
		"shading":      hi3dDefaultShading,
		"format":       formatOrDefault(req.Format, hi3dFormatCodes, hi3dFormatCodes["glb"]),
	}
	if req.Texture != nil && !*req.Texture {
		fields["request_type"] = hi3dRequestTypeMesh
	}
	if p.faceLimit > 0 {
		fields["face"] = fmt.Sprintf("%d", p.faceLimit)
	}
	applyHi3DOptions(fields, req)

	var envelope hi3dEnvelope
	err = p.callWithToken(ctx, func(callCtx context.Context, token string) error {
		envelope = hi3dEnvelope{}
		headers := map[string]string{"Authorization": "Bearer " + token}
		if postErr := postMultipart(callCtx, p.client, p.Key(), p.baseURL+hi3dSubmitPath, headers, fields, images, &envelope); postErr != nil {
			return postErr
		}
		return hi3dUnauthorized(p.Key(), envelope)
	})
	if err != nil {
		return "", err
	}
	if code := envelope.code(); code != 0 && code != http.StatusOK {
		return "", &UpstreamError{Provider: p.Key(), StatusCode: code, Code: fmt.Sprintf("%d", code), Message: envelope.errorMessage()}
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

// images 准备 multipart 文件：单图走 images 字段，多视图走 multi_images + multi_images_bit。
func (p *Hi3DProvider) images(ctx context.Context, req *CreateRequest) ([]multipartFile, error) {
	if len(req.ImageURLs) > 1 {
		if len(req.ImageURLs) > 4 {
			return nil, fmt.Errorf("%w: hi3d accepts at most 4 views", ErrInvalidRequest)
		}
		files := make([]multipartFile, 0, len(req.ImageURLs))
		for index, rawURL := range req.ImageURLs {
			data, contentType, err := fetchImage(ctx, p.client, rawURL)
			if err != nil {
				return nil, err
			}
			files = append(files, multipartFile{
				Field:       "multi_images",
				Filename:    fmt.Sprintf("view%d%s", index, imageFileExtension(contentType)),
				ContentType: contentType,
				Data:        data,
			})
		}
		return files, nil
	}

	data, contentType, err := p.singleImage(ctx, req)
	if err != nil {
		return nil, err
	}
	return []multipartFile{{
		Field:       "images",
		Filename:    "image" + imageFileExtension(contentType),
		ContentType: contentType,
		Data:        data,
	}}, nil
}

func (p *Hi3DProvider) singleImage(ctx context.Context, req *CreateRequest) ([]byte, string, error) {
	if inline := strings.TrimSpace(req.ImageBase64); inline != "" {
		return decodeInlineImage(inline)
	}
	if rawURL := strings.TrimSpace(req.ImageURL); rawURL != "" {
		return fetchImage(ctx, p.client, rawURL)
	}
	for _, rawURL := range req.ImageURLs {
		if strings.TrimSpace(rawURL) != "" {
			return fetchImage(ctx, p.client, rawURL)
		}
	}
	return nil, "", fmt.Errorf("%w: image is required for hi3d", ErrInvalidRequest)
}

// applyHi3DOptions 处理 Hi3D 特有扩展：多视图位图与回调地址。
func applyHi3DOptions(fields map[string]string, req *CreateRequest) {
	if len(req.ImageURLs) > 1 {
		if _, exists := fields["multi_images_bit"]; !exists {
			files := len(req.ImageURLs)
			if files > 4 {
				files = 4
			}
			fields["multi_images_bit"] = strings.Repeat("1", files) + strings.Repeat("0", 4-files)
		}
	}
	for key, value := range req.Options {
		key = strings.TrimSpace(key)
		if key == "" || isEmptyOption(value) {
			continue
		}
		if text, ok := value.(string); ok {
			fields[key] = text
			continue
		}
		if encoded, err := json.Marshal(value); err == nil {
			fields[key] = string(encoded)
		}
	}
}

// Get 查询任务。
func (p *Hi3DProvider) Get(ctx context.Context, taskID string) (*Task, error) {
	if err := p.ensureConfigured(); err != nil {
		return nil, err
	}
	var envelope hi3dEnvelope
	err := p.callWithToken(ctx, func(callCtx context.Context, token string) error {
		envelope = hi3dEnvelope{}
		query := url.Values{"task_id": []string{taskID}}
		headers := map[string]string{"Authorization": "Bearer " + token}
		if callErr := doJSON(callCtx, p.client, p.Key(), http.MethodGet, p.baseURL+hi3dQueryPath+"?"+query.Encode(), headers, nil, &envelope); callErr != nil {
			return callErr
		}
		return hi3dUnauthorized(p.Key(), envelope)
	})
	if err != nil {
		return nil, err
	}
	if code := envelope.code(); code != 0 && code != http.StatusOK {
		return nil, &UpstreamError{Provider: p.Key(), StatusCode: code, Code: fmt.Sprintf("%d", code), Message: envelope.errorMessage()}
	}
	var data struct {
		TaskID   string `json:"task_id"`
		State    string `json:"state"`
		ID       string `json:"id"`
		URL      string `json:"url"`
		CoverURL string `json:"cover_url"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return nil, &UpstreamError{Provider: p.Key(), Message: "decode task: " + err.Error()}
	}
	task := &Task{
		ID:             taskID,
		Mode:           ModeImageTo3D,
		Provider:       p.Key(),
		ProviderStatus: data.State,
		Status:         normalizeHi3DState(data.State),
		ThumbnailURL:   data.CoverURL,
	}
	if task.Status == StatusSucceeded {
		task.Progress = 100
	}
	if strings.TrimSpace(data.URL) != "" {
		task.Assets = []Asset{{Format: hi3dFormatFromURL(data.URL), URL: data.URL, Name: data.ID}}
	}
	if task.Status == StatusFailed {
		task.Error = "hi3d reported generate failed; credits are refunded by the provider"
	}
	return task, nil
}

// Cancel Hi3D 未开放取消接口。
func (p *Hi3DProvider) Cancel(_ context.Context, _ string) error {
	return fmt.Errorf("%w: hi3d does not expose a cancel endpoint", ErrUnsupported)
}

// Balance 查询 Hi3D 账户积分余额。
func (p *Hi3DProvider) Balance(ctx context.Context) (float64, error) {
	if err := p.ensureConfigured(); err != nil {
		return 0, err
	}
	var envelope hi3dEnvelope
	err := p.callWithToken(ctx, func(callCtx context.Context, token string) error {
		envelope = hi3dEnvelope{}
		headers := map[string]string{"Authorization": "Bearer " + token}
		if callErr := doJSON(callCtx, p.client, p.Key(), http.MethodGet, p.baseURL+hi3dBalancePath, headers, nil, &envelope); callErr != nil {
			return callErr
		}
		return hi3dUnauthorized(p.Key(), envelope)
	})
	if err != nil {
		return 0, err
	}
	if code := envelope.code(); code != 0 && code != http.StatusOK {
		return 0, &UpstreamError{Provider: p.Key(), StatusCode: code, Code: fmt.Sprintf("%d", code), Message: envelope.errorMessage()}
	}
	var data struct {
		TotalBalance float64 `json:"totalBalance"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return 0, &UpstreamError{Provider: p.Key(), Message: "decode balance: " + err.Error()}
	}
	return data.TotalBalance, nil
}

// callWithToken 执行一次需要 accessToken 的调用；遇到 401 时刷新令牌并重试一次。
//
// 只重试鉴权失败：3D 提交一旦被上游接受就可能计费，不能按通用错误重放。
func (p *Hi3DProvider) callWithToken(ctx context.Context, call func(ctx context.Context, token string) error) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		token, err := p.ensureToken(ctx, attempt > 0)
		if err != nil {
			return err
		}
		lastErr = call(ctx, token)
		if lastErr == nil {
			return nil
		}
		var upstream *UpstreamError
		if !errors.As(lastErr, &upstream) || upstream.StatusCode != http.StatusUnauthorized {
			return lastErr
		}
		p.invalidateToken()
	}
	return lastErr
}

// hi3dUnauthorized 把 HTTP 200 + code 401 的响应也识别成鉴权失败，触发令牌刷新。
func hi3dUnauthorized(provider string, envelope hi3dEnvelope) error {
	if envelope.code() != http.StatusUnauthorized {
		return nil
	}
	return &UpstreamError{Provider: provider, StatusCode: http.StatusUnauthorized, Message: envelope.errorMessage()}
}

// ensureToken 返回可用的 accessToken；force 为 true 时强制刷新。
func (p *Hi3DProvider) ensureToken(ctx context.Context, force bool) (string, error) {
	p.mu.Lock()
	if !force && p.token != "" && time.Now().Before(p.tokenExp) {
		token := p.token
		p.mu.Unlock()
		return token, nil
	}
	p.mu.Unlock()

	var envelope hi3dEnvelope
	basic := base64.StdEncoding.EncodeToString([]byte(p.clientID + ":" + p.clientSecret))
	headers := map[string]string{"Authorization": "Basic " + basic}
	if err := doJSON(ctx, p.client, p.Key(), http.MethodPost, p.baseURL+hi3dTokenPath, headers, map[string]any{}, &envelope); err != nil {
		return "", err
	}
	if code := envelope.code(); code != 0 && code != http.StatusOK {
		return "", &UpstreamError{Provider: p.Key(), StatusCode: code, Code: fmt.Sprintf("%d", code), Message: envelope.errorMessage()}
	}
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return "", &UpstreamError{Provider: p.Key(), Message: "decode access token: " + err.Error()}
	}
	if strings.TrimSpace(data.AccessToken) == "" {
		return "", &UpstreamError{Provider: p.Key(), Message: "missing access token"}
	}
	expiry := time.Now().Add(hi3dTokenFallbackTTL)
	if claimExpiry, ok := jwtExpiry(data.AccessToken); ok {
		expiry = claimExpiry
	}
	p.mu.Lock()
	p.token = data.AccessToken
	p.tokenExp = expiry
	p.mu.Unlock()
	return data.AccessToken, nil
}

func (p *Hi3DProvider) invalidateToken() {
	p.mu.Lock()
	p.token = ""
	p.tokenExp = time.Time{}
	p.mu.Unlock()
}

func (p *Hi3DProvider) ensureConfigured() error {
	if p.clientID == "" || p.clientSecret == "" {
		return fmt.Errorf("%w: hi3d client id/secret are not configured", ErrProviderUnavailable)
	}
	return nil
}

func normalizeHi3DState(state string) Status {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "created", "queueing", "queued", "pending":
		return StatusQueued
	case "processing", "running":
		return StatusRunning
	case "success", "succeeded":
		return StatusSucceeded
	case "failed", "error":
		return StatusFailed
	case "cancelled", "canceled":
		return StatusCancelled
	default:
		return StatusQueued
	}
}

// hi3dFormatFromURL 从产物 URL 的扩展名推断格式。
func hi3dFormatFromURL(rawURL string) string {
	path := rawURL
	if parsed, err := url.Parse(rawURL); err == nil && parsed.Path != "" {
		path = parsed.Path
	}
	if ext := trimExtension(path); ext != "" {
		return strings.TrimPrefix(ext, ".")
	}
	return "glb"
}

// jwtExpiry 解析 JWT 的 exp 声明；非 JWT 或解析失败时返回 false。
func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	expiry := time.Unix(claims.Exp, 0).Add(-hi3dTokenSkew)
	if !expiry.After(time.Now()) {
		return time.Now().Add(time.Minute), true
	}
	return expiry, true
}
