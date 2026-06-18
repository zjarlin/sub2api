package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var openCodeGoAnthropicModels = map[string]struct{}{
	"minimax-m3":   {},
	"minimax-m2.7": {},
	"minimax-m2.5": {},
	"qwen3.7-max":  {},
	"qwen3.7-plus": {},
	"qwen3.6-plus": {},
}

var openCodeGoChatCompletionsModels = map[string]struct{}{
	"glm-5.1":           {},
	"glm-5":             {},
	"kimi-k2.7":         {},
	"kimi-k2.7-code":    {},
	"kimi-k2.6":         {},
	"deepseek-v4-pro":   {},
	"deepseek-v4-flash": {},
	"mimo-v2.5":         {},
	"mimo-v2.5-pro":     {},
}

func accountUsesOpenCodeGoOfficialAPI(account *Account) bool {
	if account == nil || !account.IsOpenAIApiKey() {
		return false
	}
	if !strings.EqualFold(account.GetOpenAIVendor(), "opencode-go") {
		return false
	}
	return !accountUsesLocalOpenCodeServer(account)
}

func openCodeGoModelUsesAnthropicMessages(model string) bool {
	_, ok := openCodeGoAnthropicModels[strings.ToLower(strings.TrimSpace(model))]
	return ok
}

func openCodeGoModelUsesChatCompletions(model string) bool {
	_, ok := openCodeGoChatCompletionsModels[strings.ToLower(strings.TrimSpace(model))]
	return ok
}

func openCodeGoOfficialMessagesURL(baseURL string) string {
	return buildOpenAIEndpointURL(baseURL, "/v1/messages")
}

func (s *OpenAIGatewayService) forwardOpenCodeGoChatCompletionsViaMessages(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	var chatReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := strings.TrimSpace(chatReq.Model)
	if originalModel == "" {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	if !openCodeGoModelUsesAnthropicMessages(upstreamModel) {
		if !openCodeGoModelUsesChatCompletions(upstreamModel) {
			writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "OpenCode Go model is not supported by the official API endpoint mapping")
			return nil, fmt.Errorf("opencode go model %q is not supported by endpoint mapping", upstreamModel)
		}
		return s.forwardAsRawChatCompletions(ctx, c, account, body, defaultMappedModel)
	}

	responsesReq, err := apicompat.ChatCompletionsToResponses(&chatReq)
	if err != nil {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert chat completions to responses: %w", err)
	}
	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(responsesReq)
	if err != nil {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}
	anthropicReq.Model = upstreamModel
	anthropicReq.Stream = true
	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal opencode go anthropic request: %w", err)
	}

	result, err := s.forwardOpenCodeGoOfficialMessages(ctx, c, account, anthropicBody, openCodeGoForwardOptions{
		OriginalModel:    originalModel,
		BillingModel:     billingModel,
		UpstreamModel:    upstreamModel,
		ReasoningEffort:  extractOpenAIReasoningEffortFromBody(body, originalModel),
		ServiceTier:      extractOpenAIServiceTierFromBody(body),
		ClientProtocol:   openCodeGoClientProtocolChatCompletions,
		ClientStream:     chatReq.Stream,
		IncludeChatUsage: chatReq.StreamOptions != nil && chatReq.StreamOptions.IncludeUsage,
		StartTime:        time.Now(),
	})
	return result, err
}

func (s *OpenAIGatewayService) forwardOpenCodeGoResponsesViaMessages(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*OpenAIForwardResult, error) {
	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := strings.TrimSpace(responsesReq.Model)
	if originalModel == "" {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, "")
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	if !openCodeGoModelUsesAnthropicMessages(upstreamModel) {
		if !openCodeGoModelUsesChatCompletions(upstreamModel) {
			writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "OpenCode Go model is not supported by the official API endpoint mapping")
			return nil, fmt.Errorf("opencode go model %q is not supported by endpoint mapping", upstreamModel)
		}
		return s.forwardResponsesViaRawChatCompletions(ctx, c, account, body)
	}

	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(&responsesReq)
	if err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}
	anthropicReq.Model = upstreamModel
	anthropicReq.Stream = true
	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal opencode go anthropic request: %w", err)
	}

	return s.forwardOpenCodeGoOfficialMessages(ctx, c, account, anthropicBody, openCodeGoForwardOptions{
		OriginalModel:   originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: extractOpenAIReasoningEffortFromBody(body, originalModel),
		ServiceTier:     extractOpenAIServiceTierFromBody(body),
		ClientProtocol:  openCodeGoClientProtocolResponses,
		ClientStream:    responsesReq.Stream,
		StartTime:       time.Now(),
	})
}

