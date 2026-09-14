package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const reasoningReplayPrefix = "sub2api_ds_"

func isDeepSeekResponsesAccount(account *Account) bool {
	if account == nil {
		return false
	}
	if account.Platform == PlatformDeepseek {
		return true
	}
	u, err := url.Parse(account.GetCredential("base_url"))
	return err == nil && strings.EqualFold(u.Hostname(), "api.deepseek.com")
}

func reasoningReplayKey(c *gin.Context, token string) string {
	return fmt.Sprintf("ds:%d:%s", getAPIKeyIDFromContext(c), token)
}

// Keep exact provider text, not summaries. The opaque reference survives
// clients that retain encrypted_content but omit reasoning.content on replay.
func (s *OpenAIGatewayService) preserveDeepSeekReasoning(c *gin.Context, account *Account, data []byte) []byte {
	if !isDeepSeekResponsesAccount(account) || s == nil || s.cache == nil || getAPIKeyIDFromContext(c) <= 0 {
		return data
	}
	paths := []string{}
	root := gjson.ParseBytes(data)
	if root.Get("type").String() == "response.output_item.done" {
		paths = append(paths, "item")
	}
	for _, prefix := range []string{"output", "response.output"} {
		for i := range root.Get(prefix).Array() {
			paths = append(paths, fmt.Sprintf("%s.%d", prefix, i))
		}
	}
	for _, path := range paths {
		item := gjson.GetBytes(data, path)
		if item.Get("type").String() != "reasoning" || item.Get("encrypted_content").String() != "" {
			continue
		}
		content := item.Get("content")
		if !hasReasoningReplayText(content) {
			continue
		}
		token := fmt.Sprintf("%s%x", reasoningReplayPrefix, sha256.Sum256([]byte(content.Raw)))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := s.cache.SetReasoningContent(ctx, reasoningReplayKey(c, token), content.Raw, responsesReasoningCacheTTL)
		cancel()
		if err == nil {
			if next, err := sjson.SetBytes(data, path+".encrypted_content", token); err == nil {
				data = next
			}
		}
	}
	return data
}

func hasReasoningReplayText(content gjson.Result) bool {
	for _, part := range content.Array() {
		if part.Get("type").String() == "reasoning_text" && part.Get("text").String() != "" {
			return true
		}
	}
	return false
}

func (s *OpenAIGatewayService) restoreDeepSeekReasoning(c *gin.Context, account *Account, body []byte) []byte {
	if !isDeepSeekResponsesAccount(account) || s == nil || s.cache == nil || getAPIKeyIDFromContext(c) <= 0 {
		return body
	}
	for i, item := range gjson.GetBytes(body, "input").Array() {
		token := item.Get("encrypted_content").String()
		if item.Get("type").String() != "reasoning" || !strings.HasPrefix(token, reasoningReplayPrefix) {
			continue
		}
		path := fmt.Sprintf("input.%d", i)
		if !hasReasoningReplayText(item.Get("content")) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			content, err := s.cache.GetReasoningContent(ctx, reasoningReplayKey(c, token))
			cancel()
			if err == nil && hasReasoningReplayText(gjson.Parse(content)) {
				if next, err := sjson.SetRawBytes(body, path+".content", []byte(content)); err == nil {
					body = next
				}
			}
		}
		if next, err := sjson.DeleteBytes(body, path+".encrypted_content"); err == nil {
			body = next
		}
	}
	return body
}

// Old or cross-provider histories cannot reconstruct missing provider text.
// Retry this exact validation failure once in non-thinking mode, preserving
// messages and tool results. The changed effort makes the retry self-bounding.
func deepSeekReasoningReplayRetry(c *gin.Context, account *Account, status int, body, response []byte) ([]byte, bool) {
	if !isDeepSeekResponsesAccount(account) || status != http.StatusBadRequest ||
		gjson.GetBytes(body, "reasoning.effort").String() == "none" ||
		!strings.Contains(extractUpstreamErrorMessage(response), "`reasoning_text` in the thinking mode must be passed back") {
		return body, false
	}
	next, err := sjson.SetBytes(body, "reasoning.effort", "none")
	if err != nil {
		return body, false
	}
	if c != nil && !c.Writer.Written() {
		c.Header("X-Sub2api-Reasoning-Fallback", "none")
	}
	return next, true
}
