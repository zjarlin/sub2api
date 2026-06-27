package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	openAIVideosGenerationsEndpoint = "/v1/videos/generations"
	openAIVideosTasksEndpoint       = "/v1/videos/generations/tasks"

	seedanceDefaultBaseURL       = "https://ark.ap-southeast.bytepluses.com/api/v3"
	seedanceGenerationsEndpoint  = "/contents/generations/tasks"
	seedanceVolcengineModelPrefx = "doubao-seedance-"

	agnesAIVideosEndpoint = "/v1/videos"
	agnesAIVideoQueryPath = "/agnesapi"
)

type OpenAIVideosRequest struct {
	Endpoint      string
	Model         string
	Prompt        string
	Stream        bool
	Duration      int
	Resolution    string
	Ratio         string
	Content       []json.RawMessage
	Body          []byte
	StickySeed    string
	ModerationRaw []byte
}

func (s *OpenAIGatewayService) ParseOpenAIVideosRequest(c *gin.Context, body []byte) (*OpenAIVideosRequest, error) {
	if c == nil || c.Request == nil {
		return nil, fmt.Errorf("missing request context")
	}
	endpoint := normalizeOpenAIVideosEndpointPath(c.Request.URL.Path)
	if endpoint == "" {
		return nil, fmt.Errorf("unsupported videos endpoint")
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("request body is empty")
	}
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("failed to parse request body")
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}
	if streamResult := gjson.GetBytes(body, "stream"); streamResult.Exists() && streamResult.Type != gjson.True && streamResult.Type != gjson.False {
		return nil, fmt.Errorf("invalid stream field type")
	}

	req := &OpenAIVideosRequest{
		Endpoint:   endpoint,
		Model:      model,
		Stream:     gjson.GetBytes(body, "stream").Bool(),
		Duration:   int(gjson.GetBytes(body, "duration").Int()),
		Resolution: strings.TrimSpace(gjson.GetBytes(body, "resolution").String()),
		Ratio:      strings.TrimSpace(gjson.GetBytes(body, "ratio").String()),
		Body:       body,
	}
	if raw := gjson.GetBytes(body, "content").Raw; strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &req.Content); err != nil {
			return nil, fmt.Errorf("invalid content field")
		}
		req.Prompt = extractSeedancePromptFromContent(req.Content)
	} else {
		req.Prompt = strings.TrimSpace(gjson.GetBytes(body, "prompt").String())
		if req.Prompt == "" {
			req.Prompt = strings.TrimSpace(gjson.GetBytes(body, "input").String())
		}
		if req.Prompt == "" {
			return nil, fmt.Errorf("prompt or content is required")
		}
		req.Content = []json.RawMessage{json.RawMessage(fmt.Sprintf(`{"type":"text","text":%q}`, req.Prompt))}
	}
	req.ModerationRaw = buildOpenAIVideosModerationBody(req.Prompt, req.Content)
	req.StickySeed = strings.Join([]string{"openai-videos", req.Model, req.Prompt, req.Resolution, req.Ratio}, "|")
	return req, nil
}

func (r *OpenAIVideosRequest) ModerationBody() []byte {
	if r == nil {
		return nil
	}
	return r.ModerationRaw
}

func normalizeOpenAIVideosEndpointPath(path string) string {
	path = strings.TrimSpace(path)
	switch path {
	case "/v1/videos/generations", "/videos/generations":
		return openAIVideosGenerationsEndpoint
	default:
		return ""
	}
}

func extractSeedancePromptFromContent(content []json.RawMessage) string {
	for _, item := range content {
		if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(item, "type").String()), "text") {
			if text := strings.TrimSpace(gjson.GetBytes(item, "text").String()); text != "" {
				return text
			}
		}
	}
	return ""
}

func buildOpenAIVideosModerationBody(prompt string, content []json.RawMessage) []byte {
	payload := map[string]any{}
	if strings.TrimSpace(prompt) != "" {
		payload["prompt"] = strings.TrimSpace(prompt)
	}
	images := make([]map[string]string, 0)
	for _, item := range content {
		itemType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(item, "type").String()))
		if itemType != "image_url" {
			continue
		}
		imageURL := strings.TrimSpace(gjson.GetBytes(item, "image_url.url").String())
		if imageURL == "" {
			imageURL = strings.TrimSpace(gjson.GetBytes(item, "image_url").String())
		}
		if imageURL != "" {
			images = append(images, map[string]string{"image_url": imageURL})
		}
	}
	if len(images) > 0 {
		payload["images"] = images
	}
	if len(payload) == 0 {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return body
}

