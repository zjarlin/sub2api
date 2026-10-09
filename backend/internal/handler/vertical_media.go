package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

func persistVerticalState(c *gin.Context) {
	if value, exists := c.Get(autoRoutePersistKey); exists {
		value.(func())()
	}
}

// 图片选项只取协议字段，不改写输入和系统提示；不支持的供应商参数明确报错。
func verticalImageOptions(mediaBody, original []byte, dashScope bool) ([]byte, error) {
	for _, tool := range gjson.GetBytes(original, "tools").Array() {
		if tool.Get("type").String() != "image_generation" {
			continue
		}
		for _, field := range []string{"size", "quality", "background", "output_format", "output_compression", "moderation"} {
			value := tool.Get(field)
			if !value.Exists() || value.String() == "auto" {
				continue
			}
			if dashScope {
				return nil, errors.New("Requested image options are not supported by the chat image adapter")
			}
			var err error
			mediaBody, err = sjson.SetBytes(mediaBody, field, value.Value())
			if err != nil {
				return nil, err
			}
		}
		break
	}
	return mediaBody, nil
}

func (h *GatewayHandler) executeVerticalMedia(c *gin.Context, key *service.APIKey, requested string, body []byte, prompt, kind, selected, platform string, resolver *service.CompositeRouteResolver, images, videos gin.HandlerFunc, chats ...gin.HandlerFunc) {
	operation := &service.VerticalOperation{Kind: kind, Provider: platform}
	route, finish := h.verticalObservation(c, key, requested, selected, operation)
	defer finish()
	route.State = "responding"
	persistVerticalState(c)
	status, result, err := h.requestVerticalMedia(c, key, body, prompt, kind, selected, platform, resolver, images, videos, chats...)
	// 只对明确失败的生图请求降级；视频提交及成功出图后的下载不重复计费调用。
	if kind == "image_generation" && err == nil && (status == 429 || status >= 500) && h.settingService != nil {
		policy, policyErr := h.settingService.GetAutoModelPolicy(c.Request.Context())
		if policyErr == nil {
			seen := map[string]bool{selected: true}
			for _, candidate := range policy.VerticalPolicy().ImageFallbackModels {
				if c.Request.Context().Err() != nil || seen[candidate] {
					continue
				}
				seen[candidate] = true
				model, target, resolveErr := h.verticalMediaModel(c.Request.Context(), key.Group, resolver, kind, service.VerticalRoutingPolicy{ImageModel: candidate})
				if resolveErr != nil || model == "" || seen[model] && model != candidate {
					continue
				}
				seen[model] = true
				selected, platform = model, target
				route.SelectedModel, operation.Provider = selected, platform
				route.AttemptedModels = append(route.AttemptedModels, selected)
				c.Header("X-Sub2API-Selected-Model", selected)
				persistVerticalState(c)
				status, result, err = h.requestVerticalMedia(c, key, body, prompt, kind, selected, platform, resolver, images, videos, chats...)
				if err != nil || (status != 429 && status < 500) {
					break
				}
			}
		}
	}
	if err != nil {
		verticalFailure(c, route, status, err.Error())
		return
	}
	h.finishVerticalMedia(c, key, requested, body, route, kind, selected, status, result)
}

