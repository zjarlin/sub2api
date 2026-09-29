package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// ccStreamError 保存 HTTP 200 流中的业务错误，阻止协议转换把它合成为成功终态。
type ccStreamError struct {
	Payload []byte
	Code    string
	Type    string
	Message string
}

func (e *ccStreamError) Error() string {
	return "upstream response failed: " + e.Message
}

func parseCCStreamError(payload, eventName string) *ccStreamError {
	if !gjson.Valid(payload) {
		return nil
	}
	root := gjson.Parse(payload)
	errorBody := root.Get("error")
	if !errorBody.IsObject() {
		if eventName != "error" && root.Get("type").String() != "error" {
			return nil
		}
		errorBody = root
	}
	code := strings.TrimSpace(errorBody.Get("code").String())
	if code == "" {
		code = "upstream_error"
	}
	errType := strings.TrimSpace(errorBody.Get("type").String())
	if errType == "" || errType == "error" {
		errType = "upstream_error"
	}
	message := sanitizeUpstreamErrorMessage(strings.TrimSpace(errorBody.Get("message").String()))
	if message == "" {
		message = "Upstream Chat Completions stream failed"
	}
	// 统一包成 error 对象，使现有语义状态、故障转移和错误透传规则均能识别。
	body, _ := json.Marshal(gin.H{"error": errorBody.Value()})
	return &ccStreamError{Payload: body, Code: code, Type: errType, Message: message}
}

// handleCCStreamError 复用现有账号故障处理；已输出时只回报失败，禁止拼接重试流。
func (s *OpenAIGatewayService) handleCCStreamError(
	c *gin.Context,
	account *Account,
	resp *http.Response,
	upstreamModel string,
	streamErr *ccStreamError,
	clientOutputStarted bool,
	writeError func(status int, code, errType, message string),
) error {
	requestID := resp.Header.Get("x-request-id")
	if !clientOutputStarted && openAIStreamErrorEventShouldFailover(streamErr.Payload, streamErr.Message) {
		return s.newOpenAIStreamFailoverErrorWithModel(c, account, false, requestID, streamErr.Payload, streamErr.Message, upstreamModel, resp.Header)
	}
	status, _ := s.handleOpenAIStreamTerminalAccountSideEffects(c, account, streamErr.Payload, streamErr.Message, resp.Header, upstreamModel)
	message := s.recordOpenAIStreamUpstreamError(c, account, false, requestID, "http_error", streamErr.Payload, streamErr.Message)
	errType := streamErr.Type
	if ruleStatus, ruleType, ruleMessage, matched := applyOpenAIStreamFailedErrorPassthroughRule(c, account.Platform, streamErr.Payload, message); matched {
		status, errType = ruleStatus, ruleType
		if ruleMessage != "" {
			message = ruleMessage
		}
	}
	// HTTP 200 已提交或 WebSocket 无 HTTP 错误码时，仍须按请求失败计入监控。
	MarkOpsStreamFailure(c, errType, streamErr.Code, message, status)
	writeError(status, streamErr.Code, errType, message)
	MarkResponseCommitted(c)
	return fmt.Errorf("upstream response failed: %s", message)
}
