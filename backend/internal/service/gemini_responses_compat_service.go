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

// ForwardAsResponses serves OpenAI Responses clients through Gemini accounts.
// It reuses the existing Responses -> Anthropic -> Gemini conversion chain, then
// wraps Gemini output back into OpenAI Responses format.
func (s *GeminiMessagesCompatService) ForwardAsResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*ForwardResult, error) {
	startTime := time.Now()

	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := strings.TrimSpace(responsesReq.Model)
	if originalModel == "" {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model")
	}
	clientStream := responsesReq.Stream
	reasoningEffort := ExtractResponsesReasoningEffortFromBody(body)

	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(&responsesReq)
	if err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}
	anthropicReq.Stream = clientStream
	claudeBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal responses compat request: %w", err)
	}

	mappedModel := resolveGeminiForwardModel(account, originalModel)
	SetOpsModelDiagnostics(c, originalModel, mappedModel)

	geminiReq, err := convertClaudeMessagesToGeminiGenerateContent(claudeBody)
	if err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert anthropic to gemini: %w", err)
	}
	geminiReq = ensureGeminiFunctionCallThoughtSignatures(geminiReq)

	useUpstreamStream := clientStream
	if account.Type == AccountTypeOAuth && !clientStream && strings.TrimSpace(account.GetCredential("project_id")) != "" {
		useUpstreamStream = true
	}
	buildReq, requestIDHeader := s.buildGeminiChatCompletionsUpstreamRequestFunc(
		account,
		mappedModel,
		geminiReq,
		clientStream,
		useUpstreamStream,
	)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	var resp *http.Response
	for attempt := 1; attempt <= geminiMaxRetries; attempt++ {
		upstreamReq, idHeader, err := buildReq(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			writeResponsesError(c, http.StatusBadGateway, "upstream_error", err.Error())
			return nil, fmt.Errorf("build gemini upstream request: %w", err)
		}
		requestIDHeader = idHeader

		resp, err = s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
		if err != nil {
			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: 0,
				Kind:               "request_error",
				Message:            safeErr,
			})
			if attempt < geminiMaxRetries {
				logger.LegacyPrintf("service.gemini_responses_compat", "Gemini account %d: upstream request failed, retry %d/%d: %v", account.ID, attempt, geminiMaxRetries, err)
				sleepGeminiBackoff(attempt)
				continue
			}
			setOpsUpstreamError(c, 0, safeErr, "")
			writeResponsesError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed after retries: "+safeErr)
			return nil, fmt.Errorf("upstream request failed after retries: %s", safeErr)
		}

		if matched, rebuilt := s.checkErrorPolicyInLoop(ctx, account, resp); matched {
			resp = rebuilt
			break
		} else {
			resp = rebuilt
		}

		if resp.StatusCode >= 400 && s.shouldRetryGeminiUpstreamError(account, resp.StatusCode) {
			respBody := s.readUpstreamErrorBody(resp)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusForbidden && isGeminiInsufficientScope(resp.Header, respBody) {
				resp = &http.Response{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: io.NopCloser(bytes.NewReader(respBody))}
				break
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				s.handleGeminiUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
				resp = &http.Response{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: io.NopCloser(bytes.NewReader(respBody))}
				break
			}
			if attempt < geminiMaxRetries {
				upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.Header.Get(requestIDHeader),
					Kind:               "retry",
					Message:            upstreamMsg,
				})
				logger.LegacyPrintf("service.gemini_responses_compat", "Gemini account %d: upstream status %d, retry %d/%d", account.ID, resp.StatusCode, attempt, geminiMaxRetries)
				sleepGeminiBackoff(attempt)
				continue
			}
			resp = &http.Response{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: io.NopCloser(bytes.NewReader(respBody))}
			break
		}

		break
	}
	defer func() { _ = resp.Body.Close() }()

	requestID := resp.Header.Get(requestIDHeader)
	if requestID == "" {
		requestID = resp.Header.Get("x-goog-request-id")
	}
	if requestID != "" {
		c.Header("x-request-id", requestID)
	}

	isOAuth := account.Type == AccountTypeOAuth
	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		s.handleGeminiUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
		evBody := unwrapIfNeeded(isOAuth, respBody)
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(evBody)))
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  requestID,
			Kind:               "http_error",
			Message:            upstreamMsg,
		})
		if s.shouldFailoverGeminiUpstreamError(resp.StatusCode) {
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: evBody}
		}
		writeGeminiResponsesMappedError(c, resp.StatusCode, evBody)
		return nil, fmt.Errorf("gemini upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	var usage *ClaudeUsage
	var firstTokenMs *int
	if clientStream {
		streamRes, err := s.handleResponsesStreamingResponseFromGemini(c, resp, startTime, originalModel, mappedModel, reasoningEffort, isOAuth)
		if err != nil {
			return nil, err
		}
		usage = streamRes.usage
		firstTokenMs = streamRes.firstTokenMs
	} else if useUpstreamStream {
		collected, usageObj, err := collectGeminiSSE(resp.Body, isOAuth)
		if err != nil {
			writeResponsesError(c, http.StatusBadGateway, "upstream_error", "Failed to read upstream stream")
			return nil, err
		}
		usage = usageObj
		s.writeGeminiResponsesBufferedResponse(c, resp.Header, collected, originalModel, mappedModel)
	} else {
		usageObj, err := s.handleResponsesNonStreamingResponseFromGemini(c, resp, originalModel, mappedModel, isOAuth)
		if err != nil {
			return nil, err
		}
		usage = usageObj
	}
	if usage == nil {
		usage = &ClaudeUsage{}
	}

	return &ForwardResult{
		RequestID:        requestID,
		Usage:            *usage,
		Model:            originalModel,
		UpstreamModel:    mappedModel,
		Stream:           clientStream,
		Duration:         time.Since(startTime),
		FirstTokenMs:     firstTokenMs,
		ReasoningEffort:  reasoningEffort,
		ClientDisconnect: false,
	}, nil
}