func (s *OpenAIGatewayService) ForwardVideos(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *OpenAIVideosRequest,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed videos request is required")
	}
	if account == nil || account.Type != AccountTypeAPIKey {
		return nil, fmt.Errorf("videos API requires an OpenAI API key account")
	}

	startTime := time.Now()
	requestModel := strings.TrimSpace(parsed.Model)
	if mapped := strings.TrimSpace(channelMappedModel); mapped != "" {
		requestModel = mapped
	}
	upstreamModel := account.GetMappedModel(requestModel)
	if accountUsesAgnesAIResponsesCompat(account) || isAgnesVideoModel(upstreamModel) {
		return s.forwardAgnesAIVideos(ctx, c, account, body, parsed, requestModel, upstreamModel, startTime)
	}

	forwardBody, err := rewriteOpenAIVideosBody(body, parsed, upstreamModel)
	if err != nil {
		return nil, err
	}

	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, false)
	defer releaseUpstreamCtx()

	token, _, err := s.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := s.buildOpenAIVideosTaskRequest(upstreamCtx, c, account, forwardBody, token, "", "")
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
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:    account.Platform,
			AccountID:   account.ID,
			AccountName: account.Name,
			UpstreamURL: safeUpstreamURL(upstreamReq.URL.String()),
			Kind:        "request_error",
			Message:     safeErr,
		})
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
			s.handleFailoverSideEffects(upstreamCtx, resp, account, upstreamModel)
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: isOpenAITransientProcessingError(resp.StatusCode, upstreamMsg, respBody) || account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
			}
		}
		writeOpenAIVideosUpstreamResponse(c, resp, respBody, s.responseHeaderFilter, s.cfg)
		return nil, &OpenAIImagesUpstreamError{
			StatusCode: resp.StatusCode,
			Message:    upstreamMsg,
			ErrorType:  "upstream_error",
		}
	}

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	if accountUsesAgnesAIResponsesCompat(account) || isAgnesVideoModel(upstreamModel) {
		respBody = s.enrichAgnesAIVideoResponseBody(ctx, account, respBody, requestModel, upstreamModel, token)
	}
	writeOpenAIVideosUpstreamResponse(c, resp, respBody, s.responseHeaderFilter, s.cfg)

	return &OpenAIForwardResult{
		RequestID:       resp.Header.Get("x-request-id"),
		ResponseID:      extractSeedanceTaskID(respBody),
		Model:           requestModel,
		UpstreamModel:   upstreamModel,
		Stream:          false,
		ResponseHeaders: resp.Header.Clone(),
		Duration:        time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) ForwardVideoTask(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	taskID string,
	requestModel string,
) (*OpenAIForwardResult, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task id is required")
	}
	if account == nil || account.Type != AccountTypeAPIKey {
		return nil, fmt.Errorf("videos API requires an OpenAI API key account")
	}
	startTime := time.Now()
	requestModel = strings.TrimSpace(requestModel)
	upstreamModel := ""
	if requestModel != "" {
		upstreamModel = account.GetMappedModel(requestModel)
	}

	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, false)
	defer releaseUpstreamCtx()
	token, _, err := s.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := s.buildOpenAIVideosTaskRequest(upstreamCtx, c, account, nil, token, taskID, upstreamModel)
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
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	if accountUsesAgnesAIResponsesCompat(account) || isAgnesVideoModel(upstreamModel) {
		respBody = s.enrichAgnesAIVideoResponseBody(ctx, account, respBody, requestModel, upstreamModel, token)
	}
	writeOpenAIVideosUpstreamResponse(c, resp, respBody, s.responseHeaderFilter, s.cfg)
	if resp.StatusCode >= 400 {
		return nil, &OpenAIImagesUpstreamError{
			StatusCode: resp.StatusCode,
			Message:    sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody))),
			ErrorType:  "upstream_error",
		}
	}
	return &OpenAIForwardResult{
		RequestID:       resp.Header.Get("x-request-id"),
		ResponseID:      extractSeedanceTaskID(respBody),
		Model:           requestModel,
		UpstreamModel:   upstreamModel,
		ResponseHeaders: resp.Header.Clone(),
		Duration:        time.Since(startTime),
	}, nil
}

