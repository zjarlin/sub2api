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
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	openCodeLocalDefaultProviderID = "opencode-go"
	openCodeLocalDefaultModelID    = "minimax-m3"
	openCodeLocalDefaultAgent      = "build"
	openCodeLocalEventMinConns     = 64
)

var openCodeLocalDisabledTools = map[string]bool{
	"bash":        false,
	"read":        false,
	"glob":        false,
	"grep":        false,
	"edit":        false,
	"write":       false,
	"task":        false,
	"webfetch":    false,
	"websearch":   false,
	"todowrite":   false,
	"skill":       false,
	"apply_patch": false,
}

var openCodeLocalDefaultTools = map[string]bool{
	"bash":        false,
	"read":        true,
	"glob":        true,
	"grep":        true,
	"edit":        false,
	"write":       false,
	"task":        false,
	"webfetch":    false,
	"websearch":   false,
	"todowrite":   false,
	"skill":       false,
	"apply_patch": false,
}

type openCodeLocalSessionResponse struct {
	ID string `json:"id"`
}

type openCodeLocalMessageResponse struct {
	Info  openCodeLocalMessageInfo   `json:"info"`
	Parts []openCodeLocalMessagePart `json:"parts"`
}

type openCodeLocalMessageInfo struct {
	ID         string                  `json:"id"`
	ModelID    string                  `json:"modelID"`
	ProviderID string                  `json:"providerID"`
	Finish     string                  `json:"finish"`
	Tokens     openCodeLocalTokenUsage `json:"tokens"`
	Time       struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed"`
	} `json:"time"`
	Error json.RawMessage `json:"error,omitempty"`
}

type openCodeLocalTokenUsage struct {
	Input     float64 `json:"input"`
	Output    float64 `json:"output"`
	Reasoning float64 `json:"reasoning"`
	Cache     struct {
		Read  float64 `json:"read"`
		Write float64 `json:"write"`
	} `json:"cache"`
}

type openCodeLocalMessagePart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type openCodeLocalResult struct {
	ID            string
	SessionID     string
	Content       string
	FinishReason  string
	InputTokens   int
	OutputTokens  int
	CacheRead     int
	CacheWrite    int
	Reasoning     int
	Created       int64
	Completed     int64
	UpstreamModel string
	FirstTokenMs  *int
}

type openCodeLocalStreamProtocol string

const (
	openCodeLocalStreamProtocolChat      openCodeLocalStreamProtocol = "chat"
	openCodeLocalStreamProtocolResponses openCodeLocalStreamProtocol = "responses"
)

type openCodeLocalStreamOptions struct {
	Protocol    openCodeLocalStreamProtocol
	Model       string
	ServiceTier *string
}

type openCodeLocalStreamState struct {
	result       openCodeLocalResult
	text         strings.Builder
	partTypes    map[string]string
	firstTokenMs *int
	streamErr    error
}