func (s *GeminiMessagesCompatService) handleResponsesNonStreamingResponseFromGemini(
	c *gin.Context,
	resp *http.Response,
	originalModel string,
	mappedModel string,
	isOAuth bool,
) (*ClaudeUsage, error) {
	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	if isOAuth {
		if unwrappedBody, uwErr := unwrapGeminiResponse(respBody); uwErr == nil {
			respBody = unwrappedBody
		}
	}

	var geminiResp map[string]any
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		writeResponsesError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
		return nil, err
	}

	s.writeGeminiResponsesBufferedResponse(c, resp.Header, geminiResp, originalModel, mappedModel)
	if u := extractGeminiUsage(respBody); u != nil {
		return u, nil
	}
	return &ClaudeUsage{}, nil
}

func (s *GeminiMessagesCompatService) writeGeminiResponsesBufferedResponse(
	c *gin.Context,
	headers http.Header,
	geminiResp map[string]any,
	originalModel string,
	mappedModel string,
) {
	claudeRespMap, _ := convertGeminiToClaudeMessage(geminiResp, originalModel, mustMarshalMap(geminiResp))
	claudeBytes, err := json.Marshal(claudeRespMap)
	if err != nil {
		writeResponsesError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
		return
	}
	var anthropicResp apicompat.AnthropicResponse
	if err := json.Unmarshal(claudeBytes, &anthropicResp); err != nil {
		writeResponsesError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
		return
	}
	responsesResp := apicompat.AnthropicToResponsesResponse(&anthropicResp)
	responsesResp.Model = originalModel
	if mappedModel != "" && mappedModel != originalModel {
		responsesResp.Model = originalModel
	}
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), headers, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.JSON(http.StatusOK, responsesResp)
}

