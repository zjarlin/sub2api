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

func (h *GatewayHandler) executeVerticalMedia(c *gin.Context, key *service.APIKey, requested string, body []byte, prompt, kind, selected, platform string, resolver *service.CompositeRouteResolver, images, videos gin.HandlerFunc) {
	operation := &service.VerticalOperation{Kind: kind, Provider: platform}
	route, finish := h.verticalObservation(c, key, requested, selected, operation)
	defer finish()
	endpoint := service.CompositeRouteEndpointImages
	path := "/v1/images/generations"
	handler := images
	if kind == "video_generation" {
		endpoint = service.CompositeRouteEndpointAny
		path = "/v1/videos/generations"
		handler = videos
	}
	ctx := service.WithResolvedTargetPlatform(c.Request.Context(), platform)
	mediaBody := verticalMediaRequest(selected, prompt, kind)
	decision, err := resolver.Resolve(ctx, key.Group.ID, selected, endpoint)
	if err != nil {
		verticalFailure(c, route, http.StatusServiceUnavailable, "Media route unavailable")
		return
	}
	if decision.Matched {
		ctx = service.WithCompositeRouteDecision(ctx, decision)
		if decision.UpstreamModel != "" {
			mediaBody, err = sjson.SetBytes(mediaBody, "model", decision.UpstreamModel)
			if err != nil {
				verticalFailure(c, route, http.StatusInternalServerError, "Unable to prepare media request")
				return
			}
		}
	}
	route.State = "responding"
	persistVerticalState(c)
	status, result := verticalInternalRequest(c, ctx, http.MethodPost, path, mediaBody, nil, handler)
	if status < 200 || status >= 300 {
		route.State = "failed"
		if gjson.ValidBytes(result) && gjson.GetBytes(result, "error").Exists() {
			c.Data(status, "application/json", result)
		} else {
			verticalFailure(c, route, http.StatusBadGateway, "Media provider could not complete this request")
		}
		return
	}
	route.ResolvedModel = gjson.GetBytes(result, "model").String()
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
	for _, item := range gjson.GetBytes(result, "data").Array() {
		if len(outputs)+len(operation.Artifacts) >= 16 {
			break
		}
		if location := verticalArtifactURL(item.Get("url").String()); location != "" {
			operation.Artifacts = append(operation.Artifacts, service.RouteArtifact{Kind: "image", URL: location})
			text = append(text, "![生成图片](<"+location+">)")
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
				outputs = append(outputs, map[string]any{"type": "image_generation_call", "status": "completed", "result": encoded, "output_format": format})
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
		w.status = http.StatusBadGateway
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
