//go:build unit

package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIModelPoolRoundOnlyObservesCompletePool(t *testing.T) {
	ctx := context.Background()
	groupID := int64(41)
	account := &service.Account{ID: 1, Platform: service.PlatformOpenAI}
	for _, tc := range []struct {
		name       string
		decision   service.OpenAIAccountScheduleDecision
		excluded   int
		incomplete bool
		failures   int
	}{
		{name: "legacy", decision: service.OpenAIAccountScheduleDecision{Layer: "load_balance"}, failures: 2},
		{name: "sticky", decision: service.OpenAIAccountScheduleDecision{Layer: "session_sticky", CandidateCount: 2}, failures: 2},
		{name: "subscription subpool", decision: service.OpenAIAccountScheduleDecision{Layer: "load_balance", CandidateCount: 2}, failures: 2},
		{name: "already excluded", decision: service.OpenAIAccountScheduleDecision{CandidateCount: 2, CandidateCountComplete: true, CandidateAccountIDs: []int64{1, 2}}, excluded: 1, failures: 2},
		{name: "one of two failed", decision: service.OpenAIAccountScheduleDecision{CandidateCount: 2, CandidateCountComplete: true, CandidateAccountIDs: []int64{1, 2}}, failures: 1},
		{name: "non upstream exclusion", decision: service.OpenAIAccountScheduleDecision{CandidateCount: 2, CandidateCountComplete: true, CandidateAccountIDs: []int64{1, 2}}, incomplete: true, failures: 2},
		{name: "candidate identity changed", decision: service.OpenAIAccountScheduleDecision{CandidateCount: 2, CandidateCountComplete: true, CandidateAccountIDs: []int64{1, 3}}, failures: 2},
		{name: "missing candidate identities", decision: service.OpenAIAccountScheduleDecision{CandidateCount: 2, CandidateCountComplete: true}, failures: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway := &service.OpenAIGatewayService{}
			for range 2 {
				round := newOpenAIModelPoolRound(gateway, &groupID, service.APIProtocolResponses, "m", true)
				require.True(t, round.allowed(ctx))
				round.selected(tc.decision, account, tc.excluded)
				if tc.incomplete {
					round.excludeWithoutUpstreamFailure()
				}
				for id := 1; id <= tc.failures; id++ {
					round.failed(int64(id), &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway})
				}
				round.exhausted(ctx)
				round.close()
			}
			permit, allowed := gateway.AcquireOpenAIModelPool(ctx, &groupID, service.APIProtocolResponses, "m")
			require.True(t, allowed)
			permit.Release(ctx)
		})
	}
}

func TestOpenAIModelPoolRoundCheckIsolationAndRecovery(t *testing.T) {
	ctx := context.Background()
	groupID := int64(41)
	gateway := &service.OpenAIGatewayService{}
	decision := service.OpenAIAccountScheduleDecision{CandidateCount: 2, CandidateCountComplete: true, CandidateAccountIDs: []int64{1, 2}}
	account := &service.Account{ID: 1, Platform: service.PlatformOpenAI}
	round := newOpenAIModelPoolRound(gateway, &groupID, service.APIProtocolResponses, "m", true)
	require.True(t, round.allowed(ctx))
	for range 2 {
		observer := newOpenAIModelPoolRound(gateway, &groupID, service.APIProtocolResponses, "m", true)
		require.True(t, observer.allowed(ctx))
		observer.selected(decision, account, 0)
		observer.failed(1, &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway})
		observer.failed(2, &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway})
		observer.exhausted(ctx)
		observer.exhausted(ctx)
	}
	require.True(t, round.allowed(ctx), "每模型轮不重复检查正在冷却的池")
	round.reset("m")
	require.False(t, round.allowed(ctx))
	otherGroupID := groupID + 1
	require.True(t, newOpenAIModelPoolRound(gateway, &otherGroupID, service.APIProtocolResponses, "m", true).allowed(ctx))
	require.True(t, newOpenAIModelPoolRound(gateway, &groupID, service.APIProtocolAnthropic, "m", true).allowed(ctx))
	require.True(t, newOpenAIModelPoolRound(gateway, &groupID, service.APIProtocolResponses, "other", true).allowed(ctx))
	round = newOpenAIModelPoolRound(gateway, &groupID, service.APIProtocolResponses, "m", false)
	require.True(t, round.allowed(ctx), "有状态请求不使用池级冷却")
	require.False(t, round.checked)
	round = newOpenAIModelPoolRound(gateway, &groupID, service.APIProtocolResponses, "recover", true)
	require.True(t, round.allowed(ctx))
	round.selected(decision, account, 0)
	round.failed(1, &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway})
	round.failed(2, &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway})
	round.exhausted(ctx)
	round.reset("recover")
	require.True(t, round.allowed(ctx))
	round.succeeded(ctx)
	round.reset("recover")
	require.True(t, round.allowed(ctx))
	round.selected(decision, account, 0)
	round.failed(1, &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway})
	round.failed(2, &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway})
	round.exhausted(ctx)
	permit, allowed := gateway.AcquireOpenAIModelPool(ctx, &groupID, service.APIProtocolResponses, "recover")
	require.True(t, allowed, "成功清除之前的故障计数")
	permit.Release(ctx)
}

func TestOpenAIModelPoolSuccessRequiresSuccessfulTerminal(t *testing.T) {
	newContext := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		return c
	}
	require.True(t, openAIModelPoolForwardSucceeded(newContext(), &service.OpenAIForwardResult{}, nil))
	require.False(t, openAIModelPoolForwardSucceeded(newContext(), nil, errors.New("partial response")))
	require.False(t, openAIModelPoolForwardSucceeded(newContext(), &service.OpenAIForwardResult{ClientDisconnect: true}, nil))
	require.False(t, openAIModelPoolForwardSucceeded(newContext(), &service.OpenAIForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.failed"}, nil))
	c := newContext()
	service.MarkOpsStreamError(c, "upstream_error", "terminal failed", http.StatusBadGateway)
	require.False(t, openAIModelPoolForwardSucceeded(c, &service.OpenAIForwardResult{}, nil))
}