func (s *OpenAIGatewayService) forwardOpenCodeGoAnthropicMessages(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	var anthropicReq apicompat.AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse anthropic request: %w", err)
	}
	originalModel := strings.TrimSpace(anthropicReq.Model)
	if originalModel == "" {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	if !openCodeGoModelUsesAnthropicMessages(upstreamModel) {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "OpenCode Go model requires /v1/chat/completions upstream")
		return nil, fmt.Errorf("opencode go model %q is not a messages model", upstreamModel)
	}
	anthropicReq.Model = upstreamModel
	anthropicReq.Stream = true
	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal opencode go anthropic request: %w", err)
	}

	return s.forwardOpenCodeGoOfficialMessages(ctx, c, account, anthropicBody, openCodeGoForwardOptions{
		OriginalModel:  originalModel,
		BillingModel:   billingModel,
		UpstreamModel:  upstreamModel,
		ClientProtocol: openCodeGoClientProtocolAnthropic,
		ClientStream:   anthropicReq.Stream,
		StartTime:      time.Now(),
	})
}

type openCodeGoClientProtocol string

const (
	openCodeGoClientProtocolChatCompletions openCodeGoClientProtocol = "chat_completions"
	openCodeGoClientProtocolResponses       openCodeGoClientProtocol = "responses"
	openCodeGoClientProtocolAnthropic       openCodeGoClientProtocol = "anthropic"
)

type openCodeGoForwardOptions struct {
	OriginalModel    string
	BillingModel     string
	UpstreamModel    string
	ReasoningEffort  *string
	ServiceTier      *string
	ClientProtocol   openCodeGoClientProtocol
	ClientStream     bool
	IncludeChatUsage bool
	StartTime        time.Time
}

func (s *OpenAIGatewayService) forwardOpenCodeGoOfficialMessages(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	opts openCodeGoForwardOptions,
) (*OpenAIForwardResult, error) {
	startTime := opts.StartTime
	if startTime.IsZero() {
		startTime = time.Now()
	}

	updatedBody, policyErr := s.applyOpenAIFastPolicyToBody(ctx, account, opts.UpstreamModel, body)
	if policyErr != nil {
		var blocked *OpenAIFastBlockedError
		if errors.As(policyErr, &blocked) {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			switch opts.ClientProtocol {
			case openCodeGoClientProtocolAnthropic:
				writeAnthropicError(c, http.StatusForbidden, "forbidden_error", blocked.Message)
			case openCodeGoClientProtocolResponses:
				writeResponsesError(c, http.StatusForbidden, "permission_error", blocked.Message)
			default:
				writeChatCompletionsError(c, http.StatusForbidden, "permission_error", blocked.Message)
			}
		}
		return nil, policyErr
	}
	body = updatedBody

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	baseURL := account.GetOpenAIBaseURL()
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	targetURL := openCodeGoOfficialMessagesURL(validatedURL)

	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, true)
	upstreamReq, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, targetURL, bytes.NewReader(body))
	releaseUpstreamCtx()
	if err != nil {
		return nil, err
	}
	upstreamReq = upstreamReq.WithContext(WithHTTPUpstreamProfile(upstreamReq.Context(), HTTPUpstreamProfileOpenAI))
	upstreamReq.Header.Set("content-type", "application/json")
	upstreamReq.Header.Set("accept", "text/event-stream")
	upstreamReq.Header.Set("x-api-key", strings.TrimSpace(token))
	if upstreamReq.Header.Get("anthropic-version") == "" {
		upstreamReq.Header.Set("anthropic-version", "2023-06-01")
	}
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		upstreamReq.Header.Set("user-agent", customUA)
	}

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			Kind:               "request_error",
			Message:            safeErr,
		})
		s.writeOpenCodeGoProtocolError(c, opts.ClientProtocol, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		if upstreamMsg == "" {
			upstreamMsg = strings.TrimSpace(string(respBody))
		}
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		if upstreamMsg == "" {
			upstreamMsg = http.StatusText(resp.StatusCode)
		}
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "failover",
			Message:            upstreamMsg,
		})
		s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, opts.UpstreamModel)
		return nil, &UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           respBody,
			RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
		}
	}

	return s.handleOpenCodeGoOfficialMessagesResponse(resp, c, opts, startTime)
}

