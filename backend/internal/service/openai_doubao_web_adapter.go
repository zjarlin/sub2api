package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	doubaoWebDefaultBaseURL = "https://www.doubao.com"
	doubaoWebDefaultBotID   = "7338286299411103781"
	doubaoWebDefaultFP      = "doubao2api_default_fp"
	doubaoWebUserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	doubaoWebBrowserTimeout = 30 * time.Minute
)

type doubaoWebExecutor interface {
	Complete(ctx context.Context, req doubaoWebRequest) (*doubaoWebResult, error)
}

type doubaoWebRequest struct {
	BaseURL      string
	SessionID    string
	Model        string
	DefaultBotID string
	Prompt       string
	PreviousID   string
	AccountID    int64
}

type doubaoWebResult struct {
	ID                 string
	SessionID          string
	Content            string
	Deltas             []string
	FinishReason       string
	InputTokens        int
	OutputTokens       int
	Created            int64
	UpstreamModel      string
	UpstreamStatusCode int
	UpstreamBody       []byte
}

type doubaoWebBrowserFetchResponse struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	FetchHook string `json:"fetch_hook,omitempty"`
	BodyLen   int    `json:"body_len,omitempty"`
}

type doubaoWebSession struct {
	SessionID           string
	ConversationID      string
	LocalConversationID string
	SectionID           string
	LastMessageIndex    *int
	BotID               string
	TurnCount           int
}

type doubaoWebSessionMeta struct {
	ConversationID   string
	SectionID        string
	LastMessageIndex *int
}

type doubaoWebParsedSSE struct {
	OutputText  string
	Deltas      []string
	SessionMeta doubaoWebSessionMeta
	Error       string
}

type doubaoWebContentBlock struct {
	BlockType int64
	Text      string
	Images    []string
}

type doubaoWebSSEEvent struct {
	Type string
	Data any
}

type doubaoWebBrowserExecutor struct {
	mu       sync.Mutex
	sessions map[string]*doubaoWebSession
	workers  map[string]*doubaoWebBrowserWorker
}

type doubaoWebBrowserWorker struct {
	mu               sync.Mutex
	allocCancel      context.CancelFunc
	ctx              context.Context
	cancel           context.CancelFunc
	baseURL          string
	chatURL          string
	cookieDomain     string
	currentSessionID string
}

var globalDoubaoWebExecutor doubaoWebExecutor = newDoubaoWebBrowserExecutor()

func newDoubaoWebBrowserExecutor() *doubaoWebBrowserExecutor {
	return &doubaoWebBrowserExecutor{
		sessions: make(map[string]*doubaoWebSession),
		workers:  make(map[string]*doubaoWebBrowserWorker),
	}
}

func accountUsesDoubaoWebReverse(account *Account) bool {
	if account == nil || !account.IsOpenAIApiKey() {
		return false
	}
	vendor := strings.ToLower(strings.TrimSpace(account.GetOpenAIVendor()))
	return vendor == "doubao" || vendor == "doubao-web"
}

