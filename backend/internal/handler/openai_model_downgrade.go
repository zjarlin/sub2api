package handler

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func resolveOpenAIRequestDowngradeModel(account *service.Account, requestedModel string, failoverErr *service.UpstreamFailoverError) (string, bool) {
	if account == nil || failoverErr == nil {
		return "", false
	}
	if account.Platform != service.PlatformOpenAI || account.Type != service.AccountTypeAPIKey {
		return "", false
	}
	if failoverErr.StatusCode != http.StatusTooManyRequests {
		return "", false
	}
	candidates := service.ResolveOpenAIRequestedModelFallbackCandidates(account, requestedModel)
	if len(candidates) == 0 {
		return "", false
	}
	downgraded := strings.TrimSpace(candidates[0])
	if downgraded == "" || strings.EqualFold(strings.TrimSpace(downgraded), strings.TrimSpace(requestedModel)) {
		return "", false
	}
	return downgraded, true
}