func accountUsesLocalOpenCodeServer(account *Account) bool {
	if account == nil || !account.IsOpenAIApiKey() {
		return false
	}
	if !strings.EqualFold(account.GetOpenAIVendor(), "opencode-go") {
		return false
	}
	baseURL := strings.ToLower(strings.TrimSpace(account.GetOpenAIBaseURL()))
	if baseURL == "" {
		return false
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return parsed.Port() == "4096" &&
		(host == "host.docker.internal" || host == "127.0.0.1" || host == "localhost" || host == "0.0.0.0")
}

func (s *OpenAIGatewayService) forwardAsOpenCodeLocalChatCompletions(
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
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := strings.TrimSpace(chatReq.Model)
	if originalModel == "" {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	if upstreamModel == "" {
		upstreamModel = openCodeLocalDefaultModelID
	}
	chatReq.Model = upstreamModel

	updatedBody, err := s.applyOpenAIFastPolicyToBody(ctx, account, upstreamModel, body)
	if err != nil {
		var blocked *OpenAIFastBlockedError
		if errors.As(err, &blocked) {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			writeChatCompletionsError(c, http.StatusForbidden, "permission_error", blocked.Message)
		}
		return nil, err
	}
	if updatedBody != nil {
		if err := json.Unmarshal(updatedBody, &chatReq); err != nil {
			writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
			return nil, fmt.Errorf("parse policy-adjusted chat completions request: %w", err)
		}
		chatReq.Model = upstreamModel
	}

	prompt := openCodeLocalPromptFromChatRequest(&chatReq)
	if strings.TrimSpace(prompt) == "" {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "message content is required")
		return nil, fmt.Errorf("missing message content")
	}

	reasoningEffort := extractOpenAIReasoningEffortFromBody(body, originalModel)
	serviceTier := extractOpenAIServiceTierFromBody(body)

	if chatReq.Stream {
		result, err := s.streamOpenCodeLocalPrompt(ctx, c, account, upstreamModel, prompt, openCodeLocalStreamOptions{
			Protocol:    openCodeLocalStreamProtocolChat,
			Model:       originalModel,
			ServiceTier: serviceTier,
		}, startTime)
		if err != nil {
			return nil, err
		}
		usage := OpenAIUsage{
			InputTokens:              result.InputTokens,
			OutputTokens:             result.OutputTokens,
			CacheReadInputTokens:     result.CacheRead,
			CacheCreationInputTokens: result.CacheWrite,
		}
		firstTokenMs := int(time.Since(startTime).Milliseconds())
		if result.FirstTokenMs != nil {
			firstTokenMs = *result.FirstTokenMs
		}
		return &OpenAIForwardResult{
			RequestID:       result.ID,
			Usage:           usage,
			Model:           originalModel,
			BillingModel:    billingModel,
			UpstreamModel:   upstreamModel,
			ReasoningEffort: reasoningEffort,
			ServiceTier:     serviceTier,
			Stream:          true,
			Duration:        time.Since(startTime),
			FirstTokenMs:    &firstTokenMs,
		}, nil
	}

	result, err := s.sendOpenCodeLocalPrompt(ctx, c, account, upstreamModel, prompt)
	if err != nil {
		return nil, err
	}

	usage := OpenAIUsage{
		InputTokens:              result.InputTokens,
		OutputTokens:             result.OutputTokens,
		CacheReadInputTokens:     result.CacheRead,
		CacheCreationInputTokens: result.CacheWrite,
	}

	if err := s.writeOpenCodeLocalChatJSON(c, result, originalModel, serviceTier); err != nil {
		return nil, err
	}
	return &OpenAIForwardResult{
		RequestID:       result.ID,
		Usage:           usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) forwardOpenCodeLocalResponsesViaChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": "Failed to parse request body",
			},
		})
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := strings.TrimSpace(responsesReq.Model)
	if originalModel == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": "model is required",
			},
		})
		return nil, fmt.Errorf("missing model in request")
	}

	chatReq, err := apicompat.ResponsesToChatCompletionsRequest(&responsesReq)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": err.Error(),
			},
		})
		return nil, fmt.Errorf("convert responses to chat completions: %w", err)
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, "")
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	if upstreamModel == "" {
		upstreamModel = openCodeLocalDefaultModelID
	}
	chatReq.Model = upstreamModel
	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal local opencode chat request: %w", err)
	}
	chatBody, err = s.applyOpenAIFastPolicyToBody(ctx, account, upstreamModel, chatBody)
	if err != nil {
		var blocked *OpenAIFastBlockedError
		if errors.As(err, &blocked) {
			writeOpenAIFastPolicyBlockedResponse(c, blocked)
		}
		return nil, err
	}
	if err := json.Unmarshal(chatBody, &chatReq); err != nil {
		return nil, fmt.Errorf("parse policy-adjusted local opencode chat request: %w", err)
	}
	chatReq.Model = upstreamModel

	prompt := openCodeLocalPromptFromChatRequest(chatReqWithInstructions(chatReq, responsesReq.Instructions))
	if strings.TrimSpace(prompt) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": "message content is required",
			},
		})
		return nil, fmt.Errorf("missing message content")
	}

	reasoningEffort := extractOpenAIReasoningEffortFromBody(body, originalModel)
	serviceTier := extractOpenAIServiceTierFromBody(body)

	if responsesReq.Stream {
		result, err := s.streamOpenCodeLocalPrompt(ctx, c, account, upstreamModel, prompt, openCodeLocalStreamOptions{
			Protocol:    openCodeLocalStreamProtocolResponses,
			Model:       originalModel,
			ServiceTier: serviceTier,
		}, startTime)
		if err != nil {
			return nil, err
		}
		usage := OpenAIUsage{
			InputTokens:              result.InputTokens,
			OutputTokens:             result.OutputTokens,
			CacheReadInputTokens:     result.CacheRead,
			CacheCreationInputTokens: result.CacheWrite,
		}
		firstTokenMs := int(time.Since(startTime).Milliseconds())
		if result.FirstTokenMs != nil {
			firstTokenMs = *result.FirstTokenMs
		}
		return &OpenAIForwardResult{
			RequestID:       result.ID,
			Usage:           usage,
			Model:           originalModel,
			BillingModel:    billingModel,
			UpstreamModel:   upstreamModel,
			ReasoningEffort: reasoningEffort,
			ServiceTier:     serviceTier,
			Stream:          true,
			Duration:        time.Since(startTime),
			FirstTokenMs:    &firstTokenMs,
		}, nil
	}

	result, err := s.sendOpenCodeLocalPrompt(ctx, c, account, upstreamModel, prompt)
	if err != nil {
		return nil, err
	}

	usage := OpenAIUsage{
		InputTokens:              result.InputTokens,
		OutputTokens:             result.OutputTokens,
		CacheReadInputTokens:     result.CacheRead,
		CacheCreationInputTokens: result.CacheWrite,
	}

	ccResp := openCodeLocalChatResponse(result, originalModel, serviceTier)
	responsesResp := apicompat.ChatCompletionsResponseToResponses(&ccResp, originalModel)
	c.JSON(http.StatusOK, responsesResp)
	return &OpenAIForwardResult{
		RequestID:       result.ID,
		Usage:           usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) forwardOpenCodeLocalAnthropicMessages(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

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
	if upstreamModel == "" {
		upstreamModel = openCodeLocalDefaultModelID
	}

	prompt := openCodeLocalPromptFromAnthropicRequest(&anthropicReq)
	if strings.TrimSpace(prompt) == "" {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "message content is required")
		return nil, fmt.Errorf("missing message content")
	}

	result, err := s.sendOpenCodeLocalPrompt(ctx, c, account, upstreamModel, prompt)
	if err != nil {
		return nil, err
	}

	usage := OpenAIUsage{
		InputTokens:              result.InputTokens,
		OutputTokens:             result.OutputTokens,
		CacheReadInputTokens:     result.CacheRead,
		CacheCreationInputTokens: result.CacheWrite,
	}
	if anthropicReq.Stream {
		if err := s.writeOpenCodeLocalAnthropicStream(c, result, originalModel); err != nil {
			return nil, err
		}
		firstTokenMs := int(time.Since(startTime).Milliseconds())
		return &OpenAIForwardResult{
			RequestID:     result.ID,
			Usage:         usage,
			Model:         originalModel,
			BillingModel:  billingModel,
			UpstreamModel: upstreamModel,
			Stream:        true,
			Duration:      time.Since(startTime),
			FirstTokenMs:  &firstTokenMs,
		}, nil
	}

	if err := s.writeOpenCodeLocalAnthropicJSON(c, result, originalModel); err != nil {
		return nil, err
	}
	return &OpenAIForwardResult{
		RequestID:     result.ID,
		Usage:         usage,
		Model:         originalModel,
		BillingModel:  billingModel,
		UpstreamModel: upstreamModel,
		Stream:        false,
		Duration:      time.Since(startTime),
	}, nil
}

func chatReqWithInstructions(req *apicompat.ChatCompletionsRequest, instructions string) *apicompat.ChatCompletionsRequest {
	if req == nil {
		return nil
	}
	if strings.TrimSpace(instructions) != "" && strings.TrimSpace(req.Instructions) == "" {
		req.Instructions = instructions
	}
	return req
}

