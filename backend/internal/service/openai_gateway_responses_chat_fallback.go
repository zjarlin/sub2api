package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

type responsesStreamEventWriter interface {
	WriteResponsesEvent(apicompat.ResponsesStreamEvent) error
}

// forwardResponsesViaRawChatCompletions serves /v1/responses clients through an
// upstream that only supports /v1/chat/completions.
func (s *OpenAIGatewayService) forwardResponsesViaRawChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		writeOpenAIResponsesFallbackError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := strings.TrimSpace(responsesReq.Model)
	if originalModel == "" {
		writeOpenAIResponsesFallbackError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	clientStream := responsesReq.Stream
	// custom 工具（如 codex 的 exec）降级为 function 工具转发，回程需按名字还原为
	// custom_tool_call 项，先记下名字集合；tool_search 工具同理，回程还原为
	// tool_search_call 项；namespace 子工具（如 MCP 工具）摊平转发，回程按映射还原
	// 为带 namespace 字段的 function_call 项。
	effectiveTools, err := apicompat.EffectiveResponsesTools(&responsesReq)
	if err != nil {
		writeOpenAIResponsesFallbackError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("resolve responses tools: %w", err)
	}
	customTools := apicompat.CustomToolNames(effectiveTools)
	functionTools := apicompat.FunctionToolNames(effectiveTools)
	toolSearch := apicompat.HasToolSearchTool(effectiveTools)
	namespaceTools := apicompat.NamespaceToolNames(effectiveTools)

	// 自愈回写：历史里带明文 summary 的 reasoning item 刷新进缓存，覆盖 Redis
	// 被 flush / 跨实例漂移后同 id 的 encrypted-only 副本无法再取明文的情况。
	s.recacheReasoningItemsFromInput(responsesReq.Input)

	chatReq, err := apicompat.ResponsesToChatCompletionsRequestWithOptions(&responsesReq, &apicompat.ResponsesToChatOptions{
		ReasoningContentByID: s.reasoningContentByID,
		EnforceAgentLoop:     true,
	})
	if err != nil {
		writeOpenAIResponsesFallbackError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert responses to chat completions: %w", err)
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, "")
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	if err := validateGPT61SolCompatRequest(body, upstreamModel); err != nil {
		writeOpenAIResponsesFallbackError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	if openai.IsGPT61SolModelSpelling(upstreamModel) && len(effectiveTools) > 0 {
		err := fmt.Errorf("gpt-6.1-sol requires Responses for tool calls; this account only supports Chat Completions")
		writeOpenAIResponsesFallbackError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	// fork: Auto 模型路由的排除判定必须在本分支出站前生效。
	if err := checkAutoModelUpstream(ctx, originalModel, billingModel, upstreamModel); err != nil {
		return nil, err
	}
	reasoningEffort := extractOpenAIReasoningEffortFromBody(body, upstreamModel, billingModel, originalModel)
	// 国产模型默认 effort 补充：需要 mappedModel 判定，推迟到 billingModel 算出之后。
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, body, billingModel)
	if account.Platform == PlatformVibex {
		reasoningEffort = nil
	}
	chatReq.Model = upstreamModel
	if clientStream {
		chatReq.StreamOptions = &apicompat.ChatStreamOptions{IncludeUsage: true}
	}

	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completions fallback request: %w", err)
	}
	chatBody, err = s.applyOpenAIFastPolicyToBody(ctx, account, upstreamModel, chatBody)
	if err != nil {
		var blocked *OpenAIFastBlockedError
		if errors.As(err, &blocked) {
			writeOpenAIFastPolicyBlockedResponse(c, blocked)
		}
		return nil, err
	}
	// /v1/responses 降级到 raw CC 的出站与 forwardAsRawChatCompletions 共用同一个
	// 独立 Ollama Cloud token 钩子；chatReq.Model 已是模型映射后的 upstreamModel。
	chatBody = clampOllamaCloudUpstreamMaxTokens(account, chatBody)
	// 易退化模型的出站输出上限兜底钳制（见 gateway.degeneration.*）：检测熔断是主防线，
	// 这里保证即使退化不是严格周期性的也仍然有界。
	chatBody, _ = s.clampDegenerationProneUpstreamMaxTokens([]string{originalModel, billingModel, upstreamModel}, chatBody)
	chatBody, kimiEffort, err := normalizeNVIDIAKimiK3ReasoningEffort(ctx, c, account, upstreamModel, chatBody)
	if err != nil {
		if IsReasoningEffortPolicyDenied(err) {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			writeOpenAIResponsesFallbackError(c, http.StatusForbidden, "permission_error", err.Error())
		}
		return nil, err
	}
	if kimiEffort != nil {
		reasoningEffort = kimiEffort
	}
	// Keep the final outbound tier for usage-time reconciliation. A policy
	// filter that removes the field therefore leaves this nil.
	serviceTier := extractOpenAIServiceTierFromBody(chatBody)

	logger.L().Debug("openai responses: forwarding via raw chat completions",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
	)
	SetOpsUpstreamModel(c, upstreamModel)

	// Build and send upstream request via the shared CC pipeline
	apiKey, targetURL, err := s.resolveCCFallbackTarget(account)
	if err != nil {
		return nil, err
	}
	resp, err := s.sendCCUpstreamRequest(ctx, c, account, targetURL, chatBody, clientStream, apiKey, account.GetOpenAIUserAgent(), "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if _, websocket := c.Writer.(responsesStreamEventWriter); websocket {
		stopCancelBody := context.AfterFunc(ctx, func() { _ = resp.Body.Close() })
		defer stopCancelBody()
	}

	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := s.readOpenAIUpstreamError(resp)
		if foErr := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		return s.handleErrorResponse(ctx, resp, c, account, chatBody, billingModel)
	}

	if clientStream {
		return s.streamChatCompletionsAsResponses(c, resp, account, originalModel, customTools, functionTools, toolSearch, namespaceTools, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime, chatBody, targetURL, apiKey, chatToolsFromBody(chatBody))
	}
	return s.bufferChatCompletionsAsResponses(c, resp, account, originalModel, customTools, functionTools, toolSearch, namespaceTools, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime)
}