func (h *GatewayHandler) requestVerticalMedia(c *gin.Context, key *service.APIKey, body []byte, prompt, kind, selected, platform string, resolver *service.CompositeRouteResolver, images, videos gin.HandlerFunc, chats ...gin.HandlerFunc) (int, []byte, error) {
	endpoint := service.CompositeRouteEndpointImages
	if kind == "video_generation" {
		endpoint = service.CompositeRouteEndpointAny
	} else if service.IsDashScopeChatImageModel(selected) {
		endpoint = service.CompositeRouteEndpointChatCompletions
	}
	ctx := service.WithResolvedTargetPlatform(c.Request.Context(), platform)
	decision, err := resolver.Resolve(ctx, key.Group.ID, selected, endpoint)
	if err != nil {
		return http.StatusServiceUnavailable, nil, errors.New("Media route unavailable")
	}
	mediaModel := selected
	if decision.Matched && decision.UpstreamModel != "" {
		mediaModel = decision.UpstreamModel
	}
	path := "/v1/images/generations"
	handler := images
	endpoint = service.CompositeRouteEndpointImages
	dashScopeImage := kind == "image_generation" && service.IsDashScopeChatImageModel(mediaModel)
	if kind == "video_generation" {
		endpoint = service.CompositeRouteEndpointAny
		path = "/v1/videos/generations"
		handler = videos
	} else if dashScopeImage {
		// 百炼图片模型只接受 Chat Completions 的 content 列表格式。
		endpoint = service.CompositeRouteEndpointChatCompletions
		path = "/v1/chat/completions"
		if len(chats) == 0 || chats[0] == nil {
			return http.StatusServiceUnavailable, nil, errors.New("Chat image adapter is unavailable")
		}
		handler = chats[0]
	}
	mediaBody := verticalMediaRequest(mediaModel, prompt, kind)
	if dashScopeImage {
		mediaBody = verticalDashScopeImageRequest(mediaModel, prompt)
	}
	if kind == "image_generation" {
		var err error
		mediaBody, err = verticalImageOptions(mediaBody, body, dashScopeImage)
		if err != nil {
			return http.StatusBadRequest, nil, err
		}
	}
	if decision.Matched {
		decision.Endpoint = endpoint
		ctx = service.WithCompositeRouteDecision(ctx, decision)
	}
	if handler == nil {
		return http.StatusServiceUnavailable, nil, errors.New("Media adapter is unavailable")
	}
	status, result := verticalInternalRequest(c, ctx, http.MethodPost, path, mediaBody, nil, handler)
	return status, result, nil
}

func (h *GatewayHandler) finishVerticalMedia(c *gin.Context, key *service.APIKey, requested string, body []byte, route *service.AutoModelRouteObservation, kind, selected string, status int, result []byte) {
	operation := route.Operation
	if status < 200 || status >= 300 {
		route.State = "failed"
		if gjson.ValidBytes(result) && gjson.GetBytes(result, "error").Exists() {
			c.Data(status, "application/json", result)
		} else {
			verticalFailure(c, route, http.StatusBadGateway, "Media provider could not complete this request")
		}
		return
	}
	// 百炼/Wan 图片模型走 Chat Completions 出图，返回体结构与 OpenAI Images 不同。
	if kind == "image_generation" && (service.IsDashScopeChatImageModel(selected) || gjson.GetBytes(result, "output.choices").IsArray()) {
		h.finishVerticalDashScopeImage(c, key, requested, body, route, selected, result)
		return
	}
	route.ResolvedModel = gjson.GetBytes(result, "model").String()
	if route.ResolvedModel == "" {
		route.ResolvedModel = selected
	}
	if kind == "video_generation" {
		applyVerticalVideoResult(route, result)
		if route.State == "responding" {
			verticalFailure(c, route, http.StatusBadGateway, "Video provider returned no task or artifact")
			return
		}
		text := "视频任务已提交，尚未生成完成。任务 ID：" + operation.TaskID
		if route.State == "completed" {
			text = "视频已生成。"
		}
		if route.State == "failed" {
			verticalFailure(c, route, http.StatusBadGateway, "Video generation failed")
			return
		}
		for _, artifact := range operation.Artifacts {
			text += "\n\n[视频](<" + artifact.URL + ">)"
		}
		writeVerticalReply(c, body, requested, text, nil)
		return
	}
	var outputs []map[string]any
	var text []string
	for index, item := range gjson.GetBytes(result, "data").Array() {
		if index >= 16 {
			break
		}
		if location := verticalArtifactURL(item.Get("url").String()); location != "" && item.Get("b64_json").String() == "" {
			operation.Artifacts = append(operation.Artifacts, service.RouteArtifact{Kind: "image", URL: location})
			text = append(text, "![生成图片](<"+location+">)")
			if !strings.HasSuffix(c.Request.URL.Path, "/chat/completions") {
				image, err := h.verticalResponseImage(c, location)
				if err != nil {
					verticalFailure(c, route, http.StatusBadGateway, "Unable to retrieve generated image")
					return
				}
				outputs = append(outputs, image)
				if verticalImagePayloadTooLarge(outputs) {
					verticalFailure(c, route, http.StatusBadGateway, "Generated images exceed response size limit")
					return
				}
			}
		} else {
			encoded := item.Get("b64_json").String()
			format := "png"
			if encoded == "" {
				prefix, content, found := strings.Cut(item.Get("url").String(), ",")
				if found {
					switch prefix {
					case "data:image/png;base64":
						encoded = content
					case "data:image/jpeg;base64":
						encoded, format = content, "jpeg"
					case "data:image/webp;base64":
						encoded, format = content, "webp"
					}
				}
			}
			if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil && len(decoded) > 0 {
				format = strings.TrimPrefix(http.DetectContentType(decoded), "image/")
				if format != "png" && format != "jpeg" && format != "webp" {
					verticalFailure(c, route, http.StatusBadGateway, "Image provider returned invalid image data")
					return
				}
				outputs = append(outputs, map[string]any{"type": "image_generation_call", "status": "completed", "result": encoded, "output_format": format})
				if verticalImagePayloadTooLarge(outputs) {
					verticalFailure(c, route, http.StatusBadGateway, "Generated images exceed response size limit")
					return
				}
				if strings.HasSuffix(c.Request.URL.Path, "/chat/completions") {
					text = append(text, "![生成图片](data:image/"+format+";base64,"+encoded+")")
				} else {
					text = append(text, "图片已生成。")
				}
			}
		}
	}
	if len(text) == 0 {
		verticalFailure(c, route, http.StatusBadGateway, "Image provider returned no artifact")
		return
	}
	route.State = "completed"
	writeVerticalReply(c, body, requested, strings.Join(text, "\n\n"), outputs)
}

