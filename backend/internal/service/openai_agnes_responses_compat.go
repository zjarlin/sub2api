package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
)

const agnesAIResponsesCompatHost = "apihub.agnes-ai.com"

func accountUsesAgnesAIResponsesCompat(account *Account) bool {
	if account == nil || !account.IsOpenAIApiKey() {
		return false
	}
	host := normalizedOpenAIBaseURLHostname(account.GetOpenAIBaseURL())
	return host == agnesAIResponsesCompatHost
}

func normalizedOpenAIBaseURLHostname(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Hostname() == "" {
		parsed, err = url.Parse("https://" + strings.TrimLeft(trimmed, "/"))
		if err != nil {
			return ""
		}
	}
	return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
}

func applyAgnesAIResponsesCompat(reqBody map[string]any, upstreamModel string, relatedModels ...string) bool {
	if reqBody == nil {
		return false
	}
	modified := normalizeAgnesAIResponsesDeveloperRoles(reqBody["input"])
	if agnesAIResponsesModelRejectsReasoning(upstreamModel, relatedModels...) {
		if _, ok := reqBody["reasoning"]; ok {
			delete(reqBody, "reasoning")
			modified = true
		}
		if _, ok := reqBody["reasoning_effort"]; ok {
			delete(reqBody, "reasoning_effort")
			modified = true
		}
		if stripAgnesAIReasoningInclude(reqBody) {
			modified = true
		}
	}
	return modified
}

func (s *OpenAIGatewayService) shouldBridgeAgnesAIResponsesImageRequest(account *Account, model string) bool {
	return accountUsesAgnesAIResponsesCompat(account) && agnesAIResponsesModelIsImage(model)
}

func (s *OpenAIGatewayService) shouldBridgeAgnesAIResponsesVideoRequest(account *Account, model string) bool {
	return accountUsesAgnesAIResponsesCompat(account) && agnesAIResponsesModelIsVideo(model)
}

func (s *OpenAIGatewayService) forwardAgnesAIResponsesImageViaImages(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	requestModel string,
	upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	imagesBody, err := buildAgnesAIImagesRequestFromResponses(body, requestModel, upstreamModel)
	if err != nil {
		setOpsUpstreamError(c, http.StatusBadRequest, err.Error(), "")
		WriteOpenAIClientError(c, http.StatusBadRequest, "invalid_request_error", err.Error(), nil)
		return nil, err
	}
	if apiKey := getAPIKeyFromContext(c); !GroupAllowsImageGeneration(apiKeyGroup(apiKey)) {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalFeatureGate)
		setOpsUpstreamError(c, http.StatusForbidden, ImageGenerationPermissionMessage(), "")
		WriteOpenAIClientError(c, http.StatusForbidden, "permission_error", ImageGenerationPermissionMessage(), nil)
		return nil, errors.New("image generation disabled for group")
	}

	imageReq := &OpenAIImagesRequest{
		Endpoint:       openAIImagesGenerationsEndpoint,
		ContentType:    "application/json",
		Model:          requestModel,
		ExplicitModel:  true,
		Prompt:         strings.TrimSpace(gjson.GetBytes(imagesBody, "prompt").String()),
		Stream:         false,
		N:              int(gjson.GetBytes(imagesBody, "n").Int()),
		Size:           strings.TrimSpace(gjson.GetBytes(imagesBody, "size").String()),
		ExplicitSize:   gjson.GetBytes(imagesBody, "size").Exists(),
		ResponseFormat: strings.ToLower(strings.TrimSpace(gjson.GetBytes(imagesBody, "response_format").String())),
		Body:           imagesBody,
	}
	if imageReq.N <= 0 {
		imageReq.N = 1
	}
	imageReq.SizeTier = normalizeOpenAIImageSizeTier(imageReq.Size)

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()

	token, _, err := s.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := s.buildOpenAIImagesRequest(upstreamCtx, c, account, imagesBody, "application/json", token, openAIImagesGenerationsEndpoint)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, string(respBody))
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             truncateString(string(respBody), 2048),
		})
		return s.handleErrorResponse(ctx, resp, c, account, imagesBody, requestModel)
	}
	defer func() { _ = resp.Body.Close() }()

	upstreamBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	responsesBody, usage, imageCount, imageOutputSizes, responseID, err := buildAgnesAIResponsesImageResponse(upstreamBody, requestModel)
	if err != nil {
		return nil, err
	}
	clientStream := agnesAIResponsesClientWantsStream(body)

	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	if clientStream {
		if err := s.writeAgnesAIResponsesCompletedStream(c, responsesBody); err != nil {
			return nil, err
		}
	} else {
		c.Data(http.StatusOK, "application/json", responsesBody)
	}

	return &OpenAIForwardResult{
		RequestID:        resp.Header.Get("x-request-id"),
		ResponseID:       responseID,
		Usage:            usage,
		Model:            requestModel,
		UpstreamModel:    upstreamModel,
		Stream:           clientStream,
		OpenAIWSMode:     false,
		Duration:         time.Since(startTime),
		ImageCount:       imageCount,
		ImageSize:        imageReq.SizeTier,
		ImageInputSize:   imageReq.Size,
		ImageOutputSizes: imageOutputSizes,
		BillingModel:     upstreamModel,
	}, nil
}

