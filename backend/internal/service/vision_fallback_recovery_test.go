package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestVisionFallbackTimeoutBudgets(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		candidateSeconds, totalSeconds int
		parentTimeout, expected        time.Duration
	}{
		{name: "legacy_config_defaults", expected: 60 * time.Second},
		{name: "configured_candidate", candidateSeconds: 45, totalSeconds: 100, expected: 45 * time.Second},
		{name: "configured_total", candidateSeconds: 60, totalSeconds: 12, expected: 12 * time.Second},
		{name: "default_total", candidateSeconds: 200, expected: 120 * time.Second},
		{name: "parent_deadline", candidateSeconds: 60, totalSeconds: 120, parentTimeout: 5 * time.Second, expected: 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := visionTestAccount(1, "text-model", "text")
			helper := visionTestAccount(2, "vision-model", "text", "image")
			cfg := visionTestConfig()
			cfg.Gateway.VisionFallback.CandidateTimeoutSeconds = tc.candidateSeconds
			cfg.Gateway.VisionFallback.TimeoutSeconds = tc.totalSeconds
			calls := 0
			svc := &OpenAIGatewayService{
				cfg: cfg, accountRepo: &countingCodexModelsAccountRepo{accounts: []Account{helper}},
				httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
					calls++
					require.Equal(t, helper.ID, accountID)
					deadline, ok := req.Context().Deadline()
					require.True(t, ok)
					require.InDelta(t, tc.expected.Seconds(), time.Until(deadline).Seconds(), 1)
					return visionTestResponse(helper.Name, "图片描述"), nil
				}},
			}
			ctx := context.Background()
			if tc.parentTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.parentTimeout)
				defer cancel()
			}
			body := []byte(visionTestInput)
			c, _ := visionTestContext(body, 9, 7)
			converted, err := svc.prepareVisionFallback(ctx, c, &primary, body)
			require.NoError(t, err)
			require.Contains(t, string(converted), "图片描述")
			require.Equal(t, 1, calls)
		})
	}
}

func TestVisionFallbackTimeoutRetriesWithinTotalBudget(t *testing.T) {
	for _, exhaustTotal := range []bool{false, true} {
		name := "next_candidate_succeeds"
		if exhaustTotal {
			name = "total_budget_exhausted"
		}
		t.Run(name, func(t *testing.T) {
			primary := visionTestAccount(1, "text-model", "text")
			slow := visionTestAccount(2, "slow-vision", "text", "image")
			fast := visionTestAccount(3, "fast-vision", "text", "image")
			cfg := visionTestConfig()
			cfg.Gateway.VisionFallback.CandidateTimeoutSeconds = 1
			cfg.Gateway.VisionFallback.TimeoutSeconds = 10
			if exhaustTotal {
				cfg.Gateway.VisionFallback.CandidateTimeoutSeconds = 10
				cfg.Gateway.VisionFallback.TimeoutSeconds = 1
			}
			var calls []int64
			svc := &OpenAIGatewayService{
				cfg: cfg, accountRepo: &countingCodexModelsAccountRepo{accounts: []Account{slow, fast}},
				httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
					calls = append(calls, accountID)
					if accountID == slow.ID {
						<-req.Context().Done()
						return nil, req.Context().Err()
					}
					return visionTestResponse(fast.Name, "备用助手描述"), nil
				}},
			}
			body := []byte(visionTestInput)
			c, recorder := visionTestContext(body, 9, 7)
			converted, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
			if exhaustTotal {
				var failure *UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.Equal(t, http.StatusGatewayTimeout, failure.StatusCode)
				require.Equal(t, http.StatusGatewayTimeout, failure.ClientStatusCode)
				require.Contains(t, failure.ClientMessage, "timed out")
				require.True(t, failure.ShouldRetryNextAccount())
				require.False(t, failure.ShouldReportAccountScheduleFailure())
				require.Equal(t, []int64{slow.ID}, calls)
			} else {
				require.NoError(t, err)
				require.Contains(t, string(converted), "备用助手描述")
				require.Equal(t, []int64{slow.ID, fast.ID}, calls)
			}
			require.False(t, c.Writer.Written())
			require.Empty(t, recorder.Body.String())
			value, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok)
			events := value.([]*OpsUpstreamErrorEvent)
			require.Len(t, events, 1)
			require.Equal(t, http.StatusGatewayTimeout, events[0].UpstreamStatusCode)
			require.Contains(t, events[0].Message, "timed out")
			require.NotContains(t, events[0].Message, "upstream.example")
		})
	}
}

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

