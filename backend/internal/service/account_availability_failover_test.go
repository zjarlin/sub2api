//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQuotaExhaustedDisablesOnFirstFailure(t *testing.T) {
	for _, status := range []int{403, 429} {
		for _, code := range []string{"insufficient_user_quota", "insufficient_quota"} {
			t.Run(fmt.Sprintf("%d/%s", status, code), func(t *testing.T) {
				repo := &openAIAuthPolicyAccountRepo{}
				counter := &openAIAuthPolicy403Counter{counts: []int64{1}}
				rateLimits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
				rateLimits.openAI403CounterCache = counter
				svc := &OpenAIGatewayService{rateLimitService: rateLimits}
				account := &Account{ID: 287, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
				body := []byte(fmt.Sprintf(`{"error":{"code":%q,"message":"Failed to pre-consume quota, remaining: 0.013068, required: 0.018516","type":"AgnesAI_error"}}`, code))

				require.True(t, svc.shouldFailoverOpenAIUpstreamResponse(account, status, "", body))
				require.True(t, shouldFailoverOpenAIPassthroughResponse(account, status, body))
				require.True(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, status, nil, body))
				require.Equal(t, 1, repo.setErrorCalls)
				require.Zero(t, repo.tempCalls)
				require.Equal(t, []int64{1}, counter.counts, "quota exhaustion must bypass the three-strike 403 counter")
				require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
				require.False(t, account.IsModelKnownUnsupported("agnes-3.0-flash"))
				failover := newOpenAIUpstreamFailoverError(status, nil, body, "", true)
				require.Equal(t, NextAccountRetry, failover.NextAccountAction)
				require.False(t, failover.RetryableOnSameAccount)
				require.False(t, failover.RequestScopedTransient)
			})
		}
	}
}

func TestQuotaClassificationDoesNotConfuseConcurrencyOrEchoedInput(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"TOO_MANY_REQUESTS","message":"In-flight request cap reached (1). Retry shortly.","type":"billing_error"}}`,
		`{"error":{"code":"invalid_request_error","message":"Invalid input"},"echo":{"code":"insufficient_user_quota"}}`,
		`{"error":{"message":"Unknown parameter: insufficient_quota"}}`,
	} {
		require.False(t, isOpenAIUpstreamAccessStateError("", []byte(body)))
	}
	stream := []byte(`{"type":"response.failed","response":{"error":{"code":"insufficient_user_quota","message":"Quota exhausted"}}}`)
	require.True(t, openAIStreamFailedEventShouldFailover(stream, "Quota exhausted"))
	require.True(t, isOpenAIUpstreamAccessStateError("", stream))
}

func TestPassthroughUnsupportedModelPersistsAndLeavesResponseForFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, status := range []int{400, 404, 422} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			require.True(t, shouldFailoverOpenAIPassthroughResponse(&Account{Type: AccountTypeAPIKey}, status,
				[]byte(`{"error":{"type":"model_not_found"}}`)), "model rejection must fail over even without pool mode")
			repo := &modelNotFoundAccountRepoStub{}
			upstreamBody := `{"error":{"message":"Model \"agnes-3.0-flash\" is not supported by any configured account in this group","type":"model_not_found"}}`
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: status,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(upstreamBody)),
			}}
			svc := &OpenAIGatewayService{
				cfg: &config.Config{}, httpUpstream: upstream,
				rateLimitService: &RateLimitService{accountRepo: repo},
			}
			account := &Account{
				ID: 831, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{
					"model_mapping": map[string]any{"agnes-*": "agnes-*"},
					"api_key":       "sk-test", "base_url": "https://api.example.test",
					"pool_mode": true, "pool_mode_retry_status_codes": []any{status},
				},
				Extra: map[string]any{"openai_passthrough": true},
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			request := []byte(`{"model":"agnes-3.0-flash","input":"hi","stream":false}`)
			_, err := svc.Forward(context.Background(), c, account, request)

			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.False(t, c.Writer.Written(), "the scheduler must still be able to try another account")
			require.False(t, failover.RetryableOnSameAccount)
			require.Len(t, repo.unsupportedCalls, 1)
			require.Equal(t, "agnes-3.0-flash", repo.unsupportedCalls[0].model)
			require.False(t, account.IsModelSupported("agnes-3.0-flash"))
			require.True(t, account.IsModelSupported("agnes-2.0-flash"), "only the rejected model should be excluded")
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
			require.Equal(t, request, upstream.lastBody)
		})
	}
}

func TestPassthroughUnrelatedBadRequestDoesNotTriggerModelFailover(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for _, body := range []string{
		`{"error":{"message":"Parameter tools is not supported for this model"}}`,
		`{"error":{"message":"Route not found"}}`,
	} {
		require.False(t, shouldFailoverOpenAIPassthroughResponse(account, 400, []byte(body)))
		require.False(t, shouldFailoverOpenAIPassthroughResponse(account, 404, []byte(body)))
	}
}