func (s *OpenAIGatewayService) forwardAgnesAIResponsesVideoViaVideos(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	requestModel string,
	upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	videosBody, err := buildAgnesAIVideosRequestFromResponses(body, requestModel, upstreamModel)
	if err != nil {
		setOpsUpstreamError(c, http.StatusBadRequest, err.Error(), "")
		WriteOpenAIClientError(c, http.StatusBadRequest, "invalid_request_error", err.Error(), nil)
		return nil, err
	}
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()

	token, _, err := s.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := s.buildAgnesAIVideosCreateRequest(upstreamCtx, c, account, videosBody, token)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, string(respBody))
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             truncateString(string(respBody), 2048),
		})
		return s.handleErrorResponse(ctx, resp, c, account, videosBody, requestModel)
	}
	defer func() { _ = resp.Body.Close() }()

	upstreamBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	responsesBody, usage, responseID, err := buildAgnesAIResponsesVideoResponse(upstreamBody, requestModel)
	if err != nil {
		return nil, err
	}
	responsesBody = s.enrichAgnesAIResponsesVideoBody(ctx, account, upstreamBody, responsesBody, requestModel, upstreamModel, token)
	clientStream := agnesAIResponsesClientWantsStream(body)

	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	if clientStream {
		if err := s.writeAgnesAIResponsesCompletedStream(c, responsesBody); err != nil {
			return nil, err
		}
	} else {
		c.Data(http.StatusOK, "application/json", responsesBody)
	}

	return &OpenAIForwardResult{
		RequestID:     resp.Header.Get("x-request-id"),
		ResponseID:    responseID,
		Usage:         usage,
		Model:         requestModel,
		UpstreamModel: upstreamModel,
		Stream:        clientStream,
		OpenAIWSMode:  false,
		Duration:      time.Since(startTime),
		BillingModel:  upstreamModel,
	}, nil
}

func agnesAIResponsesClientWantsStream(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	return gjson.GetBytes(body, "stream").Bool()
}