func (s *OpenAIGatewayService) sendOpenCodeLocalPrompt(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	upstreamModel string,
	prompt string,
) (*openCodeLocalResult, error) {
	baseURL := account.GetOpenAIBaseURL()
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}

	sessionURL, err := openCodeLocalURL(validatedURL, "/session", account.GetCredential("opencode_directory"))
	if err != nil {
		return nil, err
	}
	model := map[string]any{
		"providerID": openCodeLocalProviderID(account),
		"id":         upstreamModel,
	}
	sessionBody := map[string]any{
		"model":      model,
		"agent":      openCodeLocalAgent(account),
		"permission": openCodeLocalPermissions(account),
	}
	sessionResp, err := s.doOpenCodeLocalJSON(ctx, c, account, http.MethodPost, sessionURL, sessionBody)
	if err != nil {
		return nil, err
	}
	if sessionResp == nil || sessionResp.Body == nil {
		return nil, fmt.Errorf("opencode session response is empty")
	}
	defer func() { _ = sessionResp.Body.Close() }()
	sessionBytes, err := ReadUpstreamResponseBody(sessionResp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, fmt.Errorf("read opencode session response: %w", err)
	}
	if sessionResp.StatusCode >= 400 {
		return nil, s.handleOpenCodeLocalHTTPError(ctx, c, account, sessionResp, sessionBytes, upstreamModel)
	}
	var session openCodeLocalSessionResponse
	if err := json.Unmarshal(sessionBytes, &session); err != nil || strings.TrimSpace(session.ID) == "" {
		return nil, fmt.Errorf("parse opencode session response: %w", err)
	}

	messageURL, err := openCodeLocalURL(validatedURL, "/session/"+url.PathEscape(session.ID)+"/message", account.GetCredential("opencode_directory"))
	if err != nil {
		return nil, err
	}
	messageResp, err := s.doOpenCodeLocalJSON(ctx, c, account, http.MethodPost, messageURL, openCodeLocalMessageBody(account, upstreamModel, prompt))
	if err != nil {
		return nil, err
	}
	if messageResp == nil || messageResp.Body == nil {
		return nil, fmt.Errorf("opencode message response is empty")
	}
	defer func() { _ = messageResp.Body.Close() }()
	messageBytes, err := ReadUpstreamResponseBody(messageResp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, fmt.Errorf("read opencode message response: %w", err)
	}
	if messageResp.StatusCode >= 400 {
		return nil, s.handleOpenCodeLocalHTTPError(ctx, c, account, messageResp, messageBytes, upstreamModel)
	}

	var message openCodeLocalMessageResponse
	if err := json.Unmarshal(messageBytes, &message); err != nil {
		return nil, fmt.Errorf("parse opencode message response: %w", err)
	}
	if len(message.Info.Error) > 0 && strings.TrimSpace(string(message.Info.Error)) != "null" {
		return nil, s.handleOpenCodeLocalModelError(ctx, c, account, message.Info.Error, upstreamModel)
	}

	content := strings.TrimSpace(openCodeLocalTextFromParts(message.Parts))
	if content == "" {
		return nil, fmt.Errorf("opencode returned empty text")
	}
	finish := strings.TrimSpace(message.Info.Finish)
	if finish == "" {
		finish = "stop"
	}
	created := openCodeLocalUnixSeconds(message.Info.Time.Created)
	if created == 0 {
		created = time.Now().Unix()
	}
	id := strings.TrimSpace(message.Info.ID)
	if id == "" {
		id = "chatcmpl_" + session.ID
	}

	return &openCodeLocalResult{
		ID:            id,
		SessionID:     session.ID,
		Content:       content,
		FinishReason:  openCodeLocalFinishReason(finish),
		InputTokens:   openCodeLocalTokenInt(message.Info.Tokens.Input),
		OutputTokens:  openCodeLocalTokenInt(message.Info.Tokens.Output),
		CacheRead:     openCodeLocalTokenInt(message.Info.Tokens.Cache.Read),
		CacheWrite:    openCodeLocalTokenInt(message.Info.Tokens.Cache.Write),
		Reasoning:     openCodeLocalTokenInt(message.Info.Tokens.Reasoning),
		Created:       created,
		Completed:     openCodeLocalUnixSeconds(message.Info.Time.Completed),
		UpstreamModel: upstreamModel,
	}, nil
}

func (s *OpenAIGatewayService) streamOpenCodeLocalPrompt(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	upstreamModel string,
	prompt string,
	options openCodeLocalStreamOptions,
	startTime time.Time,
) (*openCodeLocalResult, error) {
	baseURL := account.GetOpenAIBaseURL()
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}

	sessionID, err := s.createOpenCodeLocalSession(ctx, c, account, validatedURL, upstreamModel)
	if err != nil {
		return nil, err
	}

	eventURL, err := openCodeLocalURL(validatedURL, "/event", account.GetCredential("opencode_directory"))
	if err != nil {
		return nil, err
	}
	eventResp, err := s.doOpenCodeLocalEvent(ctx, c, account, eventURL)
	if err != nil {
		return nil, err
	}
	if eventResp == nil || eventResp.Body == nil {
		return nil, fmt.Errorf("opencode event response is empty")
	}
	defer func() { _ = eventResp.Body.Close() }()
	if eventResp.StatusCode >= 400 {
		eventBytes, readErr := ReadUpstreamResponseBody(eventResp.Body, s.cfg, c, openAITooLargeError)
		if readErr != nil {
			return nil, fmt.Errorf("read opencode event response: %w", readErr)
		}
		return nil, s.handleOpenCodeLocalHTTPError(ctx, c, account, eventResp, eventBytes, upstreamModel)
	}

	state := &openCodeLocalStreamState{
		result: openCodeLocalResult{
			ID:            "chatcmpl_" + sessionID,
			SessionID:     sessionID,
			FinishReason:  "stop",
			Created:       time.Now().Unix(),
			UpstreamModel: upstreamModel,
		},
		partTypes: make(map[string]string),
	}

	if err := s.sendOpenCodeLocalPromptAsync(ctx, c, account, validatedURL, sessionID, upstreamModel, prompt); err != nil {
		return nil, err
	}

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), http.Header{}, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	streamWriter := newOpenCodeLocalStreamWriter(c, options)
	if err := streamWriter.writeRole(state.result); err != nil {
		return nil, err
	}

	if err := s.consumeOpenCodeLocalEvents(eventResp.Body, sessionID, state, streamWriter, startTime); err != nil {
		state.streamErr = err
	}
	if state.streamErr != nil {
		return nil, state.streamErr
	}

	state.result.Content = strings.TrimSpace(state.text.String())
	if state.result.Content == "" {
		return nil, fmt.Errorf("opencode returned empty text")
	}
	if state.result.ID == "" {
		state.result.ID = "chatcmpl_" + sessionID
	}
	if state.result.Created == 0 {
		state.result.Created = time.Now().Unix()
	}
	state.result.FirstTokenMs = state.firstTokenMs

	if err := streamWriter.finish(state.result, options); err != nil {
		return nil, err
	}
	c.Writer.Flush()
	return &state.result, nil
}

func (s *OpenAIGatewayService) createOpenCodeLocalSession(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	validatedURL string,
	upstreamModel string,
) (string, error) {
	sessionURL, err := openCodeLocalURL(validatedURL, "/session", account.GetCredential("opencode_directory"))
	if err != nil {
		return "", err
	}
	sessionBody := map[string]any{
		"model": map[string]any{
			"providerID": openCodeLocalProviderID(account),
			"id":         upstreamModel,
		},
		"agent":      openCodeLocalAgent(account),
		"permission": openCodeLocalPermissions(account),
	}
	sessionResp, err := s.doOpenCodeLocalJSON(ctx, c, account, http.MethodPost, sessionURL, sessionBody)
	if err != nil {
		return "", err
	}
	if sessionResp == nil || sessionResp.Body == nil {
		return "", fmt.Errorf("opencode session response is empty")
	}
	defer func() { _ = sessionResp.Body.Close() }()
	sessionBytes, err := ReadUpstreamResponseBody(sessionResp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return "", fmt.Errorf("read opencode session response: %w", err)
	}
	if sessionResp.StatusCode >= 400 {
		return "", s.handleOpenCodeLocalHTTPError(ctx, c, account, sessionResp, sessionBytes, upstreamModel)
	}
	var session openCodeLocalSessionResponse
	if err := json.Unmarshal(sessionBytes, &session); err != nil || strings.TrimSpace(session.ID) == "" {
		return "", fmt.Errorf("parse opencode session response: %w", err)
	}
	return session.ID, nil
}