// 解析百炼/Wan Chat Completions 出图响应；只接受 http(s) 图片地址。
func (h *GatewayHandler) finishVerticalDashScopeImage(c *gin.Context, key *service.APIKey, requested string, body []byte, route *service.AutoModelRouteObservation, selected string, result []byte) {
	operation := route.Operation
	route.ResolvedModel = selected
	if model := gjson.GetBytes(result, "model").String(); model != "" {
		route.ResolvedModel = model
	}
	var text []string
	var outputs []map[string]any
	for _, content := range gjson.GetBytes(result, "output.choices.0.message.content").Array() {
		if len(operation.Artifacts) >= 16 {
			break
		}
		if content.Get("type").String() != "image" {
			continue
		}
		location := verticalArtifactURL(content.Get("image").String())
		if location == "" {
			location = verticalArtifactURL(content.Get("image_url.url").String())
		}
		if location == "" {
			continue
		}
		operation.Artifacts = append(operation.Artifacts, service.RouteArtifact{Kind: "image", URL: location})
		text = append(text, "![生成图片](<"+location+">)")
		if !strings.HasSuffix(c.Request.URL.Path, "/chat/completions") {
			image, err := h.verticalResponseImage(c, location)
			if err != nil {
				verticalFailure(c, route, http.StatusBadGateway, "Unable to retrieve generated image")
				return
			}
			outputs = append(outputs, image)
			if verticalImagePayloadTooLarge(outputs) {
				verticalFailure(c, route, http.StatusBadGateway, "Generated images exceed response size limit")
				return
			}
		}
	}
	if len(text) == 0 {
		verticalFailure(c, route, http.StatusBadGateway, "Image provider returned no artifact")
		return
	}
	route.State = "completed"
	writeVerticalReply(c, body, requested, strings.Join(text, "\n\n"), outputs)
}

func (h *GatewayHandler) verticalResponseImage(c *gin.Context, location string) (map[string]any, error) {
	encoded, format, err := h.openAIGatewayService.FetchResponseImage(c.Request.Context(), location)
	if err != nil {
		return nil, err
	}
	return map[string]any{"type": "image_generation_call", "status": "completed", "result": encoded, "output_format": format}, nil
}

func verticalImagePayloadTooLarge(images []map[string]any) bool {
	bytes := 0
	for _, image := range images {
		encoded, _ := image["result"].(string)
		bytes += len(encoded)
	}
	return bytes > 64<<20
}

// 内部请求直接调用现有处理器，沿用用户身份、审计、并发、计费与任务归属。
func verticalInternalRequest(c *gin.Context, ctx context.Context, method, path string, body []byte, params gin.Params, handler gin.HandlerFunc) (int, []byte) {
	child := c.Copy()
	child.Request = c.Request.Clone(ctx)
	child.Request.Method = method
	child.Request.URL.Path = path
	child.Request.URL.RawPath = ""
	child.Request.URL.RawQuery = ""
	child.Request.Header.Set("Content-Type", "application/json")
	child.Params = params
	requestmodel.ResetRequestBody(child.Request, body)
	writer := &verticalResultWriter{header: make(http.Header), status: http.StatusOK}
	child.Writer = writer
	handler(child)
	return writer.status, writer.body.Bytes()
}