func (s *OpenAIGatewayService) writeAgnesAIResponsesCompletedStream(c *gin.Context, responseBody []byte) error {
	if c == nil || c.Writer == nil {
		return fmt.Errorf("missing response writer")
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported by response writer")
	}
	payload := []byte(`{"type":"response.completed"}`)
	payload, _ = sjson.SetRawBytes(payload, "response", responseBody)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	if err := s.writeOpenAIImagesStreamEvent(c, flusher, "response.completed", payload); err != nil {
		return err
	}
	if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func buildAgnesAIImagesRequestFromResponses(body []byte, requestModel string, upstreamModel string) ([]byte, error) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("invalid Responses request body")
	}
	prompt := collectAgnesAIResponsesImagePrompt(gjson.GetBytes(body, "input"))
	if prompt == "" {
		prompt = strings.TrimSpace(gjson.GetBytes(body, "prompt").String())
	}
	if prompt == "" {
		return nil, fmt.Errorf("Agnes image Responses compatibility requires text input")
	}

	model := strings.TrimSpace(requestModel)
	if model == "" {
		model = strings.TrimSpace(upstreamModel)
	}
	out := []byte(`{"model":"","prompt":""}`)
	out, _ = sjson.SetBytes(out, "model", model)
	out, _ = sjson.SetBytes(out, "prompt", prompt)
	if n := gjson.GetBytes(body, "n"); n.Exists() && n.Type == gjson.Number && n.Int() > 0 {
		out, _ = sjson.SetBytes(out, "n", n.Int())
	}
	for _, field := range []string{"size", "quality", "style"} {
		if value := strings.TrimSpace(gjson.GetBytes(body, field).String()); value != "" {
			out, _ = sjson.SetBytes(out, field, value)
		}
	}
	return out, nil
}

func buildAgnesAIVideosRequestFromResponses(body []byte, requestModel string, upstreamModel string) ([]byte, error) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("invalid Responses request body")
	}
	prompt := collectAgnesAIResponsesImagePrompt(gjson.GetBytes(body, "input"))
	if prompt == "" {
		prompt = strings.TrimSpace(gjson.GetBytes(body, "prompt").String())
	}
	if prompt == "" {
		return nil, fmt.Errorf("Agnes video Responses compatibility requires text input")
	}

	model := strings.TrimSpace(requestModel)
	if model == "" {
		model = strings.TrimSpace(upstreamModel)
	}
	out := []byte(`{"model":"","prompt":""}`)
	out, _ = sjson.SetBytes(out, "model", model)
	out, _ = sjson.SetBytes(out, "prompt", prompt)
	for _, field := range []string{"height", "width", "num_frames", "frame_rate", "image", "extra_body"} {
		value := gjson.GetBytes(body, field)
		if value.Exists() {
			out, _ = sjson.SetRawBytes(out, field, []byte(value.Raw))
		}
	}
	return out, nil
}

func collectAgnesAIResponsesImagePrompt(input gjson.Result) string {
	if !input.Exists() {
		return ""
	}
	if input.Type == gjson.String {
		return strings.TrimSpace(input.String())
	}
	userParts := make([]string, 0, 4)
	fallbackParts := make([]string, 0, 4)
	var collect func(gjson.Result, bool)
	collect = func(value gjson.Result, userScope bool) {
		if !value.Exists() {
			return
		}
		switch {
		case value.IsArray():
			value.ForEach(func(_, item gjson.Result) bool {
				collect(item, userScope)
				return true
			})
		case value.IsObject():
			if role := strings.ToLower(strings.TrimSpace(value.Get("role").String())); role != "" {
				if role == "developer" || role == "system" || role == "assistant" {
					return
				}
				userScope = role == "user"
			}
			itemType := strings.TrimSpace(value.Get("type").String())
			if itemType == "input_text" || itemType == "text" {
				if text := strings.TrimSpace(value.Get("text").String()); text != "" {
					if userScope {
						userParts = append(userParts, text)
					} else {
						fallbackParts = append(fallbackParts, text)
					}
				}
				return
			}
			if content := value.Get("content"); content.Exists() {
				collect(content, userScope)
			}
		}
	}
	collect(input, false)
	if len(userParts) > 0 {
		return strings.TrimSpace(strings.Join(userParts, "\n"))
	}
	return strings.TrimSpace(strings.Join(fallbackParts, "\n"))
}