func (s *OpenAIGatewayService) sendOpenCodeLocalPromptAsync(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	validatedURL string,
	sessionID string,
	upstreamModel string,
	prompt string,
) error {
	promptURL, err := openCodeLocalURL(validatedURL, "/session/"+url.PathEscape(sessionID)+"/prompt_async", account.GetCredential("opencode_directory"))
	if err != nil {
		return err
	}
	promptResp, err := s.doOpenCodeLocalJSON(ctx, c, account, http.MethodPost, promptURL, openCodeLocalMessageBody(account, upstreamModel, prompt))
	if err != nil {
		return err
	}
	if promptResp == nil || promptResp.Body == nil {
		return fmt.Errorf("opencode prompt_async response is empty")
	}
	defer func() { _ = promptResp.Body.Close() }()
	promptBytes, err := ReadUpstreamResponseBody(promptResp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return fmt.Errorf("read opencode prompt_async response: %w", err)
	}
	if promptResp.StatusCode >= 400 {
		return s.handleOpenCodeLocalHTTPError(ctx, c, account, promptResp, promptBytes, upstreamModel)
	}
	return nil
}

func (s *OpenAIGatewayService) doOpenCodeLocalEvent(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	targetURL string,
) (*http.Response, error) {
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	req, err := http.NewRequestWithContext(upstreamCtx, http.MethodGet, targetURL, nil)
	releaseUpstreamCtx()
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenCodeEvent))
	req.Header.Set("Accept", "text/event-stream")
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		req.Header.Set("user-agent", customUA)
	}

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, openCodeLocalEventHTTPConcurrency(account))
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	return resp, nil
}

func openCodeLocalMessageBody(account *Account, upstreamModel string, prompt string) map[string]any {
	return map[string]any{
		"agent": openCodeLocalAgent(account),
		"model": map[string]any{
			"providerID": openCodeLocalProviderID(account),
			"modelID":    upstreamModel,
		},
		"tools": openCodeLocalTools(account),
		"parts": []map[string]any{{
			"type": "text",
			"text": prompt,
		}},
	}
}

func (s *OpenAIGatewayService) consumeOpenCodeLocalEvents(
	body io.Reader,
	sessionID string,
	state *openCodeLocalStreamState,
	writer *openCodeLocalStreamWriter,
	startTime time.Time,
) error {
	scanner := bufio.NewScanner(body)
	scanBuf := getSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], 1024*1024)

	var dataLines []string
	flush := func() (bool, error) {
		if len(dataLines) == 0 {
			return false, nil
		}
		raw := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = nil
		if raw == "" || raw == "[DONE]" {
			return false, nil
		}
		done, err := handleOpenCodeLocalEvent(raw, sessionID, state, writer, startTime)
		return done, err
	}

	keepaliveInterval := s.openCodeLocalStreamKeepaliveInterval()
	if keepaliveInterval <= 0 {
		defer putSSEScannerBuf64K(scanBuf)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				done, err := flush()
				if err != nil || done {
					return err
				}
				continue
			}
			if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		done, err := flush()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read opencode event stream: %w", err)
		}
		return nil
	}

	type scanEvent struct {
		line string
		err  error
	}
	events := make(chan scanEvent, 16)
	doneCh := make(chan struct{})
	sendEvent := func(ev scanEvent) bool {
		select {
		case events <- ev:
			return true
		case <-doneCh:
			return false
		}
	}
	go func(scanBuf *sseScannerBuf64K) {
		defer putSSEScannerBuf64K(scanBuf)
		defer close(events)
		for scanner.Scan() {
			if !sendEvent(scanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = sendEvent(scanEvent{err: err})
		}
	}(scanBuf)
	defer close(doneCh)

	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	processLine := func(line string) (bool, error) {
		if line == "" {
			return flush()
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		return false, nil
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				done, err := flush()
				if err != nil {
					return err
				}
				if done {
					return nil
				}
				return nil
			}
			if ev.err != nil {
				return fmt.Errorf("read opencode event stream: %w", ev.err)
			}
			done, err := processLine(ev.line)
			if err != nil || done {
				return err
			}
		case <-ticker.C:
			if time.Since(writer.lastWriteAt) < keepaliveInterval {
				continue
			}
			if err := writer.writeKeepalive(); err != nil {
				return err
			}
		}
	}
}

func handleOpenCodeLocalEvent(
	raw string,
	sessionID string,
	state *openCodeLocalStreamState,
	writer *openCodeLocalStreamWriter,
	startTime time.Time,
) (bool, error) {
	event := gjson.Parse(raw)
	eventSessionID := strings.TrimSpace(event.Get("properties.sessionID").String())
	if eventSessionID != "" && eventSessionID != sessionID {
		return false, nil
	}

	switch event.Get("type").String() {
	case "message.part.updated":
		part := event.Get("properties.part")
		partID := strings.TrimSpace(part.Get("id").String())
		if partID != "" {
			state.partTypes[partID] = strings.TrimSpace(part.Get("type").String())
		}
		switch part.Get("type").String() {
		case "step-finish":
			openCodeLocalApplyStepFinish(state, part)
		}
	case "message.part.delta":
		if event.Get("properties.field").String() != "text" {
			return false, nil
		}
		partID := strings.TrimSpace(event.Get("properties.partID").String())
		partType := state.partTypes[partID]
		delta := event.Get("properties.delta").String()
		if delta == "" {
			return false, nil
		}
		if partType == "reasoning" {
			if err := writer.writeReasoningDelta(state.result, delta); err != nil {
				return false, err
			}
			return false, nil
		}
		if partType != "" && partType != "text" {
			return false, nil
		}
		if state.firstTokenMs == nil {
			firstTokenMs := int(time.Since(startTime).Milliseconds())
			state.firstTokenMs = &firstTokenMs
		}
		_, _ = state.text.WriteString(delta)
		if err := writer.writeTextDelta(state.result, delta); err != nil {
			return false, err
		}
	case "message.updated":
		openCodeLocalApplyMessageInfo(state, event.Get("properties.info"))
	case "session.error":
		errMsg := sanitizeUpstreamErrorMessage(openCodeLocalEventErrorMessage(event.Get("properties.error")))
		if errMsg == "" {
			errMsg = "OpenCode local stream error"
		}
		return false, fmt.Errorf("%s", errMsg)
	case "session.idle":
		return true, nil
	}
	return false, nil
}

func openCodeLocalApplyStepFinish(state *openCodeLocalStreamState, part gjson.Result) {
	if state == nil {
		return
	}
	if reason := strings.TrimSpace(part.Get("reason").String()); reason != "" {
		state.result.FinishReason = openCodeLocalFinishReason(reason)
	}
	openCodeLocalApplyTokenUsage(&state.result, part.Get("tokens"))
}

func openCodeLocalApplyMessageInfo(state *openCodeLocalStreamState, info gjson.Result) {
	if state == nil || !info.Exists() {
		return
	}
	if id := strings.TrimSpace(info.Get("id").String()); id != "" && state.result.ID == "" {
		state.result.ID = id
	}
	if finish := strings.TrimSpace(info.Get("finish").String()); finish != "" {
		state.result.FinishReason = openCodeLocalFinishReason(finish)
	}
	if created := openCodeLocalUnixSeconds(info.Get("time.created").Int()); created > 0 {
		state.result.Created = created
	}
	if completed := openCodeLocalUnixSeconds(info.Get("time.completed").Int()); completed > 0 {
		state.result.Completed = completed
	}
	openCodeLocalApplyTokenUsage(&state.result, info.Get("tokens"))
}