func TestVisionFallbackWithoutHelperLeavesImageRequestForRetry(t *testing.T) {
	primary := visionTestAccount(1, "text-model", "text")
	cfg := visionTestConfig()
	svc := &OpenAIGatewayService{cfg: cfg, accountRepo: &countingCodexModelsAccountRepo{accounts: []Account{primary}}}
	body := []byte(visionTestInput)
	c, recorder := visionTestContext(body, 9, 7)
	converted, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.Nil(t, converted)
	require.Equal(t, visionTestInput, string(body))
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
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
	settings := NewSettingService(&openAIAdvancedSchedulerSettingRepoStub{}, cfg)
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

// 通过真实辅助转发边界验证：本地超时/语义失败不能被脱敏状态伪装成账号故障。
func TestVisionFallbackDoesNotTripAccountHealthForLocalFailures(t *testing.T) {
	for _, scenario := range []string{"local_deadline", "empty_description", "incomplete_response", "upstream_502"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := visionTestConfig()
			cache := &openAIAPIKeyHealthCacheStub{tripped: true}
			repo := &openAIAPIKeyHealthAccountRepo{}
			rateLimits := NewRateLimitService(repo, nil, cfg, nil, cache)
			rateLimits.SetSettingService(NewSettingService(&openAIAdvancedSchedulerSettingRepoStub{}, cfg))
			rateLimits.SetOpenAIAPIKeyHealthCache(cache)
			helper := visionTestAccount(2, "vision-model", "text", "image")
			svc := &OpenAIGatewayService{
				cfg: cfg,
				httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
					switch scenario {
					case "local_deadline":
						<-req.Context().Done()
						return nil, req.Context().Err()
					case "empty_description":
						return visionTestResponse(helper.Name, ""), nil
					case "incomplete_response":
						resp := visionTestResponse(helper.Name, "truncated description")
						body, err := io.ReadAll(resp.Body)
						require.NoError(t, err)
						resp.Body = io.NopCloser(strings.NewReader(strings.ReplaceAll(string(body), `"finish_reason":"stop"`, `"finish_reason":"length"`)))
						return resp, nil
					default:
						return &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"provider unavailable"}}`))}, nil
					}
				}},
			}
			svc.rateLimitService = rateLimits
			body := []byte(visionTestInput)
			parent, recorder := visionTestContext(body, 9, 7)
			value, _ := parent.Get("api_key")
			apiKey := value.(*APIKey)
			timeout := 5 * time.Second
			if scenario == "local_deadline" {
				timeout = 50 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			_, err := svc.callVisionHelper(ctx, parent, apiKey, visionFallbackCandidate{account: &helper, model: helper.Name}, body, 0, 1, 0, 1)
			require.Error(t, err)
			var failure *UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.False(t, failure.ShouldReportAccountScheduleFailure())
			require.Empty(t, recorder.Body.String())
			if scenario == "upstream_502" {
				// 实际上游 5xx 仍能触发熔断，不能用修复绕过健康保护。
				require.Equal(t, 1, cache.recordCalls)
				require.Equal(t, 1, repo.setCalls)
				return
			}
			require.Zero(t, cache.recordCalls)
			require.Zero(t, repo.setCalls)
			require.Nil(t, helper.TempUnschedulableUntil)
			if scenario == "local_deadline" {
				require.Equal(t, http.StatusGatewayTimeout, failure.ClientStatusCode)
			}
		})
	}
}
