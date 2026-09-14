package service

import (
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Only an explicit provider capacity message qualifies; a generic billing 429
// may mean exhausted funds or quota and must keep its existing policy.
var upstreamConcurrencyMessage = regexp.MustCompile(`^In-flight request cap reached \([1-9][0-9]*\)\. Retry shortly\.$`)

const UpstreamConcurrencyCooldown = 2 * time.Second

func isUpstreamConcurrencyLimit(status int, body []byte) bool {
	return status == http.StatusTooManyRequests && upstreamConcurrencyMessage.MatchString(strings.TrimSpace(extractUpstreamErrorMessage(body)))
}

func (e *UpstreamFailoverError) IsUpstreamConcurrencyLimited() bool {
	return e != nil && isUpstreamConcurrencyLimit(e.StatusCode, e.ResponseBody)
}