func rewriteOpenAIVideosBody(body []byte, parsed *OpenAIVideosRequest, upstreamModel string) ([]byte, error) {
	upstreamModel = strings.TrimSpace(upstreamModel)
	if upstreamModel == "" {
		upstreamModel = strings.TrimSpace(parsed.Model)
	}
	rewritten, err := sjson.SetBytes(body, "model", upstreamModel)
	if err != nil {
		return nil, fmt.Errorf("rewrite video request model: %w", err)
	}
	if !gjson.GetBytes(rewritten, "content").Exists() {
		contentJSON, marshalErr := json.Marshal(parsed.Content)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal video request content: %w", marshalErr)
		}
		rewritten, err = sjson.SetRawBytes(rewritten, "content", contentJSON)
		if err != nil {
			return nil, fmt.Errorf("rewrite video request content: %w", err)
		}
	}
	if gjson.GetBytes(rewritten, "prompt").Exists() {
		rewritten, _ = sjson.DeleteBytes(rewritten, "prompt")
	}
	if gjson.GetBytes(rewritten, "input").Exists() {
		rewritten, _ = sjson.DeleteBytes(rewritten, "input")
	}
	if gjson.GetBytes(rewritten, "stream").Exists() {
		rewritten, _ = sjson.DeleteBytes(rewritten, "stream")
	}
	return rewritten, nil
}

func (s *OpenAIGatewayService) buildOpenAIVideosTaskRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
	taskID string,
	modelName string,
) (*http.Request, error) {
	method := http.MethodPost
	if strings.TrimSpace(taskID) != "" {
		method = http.MethodGet
	}
	targetURL := buildOpenAIVideosTaskURL(account.GetOpenAIBaseURL(), strings.TrimSpace(taskID), strings.TrimSpace(modelName))
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, reader)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	applyOpenAIUpstreamAuthHeaders(req.Header, account, token)
	for key, values := range c.Request.Header {
		if !openaiPassthroughAllowedHeaders[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	customUA := account.GetOpenAIUserAgent()
	if customUA != "" {
		req.Header.Set("User-Agent", customUA)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func buildOpenAIVideosTaskURL(baseURL string, taskID string, modelName ...string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || baseURL == "https://api.openai.com" {
		baseURL = seedanceDefaultBaseURL
	}
	queryModelName := ""
	if len(modelName) > 0 {
		queryModelName = strings.TrimSpace(modelName[0])
	}
	if normalizedOpenAIBaseURLHostname(baseURL) == agnesAIResponsesCompatHost {
		if taskID != "" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(taskID)), "video_") {
			return buildAgnesAIVideoQueryURL(baseURL, taskID, queryModelName)
		}
		return buildOpenAIEndpointURL(baseURL, agnesAIVideosEndpoint) + optionalSlashSuffix(taskID)
	}
	if strings.HasSuffix(strings.ToLower(baseURL), seedanceGenerationsEndpoint) {
		if taskID != "" {
			return strings.TrimRight(baseURL, "/") + "/" + taskID
		}
		return baseURL
	}
	target := buildOpenAIEndpointURL(baseURL, openAIVideosTasksEndpoint)
	if !strings.Contains(strings.ToLower(target), "/contents/generations/tasks") {
		target = strings.TrimRight(baseURL, "/") + seedanceGenerationsEndpoint
	}
	if taskID != "" {
		target = strings.TrimRight(target, "/") + "/" + taskID
	}
	return target
}

func extractSeedanceTaskID(body []byte) string {
	for _, path := range []string{"id", "task_id", "data.id", "data.task_id"} {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); value != "" {
			return value
		}
	}
	return ""
}

func writeOpenAIVideosUpstreamResponse(c *gin.Context, resp *http.Response, body []byte, filter *responseheaders.CompiledHeaderFilter, cfg *config.Config) {
	if c == nil || resp == nil || c.Writer.Written() {
		return
	}
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, filter)
	contentType := "application/json"
	if cfg != nil && !cfg.Security.ResponseHeaders.Enabled {
		if upstreamType := strings.TrimSpace(resp.Header.Get("Content-Type")); upstreamType != "" {
			contentType = upstreamType
		}
	}
	c.Data(resp.StatusCode, contentType, body)
}

func IsSeedanceModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "seedance") || strings.HasPrefix(model, seedanceVolcengineModelPrefx)
}

