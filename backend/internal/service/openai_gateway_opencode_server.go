package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	openCodeDefaultBaseURL  = "http://127.0.0.1:4096"
	openCodeDefaultProvider = "openai"
)

type openCodeCreateSessionRequest struct {
	ParentID string `json:"parentID,omitempty"`
	Title    string `json:"title,omitempty"`
}

type openCodeSession struct {
	ID string `json:"id"`
}

type openCodePromptRequest struct {
	MessageID string                  `json:"messageID,omitempty"`
	Model     *openCodeModelSelection `json:"model,omitempty"`
	Agent     string                  `json:"agent,omitempty"`
	NoReply   bool                    `json:"noReply,omitempty"`
	System    string                  `json:"system,omitempty"`
	Tools     map[string]bool         `json:"tools,omitempty"`
	Parts     []openCodeTextPartInput `json:"parts"`
}

type openCodeModelSelection struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

type openCodeTextPartInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openCodePromptResponse struct {
	Info  openCodeAssistantMessage `json:"info"`
	Parts []openCodePart           `json:"parts"`
}

type openCodeAssistantMessage struct {
	ID         string             `json:"id"`
	ModelID    string             `json:"modelID"`
	ProviderID string             `json:"providerID"`
	Finish     string             `json:"finish"`
	Error      any                `json:"error"`
	Tokens     openCodeTokenUsage `json:"tokens"`
}

type openCodeTokenUsage struct {
	Input     int                `json:"input"`
	Output    int                `json:"output"`
	Reasoning int                `json:"reasoning"`
	Cache     openCodeCacheUsage `json:"cache"`
}

type openCodeCacheUsage struct {
	Read  int `json:"read"`
	Write int `json:"write"`
}

type openCodePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (s *OpenAIGatewayService) forwardResponsesViaOpenCodeServer(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		WriteOpenAIClientError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body", nil)
		return nil, fmt.Errorf("parse responses request for opencode server: %w", err)
	}
	return s.forwardResponsesRequestViaOpenCodeServer(ctx, c, account, &responsesReq, body, startTime)
}

func (s *OpenAIGatewayService) forwardChatCompletionsViaOpenCodeServer(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	var chatReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse chat completions request for opencode server: %w", err)
	}
	if strings.TrimSpace(chatReq.Model) == "" && strings.TrimSpace(defaultMappedModel) != "" {
		chatReq.Model = defaultMappedModel
	}
	clientStream := chatReq.Stream

	responsesReq, err := apicompat.ChatCompletionsToResponses(&chatReq)
	if err != nil {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert chat completions to responses for opencode server: %w", err)
	}
	responsesReq.Stream = clientStream

	responsesBody, err := json.Marshal(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("marshal opencode server responses request: %w", err)
	}

	result, responsesResp, err := s.callOpenCodeServerResponses(ctx, c, account, responsesReq, responsesBody, startTime)
	if err != nil {
		return result, err
	}

	if clientStream {
		return s.writeOpenCodeServerChatStream(c, responsesResp, result, startTime)
	}

	chatResp := apicompat.ResponsesToChatCompletions(responsesResp, result.Model)
	c.JSON(http.StatusOK, chatResp)
	result.Stream = false
	result.Duration = time.Since(startTime)
	return result, nil
}

func (s *OpenAIGatewayService) forwardResponsesRequestViaOpenCodeServer(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	responsesReq *apicompat.ResponsesRequest,
	body []byte,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	result, responsesResp, err := s.callOpenCodeServerResponses(ctx, c, account, responsesReq, body, startTime)
	if err != nil {
		return result, err
	}

	if responsesReq.Stream {
		return s.writeOpenCodeServerResponsesStream(c, responsesResp, result, startTime)
	}

	c.JSON(http.StatusOK, responsesResp)
	result.Stream = false
	result.Duration = time.Since(startTime)
	return result, nil
}