func (s *OpenAIGatewayService) handleOpenCodeGoOfficialMessagesResponse(
	resp *http.Response,
	c *gin.Context,
	opts openCodeGoForwardOptions,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	if opts.ClientStream {
		switch opts.ClientProtocol {
		case openCodeGoClientProtocolAnthropic:
			return s.streamOpenCodeGoAsAnthropic(resp, c, opts, startTime)
		case openCodeGoClientProtocolResponses:
			return s.streamOpenCodeGoAsResponses(resp, c, opts, startTime)
		default:
			return s.streamOpenCodeGoAsChatCompletions(resp, c, opts, startTime)
		}
	}
	finalResp, usage, requestID, err := s.bufferOpenCodeGoAnthropicResponse(resp)
	if err != nil {
		s.writeOpenCodeGoProtocolError(c, opts.ClientProtocol, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
		return nil, err
	}
	return s.writeOpenCodeGoBufferedProtocolResponse(c, resp, finalResp, usage, requestID, opts, startTime)
}

func (s *OpenAIGatewayService) bufferOpenCodeGoAnthropicResponse(resp *http.Response) (*apicompat.AnthropicResponse, ClaudeUsage, string, error) {
	requestID := resp.Header.Get("x-request-id")
	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	var finalResp *apicompat.AnthropicResponse
	var usage ClaudeUsage
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "event: ") {
			continue
		}
		if !scanner.Scan() {
			break
		}
		dataLine := scanner.Text()
		if !strings.HasPrefix(dataLine, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(dataLine, "data: ")
		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		accumulateOpenCodeGoAnthropicEvent(&event, &finalResp, &usage)
	}
	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("opencode go messages buffered: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	if finalResp == nil {
		return nil, usage, requestID, fmt.Errorf("opencode go messages stream ended without a response")
	}
	applyOpenCodeGoUsageToAnthropicResponse(finalResp, usage)
	return finalResp, usage, requestID, nil
}

func accumulateOpenCodeGoAnthropicEvent(event *apicompat.AnthropicStreamEvent, finalResp **apicompat.AnthropicResponse, usage *ClaudeUsage) {
	if event == nil {
		return
	}
	if event.Type == "message_start" && event.Message != nil {
		*finalResp = event.Message
		mergeAnthropicUsage(usage, event.Message.Usage)
	}
	if event.Type == "message_delta" {
		if event.Usage != nil {
			mergeAnthropicUsage(usage, *event.Usage)
		}
		if event.Delta != nil && event.Delta.StopReason != "" && *finalResp != nil {
			(*finalResp).StopReason = event.Delta.StopReason
		}
	}
	if event.Type == "content_block_start" && event.ContentBlock != nil && *finalResp != nil {
		(*finalResp).Content = append((*finalResp).Content, *event.ContentBlock)
	}
	if event.Type == "content_block_delta" && event.Delta != nil && *finalResp != nil && event.Index != nil {
		idx := *event.Index
		if idx < len((*finalResp).Content) {
			switch event.Delta.Type {
			case "text_delta":
				(*finalResp).Content[idx].Text += event.Delta.Text
			case "thinking_delta":
				(*finalResp).Content[idx].Thinking += event.Delta.Thinking
			case "input_json_delta":
				(*finalResp).Content[idx].Input = appendRawJSON((*finalResp).Content[idx].Input, event.Delta.PartialJSON)
			}
		}
	}
}

func applyOpenCodeGoUsageToAnthropicResponse(finalResp *apicompat.AnthropicResponse, usage ClaudeUsage) {
	if finalResp == nil {
		return
	}
	if usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
		finalResp.Usage = apicompat.AnthropicUsage{
			InputTokens:              usage.InputTokens,
			OutputTokens:             usage.OutputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
		}
	}
}

