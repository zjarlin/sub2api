package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func modelPoolFailures(status int) []OpenAIModelPoolFailure {
	return []OpenAIModelPoolFailure{
		{AccountID: 11, Err: &UpstreamFailoverError{StatusCode: status}},
		{AccountID: 12, Err: &UpstreamFailoverError{StatusCode: status}},
	}
}

func acquireModelPoolPermit(t *testing.T, svc *OpenAIGatewayService, ctx context.Context, groupID *int64, protocol, model string) *OpenAIModelPoolPermit {
	t.Helper()
	permit, allowed := svc.AcquireOpenAIModelPool(ctx, groupID, protocol, model)
	require.True(t, allowed)
	require.NotNil(t, permit)
	t.Cleanup(func() { permit.Release(context.Background()) })
	return permit
}

func TestOpenAIModelPoolCircuitRequiresCompleteIndependentFailures(t *testing.T) {
	groupID := int64(9)
	ctx := context.Background()
	svc := &OpenAIGatewayService{}
	failures := modelPoolFailures(http.StatusBadGateway)

	require.False(t, acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m").Exhausted(ctx, 3, failures))
	require.False(t, acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m").Exhausted(ctx, 1, failures[:1]))

	duplicate := []OpenAIModelPoolFailure{failures[0], failures[0]}
	require.False(t, acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m").Exhausted(ctx, 2, duplicate))

	first := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m")
	require.False(t, first.Exhausted(ctx, 2, failures))
	require.False(t, first.Exhausted(ctx, 2, failures))
	second := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m")
	require.True(t, second.Exhausted(ctx, 2, failures))
	_, allowed := svc.AcquireOpenAIModelPool(ctx, &groupID, APIProtocolResponses, "m")
	require.False(t, allowed)
}

func TestAutoModelProviderFallbackDoesNotReuseOpenAIPoolCooldown(t *testing.T) {
	groupID := int64(9)
	ctx := WithResolvedTargetPlatform(context.Background(), PlatformOpenAI)
	svc := &OpenAIGatewayService{}
	failures := modelPoolFailures(http.StatusBadGateway)
	for i := 0; i < 2; i++ {
		acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "deepseek-v4.1-flash").Exhausted(ctx, 2, failures)
	}
	_, allowed := svc.AcquireOpenAIModelPool(ctx, &groupID, APIProtocolResponses, "deepseek-v4.1-flash")
	require.False(t, allowed)
	ctx = WithResolvedTargetPlatform(ctx, PlatformDeepseek)
	permit, allowed := svc.AcquireOpenAIModelPool(ctx, &groupID, APIProtocolResponses, "deepseek-v4.1-flash")
	require.True(t, allowed)
	require.Nil(t, permit)
}

func TestOpenAIModelPoolCircuitRejectsMixedAndUnrelatedFailures(t *testing.T) {
	server := &UpstreamFailoverError{StatusCode: http.StatusBadGateway}
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "credential", err: &UpstreamFailoverError{StatusCode: http.StatusUnauthorized, Stage: GatewayFailureStageAccountAuth}},
		{name: "account scope", err: &UpstreamFailoverError{StatusCode: http.StatusBadGateway, Scope: GatewayFailureScopeAccount}},
		{name: "request scope", err: &UpstreamFailoverError{StatusCode: http.StatusBadGateway, Scope: GatewayFailureScopeRequest}},
		{name: "request format", err: &UpstreamFailoverError{StatusCode: http.StatusBadRequest}},
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "stop retry", err: &UpstreamFailoverError{StatusCode: http.StatusBadGateway, NextAccountAction: NextAccountStop}},
		{name: "auxiliary failure", err: &UpstreamFailoverError{StatusCode: http.StatusBadGateway, SkipAccountScheduleFailure: true}},
		{name: "overload mixed with server", err: &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, RequestScopedTransient: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			failures := []OpenAIModelPoolFailure{{AccountID: 11, Err: server}, {AccountID: 12, Err: test.err}}
			_, eligible := classifyOpenAIModelPoolExhaustion(2, failures)
			require.False(t, eligible)
		})
	}

	overload := []OpenAIModelPoolFailure{
		{AccountID: 11, Err: &UpstreamFailoverError{StatusCode: http.StatusTooManyRequests, RequestScopedTransient: true}},
		{AccountID: 12, Err: &UpstreamFailoverError{StatusCode: 529, RequestScopedTransient: true}},
	}
	failureClass, eligible := classifyOpenAIModelPoolExhaustion(2, overload)
	require.True(t, eligible)
	require.Equal(t, openAIModelPoolFailureOverload, failureClass)
}