func openCodeLocalApplyTokenUsage(result *openCodeLocalResult, tokens gjson.Result) {
	if result == nil || !tokens.Exists() {
		return
	}
	if input := openCodeLocalTokenInt(tokens.Get("input").Float()); input > 0 {
		result.InputTokens = input
	}
	if output := openCodeLocalTokenInt(tokens.Get("output").Float()); output > 0 {
		result.OutputTokens = output
	}
	if reasoning := openCodeLocalTokenInt(tokens.Get("reasoning").Float()); reasoning > 0 {
		result.Reasoning = reasoning
	}
	if cacheRead := openCodeLocalTokenInt(tokens.Get("cache.read").Float()); cacheRead > 0 {
		result.CacheRead = cacheRead
	}
	if cacheWrite := openCodeLocalTokenInt(tokens.Get("cache.write").Float()); cacheWrite > 0 {
		result.CacheWrite = cacheWrite
	}
}

func openCodeLocalEventErrorMessage(errResult gjson.Result) string {
	if !errResult.Exists() {
		return ""
	}
	for _, path := range []string{"message", "data.message", "error.message", "name"} {
		if msg := strings.TrimSpace(errResult.Get(path).String()); msg != "" {
			return msg
		}
	}
	return strings.TrimSpace(errResult.Raw)
}

type openCodeLocalStreamWriter struct {
	c              *gin.Context
	options        openCodeLocalStreamOptions
	responsesState *apicompat.ChatCompletionsToResponsesStreamState
	lastWriteAt    time.Time
}

func newOpenCodeLocalStreamWriter(c *gin.Context, options openCodeLocalStreamOptions) *openCodeLocalStreamWriter {
	writer := &openCodeLocalStreamWriter{c: c, options: options, lastWriteAt: time.Now()}
	if options.Protocol == openCodeLocalStreamProtocolResponses {
		writer.responsesState = apicompat.NewChatCompletionsToResponsesStreamState(options.Model)
	}
	return writer
}

func (w *openCodeLocalStreamWriter) writeRole(result openCodeLocalResult) error {
	chunk := openCodeLocalChatRoleChunk(result, w.options.Model, w.options.ServiceTier)
	return w.writeChatChunk(chunk)
}

func (w *openCodeLocalStreamWriter) writeTextDelta(result openCodeLocalResult, delta string) error {
	chunk := openCodeLocalChatTextChunk(result, w.options.Model, w.options.ServiceTier, delta)
	return w.writeChatChunk(chunk)
}

func (w *openCodeLocalStreamWriter) writeReasoningDelta(result openCodeLocalResult, delta string) error {
	chunk := openCodeLocalChatReasoningChunk(result, w.options.Model, w.options.ServiceTier, delta)
	return w.writeChatChunk(chunk)
}

func (w *openCodeLocalStreamWriter) writeKeepalive() error {
	if _, err := io.WriteString(w.c.Writer, ":\n\n"); err != nil {
		return err
	}
	w.c.Writer.Flush()
	w.lastWriteAt = time.Now()
	return nil
}

func (w *openCodeLocalStreamWriter) finish(result openCodeLocalResult, options openCodeLocalStreamOptions) error {
	finishChunk := openCodeLocalChatFinishChunk(result, options.Model, options.ServiceTier)
	usageChunk := openCodeLocalChatUsageChunk(result, options.Model, options.ServiceTier)
	if err := w.writeChatChunk(finishChunk); err != nil {
		return err
	}
	if err := w.writeChatChunk(usageChunk); err != nil {
		return err
	}
	if options.Protocol == openCodeLocalStreamProtocolResponses {
		for _, event := range apicompat.FinalizeChatCompletionsResponsesStream(w.responsesState) {
			if err := w.writeResponsesEvent(event); err != nil {
				return err
			}
		}
	}
	_, _ = io.WriteString(w.c.Writer, "data: [DONE]\n\n")
	w.c.Writer.Flush()
	return nil
}

func (w *openCodeLocalStreamWriter) writeChatChunk(chunk apicompat.ChatCompletionsChunk) error {
	if w.options.Protocol == openCodeLocalStreamProtocolResponses {
		for _, event := range apicompat.ChatCompletionsChunkToResponsesEvents(&chunk, w.responsesState) {
			if err := w.writeResponsesEvent(event); err != nil {
				return err
			}
		}
		w.c.Writer.Flush()
		w.lastWriteAt = time.Now()
		return nil
	}
	sse, err := apicompat.ChatChunkToSSE(chunk)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w.c.Writer, sse); err != nil {
		return err
	}
	w.c.Writer.Flush()
	w.lastWriteAt = time.Now()
	return nil
}

func (w *openCodeLocalStreamWriter) writeResponsesEvent(event apicompat.ResponsesStreamEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w.c.Writer, "data: %s\n\n", data); err != nil {
		return err
	}
	return nil
}

func openCodeLocalChatRoleChunk(result openCodeLocalResult, model string, serviceTier *string) apicompat.ChatCompletionsChunk {
	chunk := apicompat.ChatCompletionsChunk{
		ID:      result.ID,
		Object:  "chat.completion.chunk",
		Created: result.Created,
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{{
			Index:        0,
			Delta:        apicompat.ChatDelta{Role: "assistant"},
			FinishReason: nil,
		}},
	}
	if serviceTier != nil {
		chunk.ServiceTier = *serviceTier
	}
	return chunk
}

func openCodeLocalChatTextChunk(result openCodeLocalResult, model string, serviceTier *string, text string) apicompat.ChatCompletionsChunk {
	chunk := apicompat.ChatCompletionsChunk{
		ID:      result.ID,
		Object:  "chat.completion.chunk",
		Created: result.Created,
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{{
			Index:        0,
			Delta:        apicompat.ChatDelta{Content: &text},
			FinishReason: nil,
		}},
	}
	if serviceTier != nil {
		chunk.ServiceTier = *serviceTier
	}
	return chunk
}

func openCodeLocalChatReasoningChunk(result openCodeLocalResult, model string, serviceTier *string, text string) apicompat.ChatCompletionsChunk {
	chunk := apicompat.ChatCompletionsChunk{
		ID:      result.ID,
		Object:  "chat.completion.chunk",
		Created: result.Created,
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{{
			Index:        0,
			Delta:        apicompat.ChatDelta{ReasoningContent: &text},
			FinishReason: nil,
		}},
	}
	if serviceTier != nil {
		chunk.ServiceTier = *serviceTier
	}
	return chunk
}

func openCodeLocalChatFinishChunk(result openCodeLocalResult, model string, serviceTier *string) apicompat.ChatCompletionsChunk {
	finish := result.FinishReason
	if finish == "" {
		finish = "stop"
	}
	chunk := apicompat.ChatCompletionsChunk{
		ID:      result.ID,
		Object:  "chat.completion.chunk",
		Created: result.Created,
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{{
			Index:        0,
			Delta:        apicompat.ChatDelta{},
			FinishReason: &finish,
		}},
	}
	if serviceTier != nil {
		chunk.ServiceTier = *serviceTier
	}
	return chunk
}

