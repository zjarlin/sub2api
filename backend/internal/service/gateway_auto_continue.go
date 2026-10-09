package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// forwardWithAutoContinue 将助手停顿及内部选择隐藏在同一个 HTTP 回合中。
// 客户端只接收最终真实响应 ID 和工具调用，不需要理解新的事件或伪造用户回复。
func (s *OpenAIGatewayService) forwardWithAutoContinue(ctx context.Context, c *gin.Context, account *Account, body []byte) (result *OpenAIForwardResult, err error) {
	body, err = s.restoreDeepSeekCompaction(body)
	if err != nil {
		return nil, err
	}
	clientWriter, clientRequest := c.Writer, c.Request
	startedAt := time.Now()
	var firstClientByte *int
	defer func() {
		c.Writer, c.Request = clientWriter, clientRequest
		if result != nil {
			result.Duration = time.Since(startedAt)
			if firstClientByte != nil {
				result.FirstTokenMs = firstClientByte
			}
		}
	}()
	p := s.autoContinuePolicy()
	var previousWriter *autoContinueWriter
	var previousResult *OpenAIForwardResult
	var previousErr error
	var previousBody []byte
	var previousPricingAt time.Time
	var decisions []*AutoContinueDecision
	rounds := 0
	compacted := false
	for {
		writer := newAutoContinueWriter(clientWriter)
		writer.onRelease = func() {
			if firstClientByte == nil {
				elapsed := int(time.Since(startedAt).Milliseconds())
				firstClientByte = &elapsed
			}
		}
		writer.reportItems = autoContinueReportItems(body, decisions, getAPIKeyIDFromContext(c), s.cfg.JWT.Secret)
		if rounds > 0 || compacted {
			writer.Header().Set("X-Sub2API-Auto-Continue", "continued")
		}
		if compacted {
			writer.Header().Set("X-Sub2API-Auto-Compaction", "compacted")
		}
		c.Writer = writer
		request := clientRequest.Clone(ctx)
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = int64(len(body))
		if compacted {
			// 压缩后的历史不再沿用压缩前的不透明回合游标；会话关联仍保留。
			request.Header.Del(openAICodexTurnStateHeader)
			request.Header.Del("x-codex-turn-metadata")
		}
		c.Request = request
		pricingAt := time.Now()
		result, err := s.forwardOnce(ctx, c, account, body)
		contextError := autoContinueContextError(writer)
		if !compacted && contextError && ctx.Err() == nil {
			nextBody, decision, compactErr := s.compactAutoContinue(ctx, c, account, body, "")
			if compactErr == nil {
				compactErr = s.recordAutoContinueDecision(ctx, c, body, decision)
			}
			if compactErr == nil {
				if previousResult != nil {
					appendAutoContinueUsage(c, account, previousBody, previousPricingAt, previousResult)
				}
				previousWriter, previousResult, previousErr = writer, result, err
				previousBody, previousPricingAt = body, pricingAt
				body, compacted = nextBody, true
				decisions = append(decisions, decision)
				logger.FromContext(ctx).Info("gateway.auto_compaction", zap.Int64("account_id", account.ID))
				continue
			}
			writer.Header().Set("X-Sub2API-Auto-Compaction", "failed")
			logger.FromContext(ctx).Warn("gateway.auto_compaction_failed", zap.Int64("account_id", account.ID), zap.Error(compactErr))
		}
		failed := err != nil || result == nil || contextError || writer.Status() >= 400
		if failed && previousWriter != nil && !writer.live && ctx.Err() == nil {
			// 已有成功结果时，续跑故障不丢掉原来的询问，也不将额外错误发给客户端。
			if result != nil {
				appendAutoContinueUsage(c, account, body, pricingAt, result)
			}
			logger.FromContext(ctx).Warn("gateway.auto_continue_forward_failed", zap.Int64("account_id", account.ID), zap.Error(err))
			previousWriter.reportItems = autoContinueReportItems(previousBody, decisions, getAPIKeyIDFromContext(c), s.cfg.JWT.Secret)
			previousWriter.Header().Set("X-Sub2API-Auto-Continue", "forward_failed")
			if compacted {
				previousWriter.Header().Set("X-Sub2API-Auto-Compaction", "retry_failed")
			}
			if flushErr := previousWriter.release(); flushErr != nil {
				return previousResult, flushErr
			}
			return previousResult, previousErr
		}
		if previousResult != nil {
			appendAutoContinueUsage(c, account, previousBody, previousPricingAt, previousResult)
		}
		if failed || writer.live || rounds >= p.MaxRounds || ctx.Err() != nil {
			if rounds > 0 && !writer.live {
				writer.Header().Set("X-Sub2API-Auto-Continue", "continued")
			}
			flushErr := writer.release()
			if err != nil {
				return result, err
			}
			return result, flushErr
		}
		response := writer.body.Bytes()
		if isEventStreamResponse(writer.Header()) {
			var ok bool
			response, ok = extractCodexFinalResponse(string(response))
			if !ok {
				return result, writer.release()
			}
		}
		candidate := autoContinueExtractCandidate(response)
		if candidate == nil {
			return result, writer.release()
		}
		var nextBody []byte
		var decision *AutoContinueDecision
		var decisionErr error
		needsCompaction := !compacted && len(candidate.Questions) == 0 && autoContinueNeedsCompaction(candidate.Text)
		if needsCompaction {
			nextBody, decision, decisionErr = s.compactAutoContinue(ctx, c, account, body, candidate.Text)
		} else {
			decision, decisionErr = s.decideAutoContinue(ctx, c, account, body, candidate)
		}
		if decisionErr != nil {
			logger.FromContext(ctx).Warn("gateway.auto_continue_decision_failed", zap.Int64("account_id", account.ID), zap.Error(decisionErr))
			writer.Header().Set("X-Sub2API-Auto-Continue", "decision_unavailable")
			return result, writer.release()
		}
		if decision == nil {
			return result, writer.release()
		}
		if recordErr := s.recordAutoContinueDecision(ctx, c, body, decision); recordErr != nil {
			logger.FromContext(ctx).Warn("gateway.auto_continue_record_failed", zap.Error(recordErr))
			writer.Header().Set("X-Sub2API-Auto-Continue", "record_failed")
			return result, writer.release()
		}
		if nextBody == nil {
			var buildErr error
			nextBody, buildErr = autoContinueBuildRequest(body, response, candidate, decision)
			if buildErr != nil {
				logger.FromContext(ctx).Warn("gateway.auto_continue_replay_failed", zap.Error(buildErr))
				return result, writer.release()
			}
		}
		previousWriter, previousResult = writer, result
		previousErr = nil
		previousBody, previousPricingAt = body, pricingAt
		body = nextBody
		compacted = compacted || needsCompaction
		decisions = append(decisions, decision)
		rounds++
		logger.FromContext(ctx).Info("gateway.auto_continue", zap.Int64("account_id", account.ID), zap.Int("round", rounds), zap.Int("questions", len(candidate.Questions)))
	}
}