func buildAgnesAIResponsesImageResponse(upstreamBody []byte, model string) ([]byte, OpenAIUsage, int, []string, string, error) {
	if len(upstreamBody) == 0 || !gjson.ValidBytes(upstreamBody) {
		return nil, OpenAIUsage{}, 0, nil, "", fmt.Errorf("Agnes image upstream returned invalid json")
	}
	root := gjson.ParseBytes(upstreamBody)
	data := root.Get("data")
	if !data.IsArray() {
		return nil, OpenAIUsage{}, 0, nil, "", fmt.Errorf("Agnes image upstream response missing data array")
	}
	responseID := strings.TrimSpace(root.Get("id").String())
	if responseID == "" {
		responseID = "resp_agnes_image_" + hashOpenAIImageOutputResult(string(upstreamBody))[:24]
	}
	createdAt := root.Get("created").Int()
	if createdAt <= 0 {
		createdAt = time.Now().Unix()
	}
	response := []byte(`{"id":"","object":"response","created_at":0,"status":"completed","model":"","output":[]}`)
	response, _ = sjson.SetBytes(response, "id", responseID)
	response, _ = sjson.SetBytes(response, "created_at", createdAt)
	response, _ = sjson.SetBytes(response, "model", strings.TrimSpace(model))

	outputIndex := 0
	sizes := make([]string, 0, len(data.Array()))
	data.ForEach(func(_, item gjson.Result) bool {
		b64 := strings.TrimSpace(item.Get("b64_json").String())
		imageURL := strings.TrimSpace(item.Get("url").String())
		if b64 == "" && imageURL == "" {
			return true
		}
		output := map[string]any{
			"id":   fmt.Sprintf("ig_agnes_%d", outputIndex),
			"type": "image_generation_call",
		}
		if b64 != "" {
			output["result"] = b64
			output["output_format"] = "png"
		} else {
			output["result"] = imageURL
		}
		if revised := strings.TrimSpace(item.Get("revised_prompt").String()); revised != "" {
			output["revised_prompt"] = revised
		}
		if size := strings.TrimSpace(item.Get("size").String()); size != "" {
			output["size"] = size
			sizes = append(sizes, size)
		}
		raw, err := json.Marshal(output)
		if err == nil {
			response, _ = sjson.SetRawBytes(response, "output.-1", raw)
			outputIndex++
		}
		return true
	})
	if outputIndex == 0 {
		return nil, OpenAIUsage{}, 0, nil, "", fmt.Errorf("Agnes image upstream returned no image output")
	}
	if usageRaw := root.Get("usage"); usageRaw.Exists() && usageRaw.IsObject() {
		response, _ = sjson.SetRawBytes(response, "usage", []byte(usageRaw.Raw))
	}
	usage, _ := extractOpenAIUsageFromJSONBytes(response)
	return response, usage, outputIndex, sizes, responseID, nil
}

func buildAgnesAIResponsesVideoResponse(upstreamBody []byte, model string) ([]byte, OpenAIUsage, string, error) {
	if len(upstreamBody) == 0 || !gjson.ValidBytes(upstreamBody) {
		return nil, OpenAIUsage{}, "", fmt.Errorf("Agnes video upstream returned invalid json")
	}
	root := gjson.ParseBytes(upstreamBody)
	responseID := "resp_agnes_video_" + hashOpenAIImageOutputResult(string(upstreamBody))[:24]
	createdAt := root.Get("created_at").Int()
	if createdAt <= 0 {
		createdAt = root.Get("created").Int()
	}
	if createdAt <= 0 {
		createdAt = time.Now().Unix()
	}
	status := strings.TrimSpace(root.Get("status").String())
	if status == "" {
		status = "queued"
	}
	videoID := strings.TrimSpace(root.Get("video_id").String())
	taskID := strings.TrimSpace(root.Get("task_id").String())
	if taskID == "" {
		taskID = strings.TrimSpace(root.Get("id").String())
	}
	output := map[string]any{
		"id":     "vg_agnes_0",
		"type":   "video_generation_call",
		"status": status,
	}
	if taskID != "" {
		output["task_id"] = taskID
	}
	if videoID != "" {
		output["video_id"] = videoID
	}
	if urlValue := strings.TrimSpace(root.Get("remixed_from_video_id").String()); urlValue != "" {
		output["result"] = urlValue
	}
	if progress := root.Get("progress"); progress.Exists() && progress.Type == gjson.Number {
		output["progress"] = progress.Value()
	}
	if seconds := strings.TrimSpace(root.Get("seconds").String()); seconds != "" {
		output["seconds"] = seconds
	}
	if size := strings.TrimSpace(root.Get("size").String()); size != "" {
		output["size"] = size
	}
	outputRaw, err := json.Marshal(output)
	if err != nil {
		return nil, OpenAIUsage{}, "", err
	}
	response := []byte(`{"id":"","object":"response","created_at":0,"status":"completed","model":"","output":[]}`)
	response, _ = sjson.SetBytes(response, "id", responseID)
	response, _ = sjson.SetBytes(response, "created_at", createdAt)
	response, _ = sjson.SetBytes(response, "model", strings.TrimSpace(model))
	response, _ = sjson.SetRawBytes(response, "output.-1", outputRaw)
	if usageRaw := root.Get("usage"); usageRaw.Exists() && usageRaw.IsObject() {
		response, _ = sjson.SetRawBytes(response, "usage", []byte(usageRaw.Raw))
	}
	usage, _ := extractOpenAIUsageFromJSONBytes(response)
	return response, usage, responseID, nil
}

