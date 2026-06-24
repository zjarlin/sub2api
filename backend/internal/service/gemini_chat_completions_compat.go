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
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
)

// ForwardAsGeminiChatCompletions accepts an OpenAI Chat Completions request,
// converts it through the existing Anthropic-compatible shape into Gemini's
// generateContent format, forwards it to a Gemini account, and writes an
// OpenAI Chat Completions response.
func (s *GatewayService) ForwardAsGeminiChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*ForwardResult, error) {
	startTime := time.Now()

	var ccReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &ccReq); err != nil {
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := strings.TrimSpace(ccReq.Model)
	if originalModel == "" {
		writeGatewayCCError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model")
	}

	anthropicBody, err := chatCompletionsBodyToAnthropicBody(body)
	if err != nil {
		writeGatewayCCError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}

	geminiBody, err := convertClaudeMessagesToGeminiGenerateContent(anthropicBody)
	if err != nil {
		writeGatewayCCError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	geminiBody = ensureGeminiFunctionCallThoughtSignatures(geminiBody)

	mappedModel := resolveGeminiForwardModel(account, originalModel)
	SetOpsModelDiagnostics(c, originalModel, mappedModel)

	resp, requestIDHeader, upstreamStream, err := s.forwardGeminiOpenAICompatUpstream(ctx, c, account, mappedModel, ccReq.Stream, geminiBody)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
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
		writeGatewayCCError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
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
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		evBody := unwrapIfNeeded(isOAuth, respBody)
		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(evBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		if upstreamMsg == "" {
			upstreamMsg = http.StatusText(resp.StatusCode)
		}
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  requestID,
			Kind:               "http_error",
			Message:            upstreamMsg,
		})
		if shouldFailoverGeminiStatus(resp.StatusCode) {
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: evBody}
		}
		writeGatewayCCError(c, mapUpstreamStatusCode(resp.StatusCode), "upstream_error", upstreamMsg)
		return nil, fmt.Errorf("gemini upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	var usage *ClaudeUsage
	var firstTokenMs *int
	if ccReq.Stream {
		streamResult, err := s.handleGeminiChatCompletionsStreamingResponse(c, resp, startTime, originalModel, mappedModel, isOAuth, ccReq.StreamOptions != nil && ccReq.StreamOptions.IncludeUsage)
		if err != nil {
			return nil, err
		}
		usage = streamResult.usage
		firstTokenMs = streamResult.firstTokenMs
	} else {
		var collected map[string]any
		if upstreamStream {
			collected, usage, err = collectGeminiSSE(resp.Body, isOAuth)
			if err != nil {
				writeGatewayCCError(c, http.StatusBadGateway, "upstream_error", "Failed to read upstream stream")
				return nil, err
			}
		} else {
			collected, usage, err = readGeminiOpenAICompatNonStream(resp.Body, isOAuth)
			if err != nil {
				writeGatewayCCError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
				return nil, err
			}
		}
		s.writeGeminiChatCompletionsBufferedResponse(c, resp.Header, collected, originalModel)
	}
	if usage == nil {
		usage = &ClaudeUsage{}
	}

	return &ForwardResult{
		RequestID:     requestID,
		Usage:         *usage,
		Model:         originalModel,
		UpstreamModel: mappedModel,
		Stream:        ccReq.Stream,
		Duration:      time.Since(startTime),
		FirstTokenMs:  firstTokenMs,
	}, nil
}

func chatCompletionsBodyToAnthropicBody(body []byte) ([]byte, error) {
	var ccReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &ccReq); err != nil {
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	responsesReq, err := apicompat.ChatCompletionsToResponses(&ccReq)
	if err != nil {
		return nil, fmt.Errorf("convert chat completions to responses: %w", err)
	}
	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}
	anthropicReq.Model = ccReq.Model
	anthropicReq.Stream = ccReq.Stream
	return json.Marshal(anthropicReq)
}