func (s *OpenAIGatewayService) callOpenCodeServerResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	responsesReq *apicompat.ResponsesRequest,
	originalBody []byte,
	startTime time.Time,
) (*OpenAIForwardResult, *apicompat.ResponsesResponse, error) {
	if responsesReq == nil {
		WriteOpenAIClientError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body", nil)
		return nil, nil, fmt.Errorf("opencode server responses request is nil")
	}
	originalModel := strings.TrimSpace(responsesReq.Model)
	if originalModel == "" {
		WriteOpenAIClientError(c, http.StatusBadRequest, "invalid_request_error", "model is required", nil)
		return nil, nil, fmt.Errorf("missing model in opencode server request")
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, "")
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	modelSelection := openCodeModelSelectionFromAccount(account, upstreamModel)
	reasoningEffort := extractOpenAIReasoningEffortFromBody(originalBody, originalModel)
	serviceTier := extractOpenAIServiceTierFromBody(originalBody)

	promptParts, err := openCodeTextPartsFromResponsesRequest(responsesReq)
	if err != nil {
		WriteOpenAIClientError(c, http.StatusBadRequest, "invalid_request_error", err.Error(), nil)
		return nil, nil, fmt.Errorf("convert responses input for opencode server: %w", err)
	}
	if len(promptParts) == 0 {
		promptParts = []openCodeTextPartInput{{Type: "text", Text: ""}}
	}

	baseURL := strings.TrimSpace(account.GetOpenAIBaseURL())
	if baseURL == "" || baseURL == "https://api.openai.com" {
		baseURL = openCodeDefaultBaseURL
	}
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid opencode server base_url: %w", err)
	}

	sessionReq := openCodeCreateSessionRequest{
		ParentID: openCodeAccountSetting(account, "opencode_parent_id", "parent_id"),
		Title:    openCodePromptTitle(promptParts),
	}
	sessionURL, err := openCodeServerEndpointURL(validatedURL, "/session", account)
	if err != nil {
		return nil, nil, err
	}
	sessionBody, err := json.Marshal(sessionReq)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal opencode session request: %w", err)
	}
	sessionResp, err := s.doOpenCodeServerJSON(ctx, c, account, sessionURL, sessionBody, false)
	if err != nil {
		return nil, nil, err
	}
	var session openCodeSession
	if err := json.Unmarshal(sessionResp, &session); err != nil {
		WriteOpenAIClientError(c, http.StatusBadGateway, "api_error", "Failed to parse OpenCode session response", nil)
		return nil, nil, fmt.Errorf("parse opencode session response: %w", err)
	}
	if strings.TrimSpace(session.ID) == "" {
		WriteOpenAIClientError(c, http.StatusBadGateway, "api_error", "OpenCode session response missing id", nil)
		return nil, nil, fmt.Errorf("opencode session response missing id")
	}

	promptReq := openCodePromptRequest{
		MessageID: openCodeAccountSetting(account, "opencode_message_id", "message_id"),
		Model:     &modelSelection,
		Agent:     openCodeAccountSetting(account, "opencode_agent", "agent"),
		System:    strings.TrimSpace(responsesReq.Instructions),
		Parts:     promptParts,
	}
	messagePath := "/session/" + url.PathEscape(session.ID) + "/message"
	messageURL, err := openCodeServerEndpointURL(validatedURL, messagePath, account)
	if err != nil {
		return nil, nil, err
	}
	promptBody, err := json.Marshal(promptReq)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal opencode prompt request: %w", err)
	}

	logger.L().Debug("openai responses: forwarding via opencode server",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.String("opencode_provider", modelSelection.ProviderID),
		zap.String("opencode_model", modelSelection.ModelID),
		zap.Bool("stream", responsesReq.Stream),
	)

	promptRespBody, err := s.doOpenCodeServerJSON(ctx, c, account, messageURL, promptBody, responsesReq.Stream)
	if err != nil {
		return nil, nil, err
	}
	var promptResp openCodePromptResponse
	if err := json.Unmarshal(promptRespBody, &promptResp); err != nil {
		WriteOpenAIClientError(c, http.StatusBadGateway, "api_error", "Failed to parse OpenCode prompt response", nil)
		return nil, nil, fmt.Errorf("parse opencode prompt response: %w", err)
	}

	responsesResp := openCodePromptResponseToResponses(&promptResp, originalModel)
	usage := openCodeUsageToOpenAIUsage(promptResp.Info.Tokens)
	requestID := promptResp.Info.ID
	if requestID == "" {
		requestID = responsesResp.ID
	}

	return &OpenAIForwardResult{
		RequestID:       requestID,
		ResponseID:      responsesResp.ID,
		Usage:           usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
		Stream:          responsesReq.Stream,
		Duration:        time.Since(startTime),
	}, responsesResp, nil
}