func (s *OpenAIGatewayService) writeOpenCodeGoBufferedProtocolResponse(
	c *gin.Context,
	resp *http.Response,
	finalResp *apicompat.AnthropicResponse,
	usage ClaudeUsage,
	requestID string,
	opts openCodeGoForwardOptions,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	switch opts.ClientProtocol {
	case openCodeGoClientProtocolAnthropic:
		c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		finalResp.Model = opts.OriginalModel
		c.JSON(http.StatusOK, finalResp)
	case openCodeGoClientProtocolResponses:
		responsesResp := apicompat.AnthropicToResponsesResponse(finalResp)
		responsesResp.Model = opts.OriginalModel
		c.JSON(http.StatusOK, responsesResp)
	default:
		responsesResp := apicompat.AnthropicToResponsesResponse(finalResp)
		ccResp := apicompat.ResponsesToChatCompletions(responsesResp, opts.OriginalModel)
		c.JSON(http.StatusOK, ccResp)
	}
	return openCodeGoForwardResult(requestID, usage, opts, false, nil, time.Since(startTime)), nil
}

func (s *OpenAIGatewayService) streamOpenCodeGoAsAnthropic(
	resp *http.Response,
	c *gin.Context,
	opts openCodeGoForwardOptions,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	firstTokenMs := int(time.Since(startTime).Milliseconds())
	var firstToken *int
	var usage ClaudeUsage

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	writeEvent := func(event *apicompat.AnthropicStreamEvent, rawPayload string) bool {
		if firstToken == nil {
			firstToken = &firstTokenMs
		}
		if event.Type == "message_start" && event.Message != nil {
			mergeAnthropicUsage(&usage, event.Message.Usage)
			event.Message.Model = opts.OriginalModel
			if payload, err := json.Marshal(event); err == nil {
				rawPayload = string(payload)
			}
		}
		if event.Type == "message_delta" && event.Usage != nil {
			mergeAnthropicUsage(&usage, *event.Usage)
		}
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event.Type, rawPayload); err != nil {
			return true
		}
		c.Writer.Flush()
		return false
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "event: ") {
			continue
		}
		if !scanner.Scan() {
			break
		}
		dataLine := scanner.Text()
		if !strings.HasPrefix(dataLine, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(dataLine, "data: ")
		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		if disconnected := writeEvent(&event, payload); disconnected {
			return openCodeGoForwardResult(requestID, usage, opts, true, firstToken, time.Since(startTime)), nil
		}
	}
	return openCodeGoForwardResult(requestID, usage, opts, true, firstToken, time.Since(startTime)), scanner.Err()
}

func (s *OpenAIGatewayService) streamOpenCodeGoAsResponses(
	resp *http.Response,
	c *gin.Context,
	opts openCodeGoForwardOptions,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	firstTokenMs := int(time.Since(startTime).Milliseconds())
	var firstToken *int
	var usage ClaudeUsage
	anthState := apicompat.NewAnthropicEventToResponsesState()
	anthState.Model = opts.OriginalModel

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "event: ") {
			continue
		}
		if !scanner.Scan() {
			break
		}
		dataLine := scanner.Text()
		if !strings.HasPrefix(dataLine, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(dataLine, "data: ")
		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		if firstToken == nil {
			firstToken = &firstTokenMs
		}
		if event.Type == "message_start" && event.Message != nil {
			mergeAnthropicUsage(&usage, event.Message.Usage)
			event.Message.Model = opts.OriginalModel
		}
		if event.Type == "message_delta" && event.Usage != nil {
			mergeAnthropicUsage(&usage, *event.Usage)
		}
		for _, resEvt := range apicompat.AnthropicEventToResponsesEvents(&event, anthState) {
			if disconnected := writeOpenCodeGoResponsesSSE(c, &resEvt); disconnected {
				return openCodeGoForwardResult(requestID, usage, opts, true, firstToken, time.Since(startTime)), nil
			}
		}
	}
	for _, resEvt := range apicompat.FinalizeAnthropicResponsesStream(anthState) {
		if disconnected := writeOpenCodeGoResponsesSSE(c, &resEvt); disconnected {
			return openCodeGoForwardResult(requestID, usage, opts, true, firstToken, time.Since(startTime)), nil
		}
	}
	return openCodeGoForwardResult(requestID, usage, opts, true, firstToken, time.Since(startTime)), scanner.Err()
}