type verticalResultWriter struct {
	gin.ResponseWriter
	header  http.Header
	body    bytes.Buffer
	status  int
	written bool
}

func (w *verticalResultWriter) Header() http.Header { return w.header }
func (w *verticalResultWriter) WriteHeader(status int) {
	if !w.written {
		w.status = status
	}
}
func (w *verticalResultWriter) WriteHeaderNow() { w.written = true }
func (w *verticalResultWriter) Write(body []byte) (int, error) {
	w.written = true
	if w.body.Len()+len(body) > 64<<20 {
		// 本地大小限制不能伪装成上游 5xx，否则会重复提交已成功的生成任务。
		w.status = http.StatusRequestEntityTooLarge
		return 0, errors.New("media response too large")
	}
	return w.body.Write(body)
}
func (w *verticalResultWriter) WriteString(body string) (int, error) { return w.Write([]byte(body)) }
func (w *verticalResultWriter) Status() int                          { return w.status }
func (w *verticalResultWriter) Size() int {
	if !w.written {
		return -1
	}
	return w.body.Len()
}
func (w *verticalResultWriter) Written() bool { return w.written }
func (w *verticalResultWriter) Flush()        { w.WriteHeaderNow() }

func verticalArtifactURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || len(value) > 4096 || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") || strings.ContainsAny(value, "<>\r\n") {
		return ""
	}
	return value
}

func applyVerticalVideoResult(route *service.AutoModelRouteObservation, body []byte) {
	result := gjson.ParseBytes(body)
	operation := route.Operation
	if operation == nil {
		return
	}
	if id := result.Get("request_id").String(); id != "" && len(id) <= 128 {
		operation.TaskID = id
	}
	if operation.TaskID == "" {
		if id := result.Get("id").String(); id != "" && len(id) <= 128 {
			operation.TaskID = id
		}
	}
	location := verticalArtifactURL(result.Get("video.url").String())
	if location == "" {
		location = verticalArtifactURL(result.Get("url").String())
	}
	switch strings.ToLower(result.Get("status").String()) {
	case "failed", "error", "expired", "cancelled", "canceled":
		route.State = "failed"
	case "done", "completed", "succeeded", "success":
		if location != "" {
			route.State = "completed"
		}
	case "running", "in_progress", "processing":
		route.State = "running"
	case "pending", "queued":
		route.State = "queued"
	default:
		if operation.TaskID != "" {
			route.State = "queued"
		}
	}
	if location != "" && route.State != "failed" {
		operation.Artifacts = []service.RouteArtifact{{Kind: "video", URL: location}}
		route.State = "completed"
	}
}

func (h *GatewayHandler) refreshVerticalVideos(c *gin.Context, key *service.APIKey, store service.AutoModelRouteStore, routes []service.AutoModelRouteObservation, status gin.HandlerFunc) {
	count := 0
	for index := range routes {
		route := &routes[index]
		operation := route.Operation
		if count >= 2 || operation == nil || operation.Kind != "video_generation" || operation.TaskID == "" ||
			(route.State != "queued" && route.State != "running") || time.Now().UnixMilli()-route.UpdatedAt < 2000 {
			continue
		}
		count++
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		ctx = service.WithResolvedTargetPlatform(ctx, operation.Provider)
		code, body := verticalInternalRequest(c, ctx, http.MethodGet, "/v1/videos/"+url.PathEscape(operation.TaskID), nil,
			gin.Params{{Key: "request_id", Value: operation.TaskID}}, status)
		cancel()
		if code < 200 || code >= 300 || !gjson.ValidBytes(body) {
			continue
		}
		applyVerticalVideoResult(route, body)
		route.Revision++
		route.UpdatedAt = time.Now().UnixMilli()
		if err := store.SaveAutoModelRoute(c.Request.Context(), key.ID, *route); err != nil {
			logger.FromContext(c.Request.Context()).Warn("gateway.vertical_video_state_save_failed", zap.Error(err))
		}
	}
}
