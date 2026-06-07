package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// ForwardEmbeddings forwards an OpenAI-compatible embeddings request without protocol conversion.
func (s *OpenAIGatewayService) ForwardEmbeddings(ctx context.Context, c *gin.Context, account *Account, body []byte, defaultMappedModel string) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	originalModel := gjson.GetBytes(body, "model").String()
	if originalModel == "" {
		WriteOpenAIClientError(c, http.StatusBadRequest, "invalid_request_error", "model is required", gin.H{"param": "model"})
		return nil, fmt.Errorf("missing model in embeddings request")
	}
	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)

	upstreamBody := body
	if upstreamModel != originalModel {
		upstreamBody = ReplaceModelInBody(body, upstreamModel)
	}

	apiKey := account.GetOpenAIApiKey()
	if apiKey == "" && !account.AllowsEmptyOpenAIApiKey() {
		return nil, fmt.Errorf("account %d missing api_key", account.ID)
	}
	baseURL := account.GetOpenAIBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}
	targetURL := buildOpenAIEmbeddingsURL(validatedURL)

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	upstreamReq, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, targetURL, bytes.NewReader(upstreamBody))
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept", "application/json")
	applyOpenAIUpstreamAuthHeaders(upstreamReq.Header, account, apiKey)
	for key, values := range c.Request.Header {
		if openaiCCRawAllowedHeaders[strings.ToLower(key)] {
			for _, value := range values {
				upstreamReq.Header.Add(key, value)
			}
		}
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
		WriteOpenAIClientError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", nil)
		return nil, fmt.Errorf("upstream embeddings request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, readErr := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if readErr != nil {
		if !c.Writer.Written() {
			WriteOpenAIClientError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response", nil)
		}
		return nil, fmt.Errorf("read embeddings upstream body: %w", readErr)
	}

	if resp.StatusCode >= 400 {
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "upstream_error",
			Message:            upstreamMsg,
		})
		if s.rateLimitService != nil {
			s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
		}
		writeOpenAIEmbeddingsResponse(c, resp, respBody)
		return nil, fmt.Errorf("embeddings upstream returned %d: %s", resp.StatusCode, upstreamMsg)
	}

	writeOpenAIEmbeddingsResponse(c, resp, respBody)
	logger.L().Debug("openai embeddings: forwarded",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
	)

	return &OpenAIForwardResult{
		RequestID:     resp.Header.Get("x-request-id"),
		Usage:         extractEmbeddingsUsage(respBody),
		Model:         originalModel,
		BillingModel:  billingModel,
		UpstreamModel: upstreamModel,
		Stream:        false,
		Duration:      time.Since(startTime),
	}, nil
}

func writeOpenAIEmbeddingsResponse(c *gin.Context, resp *http.Response, body []byte) {
	writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, nil)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, body)
}

func buildOpenAIEmbeddingsURL(base string) string {
	normalized := strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(normalized, "/embeddings") {
		return normalized
	}
	if strings.HasSuffix(normalized, "/v1") {
		return normalized + "/embeddings"
	}
	if openAIBaseURLLooksLikeAPIRoot(normalized) {
		return normalized + "/embeddings"
	}
	return normalized + "/v1/embeddings"
}

func extractEmbeddingsUsage(body []byte) OpenAIUsage {
	if !gjson.GetBytes(body, "usage").Exists() {
		return OpenAIUsage{}
	}
	return OpenAIUsage{
		InputTokens:  int(gjson.GetBytes(body, "usage.prompt_tokens").Int()),
		OutputTokens: int(gjson.GetBytes(body, "usage.completion_tokens").Int()),
	}
}