func (s *OpenAIGatewayService) streamOpenCodeGoAsChatCompletions(
	resp *http.Response,
	c *gin.Context,
	opts openCodeGoForwardOptions,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	firstTokenMs := int(time.Since(startTime).Milliseconds())
	var firstToken *int
	var usage ClaudeUsage
	anthState := apicompat.NewAnthropicEventToResponsesState()
	anthState.Model = opts.OriginalModel
	ccState := apicompat.NewResponsesEventToChatState()
	ccState.Model = opts.OriginalModel
	ccState.IncludeUsage = opts.IncludeChatUsage

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	writeChunk := func(chunk apicompat.ChatCompletionsChunk) bool {
		sse, err := apicompat.ChatChunkToSSE(chunk)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprint(c.Writer, sse); err != nil {
			return true
		}
		return false
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "event: ") {
			continue
		}
		if !scanner.Scan() {
			break
		}
		dataLine := scanner.Text()
		if !strings.HasPrefix(dataLine, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(dataLine, "data: ")
		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		if firstToken == nil {
			firstToken = &firstTokenMs
		}
		if event.Type == "message_start" && event.Message != nil {
			mergeAnthropicUsage(&usage, event.Message.Usage)
			event.Message.Model = opts.OriginalModel
		}
		if event.Type == "message_delta" && event.Usage != nil {
			mergeAnthropicUsage(&usage, *event.Usage)
		}
		responsesEvents := apicompat.AnthropicEventToResponsesEvents(&event, anthState)
		for _, resEvt := range responsesEvents {
			for _, chunk := range apicompat.ResponsesEventToChatChunks(&resEvt, ccState) {
				if disconnected := writeChunk(chunk); disconnected {
					return openCodeGoForwardResult(requestID, usage, opts, true, firstToken, time.Since(startTime)), nil
				}
			}
		}
		c.Writer.Flush()
	}
	for _, resEvt := range apicompat.FinalizeAnthropicResponsesStream(anthState) {
		for _, chunk := range apicompat.ResponsesEventToChatChunks(&resEvt, ccState) {
			writeChunk(chunk) //nolint:errcheck
		}
	}
	for _, chunk := range apicompat.FinalizeResponsesChatStream(ccState) {
		writeChunk(chunk) //nolint:errcheck
	}
	fmt.Fprint(c.Writer, "data: [DONE]\n\n") //nolint:errcheck
	c.Writer.Flush()
	return openCodeGoForwardResult(requestID, usage, opts, true, firstToken, time.Since(startTime)), scanner.Err()
}

func writeOpenCodeGoResponsesSSE(c *gin.Context, event *apicompat.ResponsesStreamEvent) bool {
	payload, err := json.Marshal(event)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event.Type, payload); err != nil {
		return true
	}
	c.Writer.Flush()
	return false
}

func (s *OpenAIGatewayService) writeOpenCodeGoProtocolError(c *gin.Context, protocol openCodeGoClientProtocol, statusCode int, errType, message string) {
	switch protocol {
	case openCodeGoClientProtocolAnthropic:
		writeAnthropicError(c, statusCode, errType, message)
	case openCodeGoClientProtocolResponses:
		writeResponsesError(c, statusCode, errType, message)
	default:
		writeChatCompletionsError(c, statusCode, errType, message)
	}
}

func openCodeGoForwardResult(requestID string, usage ClaudeUsage, opts openCodeGoForwardOptions, stream bool, firstTokenMs *int, duration time.Duration) *OpenAIForwardResult {
	if requestID == "" {
		requestID = "opencode_go"
	}
	return &OpenAIForwardResult{
		RequestID:       requestID,
		Usage:           openAIUsageFromOpenCodeGoClaudeUsage(usage),
		Model:           opts.OriginalModel,
		BillingModel:    opts.BillingModel,
		UpstreamModel:   opts.UpstreamModel,
		ReasoningEffort: opts.ReasoningEffort,
		ServiceTier:     opts.ServiceTier,
		Stream:          stream,
		Duration:        duration,
		FirstTokenMs:    firstTokenMs,
	}
}

func openAIUsageFromOpenCodeGoClaudeUsage(usage ClaudeUsage) OpenAIUsage {
	return OpenAIUsage{
		InputTokens:              usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		ImageOutputTokens:        usage.ImageOutputTokens,
	}
}