func (s *GatewayService) forwardGeminiOpenAICompatUpstream(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	mappedModel string,
	clientStream bool,
	body []byte,
) (*http.Response, string, bool, error) {
	if account == nil {
		return nil, "", false, errors.New("missing account")
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	useUpstreamStream := clientStream
	if account.Type == AccountTypeOAuth && !clientStream && strings.TrimSpace(account.GetCredential("project_id")) != "" {
		useUpstreamStream = true
	}
	action := "generateContent"
	if useUpstreamStream {
		action = "streamGenerateContent"
	}

	var upstreamReq *http.Request
	var requestIDHeader string
	var err error

	switch account.Type {
	case AccountTypeAPIKey:
		apiKey := strings.TrimSpace(account.GetCredential("api_key"))
		if apiKey == "" {
			return nil, "", false, errors.New("gemini api_key not configured")
		}
		baseURL := account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
		normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return nil, "", false, err
		}
		fullURL := fmt.Sprintf("%s/v1beta/models/%s:%s", strings.TrimRight(normalizedBaseURL, "/"), mappedModel, action)
		if useUpstreamStream {
			fullURL += "?alt=sse"
		}
		restBody := normalizeGeminiRequestForAIStudio(body)
		upstreamReq, err = http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(restBody))
		if err != nil {
			return nil, "", false, err
		}
		upstreamReq.Header.Set("Content-Type", "application/json")
		upstreamReq.Header.Set("x-goog-api-key", apiKey)
		requestIDHeader = "x-request-id"

	case AccountTypeOAuth:
		token, err := s.getGeminiOpenAICompatAccessToken(ctx, account)
		if err != nil {
			return nil, "", false, err
		}
		projectID := strings.TrimSpace(account.GetCredential("project_id"))
		if projectID != "" {
			baseURL, err := s.validateUpstreamBaseURL(geminicli.GeminiCliBaseURL)
			if err != nil {
				return nil, "", false, err
			}
			fullURL := fmt.Sprintf("%s/v1internal:%s", strings.TrimRight(baseURL, "/"), action)
			if useUpstreamStream {
				fullURL += "?alt=sse"
			}
			var inner any
			if err := json.Unmarshal(body, &inner); err != nil {
				return nil, "", false, fmt.Errorf("parse gemini request: %w", err)
			}
			wrappedBytes, _ := json.Marshal(map[string]any{
				"model":   mappedModel,
				"project": projectID,
				"request": inner,
			})
			upstreamReq, err = http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(wrappedBytes))
			if err != nil {
				return nil, "", false, err
			}
			upstreamReq.Header.Set("Content-Type", "application/json")
			upstreamReq.Header.Set("Authorization", "Bearer "+token)
			upstreamReq.Header.Set("User-Agent", geminicli.GeminiCLIUserAgent)
		} else {
			baseURL := account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
			normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
			if err != nil {
				return nil, "", false, err
			}
			fullURL := fmt.Sprintf("%s/v1beta/models/%s:%s", strings.TrimRight(normalizedBaseURL, "/"), mappedModel, action)
			if useUpstreamStream {
				fullURL += "?alt=sse"
			}
			restBody := normalizeGeminiRequestForAIStudio(body)
			upstreamReq, err = http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(restBody))
			if err != nil {
				return nil, "", false, err
			}
			upstreamReq.Header.Set("Content-Type", "application/json")
			upstreamReq.Header.Set("Authorization", "Bearer "+token)
		}
		requestIDHeader = "x-request-id"

	case AccountTypeServiceAccount:
		token, err := s.getGeminiOpenAICompatAccessToken(ctx, account)
		if err != nil {
			return nil, "", false, err
		}
		fullURL, err := buildVertexGeminiURL(account.VertexProjectID(), account.VertexLocation(mappedModel), mappedModel, action, useUpstreamStream)
		if err != nil {
			return nil, "", false, err
		}
		restBody := normalizeGeminiRequestForAIStudio(body)
		upstreamReq, err = http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(restBody))
		if err != nil {
			return nil, "", false, err
		}
		upstreamReq.Header.Set("Content-Type", "application/json")
		upstreamReq.Header.Set("Authorization", "Bearer "+token)
		requestIDHeader = "x-request-id"

	default:
		return nil, "", false, fmt.Errorf("unsupported account type: %s", account.Type)
	}

	if c != nil {
		c.Set(OpsUpstreamRequestBodyKey, string(body))
	}
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	return resp, requestIDHeader, useUpstreamStream, err
}

