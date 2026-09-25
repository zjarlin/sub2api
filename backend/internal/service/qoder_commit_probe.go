package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AccountTestModeQoderCommitMessage 让管理员用账号凭据调用 Qoder 的
// 提交消息生成请求，复现 Qoder IDE / CLI 的真实请求形状。
const AccountTestModeQoderCommitMessage = "commit-message"

// testQoderCommitMessageConnection 使用账号保存的 Qoder 令牌，向官方 Model
// Server 发送与 Qoder 客户端一致的提交消息生成请求，并流式返回生成结果。
func (s *AccountTestService) testQoderCommitMessageConnection(c *gin.Context, account *Account, modelID, prompt string) error {
	ctx := c.Request.Context()
	if s.httpUpstream == nil {
		return s.sendErrorAndEnd(c, "HTTP upstream is not configured")
	}

	testModelID := strings.TrimSpace(modelID)
	if testModelID == "" {
		testModelID = DefaultQoderModel
	}
	token := qoderAccountToken(account)
	if token == "" {
		return s.sendErrorAndEnd(c, "No Qoder access token available")
	}

	diff := strings.TrimSpace(prompt)
	if diff == "" {
		diff = QoderCommitMessageSampleDiff
	}

	requestID := uuid.NewString()
	sessionID := uuid.NewString()
	payload := QoderCommitMessageRequestBody(testModelID, diff, requestID, sessionID, uuid.NewString(), "qoder-ide")
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to encode Qoder commit-message request")
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	s.sendEvent(c, TestEvent{Type: "test_start", Model: testModelID})
	s.sendEvent(c, TestEvent{Type: "status", Text: "POST " + QoderChatCompletionsURL()})
	s.sendEvent(c, TestEvent{Type: "status", Text: "Qoder commit-message request: Chat Completions, stream=true, include_usage=true"})
	s.sendEvent(c, TestEvent{Type: "curl", Text: QoderCommitMessageCurl(testModelID, diff, token)})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, QoderChatCompletionsURL(), bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create Qoder commit-message request")
	}
	for key, value := range QoderCommitMessageHeaders(requestID, sessionID) {
		req.Header.Set(key, value)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Qoder model server request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		if resp.StatusCode == http.StatusUnauthorized && s.accountRepo != nil {
			_ = s.accountRepo.SetError(ctx, account.ID, fmt.Sprintf("Qoder authentication failed (401): %s", strings.TrimSpace(string(body))))
		}
		return s.sendErrorAndEnd(c, fmt.Sprintf("Qoder /model/v1/chat/completions returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}

	return s.processQoderCommitMessageStream(c, resp.Body)
}

// processQoderCommitMessageStream 解析 OpenAI 兼容 SSE，并同时输出增量文本和
// 最终提交消息。
func (s *AccountTestService) processQoderCommitMessageStream(c *gin.Context, body io.Reader) error {
	reader := bufio.NewReader(body)
	generated := strings.Builder{}

	finish := func() error {
		text := strings.TrimSpace(generated.String())
		if text == "" {
			return s.sendErrorAndEnd(c, "Qoder commit-message stream ended without a message")
		}
		s.sendEvent(c, TestEvent{Type: "content", Text: "\n" + text})
		s.sendEvent(c, TestEvent{Type: "status", Text: "Qoder commit message generated"})
		s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
		return nil
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return finish()
			}
			return s.sendErrorAndEnd(c, fmt.Sprintf("Qoder commit-message stream read error: %s", err.Error()))
		}

		line = strings.TrimSpace(line)
		if line == "" || !sseDataPrefix.MatchString(line) {
			continue
		}
		jsonStr := sseDataPrefix.ReplaceAllString(line, "")
		if jsonStr == "[DONE]" {
			return finish()
		}

		var data map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		if errData, ok := data["error"].(map[string]any); ok {
			msg, _ := errData["message"].(string)
			if msg == "" {
				msg = "unknown error"
			}
			return s.sendErrorAndEnd(c, "Qoder model server error: "+msg)
		}

		choices, ok := data["choices"].([]any)
		if !ok {
			continue
		}
		for _, choiceValue := range choices {
			choice, ok := choiceValue.(map[string]any)
			if !ok {
				continue
			}
			delta, ok := choice["delta"].(map[string]any)
			if !ok {
				continue
			}
			content, _ := delta["content"].(string)
			if content == "" {
				continue
			}
			generated.WriteString(content)
			s.sendEvent(c, TestEvent{Type: "content", Text: content})
		}
	}
}