func (s *OpenAIGatewayService) doOpenCodeServerJSON(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	targetURL string,
	body []byte,
	acceptSSE bool,
) ([]byte, error) {
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	req, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, targetURL, bytes.NewReader(body))
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build opencode upstream request: %w", err)
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	if acceptSSE {
		req.Header.Set("Accept", "application/json, text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	applyOpenCodeServerAuthHeaders(req.Header, account)
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		req.Header.Set("user-agent", customUA)
	}
	for key, values := range c.Request.Header {
		lowerKey := strings.ToLower(key)
		if openaiCCRawAllowedHeaders[lowerKey] {
			for _, v := range values {
				req.Header.Add(key, v)
			}
		}
	}

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		if upstreamMsg == "" {
			upstreamMsg = http.StatusText(resp.StatusCode)
		}
		WriteOpenAIClientError(c, http.StatusBadGateway, "upstream_error", upstreamMsg, nil)
		s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
		return nil, fmt.Errorf("opencode upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		if !errors.Is(err, ErrUpstreamResponseBodyTooLarge) {
			WriteOpenAIClientError(c, http.StatusBadGateway, "api_error", "Failed to read OpenCode upstream response", nil)
		}
		return nil, fmt.Errorf("read opencode upstream body: %w", err)
	}
	return respBody, nil
}

