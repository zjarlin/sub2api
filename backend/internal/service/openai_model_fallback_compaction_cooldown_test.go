//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

func TestOpenAIFallbackCompactionTransientClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"rate_limit", &UpstreamFailoverError{StatusCode: http.StatusTooManyRequests}, true},
		{"payment_required", &UpstreamFailoverError{StatusCode: http.StatusPaymentRequired}, true},
		{"server_error", &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}, true},
		{"bad_request", &UpstreamFailoverError{StatusCode: http.StatusBadRequest}, false},
		{"plain_timeout", errors.New("context deadline exceeded"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openAIFallbackCompactionTransient(tc.err))
		})
	}
}

func TestOpenAIFailoverCompactionSkipsSummaryDuringCooldown(t *testing.T) {
	calls := 0
	svc, account, _, _ := autoContinueTestService(t, "repair", func([]byte, int) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"unavailable"}}`))}, nil
	})
	account.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceResponses)
	body := autoCompactTestInput(t)
	c, _ := autoContinueTestContext(body, 9, 7)

	next, compacted, err := svc.CompactOpenAIFailoverHistory(context.Background(), c, account, body)
	require.NoError(t, err)
	require.False(t, compacted)
	require.NotNil(t, next)
	require.Equal(t, 1, calls)
	require.True(t, svc.openAIFallbackCompactionCoolingDown(openAIFallbackCompactionCooldownKey(account, "text-model")))

	// 冷却窗口内第二次请求不得再打摘要回合，但仍返回可移植明文历史。
	next, compacted, err = svc.CompactOpenAIFailoverHistory(context.Background(), c, account, body)
	require.NoError(t, err)
	require.False(t, compacted)
	require.NotNil(t, next)
	require.Equal(t, 1, calls, "cooldown must suppress repeated summary attempts")
}
