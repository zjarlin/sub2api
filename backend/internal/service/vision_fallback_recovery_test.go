package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type visionUnavailableCapacityCache struct {
	ConcurrencyCache
	err error
}

func (c *visionUnavailableCapacityCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return false, c.err
}

func TestVisionFallbackUnavailableHelperAllowsOuterFailover(t *testing.T) {
	for _, scenario := range []string{"no_native_helper", "repository_unavailable", "repository_not_configured", "capacity_exhausted", "capacity_error"} {
		for _, chat := range []bool{false, true} {
			name := scenario + "/responses"
			if chat {
				name = scenario + "/chat"
			}
			t.Run(name, func(t *testing.T) {
				primary := visionTestAccount(1, "text-model", "text")
				helper := visionTestAccount(2, "vision-model", "text", "image")
				repo := &countingCodexModelsAccountRepo{accounts: []Account{helper}}
				svc := &OpenAIGatewayService{
					cfg: visionTestConfig(), accountRepo: repo,
					httpUpstream: &codexModelsHTTPUpstreamStub{do: func(*http.Request, string, int64, int) (*http.Response, error) {
						t.Fatal("辅助不可用时不能调用主账号或上游")
						return nil, nil
					}},
				}
				expectedMessage := "No native vision helper is available in this API key group"
				switch scenario {
				case "no_native_helper":
					repo.accounts = []Account{primary}
				case "repository_unavailable":
					repo.err = errors.New("private repository connection details")
					expectedMessage = "Unable to load image assistance models"
				case "repository_not_configured":
					svc.accountRepo = nil
					expectedMessage = "Unable to load image assistance models"
				case "capacity_exhausted":
					svc.concurrencyService = NewConcurrencyService(&visionUnavailableCapacityCache{})
					expectedMessage = "No image assistance capacity is currently available"
				case "capacity_error":
					svc.concurrencyService = NewConcurrencyService(&visionUnavailableCapacityCache{err: errors.New("private capacity connection details")})
					expectedMessage = "Unable to acquire image assistance capacity"
				}
				body := []byte(visionTestInput)
				if chat {
					body = []byte(`{"model":"text-model","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`)
				}
				c, recorder := visionTestContext(body, 9, 7)
				var err error
				if chat {
					c.Request.URL.Path = "/v1/chat/completions"
					_, err = svc.ForwardAsChatCompletions(context.Background(), c, &primary, body, "", "")
				} else {
					_, err = svc.Forward(context.Background(), c, &primary, body)
				}
				var failure *UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.True(t, failure.ShouldRetryNextAccount())
				require.False(t, failure.ShouldReportAccountScheduleFailure())
				require.Equal(t, http.StatusServiceUnavailable, failure.ClientStatusCode)
				require.Equal(t, expectedMessage, failure.ClientMessage)
				require.Equal(t, expectedMessage, gjson.GetBytes(failure.ResponseBody, "error.message").String())
				require.NotContains(t, string(failure.ResponseBody), "private")
				require.False(t, IsResponseCommitted(c))
				require.False(t, c.Writer.Written())
				require.Empty(t, recorder.Body.String())
				require.Empty(t, TakeVisionFallbackUsage(c))
			})
		}
	}
}

func TestVisionFallbackInvalidDescriptionAllowsOuterFailover(t *testing.T) {
	for _, description := range []string{"", strings.Repeat("a", visionDescriptionMaxBytes+1)} {
		primary := visionTestAccount(1, "text-model", "text")
		helper := visionTestAccount(2, "vision-model", "text", "image")
		svc := &OpenAIGatewayService{
			cfg: visionTestConfig(), accountRepo: &countingCodexModelsAccountRepo{accounts: []Account{helper}},
			httpUpstream: &codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
				require.Equal(t, helper.ID, accountID)
				return visionTestResponse(helper.Name, description), nil
			}},
		}
		body := []byte(visionTestInput)
		c, recorder := visionTestContext(body, 9, 7)
		_, err := svc.Forward(context.Background(), c, &primary, body)
		var failure *UpstreamFailoverError
		require.ErrorAs(t, err, &failure)
		require.True(t, failure.ShouldRetryNextAccount())
		require.False(t, failure.ShouldReportAccountScheduleFailure())
		require.Equal(t, http.StatusBadGateway, failure.ClientStatusCode)
		require.Equal(t, "The vision helper returned an empty or oversized image description", failure.ClientMessage)
		require.False(t, IsResponseCommitted(c))
		require.False(t, c.Writer.Written())
		require.Empty(t, recorder.Body.String())
		require.Empty(t, svc.visionFallbackCache.entries)
		require.Len(t, TakeVisionFallbackUsage(c), 1)
	}
}