// 内部成功回合复用已有辅助用量队列，与最终结果分别入账，失败回退也不重复计费。
func appendAutoContinueUsage(c *gin.Context, account *Account, body []byte, pricingAt time.Time, result *OpenAIForwardResult) {
	copyResult := *result
	copyResult.RequestID = "auto_continue:" + generateRequestID()
	appendVisionFallbackUsage(c, VisionFallbackUsage{Result: &copyResult, Account: account, PayloadHash: HashUsageRequestPayload(body), PricingAt: pricingAt})
}

const autoContinueBufferLimit = 256 << 10

// 小型终态回复暂存至判断结束；发现执行工具或超出缓冲上限就立即恢复普通流式透传。
// 这样不向客户端泄漏已代答的追问调用，也不重写响应 ID、序号或工具身份。
type autoContinueWriter struct {
	gin.ResponseWriter
	header       http.Header
	body         bytes.Buffer
	status       int
	written      bool
	live         bool
	inspected    int
	contentSeen  bool
	startedAt    time.Time
	reportItems  []json.RawMessage
	livePending  bytes.Buffer
	lastSequence int64
	onRelease    func()
}

func newAutoContinueWriter(client gin.ResponseWriter) *autoContinueWriter {
	return &autoContinueWriter{ResponseWriter: client, header: client.Header().Clone(), status: http.StatusOK, startedAt: time.Now()}
}