func (s *GeminiMessagesCompatService) handleResponsesStreamingResponseFromGemini(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	isOAuth bool,
) (*geminiStreamResult, error) {
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	anthState := apicompat.NewAnthropicEventToResponsesState()
	anthState.Model = originalModel
	var usage ClaudeUsage
	var firstTokenMs *int
	firstChunk := true
	finishReason := ""
	sawToolUse := false
	nextBlockIndex := 0
	openBlockIndex := -1
	openBlockType := ""
	seenText := ""
	openToolIndex := -1
	openToolName := ""
	seenToolJSON := ""

	writeResponsesEvents := func(evt *apicompat.AnthropicStreamEvent) bool {
		events := apicompat.AnthropicEventToResponsesEvents(evt, anthState)
		for _, resEvt := range events {
			sse, err := apicompat.ResponsesEventToSSE(resEvt)
			if err != nil {
				logger.L().Warn("gemini responses stream: failed to marshal event", zap.Error(err))
				continue
			}
			if _, err := io.WriteString(c.Writer, sse); err != nil {
				return true
			}
		}
		if len(events) > 0 {
			flusher.Flush()
		}
		return false
	}

	messageID := "msg_" + randomHex(12)
	if writeResponsesEvents(&apicompat.AnthropicStreamEvent{
		Type: "message_start",
		Message: &apicompat.AnthropicResponse{
			ID:      messageID,
			Type:    "message",
			Role:    "assistant",
			Model:   originalModel,
			Content: []apicompat.AnthropicContentBlock{},
			Usage:   apicompat.AnthropicUsage{},
		},
	}) {
		return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs}, nil
	}

	closeOpenBlock := func() bool {
		if openBlockIndex < 0 {
			return false
		}
		disconnected := writeResponsesEvents(&apicompat.AnthropicStreamEvent{Type: "content_block_stop"})
		openBlockIndex = -1
		openBlockType = ""
		return disconnected
	}
	closeOpenTool := func() bool {
		if openToolIndex < 0 {
			return false
		}
		disconnected := writeResponsesEvents(&apicompat.AnthropicStreamEvent{Type: "content_block_stop"})
		openToolIndex = -1
		openToolName = ""
		seenToolJSON = ""
		return disconnected
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(trimmed, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if payload != "" && payload != "[DONE]" {
					rawBytes := []byte(payload)
					if isOAuth {
						if innerBytes, uwErr := unwrapGeminiResponse(rawBytes); uwErr == nil {
							rawBytes = innerBytes
						}
					}
					var geminiResp map[string]any
					if err := json.Unmarshal(rawBytes, &geminiResp); err == nil {
						if firstChunk {
							firstChunk = false
							ms := int(time.Since(startTime).Milliseconds())
							firstTokenMs = &ms
						}
						if fr := extractGeminiFinishReason(geminiResp); fr != "" {
							finishReason = fr
						}
						if u := extractGeminiUsage(rawBytes); u != nil {
							usage = *u
						}

						for _, part := range extractGeminiParts(geminiResp) {
							if text, ok := part["text"].(string); ok && text != "" {
								if openToolIndex >= 0 && closeOpenTool() {
									return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs}, nil
								}
								delta, newSeen := computeGeminiTextDelta(seenText, text)
								seenText = newSeen
								if delta == "" {
									continue
								}
								if openBlockType != "text" {
									if closeOpenBlock() {
										return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs}, nil
									}
									idx := nextBlockIndex
									nextBlockIndex++
									openBlockIndex = idx
									openBlockType = "text"
									if writeResponsesEvents(&apicompat.AnthropicStreamEvent{
										Type:  "content_block_start",
										Index: &idx,
										ContentBlock: &apicompat.AnthropicContentBlock{
											Type: "text",
											Text: "",
										},
									}) {
										return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs}, nil
									}
								}
								if writeResponsesEvents(&apicompat.AnthropicStreamEvent{
									Type: "content_block_delta",
									Delta: &apicompat.AnthropicDelta{
										Type: "text_delta",
										Text: delta,
									},
								}) {
									return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs}, nil
								}
								continue
							}

							if fc, ok := part["functionCall"].(map[string]any); ok && fc != nil {
								name, _ := fc["name"].(string)
								if strings.TrimSpace(name) == "" {
									name = "tool"
								}
								if closeOpenBlock() {
									return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs}, nil
								}
								if openToolIndex >= 0 && openToolName != name && closeOpenTool() {
									return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs}, nil
								}
								if openToolIndex < 0 {
									idx := nextBlockIndex
									nextBlockIndex++
									openToolIndex = idx
									openToolName = name
									sawToolUse = true
									if writeResponsesEvents(&apicompat.AnthropicStreamEvent{
										Type:  "content_block_start",
										Index: &idx,
										ContentBlock: &apicompat.AnthropicContentBlock{
											Type:  "tool_use",
											ID:    "toolu_" + randomHex(8),
											Name:  name,
											Input: json.RawMessage(`{}`),
										},
									}) {
										return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs}, nil
									}
								}

								argsJSONText := "{}"
								switch v := fc["args"].(type) {
								case nil:
								case string:
									if strings.TrimSpace(v) != "" {
										argsJSONText = v
									}
								default:
									if b, err := json.Marshal(v); err == nil && len(b) > 0 {
										argsJSONText = string(b)
									}
								}
								delta, newSeen := computeGeminiTextDelta(seenToolJSON, argsJSONText)
								seenToolJSON = newSeen
								if delta != "" {
									if writeResponsesEvents(&apicompat.AnthropicStreamEvent{
										Type: "content_block_delta",
										Delta: &apicompat.AnthropicDelta{
											Type:        "input_json_delta",
											PartialJSON: delta,
										},
									}) {
										return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs}, nil
									}
								}
							}
						}
					}
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("stream read error: %w", err)
		}
	}

	if closeOpenBlock() || closeOpenTool() {
		return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs}, nil
	}
	stopReason := mapGeminiFinishReasonToClaudeStopReason(finishReason)
	if sawToolUse {
		stopReason = "tool_use"
	}
	anthState.InputTokens = usage.InputTokens
	anthState.CacheReadInputTokens = usage.CacheReadInputTokens
	if writeResponsesEvents(&apicompat.AnthropicStreamEvent{
		Type: "message_delta",
		Delta: &apicompat.AnthropicDelta{
			Type:       "message_delta",
			StopReason: stopReason,
		},
		Usage: &apicompat.AnthropicUsage{
			InputTokens:          usage.InputTokens,
			OutputTokens:         usage.OutputTokens,
			CacheReadInputTokens: usage.CacheReadInputTokens,
		},
	}) {
		return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs}, nil
	}
	if writeResponsesEvents(&apicompat.AnthropicStreamEvent{Type: "message_stop"}) {
		return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs}, nil
	}
	for _, resEvt := range apicompat.FinalizeAnthropicResponsesStream(anthState) {
		sse, err := apicompat.ResponsesEventToSSE(resEvt)
		if err != nil {
			continue
		}
		_, _ = io.WriteString(c.Writer, sse)
	}
	flusher.Flush()
	return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs}, nil
}