func TestOpenAIModelPoolCircuitIsolatesGroupProtocolAndCanonicalModel(t *testing.T) {
	groupID := int64(9)
	otherGroupID := int64(10)
	ctx := WithModelAliases(context.Background(), &ModelAliasPolicy{Groups: []ModelAliasGroup{
		{Canonical: "deepseek-v4-flash", Aliases: []string{"DeepSeek-V4-Flash"}},
	}})
	svc := &OpenAIGatewayService{}
	failures := modelPoolFailures(http.StatusBadGateway)
	successful := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "DeepSeek-V4-Flash")
	require.False(t, acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "DeepSeek-V4-Flash").Exhausted(ctx, 2, failures))
	require.True(t, acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "deepseek-v4-flash").Exhausted(ctx, 2, failures))
	_, allowed := svc.AcquireOpenAIModelPool(ctx, &groupID, APIProtocolResponses, "DeepSeek-V4-Flash")
	require.False(t, allowed)
	acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolChatCompletions, "deepseek-v4-flash").Release(ctx)
	acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolAnthropic, "deepseek-v4-flash").Release(ctx)
	acquireModelPoolPermit(t, svc, ctx, &otherGroupID, APIProtocolResponses, "deepseek-v4-flash").Release(ctx)
	acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "deepseek-v4-pro").Release(ctx)

	successful.Success(ctx)
	acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "deepseek-v4-flash").Release(ctx)
}

func TestOpenAIModelPoolCircuitExpiresAndResetsClass(t *testing.T) {
	circuit := newOpenAIModelPoolCircuit(2)
	key := openAIModelPoolKey{groupID: 1, protocol: APIProtocolResponses, model: "m"}
	start := time.Unix(1000, 0)

	tripped, _ := circuit.recordExhaustion(key, openAIModelPoolFailureServer, "first", start)
	require.False(t, tripped)
	tripped, _ = circuit.recordExhaustion(key, openAIModelPoolFailureOverload, "second", start.Add(time.Second))
	require.False(t, tripped)
	tripped, until := circuit.recordExhaustion(key, openAIModelPoolFailureOverload, "third", start.Add(2*time.Second))
	require.True(t, tripped)
	allowed, _ := circuit.checkAndClaim(key, "probe", until.Add(-time.Nanosecond))
	require.False(t, allowed)
	allowed, probe := circuit.checkAndClaim(key, "probe", until)
	require.True(t, allowed)
	require.True(t, probe)
	tripped, _ = circuit.recordExhaustion(key, openAIModelPoolFailureOverload, "probe", until)
	require.True(t, tripped)

	circuit.recordSuccess(key, "probe")
	tripped, _ = circuit.recordExhaustion(key, openAIModelPoolFailureOverload, "fourth", until.Add(time.Second))
	require.False(t, tripped)
	tripped, _ = circuit.recordExhaustion(key, openAIModelPoolFailureOverload, "fifth", until.Add(openAIModelPoolFailureWindow+2*time.Second))
	require.False(t, tripped)
}