func (s *OpenAIGatewayService) forwardAsDoubaoWebChatCompletions(
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
	upstreamModel := doubaoWebNormalizeModel(normalizeOpenAIModelForUpstream(account, billingModel))
	if upstreamModel == "" {
		upstreamModel = doubaoWebDefaultModel(account)
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

	prompt := doubaoWebPromptFromChatRequest(&chatReq)
	if strings.TrimSpace(prompt) == "" {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "message content is required")
		return nil, fmt.Errorf("missing message content")
	}

	result, err := s.sendDoubaoWebPrompt(ctx, c, account, upstreamModel, prompt, "")
	if err != nil {
		return nil, err
	}

	reasoningEffort := extractOpenAIReasoningEffortFromBody(body, originalModel)
	serviceTier := extractOpenAIServiceTierFromBody(body)
	usage := OpenAIUsage{
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
	}

	if chatReq.Stream {
		if err := s.writeDoubaoWebChatStream(c, result, originalModel, serviceTier); err != nil {
			return nil, err
		}
		firstTokenMs := int(time.Since(startTime).Milliseconds())
		return &OpenAIForwardResult{
			RequestID:       result.ID,
			ResponseID:      result.SessionID,
			Usage:           usage,
			Model:           originalModel,
			BillingModel:    billingModel,
			UpstreamModel:   result.UpstreamModel,
			ReasoningEffort: reasoningEffort,
			ServiceTier:     serviceTier,
			Stream:          true,
			Duration:        time.Since(startTime),
			FirstTokenMs:    &firstTokenMs,
		}, nil
	}

	if err := s.writeDoubaoWebChatJSON(c, result, originalModel, serviceTier); err != nil {
		return nil, err
	}
	return &OpenAIForwardResult{
		RequestID:       result.ID,
		ResponseID:      result.SessionID,
		Usage:           usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   result.UpstreamModel,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) forwardDoubaoWebResponsesViaChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "Failed to parse request body"}})
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := strings.TrimSpace(responsesReq.Model)
	if originalModel == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "model is required"}})
		return nil, fmt.Errorf("missing model in request")
	}

	chatReq, err := apicompat.ResponsesToChatCompletionsRequest(&responsesReq)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": err.Error()}})
		return nil, fmt.Errorf("convert responses to chat completions: %w", err)
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, "")
	upstreamModel := doubaoWebNormalizeModel(normalizeOpenAIModelForUpstream(account, billingModel))
	if upstreamModel == "" {
		upstreamModel = doubaoWebDefaultModel(account)
	}
	chatReq.Model = upstreamModel
	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal doubao web chat request: %w", err)
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
		return nil, fmt.Errorf("parse policy-adjusted doubao web chat request: %w", err)
	}
	chatReq.Model = upstreamModel

	prompt := doubaoWebPromptFromChatRequest(chatReqWithInstructions(chatReq, responsesReq.Instructions))
	if strings.TrimSpace(prompt) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "message content is required"}})
		return nil, fmt.Errorf("missing message content")
	}

	previousID := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
	result, err := s.sendDoubaoWebPrompt(ctx, c, account, upstreamModel, prompt, previousID)
	if err != nil {
		return nil, err
	}

	reasoningEffort := extractOpenAIReasoningEffortFromBody(body, originalModel)
	serviceTier := extractOpenAIServiceTierFromBody(body)
	usage := OpenAIUsage{
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
	}

	ccResp := doubaoWebChatResponse(result, originalModel, serviceTier)
	if responsesReq.Stream {
		if err := writeDoubaoWebResponsesStream(c, ccResp, result, originalModel); err != nil {
			return nil, err
		}
		firstTokenMs := int(time.Since(startTime).Milliseconds())
		return &OpenAIForwardResult{
			RequestID:       result.ID,
			ResponseID:      result.SessionID,
			Usage:           usage,
			Model:           originalModel,
			BillingModel:    billingModel,
			UpstreamModel:   result.UpstreamModel,
			ReasoningEffort: reasoningEffort,
			ServiceTier:     serviceTier,
			Stream:          true,
			Duration:        time.Since(startTime),
			FirstTokenMs:    &firstTokenMs,
		}, nil
	}

	responsesResp := apicompat.ChatCompletionsResponseToResponses(&ccResp, originalModel)
	responsesResp.ID = result.SessionID
	c.JSON(http.StatusOK, responsesResp)
	return &OpenAIForwardResult{
		RequestID:       result.ID,
		ResponseID:      result.SessionID,
		Usage:           usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   result.UpstreamModel,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) sendDoubaoWebPrompt(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	upstreamModel string,
	prompt string,
	previousID string,
) (*doubaoWebResult, error) {
	sessionIDs := doubaoWebSessionIDs(account)
	if len(sessionIDs) == 0 {
		return nil, doubaoWebFailover(ctx, c, s, account, http.StatusUnauthorized, []byte(`{"error":{"message":"doubao-web requires sessionid credential"}}`), upstreamModel, false)
	}
	baseURL := account.GetOpenAIBaseURL()
	if strings.TrimSpace(baseURL) == "" {
		baseURL = doubaoWebDefaultBaseURL
	}
	validatedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid doubao-web base_url: %w", err)
	}
	executor := globalDoubaoWebExecutor
	if executor == nil {
		return nil, fmt.Errorf("doubao web executor is not initialized")
	}

	var lastErr error
	for _, sessionID := range sessionIDs {
		result, err := executor.Complete(ctx, doubaoWebRequest{
			BaseURL:      validatedBaseURL,
			SessionID:    sessionID,
			Model:        upstreamModel,
			DefaultBotID: doubaoWebDefaultBotIDForAccount(account),
			Prompt:       prompt,
			PreviousID:   previousID,
			AccountID:    account.ID,
		})
		if err == nil {
			return result, nil
		}
		lastErr = err
		var upstreamErr *doubaoWebUpstreamError
		if errors.As(err, &upstreamErr) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: upstreamErr.StatusCode,
				Kind:               "failover",
				Message:            sanitizeUpstreamErrorMessage(upstreamErr.Message),
			})
			s.handleOpenAIAccountUpstreamError(ctx, account, upstreamErr.StatusCode, nil, upstreamErr.Body, upstreamModel)
			if !upstreamErr.RetryableNextSession {
				return nil, &UpstreamFailoverError{StatusCode: upstreamErr.StatusCode, ResponseBody: openAITransportFailoverBody}
			}
			continue
		}
		return nil, doubaoWebFailover(ctx, c, s, account, http.StatusBadGateway, []byte(err.Error()), upstreamModel, false)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no doubao-web sessionid available")
	}
	return nil, doubaoWebFailover(ctx, c, s, account, http.StatusBadGateway, []byte(lastErr.Error()), upstreamModel, true)
}

type doubaoWebUpstreamError struct {
	StatusCode           int
	Message              string
	Body                 []byte
	RetryableNextSession bool
}

func (e *doubaoWebUpstreamError) Error() string {
	return e.Message
}

func doubaoWebFailover(ctx context.Context, c *gin.Context, s *OpenAIGatewayService, account *Account, status int, body []byte, upstreamModel string, retryable bool) error {
	msg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if msg == "" {
		msg = sanitizeUpstreamErrorMessage(strings.TrimSpace(string(body)))
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: status,
		Kind:               "failover",
		Message:            msg,
	})
	s.handleOpenAIAccountUpstreamError(ctx, account, status, nil, body, upstreamModel)
	return &UpstreamFailoverError{
		StatusCode:             status,
		ResponseBody:           openAITransportFailoverBody,
		RetryableOnSameAccount: retryable,
	}
}