func TestVisionFallbackInvalidImageCommitsOnlyTerminalValidationError(t *testing.T) {
	primary := visionTestAccount(1, "text-model", "text")
	svc := &OpenAIGatewayService{cfg: visionTestConfig()}
	body := []byte(`{"model":"text-model","stream":true,"input":[{"role":"user","content":[{"type":"input_image","image_url":"file:///tmp/private.png"}]}]}`)
	c, recorder := visionTestContext(body, 9, 7)
	_, err := svc.Forward(context.Background(), c, &primary, body)
	var failure *visionFallbackError
	require.ErrorAs(t, err, &failure)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.True(t, IsResponseCommitted(c))
	require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
	require.NotContains(t, recorder.Body.String(), "response.failed")
	require.NotContains(t, recorder.Body.String(), "file:///tmp/private.png")
}

func TestVisionFallbackPreservesHelperFailureStatusAndHeaders(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			primary := visionTestAccount(1, "text-model", "text")
			helper := visionTestAccount(2, "vision-model", "text", "image")
			svc := &OpenAIGatewayService{
				cfg: visionTestConfig(), accountRepo: &countingCodexModelsAccountRepo{accounts: []Account{helper}},
				httpUpstream: &codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
					require.Equal(t, helper.ID, accountID)
					return &http.Response{
						StatusCode: status,
						Header: http.Header{
							"Content-Type": []string{"application/json"},
							"X-Request-Id": []string{"helper-request"},
							"Retry-After":  []string{"30"},
						},
						Body: io.NopCloser(strings.NewReader(`{"error":{"message":"private helper failure"}}`)),
					}, nil
				}},
			}
			body := []byte(visionTestInput)
			c, recorder := visionTestContext(body, 9, 7)
			_, err := svc.Forward(context.Background(), c, &primary, body)
			var failure *UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, status, failure.StatusCode)
			require.Equal(t, http.StatusBadGateway, failure.ClientStatusCode)
			require.Equal(t, "helper-request", failure.ResponseHeaders.Get("x-request-id"))
			require.Equal(t, "30", failure.ResponseHeaders.Get("Retry-After"))
			require.False(t, failure.ShouldReportAccountScheduleFailure())
			require.NotContains(t, string(failure.ResponseBody), "private helper failure")
			require.Empty(t, recorder.Body.String())
		})
	}
}

func TestVisionFallbackReportsHelperHealthOnceWithoutBlamingPrimary(t *testing.T) {
	cfg := visionTestConfig()
	cache := &openAIAPIKeyHealthCacheStub{}
	settings := NewSettingService(&openAIAPIKeyHealthSettingRepo{}, cfg)
	rateLimits := NewRateLimitService(&openAIAPIKeyHealthAccountRepo{}, nil, cfg, nil, cache)
	rateLimits.SetSettingService(settings)
	rateLimits.SetOpenAIAPIKeyHealthCache(cache)
	svc := &OpenAIGatewayService{cfg: cfg, rateLimitService: rateLimits}
	helper := visionTestAccount(2, "vision-model", "text", "image")
	failure := newVisionFallbackFailoverError(http.StatusBadGateway, "The vision helper could not describe the image; please retry later")
	svc.observeVisionHelperFailure(&helper, helper.Name, failure)
	require.Equal(t, 1, cache.recordCalls)
	require.False(t, failure.ShouldReportAccountScheduleFailure())
}