func TestOpenAIModelPoolCircuitBoundedAndConcurrent(t *testing.T) {
	circuit := newOpenAIModelPoolCircuit(2)
	start := time.Unix(1000, 0)
	for _, model := range []string{"a", "b", "c"} {
		key := openAIModelPoolKey{groupID: 1, protocol: APIProtocolResponses, model: model}
		circuit.recordExhaustion(key, openAIModelPoolFailureServer, model, start)
	}
	require.Len(t, circuit.entries, 2)

	key := openAIModelPoolKey{groupID: 1, protocol: APIProtocolResponses, model: "shared"}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			circuit.recordExhaustion(key, openAIModelPoolFailureServer, "shared", start.Add(time.Second))
			circuit.checkAndClaim(key, "shared", start.Add(time.Second))
		}()
	}
	wg.Wait()
	require.LessOrEqual(t, len(circuit.entries), 2)
}

func TestOpenAIModelPoolCircuitInvalidKeyFailsOpen(t *testing.T) {
	groupID := int64(1)
	svc := &OpenAIGatewayService{}
	failures := modelPoolFailures(http.StatusBadGateway)
	for _, input := range []struct {
		ctx      context.Context
		groupID  *int64
		protocol string
		model    string
	}{
		{ctx: context.Background(), protocol: APIProtocolResponses, model: "m"},
		{groupID: &groupID, protocol: APIProtocolResponses, model: "m"},
		{ctx: context.Background(), groupID: &groupID, protocol: APIProtocolAdaptive, model: "m"},
		{ctx: context.Background(), groupID: &groupID, protocol: APIProtocolResponses, model: ""},
	} {
		permit, allowed := svc.AcquireOpenAIModelPool(input.ctx, input.groupID, input.protocol, input.model)
		require.True(t, allowed)
		require.Nil(t, permit)
		require.False(t, permit.Exhausted(input.ctx, 2, failures))
		permit.Success(input.ctx)
		permit.Release(input.ctx)
	}
}

type modelPoolPermitStore struct {
	TempUnschedCache
	allowed        bool
	probe          bool
	checkErr       error
	recordErr      error
	tripped        bool
	checkKey       openAIModelPoolKey
	recordKey      openAIModelPoolKey
	checkOwner     string
	recordOwner    string
	failureClass   string
	recordCalls    int
	successCalls   int
	releaseCalls   int
	releaseCtxErr  error
	releaseHasTime bool
	renewMu        sync.Mutex
	renewCalls     atomic.Int64
	renewErr       error
	probeLost      bool
}

func (s *modelPoolPermitStore) CheckAndClaimOpenAIModelPool(_ context.Context, groupID int64, protocol, model, owner string) (bool, bool, error) {
	s.checkKey = openAIModelPoolKey{groupID: groupID, protocol: protocol, model: model}
	s.checkOwner = owner
	return s.allowed, s.probe, s.checkErr
}

func (s *modelPoolPermitStore) RecordOpenAIModelPoolExhaustion(_ context.Context, groupID int64, protocol, model, failureClass, owner string) (bool, error) {
	s.recordCalls++
	s.recordKey = openAIModelPoolKey{groupID: groupID, protocol: protocol, model: model}
	s.recordOwner = owner
	s.failureClass = failureClass
	return s.tripped, s.recordErr
}

func (s *modelPoolPermitStore) ClearOpenAIModelPoolSuccess(context.Context, int64, string, string, string) error {
	s.successCalls++
	return nil
}

func (s *modelPoolPermitStore) ReleaseOpenAIModelPoolProbe(ctx context.Context, _ int64, _, _, _ string) error {
	s.releaseCalls++
	s.releaseCtxErr = ctx.Err()
	_, s.releaseHasTime = ctx.Deadline()
	return nil
}

func (s *modelPoolPermitStore) RenewOpenAIModelPoolProbe(context.Context, int64, string, string, string) (bool, error) {
	s.renewMu.Lock()
	defer s.renewMu.Unlock()
	s.renewCalls.Add(1)
	return !s.probeLost, s.renewErr
}