func writeGeminiResponsesMappedError(c *gin.Context, upstreamStatus int, body []byte) {
	upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c,
		PlatformGemini,
		upstreamStatus,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		writeResponsesError(c, status, errType, errMsg)
		return
	}
	statusCode := http.StatusBadGateway
	errType := "upstream_error"
	errMsg := "Upstream request failed"
	if mapped := mapGeminiErrorBodyToClaudeError(body); mapped != nil {
		if mapped.Type != "" {
			errType = mapped.Type
		}
		if mapped.Message != "" {
			errMsg = mapped.Message
		}
		if mapped.StatusCode > 0 {
			statusCode = mapped.StatusCode
		}
	}
	switch upstreamStatus {
	case http.StatusBadRequest:
		if statusCode == http.StatusBadGateway {
			statusCode = http.StatusBadRequest
		}
		if errType == "upstream_error" {
			errType = "invalid_request_error"
		}
		if errMsg == "Upstream request failed" {
			errMsg = "Invalid request"
		}
	case http.StatusNotFound:
		statusCode = http.StatusNotFound
		if errType == "upstream_error" {
			errType = "not_found_error"
		}
		if errMsg == "Upstream request failed" {
			errMsg = "Resource not found"
		}
	case http.StatusTooManyRequests:
		statusCode = http.StatusTooManyRequests
		if errType == "upstream_error" {
			errType = "rate_limit_error"
		}
		if errMsg == "Upstream request failed" {
			errMsg = "Upstream rate limit exceeded, please retry later"
		}
	case 529:
		statusCode = http.StatusServiceUnavailable
		if errType == "upstream_error" {
			errType = "overloaded_error"
		}
		if errMsg == "Upstream request failed" {
			errMsg = "Upstream service overloaded, please retry later"
		}
	}
	if upstreamMsg != "" && errMsg == "Upstream request failed" {
		errMsg = upstreamMsg
	}
	writeResponsesError(c, statusCode, errType, errMsg)
}