func openCodeLocalChatUsageChunk(result openCodeLocalResult, model string, serviceTier *string) apicompat.ChatCompletionsChunk {
	chunk := apicompat.ChatCompletionsChunk{
		ID:      result.ID,
		Object:  "chat.completion.chunk",
		Created: result.Created,
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{},
		Usage:   openCodeLocalChatResponse(&result, model, serviceTier).Usage,
	}
	if serviceTier != nil {
		chunk.ServiceTier = *serviceTier
	}
	return chunk
}

func (s *OpenAIGatewayService) doOpenCodeLocalJSON(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	method string,
	targetURL string,
	payload any,
) (*http.Response, error) {
	body, err := marshalOpenAIUpstreamJSON(payload)
	if err != nil {
		return nil, err
	}
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	req, err := http.NewRequestWithContext(upstreamCtx, method, targetURL, bytes.NewReader(body))
	releaseUpstreamCtx()
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		req.Header.Set("user-agent", customUA)
	}

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, openCodeLocalHTTPConcurrency(account))
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	return resp, nil
}

func openCodeLocalHTTPConcurrency(account *Account) int {
	if account == nil || account.Concurrency < 2 {
		return 2
	}
	return account.Concurrency
}

func openCodeLocalEventHTTPConcurrency(account *Account) int {
	concurrency := openCodeLocalHTTPConcurrency(account)
	if concurrency < openCodeLocalEventMinConns {
		return openCodeLocalEventMinConns
	}
	return concurrency
}

func (s *OpenAIGatewayService) openCodeLocalStreamKeepaliveInterval() time.Duration {
	if s == nil || s.cfg == nil || s.cfg.Gateway.StreamKeepaliveInterval <= 0 {
		return 0
	}
	return time.Duration(s.cfg.Gateway.StreamKeepaliveInterval) * time.Second
}

func (s *OpenAIGatewayService) handleOpenCodeLocalHTTPError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	resp *http.Response,
	body []byte,
	upstreamModel string,
) error {
	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(body))
	if upstreamMsg == "" {
		upstreamMsg = strings.TrimSpace(string(body))
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
		Kind:               "failover",
		Message:            upstreamMsg,
	})
	s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, upstreamModel)
	return &UpstreamFailoverError{
		StatusCode:             resp.StatusCode,
		ResponseBody:           openAITransportFailoverBody,
		RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
	}
}

func (s *OpenAIGatewayService) handleOpenCodeLocalModelError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	raw json.RawMessage,
	upstreamModel string,
) error {
	msg := strings.TrimSpace(gjson.GetBytes(raw, "message").String())
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	msg = sanitizeUpstreamErrorMessage(msg)
	if msg == "" {
		msg = "OpenCode local model error"
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: http.StatusBadGateway,
		Kind:               "failover",
		Message:            msg,
	})
	s.handleOpenAIAccountUpstreamError(ctx, account, http.StatusBadGateway, nil, raw, upstreamModel)
	return &UpstreamFailoverError{
		StatusCode:   http.StatusBadGateway,
		ResponseBody: openAITransportFailoverBody,
	}
}

func openCodeLocalURL(baseURL, endpoint, directory string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil {
		return "", err
	}
	parsed.Path = path.Join(strings.TrimRight(parsed.Path, "/"), strings.TrimLeft(endpoint, "/"))
	q := parsed.Query()
	if strings.TrimSpace(directory) != "" {
		q.Set("directory", strings.TrimSpace(directory))
	}
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

func openCodeLocalProviderID(account *Account) string {
	if account == nil {
		return openCodeLocalDefaultProviderID
	}
	if provider := strings.TrimSpace(account.GetCredential("opencode_provider_id")); provider != "" {
		return provider
	}
	return openCodeLocalDefaultProviderID
}

func openCodeLocalAgent(account *Account) string {
	if account == nil {
		return openCodeLocalDefaultAgent
	}
	if agent := strings.TrimSpace(account.GetCredential("opencode_agent")); agent != "" {
		return agent
	}
	return openCodeLocalDefaultAgent
}

func openCodeLocalTools(account *Account) map[string]bool {
	if openCodeLocalCredentialBool(account, "opencode_tools_enabled", true) == false {
		return cloneOpenCodeLocalTools(openCodeLocalDisabledTools)
	}

	tools := cloneOpenCodeLocalTools(openCodeLocalDefaultTools)
	preset := ""
	if account != nil {
		preset = account.GetCredential("opencode_tool_preset")
	}
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "", "readonly", "read_only", "safe":
	case "none", "disabled", "off", "false":
		return cloneOpenCodeLocalTools(openCodeLocalDisabledTools)
	case "shell", "bash":
		tools["bash"] = true
	case "all", "full":
		for name := range tools {
			tools[name] = true
		}
	}

	applyOpenCodeLocalToolOverrides(tools, openCodeLocalRawCredential(account, "opencode_tools"))
	return tools
}

func openCodeLocalPermissions(account *Account) []map[string]string {
	tools := openCodeLocalTools(account)
	permissions := make([]map[string]string, 0, len(tools))
	for _, name := range sortedOpenCodeLocalToolNames(tools) {
		action := "deny"
		if tools[name] {
			action = "allow"
		}
		permissions = append(permissions, map[string]string{
			"permission": name,
			"pattern":    "*",
			"action":     action,
		})
	}
	return permissions
}

func sortedOpenCodeLocalToolNames(tools map[string]bool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func cloneOpenCodeLocalTools(input map[string]bool) map[string]bool {
	out := make(map[string]bool, len(input))
	for name, enabled := range input {
		out[name] = enabled
	}
	return out
}

func applyOpenCodeLocalToolOverrides(tools map[string]bool, raw any) {
	switch value := raw.(type) {
	case nil:
		return
	case map[string]any:
		for name, enabled := range value {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if parsed, ok := openCodeLocalBoolValue(enabled); ok {
				tools[name] = parsed
			}
		}
	case map[string]bool:
		for name, enabled := range value {
			name = strings.TrimSpace(name)
			if name != "" {
				tools[name] = enabled
			}
		}
	case []any:
		for _, item := range value {
			name, ok := item.(string)
			if ok && strings.TrimSpace(name) != "" {
				tools[strings.TrimSpace(name)] = true
			}
		}
	case []string:
		for _, name := range value {
			if strings.TrimSpace(name) != "" {
				tools[strings.TrimSpace(name)] = true
			}
		}
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		var decoded any
		if json.Unmarshal([]byte(trimmed), &decoded) == nil {
			applyOpenCodeLocalToolOverrides(tools, decoded)
			return
		}
		for _, name := range strings.FieldsFunc(trimmed, func(r rune) bool {
			return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
		}) {
			if strings.TrimSpace(name) != "" {
				tools[strings.TrimSpace(name)] = true
			}
		}
	}
}

func openCodeLocalCredentialBool(account *Account, key string, fallback bool) bool {
	if parsed, ok := openCodeLocalBoolValue(openCodeLocalRawCredential(account, key)); ok {
		return parsed
	}
	return fallback
}

func openCodeLocalRawCredential(account *Account, key string) any {
	if account == nil || account.Credentials == nil {
		return nil
	}
	return account.Credentials[key]
}

func openCodeLocalBoolValue(raw any) (bool, bool) {
	switch value := raw.(type) {
	case bool:
		return value, true
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "y", "on", "allow", "enabled":
			return true, true
		case "0", "false", "no", "n", "off", "deny", "disabled":
			return false, true
		}
	case json.Number:
		if i, err := value.Int64(); err == nil {
			return i != 0, true
		}
	case float64:
		return value != 0, true
	case int:
		return value != 0, true
	case int64:
		return value != 0, true
	}
	return false, false
}