func TestOpenAIModelPoolPermitCapturesStoreKeyAndOwner(t *testing.T) {
	store := &modelPoolPermitStore{allowed: true, probe: true, tripped: true}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{tempUnschedCache: store}}
	groupID := int64(9)
	policy := &ModelAliasPolicy{Groups: []ModelAliasGroup{{Canonical: "canonical", Aliases: []string{"alias"}}}}
	ctx := WithModelAliases(context.Background(), policy)
	permit := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "alias")
	_, err := uuid.Parse(store.checkOwner)
	require.NoError(t, err)
	require.Nil(t, svc.openaiModelPoolCircuit)

	groupID = 10
	policy.Groups[0].Canonical = "changed"
	require.True(t, permit.Exhausted(ctx, 2, modelPoolFailures(http.StatusBadGateway)))
	permit.Success(ctx)
	permit.Release(ctx)
	require.Equal(t, openAIModelPoolKey{groupID: 9, protocol: APIProtocolResponses, model: "canonical"}, store.recordKey)
	require.Equal(t, store.checkOwner, store.recordOwner)
	require.Equal(t, "server", store.failureClass)
	require.Equal(t, 1, store.recordCalls)
	require.Zero(t, store.successCalls)
	require.Zero(t, store.releaseCalls)
}

func TestOpenAIModelPoolPermitRedisCheckFailureRemainsStateless(t *testing.T) {
	store := &modelPoolPermitStore{checkErr: errors.New("Redis disconnected")}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{tempUnschedCache: store}}
	groupID := int64(9)
	ctx := context.Background()
	permit, allowed := svc.AcquireOpenAIModelPool(ctx, &groupID, APIProtocolResponses, "m")
	require.True(t, allowed)
	require.Nil(t, permit)
	require.False(t, permit.Exhausted(ctx, 2, modelPoolFailures(http.StatusBadGateway)))
	permit.Success(ctx)
	permit.Release(ctx)
	require.Zero(t, store.recordCalls)
	require.Zero(t, store.successCalls)
	require.Zero(t, store.releaseCalls)
	require.Nil(t, svc.openaiModelPoolCircuit)

	store.checkErr = nil
	permit, allowed = svc.AcquireOpenAIModelPool(ctx, &groupID, APIProtocolResponses, "m")
	require.False(t, allowed)
	require.Nil(t, permit)
}

func TestOpenAIModelPoolPermitReleaseUsesDetachedTimeout(t *testing.T) {
	store := &modelPoolPermitStore{allowed: true, probe: true}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{tempUnschedCache: store}}
	groupID := int64(9)
	ctx, cancel := context.WithCancel(context.Background())
	permit := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m")
	cancel()
	permit.Release(ctx)
	permit.Release(ctx)
	permit.Success(ctx)
	require.Equal(t, 1, store.releaseCalls)
	require.NoError(t, store.releaseCtxErr)
	require.True(t, store.releaseHasTime)
	require.Zero(t, store.successCalls)
	require.Zero(t, store.recordCalls)
}

func TestOpenAIModelPoolPermitInvalidEvidenceReleasesProbe(t *testing.T) {
	store := &modelPoolPermitStore{allowed: true, probe: true}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{tempUnschedCache: store}}
	groupID := int64(9)
	ctx := context.Background()
	permit := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m")
	require.False(t, permit.Exhausted(ctx, 3, modelPoolFailures(http.StatusBadGateway)))
	permit.Release(ctx)
	require.Equal(t, 1, store.releaseCalls)
	require.Zero(t, store.recordCalls)

	store.recordErr = errors.New("Redis disconnected after check")
	permit = acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m")
	require.False(t, permit.Exhausted(ctx, 2, modelPoolFailures(http.StatusBadGateway)))
	permit.Release(ctx)
	require.Equal(t, 2, store.releaseCalls)
	require.Equal(t, 1, store.recordCalls)
}