func (e *doubaoWebBrowserExecutor) Complete(ctx context.Context, req doubaoWebRequest) (*doubaoWebResult, error) {
	baseURL, chatURL, cookieDomain, err := doubaoWebURLs(req.BaseURL)
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(req.PreviousID)
	botID := doubaoWebResolveBotID(req.Model, req.DefaultBotID)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	session := e.getSession(sessionID, botID)
	payload := doubaoWebBuildPayload(session, req.Prompt)
	requestBody, err := marshalOpenAIUpstreamJSON(payload)
	if err != nil {
		return nil, err
	}

	fetchResp, err := e.fetchWithWorker(ctx, baseURL, chatURL, cookieDomain, req.SessionID, string(requestBody))
	if err != nil {
		return nil, err
	}
	if fetchResp.Status != http.StatusOK {
		return nil, &doubaoWebUpstreamError{
			StatusCode:           fetchResp.Status,
			Message:              doubaoWebRequestFailureMessage(fetchResp.Status, fetchResp.Body),
			Body:                 []byte(fetchResp.Body),
			RetryableNextSession: doubaoWebErrorRetryable(fetchResp.Status, fetchResp.Body),
		}
	}
	if status, message, ok := doubaoWebJSONError(fetchResp.Body); ok {
		return nil, &doubaoWebUpstreamError{
			StatusCode:           status,
			Message:              message,
			Body:                 []byte(fetchResp.Body),
			RetryableNextSession: doubaoWebErrorRetryable(status, fetchResp.Body),
		}
	}
	parsed, err := doubaoWebParseSSEBody(fetchResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse doubao-web SSE: %w", err)
	}
	if strings.TrimSpace(parsed.Error) != "" {
		return nil, &doubaoWebUpstreamError{
			StatusCode:           http.StatusBadGateway,
			Message:              parsed.Error,
			Body:                 []byte(parsed.Error),
			RetryableNextSession: doubaoWebErrorRetryable(http.StatusBadGateway, parsed.Error),
		}
	}
	if strings.TrimSpace(parsed.OutputText) == "" {
		return nil, fmt.Errorf("doubao-web returned an empty response: %s", doubaoWebResponseSample(fetchResp.Body))
	}

	e.updateSession(session.SessionID, parsed.SessionMeta)
	e.incrementSessionTurn(session.SessionID)
	inputTokens, outputTokens := doubaoWebApproximateUsage(req.Prompt, parsed.OutputText)
	return &doubaoWebResult{
		ID:            "chatcmpl-" + shortUUID12(),
		SessionID:     session.SessionID,
		Content:       parsed.OutputText,
		Deltas:        parsed.Deltas,
		FinishReason:  "stop",
		InputTokens:   inputTokens,
		OutputTokens:  outputTokens,
		Created:       time.Now().Unix(),
		UpstreamModel: req.Model,
	}, nil
}

func (e *doubaoWebBrowserExecutor) fetchWithWorker(ctx context.Context, baseURL, chatURL, cookieDomain, upstreamSessionID, requestBody string) (*doubaoWebBrowserFetchResponse, error) {
	worker, err := e.workerFor(baseURL, chatURL, cookieDomain, upstreamSessionID)
	if err != nil {
		return nil, err
	}
	resp, err := worker.fetch(ctx, upstreamSessionID, requestBody)
	if err == nil || !doubaoWebShouldRecreateWorker(err) {
		return resp, err
	}

	e.dropWorker(baseURL, worker)
	worker, recreateErr := e.workerFor(baseURL, chatURL, cookieDomain, upstreamSessionID)
	if recreateErr != nil {
		return nil, recreateErr
	}
	return worker.fetch(ctx, upstreamSessionID, requestBody)
}

func (e *doubaoWebBrowserExecutor) getSession(sessionID, botID string) *doubaoWebSession {
	e.mu.Lock()
	defer e.mu.Unlock()
	if session, ok := e.sessions[sessionID]; ok {
		if session.BotID != botID {
			session.ConversationID = ""
			session.LocalConversationID = doubaoWebLocalConversationID()
			session.SectionID = ""
			session.LastMessageIndex = nil
			session.BotID = botID
			session.TurnCount = 0
		}
		cp := *session
		return &cp
	}
	session := &doubaoWebSession{
		SessionID:           sessionID,
		LocalConversationID: doubaoWebLocalConversationID(),
		BotID:               botID,
	}
	e.sessions[sessionID] = session
	cp := *session
	return &cp
}

func (e *doubaoWebBrowserExecutor) updateSession(sessionID string, meta doubaoWebSessionMeta) {
	e.mu.Lock()
	defer e.mu.Unlock()
	session := e.sessions[sessionID]
	if session == nil {
		return
	}
	if strings.TrimSpace(meta.ConversationID) != "" {
		session.ConversationID = meta.ConversationID
	}
	if strings.TrimSpace(meta.SectionID) != "" {
		session.SectionID = meta.SectionID
	}
	if meta.LastMessageIndex != nil {
		value := *meta.LastMessageIndex
		session.LastMessageIndex = &value
	}
}

func (e *doubaoWebBrowserExecutor) incrementSessionTurn(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if session := e.sessions[sessionID]; session != nil {
		session.TurnCount++
	}
}

func (e *doubaoWebBrowserExecutor) workerFor(baseURL, chatURL, cookieDomain, sessionID string) (*doubaoWebBrowserWorker, error) {
	key := baseURL
	e.mu.Lock()
	defer e.mu.Unlock()
	if worker := e.workers[key]; worker != nil {
		return worker, nil
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), doubaoWebExecAllocatorOptions()...)
	browserCtx, cancel := chromedp.NewContext(allocCtx)
	worker := &doubaoWebBrowserWorker{
		allocCancel:  allocCancel,
		ctx:          browserCtx,
		cancel:       cancel,
		baseURL:      baseURL,
		chatURL:      chatURL,
		cookieDomain: cookieDomain,
	}
	e.workers[key] = worker
	return worker, nil
}