func (s *OpenAIGatewayService) bufferChatCompletionsAsResponses(
	c *gin.Context,
	resp *http.Response,
	account *Account,
	originalModel string,
	customTools map[string]bool,
	functionTools map[string]bool,
	toolSearch bool,
	namespaceTools map[string]apicompat.NamespacedToolName,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	ccResp, usage, err := s.readCCUpstreamJSONResponse(c, resp, account, writeOpenAIResponsesFallbackError)
	if err != nil {
		return nil, err
	}
	responsesResp := apicompat.ChatCompletionsResponseToResponses(ccResp, originalModel, customTools, functionTools, toolSearch, namespaceTools)
	s.cacheReasoningItemsFromOutput(responsesResp.Output)

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	// GLM 等只支持 Chat Completions 的上游无法返回原生 compaction item：
	// 摘要回合的普通 message 结果必须在此压成 Codex 要求的单个 compaction 输出。
	if isDeepSeekNativeCompaction(c) {
		converted, err := json.Marshal(responsesResp)
		if err != nil {
			return nil, fmt.Errorf("marshal compact fallback response: %w", err)
		}
		if err := s.writeCompatibleCompactionJSON(c, http.StatusOK, converted); err != nil {
			return nil, fmt.Errorf("convert compact fallback response: %w", err)
		}
		return &OpenAIForwardResult{
			RequestID:       requestID,
			Usage:           usage,
			Model:           originalModel,
			BillingModel:    billingModel,
			UpstreamModel:   upstreamModel,
			ReasoningEffort: reasoningEffort,
			ServiceTier:     resolvedOpenAIUpstreamServiceTier(c, serviceTier),
			Stream:          false,
			Duration:        time.Since(startTime),
		}, nil
	}
	c.JSON(http.StatusOK, responsesResp)

	return &OpenAIForwardResult{
		RequestID:                   requestID,
		UpstreamHeaders:             resp.Header,
		Usage:                       usage,
		Model:                       originalModel,
		BillingModel:                billingModel,
		UpstreamModel:               upstreamModel,
		ReasoningEffort:             reasoningEffort,
		UpstreamResponseServiceTier: observedUpstreamResponseServiceTier(c),
		ServiceTier:                 resolvedOpenAIUpstreamServiceTier(c, serviceTier),
		Stream:                      false,
		Duration:                    time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) streamChatCompletionsAsResponses(
	c *gin.Context,
	resp *http.Response,
	account *Account,
	originalModel string,
	customTools map[string]bool,
	functionTools map[string]bool,
	toolSearch bool,
	namespaceTools map[string]apicompat.NamespacedToolName,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
	chatBody []byte,
	targetURL string,
	apiKey string,
	tools []apicompat.ChatTool,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	writeStreamHeaders := s.newStreamHeaderWriter(c, resp.Header)

	state := apicompat.NewChatCompletionsToResponsesStreamState(originalModel)
	state.CustomTools = customTools
	state.FunctionTools = functionTools
	state.ToolSearchDeclared = toolSearch
	state.NamespaceTools = namespaceTools
	// 退化重复检测熔断：模型卡在短周期复读时中止本次响应，而不是让它跑满输出上限。
	state.DegenerationGuard = s.chatDegenerationGuard()
	clientDisconnected := false
	eventWriter, _ := c.Writer.(responsesStreamEventWriter)
	var totalUsage OpenAIUsage
	var firstTokenMs *int
	currentResp := resp
	currentChatBody := append([]byte(nil), chatBody...)

	writeEvents := func(events []apicompat.ResponsesStreamEvent) {
		if clientDisconnected || len(events) == 0 {
			return
		}
		writeStreamHeaders()
		for _, event := range events {
			if eventWriter != nil {
				if err := eventWriter.WriteResponsesEvent(event); err != nil {
					clientDisconnected = true
					return
				}
				continue
			}
			sse, err := apicompat.ResponsesEventToSSE(event)
			if err != nil {
				logger.L().Warn("openai responses chat fallback: failed to marshal stream event",
					zap.Error(err),
					zap.String("request_id", requestID),
				)
				continue
			}
			if _, err := fmt.Fprint(c.Writer, sse); err != nil {
				clientDisconnected = true
				logger.L().Debug("openai responses chat fallback: client disconnected, continuing to drain upstream for billing",
					zap.Error(err),
					zap.String("request_id", requestID),
				)
				return
			}
		}
		c.Writer.Flush()
	}

	// degenerated 记录退化重复熔断的判定结果；命中后立刻关闭上游响应体停止读取，
	// 避免继续烧 token 与时长，并在扫描结束后以 response.failed 显式上报。
	var degenerated *apicompat.ChatDegeneration
	emitChunk := func(chunk *apicompat.ChatCompletionsChunk) {
		events := apicompat.ChatCompletionsChunkToResponsesEvents(chunk, state)
		if detected := state.DetectDegeneration(); detected != nil {
			degenerated = detected
			if currentResp != nil && currentResp.Body != nil {
				_ = currentResp.Body.Close()
			}
			return
		}
		s.cacheReasoningItemsFromEvents(events)
		writeEvents(events)
	}

	var scan ccStreamScanState
	scanSawDone := false
	for continuation := 0; ; continuation++ {
		scan = s.scanCCStream(c, currentResp, "openai responses chat fallback", requestID, startTime, emitChunk)
		scanSawDone = scanSawDone || scan.SawDone
		addOpenAIUsage(&totalUsage, scan.Usage)
		if firstTokenMs == nil {
			firstTokenMs = scan.FirstTokenMs
		}
		// 退化重复优先于读取错误：上游响应体是我们主动关闭的，那不是上游故障。
		if degenerated != nil {
			var accountID int64
			if account != nil {
				accountID = account.ID
			}
			message := degenerationMessage(degenerated)
			s.recordDegenerationAbort(account, originalModel)
			writeEvents([]apicompat.ResponsesStreamEvent{{
				Type: "response.failed", SequenceNumber: state.SequenceNumber,
				Response: &apicompat.ResponsesResponse{
					ID: state.ResponseID, Object: "response", CreatedAt: state.Created,
					Model: originalModel, Status: "failed", Output: []apicompat.ResponsesOutput{},
					Error: &apicompat.ResponsesError{Code: OpenAIUpstreamDegenerationCode, Message: message},
				},
			}})
			logger.L().Warn("openai responses chat fallback: aborted degenerate upstream stream",
				zap.String("request_id", requestID),
				zap.Int64("account_id", accountID),
				zap.String("kind", degenerated.Kind),
				zap.String("channel", degenerated.Channel),
				zap.Int("period_runes", degenerated.PeriodRunes),
				zap.Int("repeats", degenerated.Repeats),
				zap.Int("distinct_lines", degenerated.DistinctLines),
				zap.Int("markup_closes", degenerated.MarkupCloses),
				zap.Int("total_runes", degenerated.TotalRunes),
				zap.Int("output_tokens", totalUsage.OutputTokens),
			)
			return &OpenAIForwardResult{
				RequestID:                   requestID,
				UpstreamHeaders:             currentResp.Header,
				Usage:                       totalUsage,
				Model:                       originalModel,
				BillingModel:                billingModel,
				UpstreamModel:               upstreamModel,
				ReasoningEffort:             reasoningEffort,
				UpstreamResponseServiceTier: observedUpstreamResponseServiceTier(c),
				ServiceTier:                 resolvedOpenAIUpstreamServiceTier(c, serviceTier),
				Stream:                      true,
				Duration:                    time.Since(startTime),
				FirstTokenMs:                firstTokenMs,
			}, newOpenAIUpstreamDegenerationError(message)
		}
		// 首次输出前的传输中断仍可透明换号；已提交响应后保留原失败与部分用量。
		if scan.Err != nil && !state.CreatedSent {
			scan.Err = s.newOpenAICompatBufferedReadFailoverError(c, account, currentResp, requestID, scan.Err)
			var failover *UpstreamFailoverError
			if errors.As(scan.Err, &failover) {
				return nil, scan.Err
			}
		}

		var streamErr *ccStreamError
		if errors.As(scan.Err, &streamErr) {
			scan.Err = s.handleCCStreamError(c, account, currentResp, upstreamModel, streamErr, state.CreatedSent, func(status int, code, errType, message string) {
				if clientDisconnected {
					return
				}
				if !state.CreatedSent && !c.Writer.Written() && eventWriter == nil {
					c.JSON(status, gin.H{"error": gin.H{"code": code, "type": errType, "message": message}})
					return
				}
				writeEvents([]apicompat.ResponsesStreamEvent{{
					Type: "response.failed", SequenceNumber: state.SequenceNumber,
					Response: &apicompat.ResponsesResponse{
						ID: state.ResponseID, Object: "response", CreatedAt: state.Created,
						Model: originalModel, Status: "failed", Output: []apicompat.ResponsesOutput{},
						Error: &apicompat.ResponsesError{Code: code, Message: message},
					},
				}})
			})
			// 额度拒绝等零用量失败不产生成功用量行；有真实用量时沿用部分计费路径。
			var failoverErr *UpstreamFailoverError
			if errors.As(scan.Err, &failoverErr) || !openAIUsageHasTokens(&scan.Usage) {
				return nil, scan.Err
			}
		}
		if scan.Err != nil {
			if streamErr == nil {
				scan.Err = fmt.Errorf("stream usage incomplete: %w", scan.Err)
			}
			return &OpenAIForwardResult{
				RequestID:                   requestID,
				UpstreamHeaders:             currentResp.Header,
				Usage:                       totalUsage,
				Model:                       originalModel,
				BillingModel:                billingModel,
				UpstreamModel:               upstreamModel,
				ReasoningEffort:             reasoningEffort,
				UpstreamResponseServiceTier: observedUpstreamResponseServiceTier(c),
				ServiceTier:                 resolvedOpenAIUpstreamServiceTier(c, serviceTier),
				Stream:                      true,
				Duration:                    time.Since(startTime),
				FirstTokenMs:                firstTokenMs,
			}, scan.Err
		}
		if err := state.ValidateToolCallArguments(); err != nil {
			return &OpenAIForwardResult{
				RequestID:                   requestID,
				UpstreamHeaders:             currentResp.Header,
				Usage:                       totalUsage,
				Model:                       originalModel,
				BillingModel:                billingModel,
				UpstreamModel:               upstreamModel,
				ReasoningEffort:             reasoningEffort,
				UpstreamResponseServiceTier: observedUpstreamResponseServiceTier(c),
				ServiceTier:                 resolvedOpenAIUpstreamServiceTier(c, serviceTier),
				Stream:                      true,
				Duration:                    time.Since(startTime),
				FirstTokenMs:                firstTokenMs,
			}, fmt.Errorf("invalid tool call arguments from upstream: %w", err)
		}

		if continuation < 2 && shouldRetryChatAgentLoop(state, tools) {
			nextBody, nextErr := buildChatAgentLoopContinuationBody(currentChatBody, state.Text.String())
			if nextErr != nil {
				logger.L().Warn("openai responses chat fallback: build agent-loop continuation failed",
					zap.Error(nextErr),
					zap.String("request_id", requestID),
				)
				break
			}
			if currentResp.Body != nil {
				_ = currentResp.Body.Close()
			}
			nextResp, sendErr := s.sendCCUpstreamRequest(c.Request.Context(), c, account, targetURL, nextBody, true, apiKey, account.GetOpenAIUserAgent(), "")
			if sendErr != nil || nextResp == nil || nextResp.StatusCode >= 400 {
				if nextResp != nil && nextResp.Body != nil {
					_ = nextResp.Body.Close()
				}
				logger.L().Warn("openai responses chat fallback: agent-loop continuation failed",
					zap.Error(sendErr),
					zap.String("request_id", requestID),
				)
				message := "Upstream stopped after a progress update and automatic continuation failed; please retry"
				writeEvents([]apicompat.ResponsesStreamEvent{{
					Type: "response.failed", SequenceNumber: state.SequenceNumber,
					Response: &apicompat.ResponsesResponse{
						ID: state.ResponseID, Object: "response", CreatedAt: state.Created,
						Model: originalModel, Status: "failed", Output: []apicompat.ResponsesOutput{},
						Error: &apicompat.ResponsesError{Code: "upstream_agent_loop_continuation_failed", Message: message},
					},
				}})
				return &OpenAIForwardResult{
					RequestID: requestID, UpstreamHeaders: currentResp.Header, Usage: totalUsage,
					Model: originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel,
					ReasoningEffort: reasoningEffort, ServiceTier: resolvedOpenAIUpstreamServiceTier(c, serviceTier),
					Stream: true, Duration: time.Since(startTime), FirstTokenMs: firstTokenMs,
				}, errors.New(message)
			}
			segmentEvents := apicompat.CloseChatMessageItem(state)
			s.cacheReasoningItemsFromEvents(segmentEvents)
			writeEvents(segmentEvents)
			currentResp = nextResp
			currentChatBody = nextBody
			// 续跑 segment 新建 message/tool item；清理只属于上一段的 finish 状态。
			state.FinishReason = ""
			state.CompletedSent = false
			continue
		}
		break
	}

	finalEvents := apicompat.FinalizeChatCompletionsResponsesStream(state)
	s.cacheReasoningItemsFromEvents(finalEvents)
	writeEvents(finalEvents)
	if !clientDisconnected && eventWriter == nil {
		writeStreamHeaders()
		if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err != nil {
			clientDisconnected = true
		}
		if !clientDisconnected {
			c.Writer.Flush()
		}
	}
	if !scanSawDone {
		logCCStreamMissingDoneSentinel("openai responses chat fallback", requestID)
	}

	return &OpenAIForwardResult{
		RequestID:                   requestID,
		UpstreamHeaders:             currentResp.Header,
		Usage:                       totalUsage,
		Model:                       originalModel,
		BillingModel:                billingModel,
		UpstreamModel:               upstreamModel,
		ReasoningEffort:             reasoningEffort,
		UpstreamResponseServiceTier: observedUpstreamResponseServiceTier(c),
		ServiceTier:                 resolvedOpenAIUpstreamServiceTier(c, serviceTier),
		Stream:                      true,
		Duration:                    time.Since(startTime),
		FirstTokenMs:                firstTokenMs,
	}, nil
}

func chatToolsFromBody(body []byte) []apicompat.ChatTool {
	var req apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil
	}
	return req.Tools
}

