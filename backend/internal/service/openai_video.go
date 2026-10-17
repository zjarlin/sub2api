package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// OpenAI-compatible video endpoints. wuyeai.cn (and other New API-compatible
// relays) expose generation through POST /v1/videos, status through
// GET /v1/videos/{task_id}, and the rendered file through
// GET /v1/videos/{task_id}/content.
const (
	OpenAIVideoEndpointCreate  GrokMediaEndpoint = "openai_video_create"
	OpenAIVideoEndpointStatus  GrokMediaEndpoint = "openai_video_status"
	OpenAIVideoEndpointContent GrokMediaEndpoint = "openai_video_content"

	// OpenAIEndpointCapabilityVideoGeneration marks an OpenAI API-key account
	// that may receive OpenAI-compatible video requests. Accounts must opt in
	// explicitly so chat-only relays are never selected for /v1/videos.
	OpenAIEndpointCapabilityVideoGeneration OpenAIEndpointCapability = "video_generation"
)

const openAIVideoTaskPrefix = "openai-video:"

func (e GrokMediaEndpoint) IsOpenAIVideo() bool {
	return e == OpenAIVideoEndpointCreate || e == OpenAIVideoEndpointStatus || e == OpenAIVideoEndpointContent
}

func (e GrokMediaEndpoint) IsOpenAIVideoLookup() bool {
	return e == OpenAIVideoEndpointStatus || e == OpenAIVideoEndpointContent
}

// OpenAIVideoTaskKey isolates OpenAI video ownership/billing keys from Grok and
// Seedance task IDs in the shared sticky-session cache.
func OpenAIVideoTaskKey(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if strings.HasPrefix(id, openAIVideoTaskPrefix) {
		return id
	}
	return openAIVideoTaskPrefix + id
}

// OpenAIVideoBillingRequestID is the durable usage_logs / dedup id for one
// OpenAI-compatible video task. A create is charged once per submission, so the
// task id must be the billing key even when the client retries the poll.
func OpenAIVideoBillingRequestID(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ""
	}
	if strings.HasPrefix(taskID, openAIVideoTaskPrefix) {
		return taskID
	}
	return openAIVideoTaskPrefix + taskID
}

// ParseOpenAIVideoRequest extracts the fields used for routing, moderation and
// billing from an OpenAI-compatible video request. Unknown fields are preserved
// in the forwarded body.
func ParseOpenAIVideoRequest(body []byte) (GrokMediaRequestInfo, error) {
	info := GrokMediaRequestInfo{N: 1}
	if len(body) == 0 || !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return info, fmt.Errorf("request body must be a JSON object")
	}
	info.Model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if info.Model == "" {
		return info, fmt.Errorf("model is required")
	}
	info.Prompt = strings.TrimSpace(gjson.GetBytes(body, "prompt").String())
	info.Resolution = strings.TrimSpace(gjson.GetBytes(body, "resolution").String())
	if info.Resolution == "" {
		info.Resolution = openAIVideoResolutionFromSize(gjson.GetBytes(body, "size").String())
	}
	info.AspectRatio = strings.TrimSpace(gjson.GetBytes(body, "ratio").String())
	for _, path := range []string{"duration", "seconds"} {
		if value := gjson.GetBytes(body, path); value.Exists() && value.Type == gjson.Number {
			info.DurationSeconds = int(value.Int())
			break
		}
	}
	info.Resolution = normalizeOpenAIVideoBillingResolution(info.Resolution, info.Model)
	info.InputImageURLs = openAIVideoImageURLs(body)
	return info, nil
}

// normalizeOpenAIVideoBillingResolution maps provider-specific tiers onto the
// three prices the billing engine understands. 768p is a 720p-class tier;
// 2K is treated as the 1080p tier when no better match exists.
func normalizeOpenAIVideoBillingResolution(raw, model string) string {
	if normalized, ok := LookupVideoBillingResolution(raw); ok {
		return normalized
	}
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(lower, "768"), strings.Contains(lower, "720"):
		return VideoBillingResolution720P
	case strings.Contains(lower, "1080"), strings.Contains(lower, "2k"):
		return VideoBillingResolution1080P
	}
	lowerModel := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(lowerModel, "768p"), strings.Contains(lowerModel, "720p"):
		return VideoBillingResolution720P
	case strings.Contains(lowerModel, "2k"), strings.Contains(lowerModel, "1080p"):
		return VideoBillingResolution1080P
	}
	return ""
}

// openAIVideoResolutionFromSize converts an OpenAI-style "1280x720" size into
// a billing tier. It is only used for billing; the original body is forwarded.
func openAIVideoResolutionFromSize(size string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return ""
	}
	width, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	height, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return ""
	}
	shortSide := width
	if height < shortSide {
		shortSide = height
	}
	switch {
	case shortSide >= 1000:
		return VideoBillingResolution1080P
	case shortSide >= 700:
		return VideoBillingResolution720P
	default:
		return VideoBillingResolution480P
	}
}