func normalizeAgnesAIResponsesDeveloperRoles(input any) bool {
	switch value := input.(type) {
	case []any:
		modified := false
		for _, item := range value {
			if message, ok := item.(map[string]any); ok && normalizeAgnesAIResponsesMessageRole(message) {
				modified = true
			}
		}
		return modified
	case map[string]any:
		return normalizeAgnesAIResponsesMessageRole(value)
	default:
		return false
	}
}

func normalizeAgnesAIResponsesMessageRole(message map[string]any) bool {
	role, ok := message["role"].(string)
	if !ok || !strings.EqualFold(strings.TrimSpace(role), "developer") {
		return false
	}
	message["role"] = "system"
	return true
}

func agnesAIResponsesModelRejectsReasoning(upstreamModel string, relatedModels ...string) bool {
	models := append([]string{upstreamModel}, relatedModels...)
	for _, model := range models {
		normalized := strings.ToLower(strings.TrimSpace(model))
		if normalized == "" {
			continue
		}
		if agnesAIResponsesModelIsImage(normalized) ||
			strings.Contains(normalized, "agnes-video") ||
			strings.Contains(normalized, "video") {
			return true
		}
	}
	return false
}

func agnesAIResponsesModelIsVideo(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(normalized, "agnes-video") ||
		strings.Contains(normalized, "video")
}

func agnesAIResponsesModelIsImage(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(normalized, "agnes-image") ||
		strings.Contains(normalized, "agnes-t2i") ||
		strings.Contains(normalized, "t2i") ||
		strings.Contains(normalized, "image")
}

func stripAgnesAIReasoningInclude(reqBody map[string]any) bool {
	raw, ok := reqBody["include"]
	if !ok {
		return false
	}
	switch includes := raw.(type) {
	case []any:
		next := make([]any, 0, len(includes))
		removed := false
		for _, include := range includes {
			if includeString, ok := include.(string); ok && isReasoningEncryptedContentInclude(includeString) {
				removed = true
				continue
			}
			next = append(next, include)
		}
		if removed {
			if len(next) == 0 {
				delete(reqBody, "include")
			} else {
				reqBody["include"] = next
			}
		}
		return removed
	case []string:
		next := make([]string, 0, len(includes))
		removed := false
		for _, include := range includes {
			if isReasoningEncryptedContentInclude(include) {
				removed = true
				continue
			}
			next = append(next, include)
		}
		if removed {
			if len(next) == 0 {
				delete(reqBody, "include")
			} else {
				reqBody["include"] = next
			}
		}
		return removed
	case string:
		if isReasoningEncryptedContentInclude(includes) {
			delete(reqBody, "include")
			return true
		}
		return false
	default:
		return false
	}
}

func isReasoningEncryptedContentInclude(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "reasoning.encrypted_content")
}
