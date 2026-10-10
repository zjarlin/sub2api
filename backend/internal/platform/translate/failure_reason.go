package translate

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

var failureURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)
var failureBearerPattern = regexp.MustCompile(`(?i)\bBearer\s+[^\s,;"']+`)

// 失败详情去掉请求 URL、原文和凭据，并限制长度。
func failureReason(err error, texts []string) string {
	if err == nil {
		return ""
	}
	var requestError *url.Error
	if errors.As(err, &requestError) {
		err = requestError.Err
	}
	reason := err.Error()
	reason = failureURLPattern.ReplaceAllString(reason, "[URL redacted]")
	reason = failureBearerPattern.ReplaceAllString(reason, "Bearer [redacted]")
	for _, text := range texts {
		if text != "" {
			reason = strings.ReplaceAll(reason, text, "[text redacted]")
		}
	}
	reason = logredact.RedactText(reason, "token", "key", "api_key", "secret", "authorization", "appsecret", "sign")
	runes := []rune(reason)
	if len(runes) > 300 {
		return string(runes[:300]) + "…"
	}
	return reason
}