func (s *OpenAIGatewayService) writeOpenCodeServerResponsesStream(
	c *gin.Context,
	resp *apicompat.ResponsesResponse,
	result *OpenAIForwardResult,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	if resp == nil || result == nil {
		return result, fmt.Errorf("opencode responses stream missing response")
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	firstTokenMs := int(time.Since(startTime).Milliseconds())
	result.FirstTokenMs = &firstTokenMs
	result.Stream = true
	result.Duration = time.Since(startTime)

	events := openCodeResponsesStreamEvents(resp)
	for _, event := range events {
		sse, err := apicompat.ResponsesEventToSSE(event)
		if err != nil {
			logger.L().Warn("opencode responses stream: failed to marshal event", zap.Error(err))
			continue
		}
		if _, err := fmt.Fprint(c.Writer, sse); err != nil {
			result.ClientDisconnect = true
			return result, nil
		}
	}
	if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err != nil {
		result.ClientDisconnect = true
		return result, nil
	}
	c.Writer.Flush()
	return result, nil
}

func (s *OpenAIGatewayService) writeOpenCodeServerChatStream(
	c *gin.Context,
	resp *apicompat.ResponsesResponse,
	result *OpenAIForwardResult,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	if resp == nil || result == nil {
		return result, fmt.Errorf("opencode chat stream missing response")
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	firstTokenMs := int(time.Since(startTime).Milliseconds())
	result.FirstTokenMs = &firstTokenMs
	result.Stream = true
	result.Duration = time.Since(startTime)

	state := apicompat.NewResponsesEventToChatState()
	for _, event := range openCodeResponsesStreamEvents(resp) {
		chunks := apicompat.ResponsesEventToChatChunks(&event, state)
		for _, chunk := range chunks {
			data, err := json.Marshal(chunk)
			if err != nil {
				logger.L().Warn("opencode chat stream: failed to marshal chunk", zap.Error(err))
				continue
			}
			if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
				result.ClientDisconnect = true
				return result, nil
			}
		}
	}
	if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err != nil {
		result.ClientDisconnect = true
		return result, nil
	}
	c.Writer.Flush()
	return result, nil
}

func openCodeResponsesStreamEvents(resp *apicompat.ResponsesResponse) []apicompat.ResponsesStreamEvent {
	if resp == nil {
		return nil
	}
	created := *resp
	created.Status = "in_progress"
	created.Output = nil
	created.Usage = nil

	events := []apicompat.ResponsesStreamEvent{{
		Type:     "response.created",
		Response: &created,
	}}

	for outputIndex, item := range resp.Output {
		itemCopy := item
		itemCopy.Content = nil
		itemCopy.Summary = nil
		events = append(events, apicompat.ResponsesStreamEvent{
			Type:        "response.output_item.added",
			OutputIndex: outputIndex,
			Item:        &itemCopy,
		})

		switch item.Type {
		case "message":
			for contentIndex, part := range item.Content {
				partCopy := part
				events = append(events, apicompat.ResponsesStreamEvent{
					Type:         "response.content_part.added",
					OutputIndex:  outputIndex,
					ContentIndex: contentIndex,
					ItemID:       item.ID,
					Part:         &partCopy,
				})
				if part.Text != "" {
					events = append(events, apicompat.ResponsesStreamEvent{
						Type:         "response.output_text.delta",
						OutputIndex:  outputIndex,
						ContentIndex: contentIndex,
						ItemID:       item.ID,
						Delta:        part.Text,
					})
					events = append(events, apicompat.ResponsesStreamEvent{
						Type:         "response.output_text.done",
						OutputIndex:  outputIndex,
						ContentIndex: contentIndex,
						ItemID:       item.ID,
						Text:         part.Text,
					})
				}
				events = append(events, apicompat.ResponsesStreamEvent{
					Type:         "response.content_part.done",
					OutputIndex:  outputIndex,
					ContentIndex: contentIndex,
					ItemID:       item.ID,
					Part:         &partCopy,
				})
			}
		case "reasoning":
			for summaryIndex, summary := range item.Summary {
				if summary.Text == "" {
					continue
				}
				events = append(events, apicompat.ResponsesStreamEvent{
					Type:         "response.reasoning_summary_text.delta",
					OutputIndex:  outputIndex,
					SummaryIndex: summaryIndex,
					Delta:        summary.Text,
				})
				events = append(events, apicompat.ResponsesStreamEvent{
					Type:         "response.reasoning_summary_text.done",
					OutputIndex:  outputIndex,
					SummaryIndex: summaryIndex,
					Text:         summary.Text,
				})
			}
		}

		itemDone := item
		events = append(events, apicompat.ResponsesStreamEvent{
			Type:        "response.output_item.done",
			OutputIndex: outputIndex,
			Item:        &itemDone,
		})
	}

	completed := *resp
	events = append(events, apicompat.ResponsesStreamEvent{
		Type:     "response.completed",
		Response: &completed,
	})
	return events
}

func openCodePromptResponseToResponses(resp *openCodePromptResponse, model string) *apicompat.ResponsesResponse {
	responseID := ""
	if resp != nil {
		responseID = strings.TrimSpace(resp.Info.ID)
	}
	if responseID == "" {
		responseID = "resp_" + randomHex(12)
	}

	out := &apicompat.ResponsesResponse{
		ID:     responseID,
		Object: "response",
		Model:  model,
		Status: "completed",
	}
	if resp == nil {
		return out
	}

	var textParts []string
	var reasoningParts []string
	for _, part := range resp.Parts {
		text := strings.TrimSpace(part.Text)
		if text == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(part.Type)) {
		case "reasoning":
			reasoningParts = append(reasoningParts, text)
		case "text":
			textParts = append(textParts, text)
		}
	}

	if len(reasoningParts) > 0 {
		summary := make([]apicompat.ResponsesSummary, 0, len(reasoningParts))
		for _, text := range reasoningParts {
			summary = append(summary, apicompat.ResponsesSummary{Type: "summary_text", Text: text})
		}
		out.Output = append(out.Output, apicompat.ResponsesOutput{
			Type:    "reasoning",
			Summary: summary,
		})
	}
	if len(textParts) > 0 {
		out.Output = append(out.Output, apicompat.ResponsesOutput{
			Type:   "message",
			ID:     "msg_" + randomHex(12),
			Role:   "assistant",
			Status: "completed",
			Content: []apicompat.ResponsesContentPart{{
				Type: "output_text",
				Text: strings.Join(textParts, "\n"),
			}},
		})
	}
	if len(out.Output) == 0 {
		out.Output = append(out.Output, apicompat.ResponsesOutput{
			Type:   "message",
			ID:     "msg_" + randomHex(12),
			Role:   "assistant",
			Status: "completed",
			Content: []apicompat.ResponsesContentPart{{
				Type: "output_text",
				Text: "",
			}},
		})
	}

	out.Usage = openCodeTokensToResponsesUsage(resp.Info.Tokens)
	if resp.Info.Error != nil {
		out.Status = "failed"
		out.Error = &apicompat.ResponsesError{
			Code:    "opencode_error",
			Message: fmt.Sprint(resp.Info.Error),
		}
	}
	return out
}