func TestOpenAIModelPoolPermitSuccessFinalizesOnce(t *testing.T) {
	store := &modelPoolPermitStore{allowed: true, probe: true}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{tempUnschedCache: store}}
	groupID := int64(9)
	ctx := context.Background()
	permit := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m")
	permit.Success(ctx)
	permit.Success(ctx)
	permit.Release(ctx)
	require.False(t, permit.Exhausted(ctx, 2, modelPoolFailures(http.StatusBadGateway)))
	require.Equal(t, 1, store.successCalls)
	require.Zero(t, store.releaseCalls)
	require.Zero(t, store.recordCalls)
}

func TestOpenAIModelPoolPermitLocalHalfOpenSingleClaim(t *testing.T) {
	svc := &OpenAIGatewayService{}
	groupID := int64(9)
	ctx := context.Background()
	failures := modelPoolFailures(http.StatusBadGateway)
	require.False(t, acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m").Exhausted(ctx, 2, failures))
	second := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m")
	require.True(t, second.Exhausted(ctx, 2, failures))
	circuit := svc.getOpenAIModelPoolCircuit()
	circuit.mu.Lock()
	entry := circuit.entries[second.key]
	entry.blockedUntil = time.Now().Add(-time.Second)
	circuit.entries[second.key] = entry
	circuit.mu.Unlock()

	permits := make(chan *OpenAIModelPoolPermit, 20)
	var wg sync.WaitGroup
	for i := 0; i < cap(permits); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			permit, allowed := svc.AcquireOpenAIModelPool(ctx, &groupID, APIProtocolResponses, "m")
			if allowed {
				permits <- permit
			}
		}()
	}
	wg.Wait()
	close(permits)
	require.Len(t, permits, 1)
	probe := <-permits
	require.True(t, probe.probe)
	probe.Release(ctx)

	newProbe := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m")
	require.True(t, newProbe.probe)
	probe.Success(ctx)
	_, allowed := svc.AcquireOpenAIModelPool(ctx, &groupID, APIProtocolResponses, "m")
	require.False(t, allowed)
	newProbe.Success(ctx)
	closed := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m")
	require.False(t, closed.probe)
	closed.Release(ctx)
}

func TestOpenAIModelPoolPermitHeartbeatStopsAfterOutcome(t *testing.T) {
	for _, outcome := range []string{"success", "exhausted", "release", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store := &modelPoolPermitStore{allowed: true, probe: true}
				svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{tempUnschedCache: store}}
				groupID := int64(9)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				permit := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m")
				time.Sleep(openAIModelPoolProbeLease + 5*time.Second)
				synctest.Wait()
				require.EqualValues(t, 3, store.renewCalls.Load())
				switch outcome {
				case "success":
					permit.Success(ctx)
				case "exhausted":
					permit.Exhausted(ctx, 2, modelPoolFailures(http.StatusBadGateway))
				case "release":
					permit.Release(ctx)
				case "cancel":
					cancel()
				}
				time.Sleep(2 * openAIModelPoolHeartbeat)
				synctest.Wait()
				require.EqualValues(t, 3, store.renewCalls.Load())
				permit.Release(ctx)
			})
		})
	}
}

func TestOpenAIModelPoolPermitHeartbeatLocalLongRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &OpenAIGatewayService{}
		groupID := int64(9)
		ctx := context.Background()
		failures := modelPoolFailures(http.StatusBadGateway)
		acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m").Exhausted(ctx, 2, failures)
		acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m").Exhausted(ctx, 2, failures)
		time.Sleep(openAIModelPoolCooldown)
		probe := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m")
		require.True(t, probe.probe)
		time.Sleep(2*openAIModelPoolProbeLease + time.Second)
		synctest.Wait()
		_, allowed := svc.AcquireOpenAIModelPool(ctx, &groupID, APIProtocolResponses, "m")
		require.False(t, allowed)
		probe.Release(ctx)
		acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m").Success(ctx)
	})
}