func openCodeLocalPromptFromChatRequest(req *apicompat.ChatCompletionsRequest) string {
	if req == nil {
		return ""
	}
	var builder strings.Builder
	if instructions := strings.TrimSpace(req.Instructions); instructions != "" {
		builder.WriteString("Instructions:\n")
		builder.WriteString(instructions)
		builder.WriteString("\n\n")
	}
	for _, msg := range req.Messages {
		text := openCodeLocalTextFromRawContent(msg.Content)
		if text == "" && msg.FunctionCall != nil {
			text = msg.FunctionCall.Name + "(" + msg.FunctionCall.Arguments + ")"
		}
		if text == "" && len(msg.ToolCalls) > 0 {
			var calls []string
			for _, call := range msg.ToolCalls {
				calls = append(calls, call.Function.Name+"("+call.Function.Arguments+")")
			}
			text = strings.Join(calls, "\n")
		}
		if text == "" {
			continue
		}
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		builder.WriteString(strings.ToUpper(role[:1]))
		if len(role) > 1 {
			builder.WriteString(role[1:])
		}
		builder.WriteString(":\n")
		builder.WriteString(text)
		builder.WriteString("\n\n")
	}
	return strings.TrimSpace(builder.String())
}

func openCodeLocalPromptFromAnthropicRequest(req *apicompat.AnthropicRequest) string {
	if req == nil {
		return ""
	}
	var builder strings.Builder
	if system := openCodeLocalTextFromRawContent(req.System); system != "" {
		builder.WriteString("System:\n")
		builder.WriteString(system)
		builder.WriteString("\n\n")
	}
	for _, msg := range req.Messages {
		text := openCodeLocalTextFromAnthropicContent(msg.Content)
		if text == "" {
			continue
		}
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		builder.WriteString(strings.ToUpper(role[:1]))
		if len(role) > 1 {
			builder.WriteString(role[1:])
		}
		builder.WriteString(":\n")
		builder.WriteString(text)
		builder.WriteString("\n\n")
	}
	return strings.TrimSpace(builder.String())
}

func openCodeLocalTextFromRawContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	switch gjson.ParseBytes(raw).Type {
	case gjson.String:
		return gjson.ParseBytes(raw).String()
	case gjson.JSON:
		content := gjson.ParseBytes(raw)
		if !content.IsArray() {
			return ""
		}
		var builder strings.Builder
		content.ForEach(func(_, part gjson.Result) bool {
			switch part.Get("type").String() {
			case "text", "input_text", "output_text", "":
				if text := part.Get("text").String(); text != "" {
					if builder.Len() > 0 {
						builder.WriteByte('\n')
					}
					builder.WriteString(text)
				}
			case "image_url", "input_image":
				if builder.Len() > 0 {
					builder.WriteByte('\n')
				}
				builder.WriteString("[image omitted]")
			}
			return true
		})
		return strings.TrimSpace(builder.String())
	default:
		return ""
	}
}

func openCodeLocalTextFromAnthropicContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	switch gjson.ParseBytes(raw).Type {
	case gjson.String:
		return gjson.ParseBytes(raw).String()
	case gjson.JSON:
		content := gjson.ParseBytes(raw)
		if !content.IsArray() {
			return ""
		}
		var builder strings.Builder
		content.ForEach(func(_, part gjson.Result) bool {
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			switch part.Get("type").String() {
			case "text", "":
				builder.WriteString(part.Get("text").String())
			case "tool_result":
				if text := openCodeLocalTextFromRawContent(json.RawMessage(part.Get("content").Raw)); text != "" {
					builder.WriteString(text)
				} else {
					builder.WriteString("[tool result omitted]")
				}
			case "tool_use":
				name := part.Get("name").String()
				if name == "" {
					name = "tool_use"
				}
				input := strings.TrimSpace(part.Get("input").Raw)
				builder.WriteString(name)
				if input != "" {
					builder.WriteByte('(')
					builder.WriteString(input)
					builder.WriteByte(')')
				}
			case "image":
				builder.WriteString("[image omitted]")
			case "thinking":
				builder.WriteString(part.Get("thinking").String())
			default:
				builder.WriteString("[" + part.Get("type").String() + " omitted]")
			}
			return true
		})
		return strings.TrimSpace(builder.String())
	default:
		return ""
	}
}

func openCodeLocalTextFromParts(parts []openCodeLocalMessagePart) string {
	var builder strings.Builder
	for _, part := range parts {
		if part.Type != "text" || strings.TrimSpace(part.Text) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(part.Text)
	}
	return builder.String()
}

func openCodeLocalChatResponse(result *openCodeLocalResult, model string, serviceTier *string) apicompat.ChatCompletionsResponse {
	usage := &apicompat.ChatUsage{
		PromptTokens:     result.InputTokens,
		CompletionTokens: result.OutputTokens,
		TotalTokens:      result.InputTokens + result.OutputTokens,
	}
	if result.CacheRead > 0 || result.CacheWrite > 0 {
		usage.PromptTokensDetails = &apicompat.ChatTokenDetails{CachedTokens: result.CacheRead}
	}
	if result.Reasoning > 0 {
		usage.CompletionTokensDetails = &apicompat.ChatTokenDetails{ReasoningTokens: result.Reasoning}
	}
	resp := apicompat.ChatCompletionsResponse{
		ID:      result.ID,
		Object:  "chat.completion",
		Created: result.Created,
		Model:   model,
		Choices: []apicompat.ChatChoice{{
			Index: 0,
			Message: apicompat.ChatMessage{
				Role:    "assistant",
				Content: json.RawMessage(strconv.Quote(result.Content)),
			},
			FinishReason: result.FinishReason,
		}},
		Usage: usage,
	}
	if serviceTier != nil {
		resp.ServiceTier = *serviceTier
	}
	return resp
}

func openCodeLocalAnthropicResponse(result *openCodeLocalResult, model string) apicompat.AnthropicResponse {
	return apicompat.AnthropicResponse{
		ID:   result.ID,
		Type: "message",
		Role: "assistant",
		Content: []apicompat.AnthropicContentBlock{{
			Type: "text",
			Text: result.Content,
		}},
		Model:        model,
		StopReason:   openCodeLocalAnthropicStopReason(result.FinishReason),
		StopSequence: nil,
		Usage: apicompat.AnthropicUsage{
			InputTokens:              result.InputTokens,
			OutputTokens:             result.OutputTokens,
			CacheCreationInputTokens: result.CacheWrite,
			CacheReadInputTokens:     result.CacheRead,
		},
	}
}