func openAIVideoImageURLs(body []byte) []string {
	var urls []string
	appendURL := func(value string) {
		if value = strings.TrimSpace(value); value != "" {
			urls = append(urls, value)
		}
	}
	if refs := gjson.GetBytes(body, "referenceImages"); refs.IsArray() {
		for _, item := range refs.Array() {
			if item.Type == gjson.String {
				appendURL(item.String())
			}
		}
	}
	appendURL(gjson.GetBytes(body, "first_frame_image").String())
	appendURL(gjson.GetBytes(body, "last_frame_image").String())
	// Metaso/Sora-style input_reference is accepted by some relays.
	appendURL(gjson.GetBytes(body, "input_reference.image_url").String())
	return urls
}

func buildOpenAIVideoURL(base string, endpoint GrokMediaEndpoint, requestID string) (string, error) {
	if !endpoint.IsOpenAIVideo() {
		return "", fmt.Errorf("unsupported openai video endpoint: %s", endpoint)
	}
	if endpoint == OpenAIVideoEndpointCreate {
		return buildOpenAIEndpointURL(base, "/v1/videos"), nil
	}
	taskID := strings.TrimPrefix(strings.TrimSpace(requestID), openAIVideoTaskPrefix)
	if taskID == "" {
		return "", fmt.Errorf("request_id is required")
	}
	if err := validateUpstreamPathSegment("OpenAI video task ID", taskID); err != nil {
		return "", err
	}
	path := "/v1/videos/" + url.PathEscape(taskID)
	if endpoint == OpenAIVideoEndpointContent {
		path += "/content"
	}
	return buildOpenAIEndpointURL(base, path), nil
}

func extractOpenAIVideoTaskID(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	for _, path := range []string{"id", "task_id", "request_id", "data.id", "data.task_id", "video.id", "video.task_id"} {
		if id := strings.TrimSpace(gjson.GetBytes(body, path).String()); id != "" {
			return id
		}
	}
	return ""
}