func shouldRetryChatAgentLoop(state *apicompat.ChatCompletionsToResponsesStreamState, tools []apicompat.ChatTool) bool {
	if state == nil || len(tools) == 0 || len(state.ToolCalls) > 0 || state.FinishReason != "stop" {
		return false
	}
	text := strings.TrimSpace(state.Text.String())
	if text == "" || len(text) > 600 {
		return false
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{"已完成", "已经完成", "修改完成", "测试通过", "implemented", "completed"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	for _, marker := range []string{
		"我会", "我准备", "我将", "接下来", "下一步", "然后我会",
		"i will", "i'll", "next,", "next i", "now i", "let me", "i'm going to",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func buildChatAgentLoopContinuationBody(body []byte, progress string) ([]byte, error) {
	progress = strings.TrimSpace(progress)
	if progress == "" {
		return nil, fmt.Errorf("empty progress text")
	}
	assistant, err := json.Marshal(progress)
	if err != nil {
		return nil, err
	}
	next, err := sjson.SetRawBytes(body, "messages.-1", []byte(`{"role":"assistant","content":`+string(assistant)+`}`))
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(next, "messages.-1", []byte(`{"role":"user","content":"Continue the task now. Do not repeat a progress update. Perform the next required tool call in this response if work remains; otherwise provide the complete final answer."}`))
}

func chatChunkStartsResponsesOutput(chunk *apicompat.ChatCompletionsChunk) bool {
	if chunk == nil {
		return false
	}
	for _, choice := range chunk.Choices {
		delta := choice.Delta
		if (delta.Content != nil && *delta.Content != "") ||
			(delta.ReasoningContent != nil && *delta.ReasoningContent != "") ||
			(delta.Reasoning != nil && *delta.Reasoning != "") {
			return true
		}
		for _, tool := range delta.ToolCalls {
			// 索引与 function 类型仅为前导；未知工具类型保守视为已经输出。
			if tool.ID != "" || tool.Function.Name != "" || tool.Function.Arguments != "" ||
				(tool.Type != "" && tool.Type != "function") {
				return true
			}
		}
	}
	return false
}

// responsesReasoningCacheTTL 是 reasoning 缓存（按 reasoning item id）的过期时间。
// Codex 会话可能跨多天恢复历史，取 7 天。
const responsesReasoningCacheTTL = 7 * 24 * time.Hour

// reasoningContentByID 按 reasoning item id 回查缓存的 reasoning 全文，供
// Responses→CC 桥接在客户端不回传明文 summary（encrypted-only reasoning
// item）时回注 reasoning_content。任何失败都 fail-open 返回 ""（维持桥接原
// 行为），因为缓存只是优化而非正确性前提。
func (s *OpenAIGatewayService) reasoningContentByID(itemID string) string {
	if s == nil || s.cache == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	content, err := s.cache.GetReasoningContent(ctx, itemID)
	if err != nil {
		return ""
	}
	return content
}

// recacheReasoningItemsFromInput 把请求历史里带明文 summary 的 reasoning item
// 重新写入缓存（best-effort）。Codex 多数时候会原样回传明文 summary，借机
// 刷新 TTL 并自愈 Redis 被 flush / 跨实例漂移造成的缓存缺失。
func (s *OpenAIGatewayService) recacheReasoningItemsFromInput(inputRaw json.RawMessage) {
	if s == nil || s.cache == nil {
		return
	}
	inputRaw = bytes.TrimSpace(inputRaw)
	if len(inputRaw) == 0 || inputRaw[0] != '[' {
		return
	}
	var items []json.RawMessage
	if err := json.Unmarshal(inputRaw, &items); err != nil {
		return
	}
	for _, raw := range items {
		id, text, ok := apicompat.ExtractResponsesReasoningItem(raw)
		if !ok || id == "" || text == "" {
			continue
		}
		s.setReasoningContent(id, text)
	}
}

// cacheReasoningItemsFromEvents 从 Responses 流事件里提取完成的 reasoning
// item 写入缓存（覆盖一个流中的多个 reasoning item）。
func (s *OpenAIGatewayService) cacheReasoningItemsFromEvents(events []apicompat.ResponsesStreamEvent) {
	for _, event := range events {
		if event.Type != "response.output_item.done" || event.Item == nil {
			continue
		}
		s.cacheReasoningItem(event.Item)
	}
}

// cacheReasoningItemsFromOutput 从非流式 Responses 响应的 output 里提取
// reasoning item 写入缓存。
func (s *OpenAIGatewayService) cacheReasoningItemsFromOutput(output []apicompat.ResponsesOutput) {
	for i := range output {
		s.cacheReasoningItem(&output[i])
	}
}

func (s *OpenAIGatewayService) cacheReasoningItem(item *apicompat.ResponsesOutput) {
	if item == nil || item.Type != "reasoning" || item.ID == "" {
		return
	}
	var parts []string
	for _, sum := range item.Summary {
		if t := strings.TrimSpace(sum.Text); t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return
	}
	s.setReasoningContent(item.ID, strings.Join(parts, "\n"))
}

// setReasoningContent 写入缓存，使用 detached ctx：客户端断连后仍在 drain
// 上游流（计费需要），此时的 reasoning 也是后续轮次回注所依赖的，不能随
// 请求 ctx 一起取消。失败仅记日志，不影响转发。
func (s *OpenAIGatewayService) setReasoningContent(itemID, content string) {
	if s == nil || s.cache == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.cache.SetReasoningContent(ctx, itemID, content, responsesReasoningCacheTTL); err != nil {
		logger.L().Warn("openai responses chat fallback: cache reasoning content failed",
			zap.Error(err),
			zap.String("item_id", itemID),
		)
	}
}

// deepSeekChatReasoningPlaceholderText 是 Chat Completions 侧 thinking-mode
// 占位明文。必须是单个空格：DeepSeek 拒绝空串，非空即可通过；LiteLLM 同样注入
// 单个空格。
const deepSeekChatReasoningPlaceholderText = " "

// targetsDeepSeekAPIHost 报告该账号实际上游是否是 DeepSeek 官方 API。
// platform=deepseek 直接命中；platform=openai 但 base_url 指向
// api.deepseek.com 的映射账号同样命中（Codex 把 GPT 模型名映射到 DeepSeek
// 时的典型接入方式）。
func targetsDeepSeekAPIHost(account *Account) bool {
	if account == nil {
		return false
	}
	if account.Platform == PlatformDeepseek {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(account.GetOpenAIBaseURL()))
	if err != nil {
		return false
	}
	ds, err := url.Parse(DefaultDeepseekBaseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), ds.Hostname())
}

// targetsOpenCodeZenUpstream 报告该账号上游是否是官方 OpenCode Zen / Go 网关。
// platform=opencode_go 直接命中；其他平台按 base_url 主机判定，覆盖用 API Key
// 直接把 openai 平台账号指向 opencode.ai 的接入方式。
func targetsOpenCodeZenUpstream(account *Account) bool {
	if account == nil {
		return false
	}
	if account.IsOpenCodeGo() {
		return true
	}
	return isOfficialOpenCodeHost(account.GetOpenAIBaseURL())
}

// isDeepSeekCatalogModel 判定（剥掉 opencode 前缀后的）模型 ID 是否属于
// DeepSeek 系列，例如 deepseek-v4-flash / deepseek-v4.1-flash / deepseek-chat。
func isDeepSeekCatalogModel(model string) bool {
	return strings.HasPrefix(normalizeOpenCodeGoModelID(model), "deepseek")
}

// requiresDeepSeekChatReasoning 报告该 Chat Completions 请求的上游是否按
// DeepSeek thinking mode 语义校验 reasoning_content。
//
// DeepSeek 官方 API 直接命中。OpenCode Zen / Go 是转发型订阅网关：deepseek-*
// 模型由 DeepSeek 实际承载，同一条 400 原文会被一字不差地回吐，所以按
// 「官方 OpenCode 上游 + deepseek-* 模型」补一条判定。
//
// 不放宽成「任意上游的 deepseek-* 模型名」：占位符是写进请求体的额外字段，
// 对没有这条约束的上游属于无谓改写，且可能触发未知字段校验。
func requiresDeepSeekChatReasoning(account *Account, body []byte) bool {
	if targetsDeepSeekAPIHost(account) {
		return true
	}
	if !targetsOpenCodeZenUpstream(account) {
		return false
	}
	return isDeepSeekCatalogModel(gjson.GetBytes(body, "model").String())
}

// ensureDeepSeekChatReasoningPlaceholders 给缺 reasoning_content 的 assistant
// 消息补单个空格占位。DeepSeek thinking mode 要求历史里每条产生过思维的
// assistant 消息都回传该字段，否则 400
// "The `reasoning_content` in the thinking mode must be passed back to the API"。
//
// 桥接会从 summary / 缓存回注真实明文；这里只填仍为空的缺口，不覆盖已有内容。
// 判定见 requiresDeepSeekChatReasoning，不命中的上游原样返回（字节不变）。
func ensureDeepSeekChatReasoningPlaceholders(account *Account, body []byte) []byte {
	if !requiresDeepSeekChatReasoning(account, body) {
		return body
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body
	}
	updated := body
	changed := false
	for i, msg := range messages.Array() {
		if strings.TrimSpace(msg.Get("role").String()) != "assistant" {
			continue
		}
		if msg.Get("reasoning_content").String() != "" {
			continue
		}
		next, err := sjson.SetBytes(updated, "messages."+strconv.Itoa(i)+".reasoning_content", deepSeekChatReasoningPlaceholderText)
		if err != nil {
			return body
		}
		updated = next
		changed = true
	}
	if !changed {
		return body
	}
	return updated
}