func openCodeTokensToResponsesUsage(tokens openCodeTokenUsage) *apicompat.ResponsesUsage {
	total := tokens.Input + tokens.Output
	usage := &apicompat.ResponsesUsage{
		InputTokens:  tokens.Input,
		OutputTokens: tokens.Output,
		TotalTokens:  total,
	}
	if tokens.Cache.Read > 0 {
		usage.InputTokensDetails = &apicompat.ResponsesInputTokensDetails{CachedTokens: tokens.Cache.Read}
	}
	if tokens.Reasoning > 0 {
		usage.OutputTokensDetails = &apicompat.ResponsesOutputTokensDetails{ReasoningTokens: tokens.Reasoning}
	}
	return usage
}

func openCodeUsageToOpenAIUsage(tokens openCodeTokenUsage) OpenAIUsage {
	return OpenAIUsage{
		InputTokens:              tokens.Input,
		OutputTokens:             tokens.Output,
		CacheCreationInputTokens: tokens.Cache.Write,
		CacheReadInputTokens:     tokens.Cache.Read,
	}
}

func openCodeModelSelectionFromAccount(account *Account, upstreamModel string) openCodeModelSelection {
	upstreamModel = strings.TrimSpace(upstreamModel)
	provider := strings.TrimSpace(openCodeAccountSetting(account, "opencode_provider", "provider"))
	model := upstreamModel
	if before, after, ok := strings.Cut(upstreamModel, "/"); ok {
		if strings.TrimSpace(before) != "" {
			provider = strings.TrimSpace(before)
		}
		model = strings.TrimSpace(after)
	}
	if provider == "" {
		provider = openCodeDefaultProvider
	}
	return openCodeModelSelection{ProviderID: provider, ModelID: model}
}

func openCodeTextPartsFromResponsesRequest(req *apicompat.ResponsesRequest) ([]openCodeTextPartInput, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	text, err := openCodeResponsesInputText(req.Input)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	return []openCodeTextPartInput{{Type: "text", Text: text}}, nil
}