func (w *autoContinueWriter) Header() http.Header {
	if w.live {
		return w.ResponseWriter.Header()
	}
	return w.header
}

func (w *autoContinueWriter) Status() int {
	if w.live {
		return w.ResponseWriter.Status()
	}
	return w.status
}

func (w *autoContinueWriter) Size() int {
	if w.live {
		return w.ResponseWriter.Size()
	}
	if !w.written {
		return -1
	}
	return w.body.Len()
}

func (w *autoContinueWriter) Written() bool {
	return w.written || w.ResponseWriter.Written()
}

func (w *autoContinueWriter) WriteHeader(status int) {
	if w.live {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if !w.written {
		w.status = status
	}
}

func (w *autoContinueWriter) WriteHeaderNow() {
	if w.live {
		w.ResponseWriter.WriteHeaderNow()
	}
	w.written = true
}

func (w *autoContinueWriter) Write(data []byte) (int, error) {
	if w.live {
		return w.writeLive(data)
	}
	w.written = true
	if w.body.Len()+len(data) > autoContinueBufferLimit {
		if err := w.release(); err != nil {
			return 0, err
		}
		return w.writeLive(data)
	}
	n, err := w.body.Write(data)
	if err != nil {
		return n, err
	}
	if !isEventStreamResponse(w.header) {
		return n, nil
	}
	pending := w.body.Bytes()[w.inspected:]
	boundary := bytes.LastIndex(pending, []byte("\n\n"))
	if boundary < 0 {
		return n, nil
	}
	liveTool := false
	forEachOpenAISSEFrame(string(pending[:boundary+2]), func(eventType string, frame []byte) {
		if strings.HasSuffix(eventType, ".delta") || eventType == "response.completed" {
			w.contentSeen = true
		}
		if eventType != "response.output_item.added" && eventType != "response.output_item.done" {
			return
		}
		item := gjson.GetBytes(frame, "item")
		switch item.Get("type").String() {
		case "message", "reasoning", "":
		case "function_call":
			name := item.Get("name").String()
			if name != "" && !autoContinueIsQuestionTool(name) {
				liveTool = true
			}
		default:
			liveTool = true
		}
	})
	w.inspected += boundary + 2
	// created/心跳与半帧不代表已有可见输出；晚到的终态错误仍可先恢复再向客户端提交。
	if liveTool || (w.contentSeen && time.Since(w.startedAt) > 15*time.Second && !autoContinueContextError(w)) {
		return n, w.release()
	}
	return n, nil
}

func (w *autoContinueWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func (w *autoContinueWriter) Flush() {
	if w.live {
		w.ResponseWriter.Flush()
	}
}

func (w *autoContinueWriter) release() error {
	if w.live {
		return nil
	}
	// 未写出任何字节的 failover 保留外层换号机会，不能仅为冲刷空缓冲提交 200。
	if !w.written {
		return nil
	}
	for key := range w.ResponseWriter.Header() {
		delete(w.ResponseWriter.Header(), key)
	}
	for key, values := range w.header {
		w.ResponseWriter.Header()[key] = append([]string(nil), values...)
	}
	// 最终输出可能与上一回合长度不同，不能沿用暂存响应的长度声明。
	w.ResponseWriter.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(w.status)
	w.live = true
	if w.onRelease != nil {
		w.onRelease()
	}
	data := append([]byte(nil), w.body.Bytes()...)
	w.body.Reset()
	if !isEventStreamResponse(w.header) && len(w.reportItems) > 0 && w.status < 400 {
		data = autoContinueAddReportJSON(data, w.reportItems)
		w.reportItems = nil
	}
	_, err := w.writeLive(data)
	if strings.Contains(w.header.Get("Content-Type"), "text/event-stream") {
		w.ResponseWriter.Flush()
	}
	return err
}
