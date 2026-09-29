package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/tidwall/gjson"
)

const antigravityHealthProbeResponseLimit = 1 << 20

// 周期探测只发送一次推理请求，不进入智能重试或额外积分消费流程。
func (s *AntigravityGatewayService) testAntigravityHealthConnection(ctx context.Context, account *Account, mappedModel, accessToken, proxyURL string, body []byte) (*TestConnectionResult, error) {
	baseURL := resolveAntigravityForwardBaseURL(account)
	if baseURL == "" {
		return nil, errors.New("no antigravity forward base url configured")
	}
	req, err := antigravity.NewAPIRequestWithURL(ctx, baseURL, "streamGenerateContent", accessToken, body)
	if err != nil {
		return nil, err
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, fmt.Errorf("Antigravity health probe request failed: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("Antigravity health probe returned an empty response")
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, antigravityHealthProbeResponseLimit+1))
	if err != nil {
		return nil, fmt.Errorf("Antigravity health probe read failed: %w", err)
	}
	if len(responseBody) > antigravityHealthProbeResponseLimit {
		return nil, errors.New("Antigravity health probe response exceeded limit")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Antigravity health probe returned HTTP %d: %s", resp.StatusCode, sanitizeUpstreamErrorMessage(extractAntigravityErrorMessage(responseBody)))
	}
	text, err := parseAntigravityHealthProbeResponse(responseBody)
	if err != nil {
		return nil, err
	}
	return &TestConnectionResult{Text: text, MappedModel: mappedModel}, nil
}

// 必须收到实际回答与正常结束；思考内容、局部输出和 HTTP 200 内的错误不算健康。
func parseAntigravityHealthProbeResponse(body []byte) (string, error) {
	var text strings.Builder
	completed := false
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("data:")) {
			line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		}
		if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) || bytes.HasPrefix(line, []byte(":")) || bytes.HasPrefix(line, []byte("event:")) {
			continue
		}
		if !gjson.ValidBytes(line) {
			return "", errors.New("Antigravity health probe returned invalid stream data")
		}
		data := gjson.ParseBytes(line)
		for _, path := range []string{"error", "response.error"} {
			if upstreamError := data.Get(path); upstreamError.Exists() && upstreamError.Type != gjson.Null {
				return "", fmt.Errorf("Antigravity health probe stream error: %s", sanitizeUpstreamErrorMessage(upstreamError.Get("message").String()))
			}
		}
		if response := data.Get("response"); response.Exists() {
			data = response
		}
		candidate := data.Get("candidates.0")
		for _, part := range candidate.Get("content.parts").Array() {
			answer := part.Get("text")
			if !part.Get("thought").Bool() && answer.Type == gjson.String {
				text.WriteString(answer.String())
			}
		}
		finishReason := candidate.Get("finishReason").String()
		if finishReason == "" {
			continue
		}
		if finishReason != "STOP" && finishReason != "MAX_TOKENS" {
			return "", fmt.Errorf("Antigravity health probe ended with %s", finishReason)
		}
		completed = true
	}
	if !completed {
		return "", errors.New("Antigravity health probe stream ended before completion")
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", errors.New("Antigravity health probe returned no generated text")
	}
	return text.String(), nil
}