func (e *doubaoWebBrowserExecutor) dropWorker(baseURL string, worker *doubaoWebBrowserWorker) {
	if worker == nil {
		return
	}
	e.mu.Lock()
	if current := e.workers[baseURL]; current == worker {
		delete(e.workers, baseURL)
	}
	e.mu.Unlock()
	worker.close()
}

func (w *doubaoWebBrowserWorker) close() {
	if w == nil {
		return
	}
	if w.cancel != nil {
		w.cancel()
	}
	if w.allocCancel != nil {
		w.allocCancel()
	}
}

func doubaoWebShouldRecreateWorker(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "context canceled")
}

func doubaoWebExecAllocatorOptions() []chromedp.ExecAllocatorOption {
	options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	if path := strings.TrimSpace(os.Getenv("CHROME_BIN")); path != "" {
		options = append(options, chromedp.ExecPath(path))
	}
	if doubaoWebEnvBool("CHROMEDP_NO_SANDBOX") {
		options = append(options, chromedp.Flag("no-sandbox", true))
	}
	options = append(options,
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("lang", "zh-CN"),
		chromedp.UserAgent(doubaoWebUserAgent),
	)
	return options
}

func doubaoWebEnvBool(name string) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false
	}
	parsed, err := strconv.ParseBool(raw)
	if err == nil {
		return parsed
	}
	switch strings.ToLower(raw) {
	case "on", "yes", "y", "enabled":
		return true
	default:
		return false
	}
}

func (w *doubaoWebBrowserWorker) fetch(ctx context.Context, sessionID, requestBody string) (*doubaoWebBrowserFetchResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	runCtx, cancel := context.WithTimeout(w.ctx, doubaoWebBrowserTimeout)
	defer cancel()
	if ctx != nil {
		stop := context.AfterFunc(ctx, cancel)
		defer stop()
	}

	if w.currentSessionID != sessionID {
		if err := chromedp.Run(runCtx,
			network.Enable(),
			emulation.SetUserAgentOverride(doubaoWebUserAgent).WithAcceptLanguage("zh-CN"),
			w.setSessionCookies(sessionID),
			chromedp.Navigate(w.baseURL),
			chromedp.Sleep(1500*time.Millisecond),
		); err != nil {
			w.currentSessionID = ""
			return nil, err
		}
		w.currentSessionID = sessionID
	}

	var encodedValue any
	script, err := doubaoWebBuildFetchScript(w.chatURL, requestBody)
	if err != nil {
		return nil, err
	}
	if err := chromedp.Run(runCtx,
		chromedp.Sleep(180*time.Millisecond),
		chromedp.Evaluate(script, &encodedValue, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
			return p.WithAwaitPromise(true)
		}),
	); err != nil {
		w.currentSessionID = ""
		return nil, err
	}
	encoded, err := doubaoWebBrowserFetchResultJSON(encodedValue)
	if err != nil {
		return nil, err
	}
	var resp doubaoWebBrowserFetchResponse
	if err := json.Unmarshal([]byte(encoded), &resp); err != nil {
		return nil, err
	}
	if resp.Status == 0 {
		w.currentSessionID = ""
	}
	return &resp, nil
}

func doubaoWebResponseSample(body string) string {
	sample := truncateString(strings.TrimSpace(body), 2048)
	if sample == "" {
		return "(empty body)"
	}
	return sanitizeUpstreamErrorMessage(sample)
}

func doubaoWebJSONError(body string) (int, string, bool) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return 0, "", false
	}
	message := strings.TrimSpace(extractUpstreamErrorMessage([]byte(trimmed)))
	if message == "" {
		message = strings.TrimSpace(gjson.Get(trimmed, "msg").String())
	}
	if message == "" {
		return 0, "", false
	}
	status := http.StatusBadGateway
	lowerBody := strings.ToLower(trimmed)
	lowerMessage := strings.ToLower(message)
	if strings.Contains(lowerBody, "login invalid") ||
		strings.Contains(lowerMessage, "login invalid") ||
		strings.Contains(message, "登录已过期") {
		status = http.StatusUnauthorized
	}
	return status, sanitizeUpstreamErrorMessage(message), true
}

func doubaoWebNormalizeTestErrorMessage(message string) string {
	message = strings.TrimSpace(message)
	switch {
	case message == "":
		return "Doubao Web test failed"
	case strings.Contains(message, "登录已过期"):
		return "Doubao Web session 已过期，请更新 sessionid"
	case strings.Contains(strings.ToLower(message), "login invalid"):
		return "Doubao Web session 已过期，请更新 sessionid"
	default:
		return message
	}
}

func doubaoWebRequestFailureMessage(status int, body string) string {
	message := fmt.Sprintf("doubao-web request failed with %d", status)
	detail := strings.TrimSpace(extractUpstreamErrorMessage([]byte(body)))
	if detail == "" {
		detail = strings.TrimSpace(body)
	}
	if detail == "" {
		return message
	}
	return message + ": " + truncateString(sanitizeUpstreamErrorMessage(detail), 1024)
}

func doubaoWebBrowserFetchResultJSON(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return "", fmt.Errorf("doubao-web browser fetch returned an empty result")
		}
		return typed, nil
	case nil:
		return "", fmt.Errorf("doubao-web browser fetch returned null")
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return "", fmt.Errorf("marshal doubao-web browser fetch result: %w", err)
		}
		return string(data), nil
	}
}

func (r *doubaoWebBrowserFetchResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
		Status    int             `json:"status"`
		Body      json.RawMessage `json:"body"`
		FetchHook json.RawMessage `json:"fetch_hook,omitempty"`
		BodyLen   int             `json:"body_len,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Status = raw.Status
	r.Body = doubaoWebRawJSONToString(raw.Body)
	r.FetchHook = doubaoWebRawJSONToString(raw.FetchHook)
	r.BodyLen = raw.BodyLen
	return nil
}

func doubaoWebRawJSONToString(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return trimmed
}

func (w *doubaoWebBrowserWorker) setSessionCookies(sessionID string) chromedp.ActionFunc {
	return func(ctx context.Context) error {
		expires := cdpCookieExpires(time.Now().Add(365 * 24 * time.Hour))
		for _, name := range []string{"sessionid", "sessionid_ss", "sid_tt", "sid_guard"} {
			if err := network.SetCookie(name, sessionID).
				WithURL(w.baseURL).
				WithDomain(w.cookieDomain).
				WithPath("/").
				WithSecure(true).
				WithHTTPOnly(true).
				WithExpires(expires).
				Do(ctx); err != nil {
				return err
			}
		}
		return nil
	}
}

func cdpCookieExpires(t time.Time) *cdp.TimeSinceEpoch {
	value := cdp.TimeSinceEpoch(t)
	return &value
}

func doubaoWebURLs(rawBaseURL string) (baseURL string, chatURL string, cookieDomain string, err error) {
	baseURL = strings.TrimSpace(rawBaseURL)
	if baseURL == "" {
		baseURL = doubaoWebDefaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid doubao-web base_url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", "", "", fmt.Errorf("doubao-web base_url must include scheme and host")
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/"
	}
	baseURL = parsed.String()
	chatParsed := *parsed
	chatParsed.Path = strings.TrimRight(parsed.Path, "/") + "/chat/completion"
	chatParsed.RawQuery = ""
	chatParsed.Fragment = ""
	host := parsed.Hostname()
	host = strings.TrimPrefix(host, "www.")
	return baseURL, chatParsed.String(), "." + host, nil
}

func doubaoWebBuildFetchScript(chatURL, requestBody string) (string, error) {
	args, err := json.Marshal(map[string]any{
		"url":      chatURL,
		"body":     requestBody,
		"trace_id": strings.ReplaceAll(uuid.NewString(), "-", ""),
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`(async (args) => {
const fetchStr = window.fetch.toString().substring(0, 80);
const headers = {
  'Content-Type': 'application/json',
  'Agw-Js-Conv': 'str',
  'x-flow-trace': args.trace_id,
  'last-event-id': 'undefined'
};
const opts = {
  method: 'POST',
  headers,
  body: args.body,
  credentials: 'include',
  signal: AbortSignal.timeout(1800000)
};
try {
  const res = await fetch(args.url, opts);
  if (!res.ok) {
    const text = await res.text();
    return JSON.stringify({ status: res.status, body: text.substring(0, 2000), fetch_hook: fetchStr });
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let body = '';
  while (true) {
    const chunk = await reader.read();
    if (chunk.done) break;
    body += decoder.decode(chunk.value, { stream: true });
  }
  return JSON.stringify({ status: res.status, body, fetch_hook: fetchStr, body_len: body.length });
} catch (error) {
  return JSON.stringify({ status: 0, body: 'JS error: ' + error.message, fetch_hook: fetchStr });
}
})(%s);`, args), nil
}

func doubaoWebBuildPayload(session *doubaoWebSession, text string) map[string]any {
	isNew := session == nil || strings.TrimSpace(session.ConversationID) == ""
	conversationID := ""
	sectionID := ""
	botID := doubaoWebDefaultBotID
	localConversationID := doubaoWebLocalConversationID()
	var lastMessageIndex any
	if session != nil {
		conversationID = session.ConversationID
		sectionID = session.SectionID
		botID = session.BotID
		localConversationID = session.LocalConversationID
		if session.LastMessageIndex != nil {
			lastMessageIndex = *session.LastMessageIndex
		}
	}
	if isNew {
		conversationID = ""
		sectionID = ""
		lastMessageIndex = nil
	}
	return map[string]any{
		"client_meta": map[string]any{
			"local_conversation_id": localConversationID,
			"conversation_id":       conversationID,
			"bot_id":                botID,
			"last_section_id":       sectionID,
			"last_message_index":    lastMessageIndex,
		},
		"messages": []any{
			map[string]any{
				"local_message_id": uuid.NewString(),
				"content_block": []any{
					map[string]any{
						"block_type": 10000,
						"content": map[string]any{
							"text_block": map[string]any{
								"text":          text,
								"icon_url":      "",
								"icon_url_dark": "",
								"summary":       "",
							},
							"pc_event_block": "",
						},
						"block_id":      uuid.NewString(),
						"parent_id":     "",
						"meta_info":     []any{},
						"append_fields": []any{},
					},
				},
				"message_status": 0,
			},
		},
		"option": map[string]any{
			"send_message_scene":       "",
			"create_time_ms":           time.Now().UnixMilli(),
			"collect_id":               "",
			"is_audio":                 false,
			"answer_with_suggest":      false,
			"tts_switch":               false,
			"need_deep_think":          0,
			"click_clear_context":      false,
			"from_suggest":             false,
			"is_regen":                 false,
			"is_replace":               false,
			"disable_sse_cache":        false,
			"select_text_action":       "",
			"resend_for_regen":         false,
			"scene_type":               0,
			"unique_key":               uuid.NewString(),
			"start_seq":                0,
			"need_create_conversation": isNew,
			"regen_query_id":           []any{},
			"edit_query_id":            []any{},
			"regen_instruction":        "",
			"no_replace_for_regen":     false,
			"message_from":             0,
			"shared_app_name":          "",
			"sse_recv_event_options":   map[string]any{"support_chunk_delta": true},
			"is_ai_playground":         false,
			"recovery_option": map[string]any{
				"is_recovery":         false,
				"req_create_time_sec": time.Now().Unix(),
			},
		},
		"ext": map[string]any{
			"use_deep_think":                "0",
			"fp":                            doubaoWebDefaultFP,
			"commerce_credit_config_enable": "0",
			"sub_conv_firstmet_type":        "0",
		},
	}
}

func doubaoWebParseSSEBody(rawBody string) (*doubaoWebParsedSSE, error) {
	result := &doubaoWebParsedSSE{}
	seenImages := make(map[string]bool)
	events, err := doubaoWebSplitSSEEvents(rawBody)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		data, _ := event.Data.(map[string]any)
		switch event.Type {
		case "SSE_HEARTBEAT", "FULL_MSG_NOTIFY", "SSE_REPLY_END":
		case "SSE_ACK":
			ackMeta, _ := data["ack_client_meta"].(map[string]any)
			if value, _ := ackMeta["conversation_id"].(string); value != "" {
				result.SessionMeta.ConversationID = value
			}
			if value, _ := ackMeta["section_id"].(string); value != "" {
				result.SessionMeta.SectionID = value
			}
			if items, _ := data["query_list"].([]any); len(items) > 0 {
				if item, _ := items[0].(map[string]any); item != nil {
					if index, ok := jsonNumberToInt(item["message_index"]); ok {
						result.SessionMeta.LastMessageIndex = &index
					}
				}
			}
		case "STREAM_MSG_NOTIFY":
			if meta, _ := data["meta"].(map[string]any); meta != nil {
				if index, ok := jsonNumberToInt(meta["index_in_conv"]); ok {
					result.SessionMeta.LastMessageIndex = &index
				}
				if sectionID, _ := meta["section_id"].(string); sectionID != "" {
					result.SessionMeta.SectionID = sectionID
				}
			}
			for _, block := range doubaoWebContentBlocksFromStreamMsg(data) {
				switch block.BlockType {
				case 10000:
					doubaoWebAppendDelta(result, block.Text)
				case 2074:
					doubaoWebAppendImages(result, block.Images, seenImages)
				}
			}
		case "CHUNK_DELTA":
			if text, _ := data["text"].(string); text != "" {
				doubaoWebAppendDelta(result, text)
			}
		case "STREAM_CHUNK":
			ops, _ := data["patch_op"].([]any)
			for _, rawOp := range ops {
				op, _ := rawOp.(map[string]any)
				patchObject, _ := jsonNumberToInt(op["patch_object"])
				patchValue, _ := op["patch_value"].(map[string]any)
				switch patchObject {
				case 1:
					blocks, _ := patchValue["content_block"].([]any)
					for _, rawBlock := range blocks {
						block, _ := rawBlock.(map[string]any)
						parsed := doubaoWebParseContentBlock(block)
						switch parsed.BlockType {
						case 10000:
							doubaoWebAppendDelta(result, parsed.Text)
						case 2074:
							doubaoWebAppendImages(result, parsed.Images, seenImages)
						}
					}
				case 102:
					if text := doubaoWebExtractPatch102Text(patchValue["content"]); text != "" {
						doubaoWebAppendDelta(result, text)
					}
				}
			}
		case "STREAM_ERROR":
			if msg, _ := data["error_msg"].(string); msg != "" {
				result.Error = msg
			} else {
				result.Error = "unknown doubao SSE error"
			}
		}
	}
	return result, nil
}

func doubaoWebSplitSSEEvents(rawBody string) ([]doubaoWebSSEEvent, error) {
	var events []doubaoWebSSEEvent
	var currentEvent string
	var dataBuilder strings.Builder
	flush := func() error {
		if currentEvent == "" {
			dataBuilder.Reset()
			return nil
		}
		var data any
		dataText := strings.TrimSpace(dataBuilder.String())
		if dataText == "" || dataText == "null" {
			data = map[string]any{}
		} else if err := json.Unmarshal([]byte(dataText), &data); err != nil {
			return err
		}
		events = append(events, doubaoWebSSEEvent{Type: currentEvent, Data: data})
		currentEvent = ""
		dataBuilder.Reset()
		return nil
	}
	for _, rawLine := range strings.Split(rawBody, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if value, ok := strings.CutPrefix(line, "event:"); ok {
			currentEvent = strings.TrimSpace(value)
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			if dataBuilder.Len() > 0 {
				dataBuilder.WriteByte('\n')
			}
			dataBuilder.WriteString(strings.TrimSpace(value))
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return events, nil
}

func doubaoWebContentBlocksFromStreamMsg(data map[string]any) []doubaoWebContentBlock {
	content, _ := data["content"].(map[string]any)
	blocks, _ := content["content_block"].([]any)
	out := make([]doubaoWebContentBlock, 0, len(blocks))
	for _, rawBlock := range blocks {
		block, _ := rawBlock.(map[string]any)
		out = append(out, doubaoWebParseContentBlock(block))
	}
	return out
}

func doubaoWebParseContentBlock(block map[string]any) doubaoWebContentBlock {
	blockType, _ := jsonNumberToInt64(block["block_type"])
	switch blockType {
	case 10000:
		content, _ := block["content"].(map[string]any)
		textBlock, _ := content["text_block"].(map[string]any)
		text, _ := textBlock["text"].(string)
		return doubaoWebContentBlock{BlockType: blockType, Text: text}
	case 2074:
		return doubaoWebContentBlock{BlockType: blockType, Images: doubaoWebExtractImageURLs(block)}
	default:
		return doubaoWebContentBlock{BlockType: blockType}
	}
}

func doubaoWebExtractImageURLs(block map[string]any) []string {
	content, _ := block["content"].(map[string]any)
	creationBlock, _ := content["creation_block"].(map[string]any)
	creations, _ := creationBlock["creations"].([]any)
	var urls []string
	for _, rawCreation := range creations {
		creation, _ := rawCreation.(map[string]any)
		image, _ := creation["image"].(map[string]any)
		for _, key := range []string{"image_ori_raw", "image_ori"} {
			obj, _ := image[key].(map[string]any)
			if rawURL, _ := obj["url"].(string); rawURL != "" {
				urls = append(urls, rawURL)
				goto next
			}
		}
		if rawURL, _ := image["image_url"].(string); rawURL != "" {
			urls = append(urls, rawURL)
		}
	next:
	}
	return urls
}

func doubaoWebExtractPatch102Text(value any) string {
	switch typed := value.(type) {
	case string:
		var parsed map[string]any
		if err := json.Unmarshal([]byte(typed), &parsed); err == nil {
			if text, _ := parsed["text"].(string); text != "" {
				return text
			}
		}
		return typed
	case map[string]any:
		text, _ := typed["text"].(string)
		return text
	default:
		return ""
	}
}

func doubaoWebAppendDelta(result *doubaoWebParsedSSE, text string) {
	if text == "" {
		return
	}
	result.OutputText += text
	result.Deltas = append(result.Deltas, text)
}

func doubaoWebAppendImages(result *doubaoWebParsedSSE, imageURLs []string, seen map[string]bool) {
	for _, imageURL := range imageURLs {
		if seen[imageURL] {
			continue
		}
		seen[imageURL] = true
		markdown := "![generated](" + imageURL + ")"
		if result.OutputText != "" {
			result.OutputText += "\n"
		}
		result.OutputText += markdown
		result.Deltas = append(result.Deltas, markdown)
	}
}

func doubaoWebPromptFromChatRequest(req *apicompat.ChatCompletionsRequest) string {
	if req == nil {
		return ""
	}
	var systemParts []string
	var builder strings.Builder
	if instructions := strings.TrimSpace(req.Instructions); instructions != "" {
		systemParts = append(systemParts, instructions)
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
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "system" || role == "developer" {
			systemParts = append(systemParts, text)
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		if role == "" {
			role = "user"
		}
		builder.WriteString(strings.ToUpper(role[:1]))
		if len(role) > 1 {
			builder.WriteString(role[1:])
		}
		builder.WriteString(":\n")
		builder.WriteString(text)
	}
	prompt := strings.TrimSpace(builder.String())
	systemPrompt := strings.TrimSpace(strings.Join(systemParts, "\n\n"))
	if systemPrompt != "" && prompt != "" {
		return "System instructions:\n" + systemPrompt + "\n\nUser request:\n" + prompt
	}
	if systemPrompt != "" {
		return "System instructions:\n" + systemPrompt
	}
	return prompt
}

func doubaoWebChatResponse(result *doubaoWebResult, model string, serviceTier *string) apicompat.ChatCompletionsResponse {
	usage := &apicompat.ChatUsage{
		PromptTokens:     result.InputTokens,
		CompletionTokens: result.OutputTokens,
		TotalTokens:      result.InputTokens + result.OutputTokens,
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

func (s *OpenAIGatewayService) writeDoubaoWebChatJSON(c *gin.Context, result *doubaoWebResult, model string, serviceTier *string) error {
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), http.Header{}, s.responseHeaderFilter)
	}
	c.JSON(http.StatusOK, doubaoWebChatResponse(result, model, serviceTier))
	return nil
}

func (s *OpenAIGatewayService) writeDoubaoWebChatStream(c *gin.Context, result *doubaoWebResult, model string, serviceTier *string) error {
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), http.Header{}, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	roleChunk := doubaoWebChatChunk(result, model, apicompat.ChatDelta{Role: "assistant"}, nil, nil, serviceTier)
	if err := doubaoWebWriteChatChunk(c, roleChunk); err != nil {
		return err
	}
	deltas := result.Deltas
	if len(deltas) == 0 && result.Content != "" {
		deltas = []string{result.Content}
	}
	for _, delta := range deltas {
		text := delta
		if err := doubaoWebWriteChatChunk(c, doubaoWebChatChunk(result, model, apicompat.ChatDelta{Content: &text}, nil, nil, serviceTier)); err != nil {
			return err
		}
	}
	finish := result.FinishReason
	if err := doubaoWebWriteChatChunk(c, doubaoWebChatChunk(result, model, apicompat.ChatDelta{}, &finish, nil, serviceTier)); err != nil {
		return err
	}
	usage := doubaoWebChatResponse(result, model, serviceTier).Usage
	if err := doubaoWebWriteChatChunk(c, doubaoWebChatChunk(result, model, apicompat.ChatDelta{}, nil, usage, serviceTier)); err != nil {
		return err
	}
	_, _ = io.WriteString(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
	return nil
}

func writeDoubaoWebResponsesStream(c *gin.Context, ccResp apicompat.ChatCompletionsResponse, result *doubaoWebResult, model string) error {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	state := apicompat.NewChatCompletionsToResponsesStreamState(model)
	roleChunk := doubaoWebChatChunk(result, model, apicompat.ChatDelta{Role: "assistant"}, nil, nil, nil)
	if err := doubaoWebWriteResponsesEvents(c, roleChunk, state); err != nil {
		return err
	}
	deltas := result.Deltas
	if len(deltas) == 0 && result.Content != "" {
		deltas = []string{result.Content}
	}
	for _, delta := range deltas {
		text := delta
		if err := doubaoWebWriteResponsesEvents(c, doubaoWebChatChunk(result, model, apicompat.ChatDelta{Content: &text}, nil, nil, nil), state); err != nil {
			return err
		}
	}
	finish := result.FinishReason
	finalChunk := doubaoWebChatChunk(result, model, apicompat.ChatDelta{}, &finish, ccResp.Usage, nil)
	if err := doubaoWebWriteResponsesEvents(c, finalChunk, state); err != nil {
		return err
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

func doubaoWebChatChunk(result *doubaoWebResult, model string, delta apicompat.ChatDelta, finish *string, usage *apicompat.ChatUsage, serviceTier *string) apicompat.ChatCompletionsChunk {
	chunk := apicompat.ChatCompletionsChunk{
		ID:      result.ID,
		Object:  "chat.completion.chunk",
		Created: result.Created,
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{{
			Index:        0,
			Delta:        delta,
			FinishReason: finish,
		}},
		Usage: usage,
	}
	if usage != nil && finish == nil && delta.Role == "" && delta.Content == nil {
		chunk.Choices = []apicompat.ChatChunkChoice{}
	}
	if serviceTier != nil {
		chunk.ServiceTier = *serviceTier
	}
	return chunk
}

func doubaoWebWriteChatChunk(c *gin.Context, chunk apicompat.ChatCompletionsChunk) error {
	sse, err := apicompat.ChatChunkToSSE(chunk)
	if err != nil {
		return err
	}
	_, err = io.WriteString(c.Writer, sse)
	return err
}

func doubaoWebWriteResponsesEvents(c *gin.Context, chunk apicompat.ChatCompletionsChunk, state *apicompat.ChatCompletionsToResponsesStreamState) error {
	for _, event := range apicompat.ChatCompletionsChunkToResponsesEvents(&chunk, state) {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
			return err
		}
	}
	return nil
}

func doubaoWebSessionIDs(account *Account) []string {
	if account == nil {
		return nil
	}
	var out []string
	for _, key := range []string{"sessionid", "session_id"} {
		if value := strings.TrimSpace(account.GetCredential(key)); value != "" {
			out = append(out, value)
		}
	}
	if accountUsesDoubaoWebReverse(account) {
		if value := strings.TrimSpace(account.GetOpenAIApiKey()); value != "" {
			out = append(out, value)
		}
	}
	for _, key := range []string{"sessionids", "session_ids"} {
		switch raw := account.Credentials[key].(type) {
		case string:
			out = append(out, splitCredentialList(raw)...)
		case []any:
			for _, item := range raw {
				if value := strings.TrimSpace(fmt.Sprint(item)); value != "" {
					out = append(out, value)
				}
			}
		case []string:
			for _, item := range raw {
				if value := strings.TrimSpace(item); value != "" {
					out = append(out, value)
				}
			}
		}
	}
	seen := make(map[string]bool)
	unique := out[:0]
	for _, value := range out {
		if !seen[value] {
			seen[value] = true
			unique = append(unique, value)
		}
	}
	return unique
}

func splitCredentialList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	var out []string
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func doubaoWebDefaultModel(account *Account) string {
	if account == nil {
		return "doubao"
	}
	if model := strings.TrimSpace(account.GetCredential("doubao_default_model")); model != "" {
		return model
	}
	return "doubao"
}

func doubaoWebDefaultBotIDForAccount(account *Account) string {
	if account == nil {
		return doubaoWebDefaultBotID
	}
	if botID := strings.TrimSpace(account.GetCredential("doubao_bot_id")); botID != "" {
		return botID
	}
	if botID := strings.TrimSpace(account.GetCredential("bot_id")); botID != "" {
		return botID
	}
	return doubaoWebDefaultBotID
}

func doubaoWebNormalizeModel(model string) string {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, "doubao:")
	return model
}

func doubaoWebResolveBotID(model, defaultBotID string) string {
	model = strings.TrimSpace(model)
	if model != "" {
		allDigits := true
		for _, r := range model {
			if !unicode.IsDigit(r) {
				allDigits = false
				break
			}
		}
		if allDigits {
			return model
		}
	}
	if strings.TrimSpace(defaultBotID) == "" {
		return doubaoWebDefaultBotID
	}
	return defaultBotID
}

func doubaoWebApproximateUsage(prompt, outputText string) (int, int) {
	return len([]rune(prompt)), len([]rune(outputText))
}

func doubaoWebLocalConversationID() string {
	return fmt.Sprintf("local_%d%s", time.Now().UnixMilli(), shortUUID12())
}

func shortUUID12() string {
	value := strings.ReplaceAll(uuid.NewString(), "-", "")
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func doubaoWebErrorRetryable(status int, body string) bool {
	lower := strings.ToLower(body)
	if strings.Contains(lower, "login invalid") || strings.Contains(body, "登录已过期") {
		return true
	}
	if strings.Contains(lower, "expired") && strings.Contains(lower, "session") {
		return true
	}
	if status == http.StatusTooManyRequests || strings.Contains(lower, "429") ||
		strings.Contains(lower, "rate") || strings.Contains(lower, "limit") {
		return true
	}
	return status >= 500 || status == 0
}

func jsonNumberToInt(value any) (int, bool) {
	int64Value, ok := jsonNumberToInt64(value)
	if !ok || int64Value > math.MaxInt || int64Value < math.MinInt {
		return 0, false
	}
	return int(int64Value), true
}

func jsonNumberToInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
