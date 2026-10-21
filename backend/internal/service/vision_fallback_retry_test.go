package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 线上 2026-10-20 17:27 事故：助手被限流后，同一回合的后续图片（以及外层换主账号的重试）
// 仍按原顺序先打同一助手，每次都要等满 60 秒候选预算，最终回写
// “No image assistance capacity”，P99 被拖到 116 秒。
// 限流/配额/无容量类失败必须在整次请求内记住，后续图片直接跳过。
func TestVisionFallbackRateLimitedHelperIsSkippedForLaterImages(t *testing.T) {
	primary := visionTestAccount(1, "text-model", "text")
	limited := visionTestAccount(2, "limited-vision", "text", "image")
	healthy := visionTestAccount(3, "healthy-vision", "text", "image")
	var calls []int64
	svc := &OpenAIGatewayService{
		cfg:         visionTestConfig(),
		accountRepo: &countingCodexModelsAccountRepo{accounts: []Account{limited, healthy}},
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
			calls = append(calls, accountID)
			if accountID == limited.ID {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"rate_limit_error","message":"Your token-plan 1-month quota has been exhausted."}}`)),
				}, nil
			}
			return visionTestResponse(healthy.Name, "图片描述"), nil
		}},
	}
	// 两张不同图片：第一张触发限流后，第二张不能再次打到同一助手。
	body := []byte(`{"model":"text-model","stream":false,"input":[{"role":"user","content":[
		{"type":"input_text","text":"比较两张图"},
		{"type":"input_image","image_url":"data:image/png;base64,AAAA"},
		{"type":"input_image","image_url":"data:image/png;base64,BBBB"}]}]}`)
	c, _ := visionTestContext(body, 9, 7)
	converted, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
	require.NoError(t, err)
	require.Equal(t, []int64{limited.ID, healthy.ID, healthy.ID}, calls)
	require.NotContains(t, string(converted), "data:image")
	require.Equal(t, 2, strings.Count(string(converted), "图片描述"))

	// 外层换主账号后的重试：两个助手都已在本回合消耗过，不再重复调用。
	calls = nil
	_, err = svc.prepareVisionFallback(context.Background(), c, &primary, body)
	require.NoError(t, err)
	require.Empty(t, calls)
}

// 并发槽是瞬时容量：不能当作助手不可用缓存，否则同一回合换号后连可用候选也拿不到名额。
func TestVisionFallbackSlotExhaustionDoesNotPoisonNextAttempt(t *testing.T) {
	primary := visionTestAccount(1, "text-model", "text")
	helper := visionTestAccount(2, "vision-model", "text", "image")
	capacity := &visionConcurrencyCache{available: false}
	svc := &OpenAIGatewayService{
		cfg: visionTestConfig(), accountRepo: &countingCodexModelsAccountRepo{accounts: []Account{helper}},
		concurrencyService: NewConcurrencyService(capacity),
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(*http.Request, string, int64, int) (*http.Response, error) {
			return visionTestResponse(helper.Name, "图片描述"), nil
		}},
	}
	body := []byte(visionTestInput)
	c, _ := visionTestContext(body, 9, 7)
	_, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, http.StatusServiceUnavailable, failure.ClientStatusCode)

	capacity.available = true
	converted, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
	require.NoError(t, err)
	require.Contains(t, string(converted), "图片描述")
	require.Equal(t, 2, capacity.acquired)
}

func TestVisionFallbackUpstreamCapacityClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"quota", http.StatusTooManyRequests, `{"error":{"message":"Your token-plan 1-month quota has been exhausted."}}`, true},
		{"concurrency", http.StatusTooManyRequests, `{"error":{"message":"此 Key 最多支持 1 个并发会话，当前已占满。"}}`, true},
		{"capacity", http.StatusServiceUnavailable, `{"error":{"message":"No image assistance capacity is currently available"}}`, true},
		{"bad_gateway_unavailable", http.StatusBadGateway, `{"error":{"message":"upstream service unavailable"}}`, false},
		{"auth_is_not_capacity", http.StatusUnauthorized, `{"error":{"message":"invalid api key"}}`, false},
		{"transient_500", http.StatusInternalServerError, `{"error":{"message":"internal error"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failoverErr := &UpstreamFailoverError{StatusCode: tc.status}
			require.Equal(t, tc.want, visionHelperCapacityByUpstream(failoverErr, visionHelperUpstreamErrorText([]byte(tc.body), nil)))
		})
	}
}

func TestVisionFallbackImageGenerationModelIsNotDescriptor(t *testing.T) {
	for _, model := range []string{"gpt-image-1", "Grok-Imagine", "grok-imagine-edit"} {
		account := visionTestAccount(1, model, "text", "image")
		require.False(t, visionDescriptorCandidate(&account, model), model)
	}
	account := visionTestAccount(2, "vision-model", "text", "image")
	require.True(t, visionDescriptorCandidate(&account, "vision-model"))
}