func openCodeResponsesInputText(input json.RawMessage) (string, error) {
	input = bytes.TrimSpace(input)
	if len(input) == 0 || bytes.Equal(input, []byte("null")) {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(input, &text); err == nil {
		return text, nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(input, &items); err != nil {
		return "", fmt.Errorf("parse responses input: %w", err)
	}

	var lines []string
	for _, raw := range items {
		itemText, err := openCodeResponsesInputItemText(raw)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(itemText) != "" {
			lines = append(lines, itemText)
		}
	}
	return strings.Join(lines, "\n\n"), nil
}

func openCodeResponsesInputItemText(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return "", fmt.Errorf("parse responses input item: %w", err)
	}

	itemType := strings.TrimSpace(rawJSONString(item["type"]))
	role := strings.TrimSpace(rawJSONString(item["role"]))
	prefix := ""
	if role != "" {
		prefix = role + ": "
	}

	switch itemType {
	case "function_call":
		return prefix + "tool call " + rawJSONString(item["name"]) + ": " + rawJSONString(item["arguments"]), nil
	case "function_call_output":
		return prefix + "tool result: " + rawJSONString(item["output"]), nil
	case "reasoning":
		return "", nil
	case "input_image":
		return prefix + "[image omitted]", nil
	case "input_text", "output_text", "text":
		return prefix + rawJSONString(item["text"]), nil
	}

	if content := item["content"]; len(bytes.TrimSpace(content)) > 0 {
		contentText, err := openCodeResponsesContentText(content)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(contentText) != "" {
			return prefix + contentText, nil
		}
	}
	if txt := rawJSONString(item["text"]); txt != "" {
		return prefix + txt, nil
	}
	return "", nil
}

func openCodeResponsesContentText(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err == nil {
		lines := make([]string, 0, len(parts))
		for _, partRaw := range parts {
			partText, err := openCodeResponsesContentPartText(partRaw)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(partText) != "" {
				lines = append(lines, partText)
			}
		}
		return strings.Join(lines, "\n"), nil
	}
	var part map[string]json.RawMessage
	if err := json.Unmarshal(raw, &part); err == nil {
		return openCodeResponsesContentPartMapText(part), nil
	}
	return "", nil
}

func openCodeResponsesContentPartText(raw json.RawMessage) (string, error) {
	var part map[string]json.RawMessage
	if err := json.Unmarshal(raw, &part); err != nil {
		var text string
		if textErr := json.Unmarshal(raw, &text); textErr == nil {
			return text, nil
		}
		return "", fmt.Errorf("parse responses content part: %w", err)
	}
	return openCodeResponsesContentPartMapText(part), nil
}

func openCodeResponsesContentPartMapText(part map[string]json.RawMessage) string {
	switch strings.TrimSpace(rawJSONString(part["type"])) {
	case "input_image":
		return "[image omitted]"
	case "input_file", "file":
		return "[file omitted]"
	default:
		return rawJSONString(part["text"])
	}
}

func rawJSONString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

func openCodePromptTitle(parts []openCodeTextPartInput) string {
	for _, part := range parts {
		title := strings.Join(strings.Fields(part.Text), " ")
		if title == "" {
			continue
		}
		if len(title) > 80 {
			return title[:80]
		}
		return title
	}
	return "sub2api"
}

func openCodeAccountSetting(account *Account, keys ...string) string {
	if account == nil {
		return ""
	}
	for _, key := range keys {
		if v := strings.TrimSpace(account.GetCredential(key)); v != "" {
			return v
		}
		if v := strings.TrimSpace(account.GetExtraString(key)); v != "" {
			return v
		}
	}
	return ""
}

func openCodeServerEndpointURL(baseURL, path string, account *Account) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("parse opencode base_url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("opencode base_url must include scheme and host")
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	parsed.Path = basePath + path
	q := parsed.Query()
	if directory := openCodeAccountSetting(account, "opencode_directory", "directory"); directory != "" {
		q.Set("directory", directory)
	}
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

func applyOpenCodeServerAuthHeaders(headers http.Header, account *Account) {
	if headers == nil || account == nil {
		return
	}
	token := strings.TrimSpace(openCodeAccountSetting(account, "opencode_server_password", "opencode_password", "password"))
	if token == "" {
		token = strings.TrimSpace(account.GetOpenAIApiKey())
	}
	if token == "" {
		return
	}
	headers.Set("Authorization", "Bearer "+token)
}
