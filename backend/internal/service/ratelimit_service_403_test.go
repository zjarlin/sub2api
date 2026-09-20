//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRateLimitService_OpenAI403InsufficientBalanceStopsSchedulingImmediately(t *testing.T) {
	for _, message := range []string{"Insufficient account balance", "insufficient balance", "余额不足"} {
		t.Run(message, func(t *testing.T) {
			repo := &rateLimitAccountRepoStub{}
			counter := &countingOpenAI403CounterCache{}
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc.SetOpenAI403CounterCache(counter)
			account := &Account{ID: 836, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			body, err := json.Marshal(map[string]any{"error": map[string]any{"message": message}})
			require.NoError(t, err)
			require.True(t, svc.HandleUpstreamError(context.Background(), account, http.StatusForbidden, http.Header{}, body))
			require.Equal(t, 1, repo.setErrorCalls)
			require.Equal(t, 0, repo.tempCalls)
			require.Equal(t, 0, counter.increments)
			require.Contains(t, repo.lastErrorMsg, "Payment required (403)")
		})
	}
}

type runtimeBlockRecorder struct {
	accounts   []*Account
	until      []time.Time
	reasons    []string
	clearedIDs []int64
}

func (r *runtimeBlockRecorder) BlockAccountScheduling(account *Account, until time.Time, reason string) {
	r.accounts = append(r.accounts, account)
	r.until = append(r.until, until)
	r.reasons = append(r.reasons, reason)
}

func (r *runtimeBlockRecorder) ClearAccountSchedulingBlock(accountID int64) {
	r.clearedIDs = append(r.clearedIDs, accountID)
}

func TestRateLimitService_HandleUpstreamError_OpenAI403FirstHitTempUnschedulable(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{1}}
	blocker := &runtimeBlockRecorder{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetOpenAI403CounterCache(counter)
	service.SetAccountRuntimeBlocker(blocker)
	account := &Account{
		ID:       301,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
	}

	shouldDisable := service.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"error":{"message":"temporary edge rejection"}}`),
	)

	require.True(t, shouldDisable)
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, 1, repo.tempCalls)
	require.Contains(t, repo.lastTempReason, "temporary edge rejection")
	require.Contains(t, repo.lastTempReason, "(1/3)")
	require.Len(t, blocker.accounts, 1)
	require.Equal(t, account.ID, blocker.accounts[0].ID)
	require.Equal(t, "openai_403_temp", blocker.reasons[0])
	require.True(t, blocker.until[0].After(time.Now()))
}

func TestRateLimitService_HandleUpstreamError_OpenAI403ThresholdDisables(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{3}}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetOpenAI403CounterCache(counter)
	account := &Account{
		ID:       302,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
	}

	shouldDisable := service.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"error":{"message":"workspace forbidden by policy"}}`),
	)

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, 0, repo.tempCalls)
	require.Contains(t, repo.lastErrorMsg, "workspace forbidden by policy")
	require.Contains(t, repo.lastErrorMsg, "consecutive_403=3/3")
}