func (s *OpenAIGatewayService) forwardAgnesAIVideos(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *OpenAIVideosRequest,
	requestModel string,
	upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	forwardBody, err := rewriteAgnesAIVideosBody(body, parsed, upstreamModel)
	if err != nil {
		return nil, err
	}

	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, false)
	defer releaseUpstreamCtx()

	token, _, err := s.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := s.buildAgnesAIVideosCreateRequest(upstreamCtx, c, account, forwardBody, token)
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
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:    account.Platform,
			AccountID:   account.ID,
			AccountName: account.Name,
			UpstreamURL: safeUpstreamURL(upstreamReq.URL.String()),
			Kind:        "request_error",
			Message:     safeErr,
		})
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
			s.handleFailoverSideEffects(upstreamCtx, resp, account, upstreamModel)
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: isOpenAITransientProcessingError(resp.StatusCode, upstreamMsg, respBody) || account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
			}
		}
		writeOpenAIVideosUpstreamResponse(c, resp, respBody, s.responseHeaderFilter, s.cfg)
		return nil, &OpenAIImagesUpstreamError{
			StatusCode: resp.StatusCode,
			Message:    upstreamMsg,
			ErrorType:  "upstream_error",
		}
	}

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	respBody = s.enrichAgnesAIVideoResponseBody(ctx, account, respBody, requestModel, upstreamModel, token)
	writeOpenAIVideosUpstreamResponse(c, resp, respBody, s.responseHeaderFilter, s.cfg)

	return &OpenAIForwardResult{
		RequestID:       resp.Header.Get("x-request-id"),
		ResponseID:      extractAgnesAIVideoID(respBody),
		Model:           requestModel,
		UpstreamModel:   upstreamModel,
		Stream:          false,
		ResponseHeaders: resp.Header.Clone(),
		Duration:        time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) buildAgnesAIVideosCreateRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
) (*http.Request, error) {
	baseURL := account.GetOpenAIBaseURL()
	if baseURL == "" {
		baseURL = "https://" + agnesAIResponsesCompatHost
	}
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	targetURL := buildOpenAIEndpointURL(validatedURL, agnesAIVideosEndpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	applyOpenAIUpstreamAuthHeaders(req.Header, account, token)
	for key, values := range c.Request.Header {
		if !openaiPassthroughAllowedHeaders[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	customUA := account.GetOpenAIUserAgent()
	if customUA != "" {
		req.Header.Set("User-Agent", customUA)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func rewriteAgnesAIVideosBody(body []byte, parsed *OpenAIVideosRequest, upstreamModel string) ([]byte, error) {
	upstreamModel = strings.TrimSpace(upstreamModel)
	if upstreamModel == "" {
		upstreamModel = strings.TrimSpace(parsed.Model)
	}
	rewritten, err := sjson.SetBytes(body, "model", upstreamModel)
	if err != nil {
		return nil, fmt.Errorf("rewrite Agnes video request model: %w", err)
	}
	if prompt := strings.TrimSpace(parsed.Prompt); prompt != "" {
		rewritten, _ = sjson.SetBytes(rewritten, "prompt", prompt)
	}
	if gjson.GetBytes(rewritten, "content").Exists() {
		rewritten, _ = sjson.DeleteBytes(rewritten, "content")
	}
	if gjson.GetBytes(rewritten, "input").Exists() {
		rewritten, _ = sjson.DeleteBytes(rewritten, "input")
	}
	if gjson.GetBytes(rewritten, "stream").Exists() {
		rewritten, _ = sjson.DeleteBytes(rewritten, "stream")
	}
	return rewritten, nil
}

func isAgnesVideoModel(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "agnes-video")
}

func buildAgnesAIVideoQueryURL(baseURL string, videoID string, modelName string) string {
	root := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if root == "" {
		root = "https://" + agnesAIResponsesCompatHost
	}
	root = trimOpenAIEndpointSuffix(root)
	target := root + agnesAIVideoQueryPath + "?video_id=" + url.QueryEscape(videoID)
	if strings.TrimSpace(modelName) != "" {
		target += "&model_name=" + url.QueryEscape(modelName)
	}
	return target
}

func optionalSlashSuffix(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return "/" + value
}

func extractAgnesAIVideoID(body []byte) string {
	for _, path := range []string{"video_id", "data.video_id", "id", "task_id", "data.id", "data.task_id"} {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); value != "" {
			return value
		}
	}
	return ""
}