func (s *GatewayService) getGeminiOpenAICompatAccessToken(ctx context.Context, account *Account) (string, error) {
	if s.geminiTokenProvider != nil {
		return s.geminiTokenProvider.GetAccessToken(ctx, account)
	}
	token := strings.TrimSpace(account.GetCredential("access_token"))
	if token == "" {
		return "", errors.New("gemini token provider not configured")
	}
	return token, nil
}

func readGeminiOpenAICompatNonStream(body io.Reader, isOAuth bool) (map[string]any, *ClaudeUsage, error) {
	respBody, err := io.ReadAll(io.LimitReader(body, 8<<20))
	if err != nil {
		return nil, nil, err
	}
	if isOAuth {
		if unwrapped, err := unwrapGeminiResponse(respBody); err == nil {
			respBody = unwrapped
		}
	}
	var parsed map[string]any
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, nil, err
	}
	usage := extractGeminiUsage(respBody)
	if usage == nil {
		usage = &ClaudeUsage{}
	}
	return parsed, usage, nil
}

func (s *GatewayService) writeGeminiChatCompletionsBufferedResponse(c *gin.Context, headers http.Header, geminiResp map[string]any, originalModel string) {
	claudeResp, _ := convertGeminiToClaudeMessage(geminiResp, originalModel, mustMarshalMap(geminiResp))
	anthropicResp := anthropicResponseFromMap(claudeResp)
	ccResp := apicompat.ResponsesToChatCompletions(apicompat.AnthropicToResponsesResponse(anthropicResp), originalModel)
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), headers, s.responseHeaderFilter)
	}
	c.JSON(http.StatusOK, ccResp)
}

type geminiChatCompletionsStreamResult struct {
	usage        *ClaudeUsage
	firstTokenMs *int
}