func (s *OpenAIGatewayService) writeOpenCodeLocalChatJSON(c *gin.Context, result *openCodeLocalResult, model string, serviceTier *string) error {
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), http.Header{}, s.responseHeaderFilter)
	}
	c.JSON(http.StatusOK, openCodeLocalChatResponse(result, model, serviceTier))
	return nil
}

func (s *OpenAIGatewayService) writeOpenCodeLocalAnthropicJSON(c *gin.Context, result *openCodeLocalResult, model string) error {
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), http.Header{}, s.responseHeaderFilter)
	}
	c.JSON(http.StatusOK, openCodeLocalAnthropicResponse(result, model))
	return nil
}

func (s *OpenAIGatewayService) writeOpenCodeLocalAnthropicStream(c *gin.Context, result *openCodeLocalResult, model string) error {
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), http.Header{}, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	index := 0
	events := []apicompat.AnthropicStreamEvent{
		{
			Type:    "message_start",
			Message: openCodeLocalAnthropicResponsePtr(result, model, ""),
		},
		{
			Type:  "content_block_start",
			Index: &index,
			ContentBlock: &apicompat.AnthropicContentBlock{
				Type: "text",
				Text: "",
			},
		},
		{
			Type:  "content_block_delta",
			Index: &index,
			Delta: &apicompat.AnthropicDelta{
				Type: "text_delta",
				Text: result.Content,
			},
		},
		{
			Type:  "content_block_stop",
			Index: &index,
		},
		{
			Type: "message_delta",
			Delta: &apicompat.AnthropicDelta{
				StopReason: openCodeLocalAnthropicStopReason(result.FinishReason),
			},
			Usage: &apicompat.AnthropicUsage{
				OutputTokens: result.OutputTokens,
			},
		},
		{Type: "message_stop"},
	}
	for _, event := range events {
		sse, err := apicompat.ResponsesAnthropicEventToSSE(event)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(c.Writer, sse); err != nil {
			return err
		}
	}
	c.Writer.Flush()
	return nil
}

func openCodeLocalAnthropicResponsePtr(result *openCodeLocalResult, model string, content string) *apicompat.AnthropicResponse {
	resp := openCodeLocalAnthropicResponse(result, model)
	resp.Content = []apicompat.AnthropicContentBlock{}
	resp.Usage.OutputTokens = 0
	if content != "" {
		resp.Content = []apicompat.AnthropicContentBlock{{Type: "text", Text: content}}
		resp.Usage.OutputTokens = result.OutputTokens
	}
	return &resp
}

func (s *OpenAIGatewayService) writeOpenCodeLocalChatStream(c *gin.Context, result *openCodeLocalResult, model string, serviceTier *string) error {
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), http.Header{}, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	roleChunk := apicompat.ChatCompletionsChunk{
		ID:      result.ID,
		Object:  "chat.completion.chunk",
		Created: result.Created,
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{{
			Index:        0,
			Delta:        apicompat.ChatDelta{Role: "assistant"},
			FinishReason: nil,
		}},
	}
	if serviceTier != nil {
		roleChunk.ServiceTier = *serviceTier
	}
	text := result.Content
	textChunk := apicompat.ChatCompletionsChunk{
		ID:      result.ID,
		Object:  "chat.completion.chunk",
		Created: result.Created,
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{{
			Index:        0,
			Delta:        apicompat.ChatDelta{Content: &text},
			FinishReason: nil,
		}},
	}
	if serviceTier != nil {
		textChunk.ServiceTier = *serviceTier
	}
	finish := result.FinishReason
	finishChunk := apicompat.ChatCompletionsChunk{
		ID:      result.ID,
		Object:  "chat.completion.chunk",
		Created: result.Created,
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{{
			Index:        0,
			Delta:        apicompat.ChatDelta{},
			FinishReason: &finish,
		}},
	}
	usageChunk := apicompat.ChatCompletionsChunk{
		ID:      result.ID,
		Object:  "chat.completion.chunk",
		Created: result.Created,
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{},
		Usage:   openCodeLocalChatResponse(result, model, serviceTier).Usage,
	}
	if serviceTier != nil {
		finishChunk.ServiceTier = *serviceTier
		usageChunk.ServiceTier = *serviceTier
	}

	for _, chunk := range []apicompat.ChatCompletionsChunk{roleChunk, textChunk, finishChunk, usageChunk} {
		sse, err := apicompat.ChatChunkToSSE(chunk)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(c.Writer, sse); err != nil {
			return err
		}
	}
	_, _ = io.WriteString(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
	return nil
}

func writeOpenCodeLocalResponsesStream(c *gin.Context, ccResp apicompat.ChatCompletionsResponse, result *openCodeLocalResult, model string) error {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	state := apicompat.NewChatCompletionsToResponsesStreamState(model)
	text := result.Content
	finish := result.FinishReason
	chunks := []apicompat.ChatCompletionsChunk{
		{
			ID:      ccResp.ID,
			Object:  "chat.completion.chunk",
			Created: ccResp.Created,
			Model:   model,
			Choices: []apicompat.ChatChunkChoice{{
				Index:        0,
				Delta:        apicompat.ChatDelta{Role: "assistant"},
				FinishReason: nil,
			}},
		},
		{
			ID:      ccResp.ID,
			Object:  "chat.completion.chunk",
			Created: ccResp.Created,
			Model:   model,
			Choices: []apicompat.ChatChunkChoice{{
				Index:        0,
				Delta:        apicompat.ChatDelta{Content: &text},
				FinishReason: nil,
			}},
		},
		{
			ID:      ccResp.ID,
			Object:  "chat.completion.chunk",
			Created: ccResp.Created,
			Model:   model,
			Choices: []apicompat.ChatChunkChoice{{
				Index:        0,
				Delta:        apicompat.ChatDelta{},
				FinishReason: &finish,
			}},
			Usage: ccResp.Usage,
		},
	}
	for _, chunk := range chunks {
		for _, event := range apicompat.ChatCompletionsChunkToResponsesEvents(&chunk, state) {
			data, err := json.Marshal(event)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
				return err
			}
		}
	}
	for _, event := range apicompat.FinalizeChatCompletionsResponsesStream(state) {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
			return err
		}
	}
	_, _ = io.WriteString(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
	return nil
}

func openCodeLocalFinishReason(finish string) string {
	switch strings.ToLower(strings.TrimSpace(finish)) {
	case "", "stop", "end_turn":
		return "stop"
	case "length", "max_tokens":
		return "length"
	case "tool_calls":
		return "tool_calls"
	case "content_filter":
		return "content_filter"
	default:
		return "stop"
	}
}

func openCodeLocalAnthropicStopReason(finish string) string {
	switch strings.ToLower(strings.TrimSpace(finish)) {
	case "", "stop", "end_turn":
		return "end_turn"
	case "length", "max_tokens":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

func openCodeLocalUnixSeconds(ms int64) int64 {
	if ms <= 0 {
		return 0
	}
	if ms > 1_000_000_000_000 {
		return ms / 1000
	}
	return ms
}

func openCodeLocalTokenInt(v float64) int {
	if v <= 0 {
		return 0
	}
	return int(v + 0.5)
}