func TestOpenAIModelPoolCircuitRenewChecksCurrentOwner(t *testing.T) {
	circuit := newOpenAIModelPoolCircuit(2)
	key := openAIModelPoolKey{groupID: 9, protocol: APIProtocolResponses, model: "m"}
	start := time.Unix(1000, 0)
	circuit.recordExhaustion(key, openAIModelPoolFailureServer, "first", start)
	circuit.recordExhaustion(key, openAIModelPoolFailureServer, "second", start)
	probeAt := start.Add(openAIModelPoolCooldown)
	allowed, probe := circuit.checkAndClaim(key, "owner", probeAt)
	require.True(t, allowed)
	require.True(t, probe)
	require.False(t, circuit.renewProbe(key, "wrong-owner", probeAt.Add(time.Second)))
	require.True(t, circuit.renewProbe(key, "owner", probeAt.Add(20*time.Second)))
	allowed, _ = circuit.checkAndClaim(key, "other", probeAt.Add(35*time.Second))
	require.False(t, allowed)
	circuit.releaseProbe(key, "owner")
	require.False(t, circuit.renewProbe(key, "owner", probeAt.Add(36*time.Second)))
	allowed, probe = circuit.checkAndClaim(key, "new-owner", probeAt.Add(36*time.Second))
	require.True(t, allowed)
	require.True(t, probe)
	require.False(t, circuit.renewProbe(key, "owner", probeAt.Add(37*time.Second)))
	require.False(t, circuit.renewProbe(key, "new-owner", probeAt.Add(36*time.Second+openAIModelPoolProbeLease)))
}

func TestOpenAIModelPoolPermitHeartbeatFailureAndOwnerLoss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &modelPoolPermitStore{allowed: true, probe: true, renewErr: errors.New("Redis partition")}
		svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{tempUnschedCache: store}}
		groupID := int64(9)
		ctx := context.Background()
		permit := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m")
		time.Sleep(2*openAIModelPoolHeartbeat + time.Second)
		synctest.Wait()
		require.EqualValues(t, 2, store.renewCalls.Load())
		require.Nil(t, svc.openaiModelPoolCircuit)
		store.renewMu.Lock()
		store.renewErr = nil
		store.probeLost = true
		store.renewMu.Unlock()
		time.Sleep(3 * openAIModelPoolHeartbeat)
		synctest.Wait()
		require.EqualValues(t, 3, store.renewCalls.Load())
		require.Nil(t, svc.openaiModelPoolCircuit)
		permit.Release(ctx)
	})
}

type blockingModelPoolRenewStore struct {
	modelPoolPermitStore
	started chan struct{}
	proceed chan struct{}
	events  []string
}

func (s *blockingModelPoolRenewStore) RenewOpenAIModelPoolProbe(context.Context, int64, string, string, string) (bool, error) {
	close(s.started)
	<-s.proceed
	s.events = append(s.events, "renew")
	return true, nil
}

func (s *blockingModelPoolRenewStore) ClearOpenAIModelPoolSuccess(context.Context, int64, string, string, string) error {
	s.events = append(s.events, "success")
	return nil
}

func TestOpenAIModelPoolPermitOutcomeWaitsForCurrentRenewal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &blockingModelPoolRenewStore{
			modelPoolPermitStore: modelPoolPermitStore{allowed: true, probe: true},
			started:              make(chan struct{}),
			proceed:              make(chan struct{}),
		}
		svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{tempUnschedCache: store}}
		groupID := int64(9)
		ctx := context.Background()
		permit := acquireModelPoolPermit(t, svc, ctx, &groupID, APIProtocolResponses, "m")
		time.Sleep(openAIModelPoolHeartbeat)
		<-store.started
		finishing := make(chan struct{})
		finished := make(chan struct{})
		go func() {
			close(finishing)
			permit.Success(ctx)
			close(finished)
		}()
		<-finishing
		select {
		case <-finished:
			t.Fatal("success completed before the in-flight renewal")
		default:
		}
		close(store.proceed)
		<-finished
		time.Sleep(2 * openAIModelPoolHeartbeat)
		synctest.Wait()
		require.Equal(t, []string{"renew", "success"}, store.events)
	})
}
