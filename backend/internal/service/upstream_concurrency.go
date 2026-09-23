package service

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// Only an explicit provider capacity message qualifies; a generic billing 429
// may mean exhausted funds or quota and must keep its existing policy.
var upstreamConcurrencyMessage = regexp.MustCompile(`^In-flight request cap reached \([1-9][0-9]*\)\. Retry shortly\.$`)

const UpstreamConcurrencyCooldown = 2 * time.Second

func isUpstreamConcurrencyLimit(status int, body []byte) bool {
	if status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable {
		return false
	}
	message := strings.TrimSpace(extractUpstreamErrorMessage(body))
	if message == "" {
		message = strings.TrimSpace(gjson.GetBytes(body, "response.error.message").String())
	}
	if status == http.StatusServiceUnavailable {
		return message == "模型服务当前并发繁忙，请稍后重试"
	}
	if upstreamConcurrencyMessage.MatchString(message) {
		return true
	}
	switch message {
	case "Concurrency limit exceeded for account, please retry later",
		"当前账号并发请求过多，请等待已有请求完成":
		return true
	default:
		return false
	}
}

func (e *UpstreamFailoverError) IsUpstreamConcurrencyLimited() bool {
	return e != nil && isUpstreamConcurrencyLimit(e.StatusCode, e.ResponseBody)
}