// ForwardOpenAIVideo proxies one OpenAI-compatible video request to the account
// base URL. The body is forwarded verbatim except for the account model mapping.
func (s *OpenAIGatewayService) ForwardOpenAIVideo(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	endpoint GrokMediaEndpoint,
	taskID string,
	body []byte,
	contentType string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	if account == nil {
		return nil, fmt.Errorf("openai video account is required")
	}
	if !endpoint.IsOpenAIVideo() {
		return nil, fmt.Errorf("unsupported openai video endpoint: %s", endpoint)
	}
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey {
		return nil, fmt.Errorf("openai video requires an OpenAI API key account")
	}
	if !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityVideoGeneration) {
		return nil, fmt.Errorf("openai video endpoint capability is not enabled")
	}
	base, err := s.validateUpstreamBaseURL(account.GetOpenAIBaseURL())
	if err != nil {
		return nil, err
	}
	targetURL, err := buildOpenAIVideoURL(base, endpoint, taskID)
	if err != nil {
		return nil, err
	}

	model, upstreamModel := "", ""
	var requestInfo GrokMediaRequestInfo
	if endpoint == OpenAIVideoEndpointCreate {
		requestInfo, err = ParseOpenAIVideoRequest(body)
		if err != nil {
			return nil, err
		}
		model = requestInfo.Model
		if model == "" {
			return nil, fmt.Errorf("model is required")
		}
		upstreamModel = strings.TrimSpace(account.GetMappedModel(model))
		if upstreamModel == "" {
			upstreamModel = model
		}
		if upstreamModel != model {
			body, err = sjson.SetBytes(body, "model", upstreamModel)
			if err != nil {
				return nil, fmt.Errorf("rewrite openai video mapped model: %w", err)
			}
		}
	}

	token := strings.TrimSpace(account.GetCredential("api_key"))
	if token == "" {
		return nil, fmt.Errorf("openai video account missing api_key")
	}

	var bodyReader io.Reader
	if endpoint.RequiresRequestBody() {
		bodyReader = bytes.NewReader(body)
	}
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	upstreamReq, err := http.NewRequestWithContext(upstreamCtx, endpoint.httpMethod(), targetURL, bodyReader)
	if err != nil {
		return nil, err
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+token)
	if endpoint == OpenAIVideoEndpointContent {
		upstreamReq.Header.Set("Accept", "*/*")
		if c != nil {
			if rangeHeader := strings.TrimSpace(c.GetHeader("Range")); rangeHeader != "" {
				upstreamReq.Header.Set("Range", rangeHeader)
			}
			if etag := strings.TrimSpace(c.GetHeader("If-None-Match")); etag != "" {
				upstreamReq.Header.Set("If-None-Match", etag)
			}
		}
	} else {
		upstreamReq.Header.Set("Accept", "application/json")
	}
	if endpoint.RequiresRequestBody() {
		contentType = strings.TrimSpace(contentType)
		if contentType == "" {
			contentType = "application/json"
		}
		upstreamReq.Header.Set("Content-Type", contentType)
	}
	if userAgent := strings.TrimSpace(account.GetOpenAIUserAgent()); userAgent != "" {
		upstreamReq.Header.Set("User-Agent", userAgent)
	}
	account.ApplyHeaderOverrides(upstreamReq.Header)

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
	defer func() { _ = resp.Body.Close() }()

	requestIDHeader := firstNonEmpty(
		resp.Header.Get("x-request-id"),
		resp.Header.Get("openai-request-id"),
		resp.Header.Get("x-oneapi-request-id"),
	)
	if resp.StatusCode >= 400 && !(endpoint == OpenAIVideoEndpointContent && resp.StatusCode == http.StatusRequestedRangeNotSatisfiable) {
		return s.handleOpenAIVideoErrorResponse(ctx, resp, c, account, requestIDHeader, model)
	}

	if endpoint == OpenAIVideoEndpointContent {
		if err := writeGrokMediaContentResponse(c, resp); err != nil {
			return nil, err
		}
		return &OpenAIForwardResult{
			RequestID:       requestIDHeader,
			ResponseID:      strings.TrimPrefix(strings.TrimSpace(taskID), openAIVideoTaskPrefix),
			ResponseHeaders: resp.Header.Clone(),
			Duration:        time.Since(startTime),
		}, nil
	}

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	writeGrokMediaResponse(c, resp, respBody, s.responseHeaderFilter)

	result := &OpenAIForwardResult{
		RequestID:       requestIDHeader,
		UpstreamHeaders: resp.Header,
		ResponseHeaders: resp.Header.Clone(),
		Duration:        time.Since(startTime),
	}
	switch endpoint {
	case OpenAIVideoEndpointCreate:
		id := extractOpenAIVideoTaskID(respBody)
		if id == "" {
			MarkResponseCommitted(c)
			return nil, fmt.Errorf("openai video create response missing task id")
		}
		// Namespaced key shared by the ownership binding and the per-submission
		// billing dedup (mirrors SeedanceTaskKey). The client still receives the
		// upstream body verbatim.
		result.ResponseID = OpenAIVideoTaskKey(id)
		result.Model = model
		result.BillingModel = model
		result.UpstreamModel = upstreamModel
		result.VideoCount = 1
		result.VideoResolution = normalizeOpenAIVideoBillingResolution(requestInfo.Resolution, upstreamModel)
		if result.VideoResolution == "" {
			result.VideoResolution = VideoBillingResolution480P
		}
		result.VideoDurationSeconds = requestInfo.DurationSeconds
	case OpenAIVideoEndpointStatus:
		result.ResponseID = strings.TrimPrefix(strings.TrimSpace(taskID), openAIVideoTaskPrefix)
		statusModel := strings.TrimSpace(gjson.GetBytes(respBody, "model").String())
		if statusModel != "" {
			result.Model = statusModel
			result.BillingModel = statusModel
			result.UpstreamModel = statusModel
		}
	}
	return result, nil
}

func (s *OpenAIGatewayService) handleOpenAIVideoErrorResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	requestIDHeader string,
	requestedModel string,
) (*OpenAIForwardResult, error) {
	body := s.readUpstreamErrorBody(resp)
	upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if upstreamMsg == "" {
		upstreamMsg = fmt.Sprintf("OpenAI video upstream returned status %d", resp.StatusCode)
	}
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(body), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)

	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c,
		account.Platform,
		resp.StatusCode,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		MarkResponseCommitted(c)
		writeGrokMediaErrorResponse(c, status, errType, errMsg)
		return nil, fmt.Errorf("openai video upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
	}

	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  requestIDHeader,
		Kind:               "http_error",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})

	// Do not turn ambiguous asynchronous create failures into failover retries:
	// the upstream may already have accepted and billed the job.
	if s.shouldFailoverUpstreamError(resp.StatusCode) && account.ShouldHandleErrorCode(resp.StatusCode) {
		s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, requestedModel)
	}

	MarkResponseCommitted(c)
	writeGrokMediaErrorResponse(c, resp.StatusCode, "upstream_error", upstreamMsg)
	return nil, fmt.Errorf("openai video upstream status %d: %s", resp.StatusCode, upstreamMsg)
}