func (s *GatewayService) handleGeminiChatCompletionsStreamingResponse(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	mappedModel string,
	isOAuth bool,
	includeUsage bool,
) (*geminiChatCompletionsStreamResult, error) {
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

	state := apicompat.NewResponsesEventToChatState()
	state.Model = originalModel
	state.IncludeUsage = includeUsage
	usage := &ClaudeUsage{}
	var firstTokenMs *int
	var sawContent bool
	var seenText string

	writeChunk := func(chunk apicompat.ChatCompletionsChunk) error {
		sse, err := apicompat.ChatChunkToSSE(chunk)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprint(c.Writer, sse); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	processGeminiResponse := func(geminiResp map[string]any, raw []byte) error {
		if u := extractGeminiUsage(raw); u != nil {
			usage = u
		}
		if firstTokenMs == nil {
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}
		parts := extractGeminiParts(geminiResp)
		for _, part := range parts {
			if text, ok := part["text"].(string); ok && text != "" {
				delta, newSeen := computeGeminiTextDelta(seenText, text)
				seenText = newSeen
				if delta == "" {
					continue
				}
				if !sawContent {
					for _, chunk := range apicompat.ResponsesEventToChatChunks(&apicompat.ResponsesStreamEvent{
						Type:     "response.created",
						Response: &apicompat.ResponsesResponse{ID: "resp_" + randomHex(12), Object: "response", Model: originalModel, Status: "in_progress"},
					}, state) {
						if err := writeChunk(chunk); err != nil {
							return err
						}
					}
					for _, chunk := range apicompat.ResponsesEventToChatChunks(&apicompat.ResponsesStreamEvent{
						Type: "response.output_item.added",
						Item: &apicompat.ResponsesOutput{
							Type:    "message",
							ID:      "msg_" + randomHex(12),
							Role:    "assistant",
							Content: []apicompat.ResponsesContentPart{{Type: "output_text", Text: ""}},
							Status:  "in_progress",
						},
					}, state) {
						if err := writeChunk(chunk); err != nil {
							return err
						}
					}
					sawContent = true
				}
				for _, chunk := range apicompat.ResponsesEventToChatChunks(&apicompat.ResponsesStreamEvent{
					Type:  "response.output_text.delta",
					Delta: delta,
				}, state) {
					if err := writeChunk(chunk); err != nil {
						return err
					}
				}
			}
			if fc, ok := part["functionCall"].(map[string]any); ok && fc != nil {
				if !sawContent {
					for _, chunk := range apicompat.ResponsesEventToChatChunks(&apicompat.ResponsesStreamEvent{
						Type:     "response.created",
						Response: &apicompat.ResponsesResponse{ID: "resp_" + randomHex(12), Object: "response", Model: originalModel, Status: "in_progress"},
					}, state) {
						if err := writeChunk(chunk); err != nil {
							return err
						}
					}
					sawContent = true
				}
				name, _ := fc["name"].(string)
				if strings.TrimSpace(name) == "" {
					name = "tool"
				}
				args := "{}"
				if rawArgs, err := json.Marshal(fc["args"]); err == nil && len(rawArgs) > 0 && string(rawArgs) != "null" {
					args = string(rawArgs)
				}
				for _, evt := range []apicompat.ResponsesStreamEvent{
					{Type: "response.output_item.added", Item: &apicompat.ResponsesOutput{Type: "function_call", CallID: "call_" + randomHex(8), Name: name, Arguments: ""}},
					{Type: "response.function_call_arguments.delta", Arguments: args},
				} {
					for _, chunk := range apicompat.ResponsesEventToChatChunks(&evt, state) {
						if err := writeChunk(chunk); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(trimmed, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if payload != "" && payload != "[DONE]" {
					raw := []byte(payload)
					if isOAuth {
						if unwrapped, err := unwrapGeminiResponse(raw); err == nil {
							raw = unwrapped
						}
					}
					var parsed map[string]any
					if json.Unmarshal(raw, &parsed) == nil {
						if err := processGeminiResponse(parsed, raw); err != nil {
							return nil, err
						}
					}
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}

	if !sawContent {
		for _, chunk := range apicompat.ResponsesEventToChatChunks(&apicompat.ResponsesStreamEvent{
			Type:     "response.created",
			Response: &apicompat.ResponsesResponse{ID: "resp_" + randomHex(12), Object: "response", Model: originalModel, Status: "in_progress"},
		}, state) {
			if err := writeChunk(chunk); err != nil {
				return nil, err
			}
		}
	}
	state.Usage = chatUsageFromClaudeUsage(usage)
	for _, chunk := range apicompat.FinalizeResponsesChatStream(state) {
		if err := writeChunk(chunk); err != nil {
			return nil, err
		}
	}
	_, _ = io.WriteString(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()

	_ = mappedModel
	return &geminiChatCompletionsStreamResult{usage: usage, firstTokenMs: firstTokenMs}, nil
}

func mustMarshalMap(v map[string]any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func anthropicResponseFromMap(v map[string]any) *apicompat.AnthropicResponse {
	raw, _ := json.Marshal(v)
	var resp apicompat.AnthropicResponse
	_ = json.Unmarshal(raw, &resp)
	return &resp
}

func chatUsageFromClaudeUsage(usage *ClaudeUsage) *apicompat.ChatUsage {
	if usage == nil {
		usage = &ClaudeUsage{}
	}
	cached := usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	return &apicompat.ChatUsage{
		PromptTokens:     usage.InputTokens + cached,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.InputTokens + cached + usage.OutputTokens,
		PromptTokensDetails: &apicompat.ChatTokenDetails{
			CachedTokens: usage.CacheReadInputTokens,
		},
	}
}

func shouldFailoverGeminiStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	default:
		return status >= 500
	}
}
