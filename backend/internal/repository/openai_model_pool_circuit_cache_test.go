package repository

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func modelPoolRedisStores(t *testing.T) (*miniredis.Miniredis, *tempUnschedCache, *tempUnschedCache) {
	t.Helper()
	server := miniredis.RunT(t)
	server.SetTime(time.Unix(1_700_000_000, 0))
	firstClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	secondClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = firstClient.Close()
		_ = secondClient.Close()
	})
	first := NewTempUnschedCache(firstClient).(*tempUnschedCache)
	second := NewTempUnschedCache(secondClient).(*tempUnschedCache)
	return server, first, second
}

func tripModelPoolRedisCircuit(t *testing.T, first, second *tempUnschedCache, groupID int64, protocol, model string) {
	t.Helper()
	ctx := context.Background()
	tripped, err := first.RecordOpenAIModelPoolExhaustion(ctx, groupID, protocol, model, "server", "")
	require.NoError(t, err)
	require.False(t, tripped)
	tripped, err = second.RecordOpenAIModelPoolExhaustion(ctx, groupID, protocol, model, "server", "")
	require.NoError(t, err)
	require.True(t, tripped)
}

func TestOpenAIModelPoolRedisCircuitCrossInstanceSingleProbe(t *testing.T) {
	server, first, second := modelPoolRedisStores(t)
	ctx := context.Background()
	tripModelPoolRedisCircuit(t, first, second, 9, service.APIProtocolResponses, "model-a")

	allowed, probe, err := second.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "before")
	require.NoError(t, err)
	require.False(t, allowed)
	require.False(t, probe)

	server.SetTime(time.Unix(1_700_000_000, 0).Add(openAIModelPoolCircuitCooldown))
	type checkResult struct {
		owner   string
		allowed bool
		probe   bool
		err     error
	}
	results := make(chan checkResult, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, candidate := range []struct {
		store *tempUnschedCache
		owner string
	}{{first, "first"}, {second, "second"}} {
		wg.Add(1)
		go func(store *tempUnschedCache, owner string) {
			defer wg.Done()
			<-start
			allowed, probe, err := store.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", owner)
			results <- checkResult{owner: owner, allowed: allowed, probe: probe, err: err}
		}(candidate.store, candidate.owner)
	}
	close(start)
	wg.Wait()
	close(results)

	var winner string
	for result := range results {
		require.NoError(t, result.err)
		if result.allowed {
			require.True(t, result.probe)
			require.Empty(t, winner)
			winner = result.owner
		} else {
			require.False(t, result.probe)
		}
	}
	require.NotEmpty(t, winner)

	tripped, err := second.RecordOpenAIModelPoolExhaustion(ctx, 9, service.APIProtocolResponses, "model-a", "overload", winner)
	require.NoError(t, err)
	require.True(t, tripped)
	allowed, probe, err = first.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "next")
	require.NoError(t, err)
	require.False(t, allowed)
	require.False(t, probe)
}

func TestOpenAIModelPoolRedisCircuitReleaseAndStaleOwner(t *testing.T) {
	server, first, second := modelPoolRedisStores(t)
	ctx := context.Background()
	tripModelPoolRedisCircuit(t, first, second, 9, service.APIProtocolResponses, "model-a")
	server.SetTime(time.Unix(1_700_000_000, 0).Add(openAIModelPoolCircuitCooldown))

	allowed, probe, err := first.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "owner-a")
	require.NoError(t, err)
	require.True(t, allowed)
	require.True(t, probe)
	require.NoError(t, second.ReleaseOpenAIModelPoolProbe(ctx, 9, service.APIProtocolResponses, "model-a", "wrong-owner"))
	allowed, _, err = second.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "owner-b")
	require.NoError(t, err)
	require.False(t, allowed)

	require.NoError(t, first.ReleaseOpenAIModelPoolProbe(ctx, 9, service.APIProtocolResponses, "model-a", "owner-a"))
	allowed, probe, err = second.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "owner-b")
	require.NoError(t, err)
	require.True(t, allowed)
	require.True(t, probe)

	tripped, err := first.RecordOpenAIModelPoolExhaustion(ctx, 9, service.APIProtocolResponses, "model-a", "server", "owner-a")
	require.NoError(t, err)
	require.False(t, tripped)
	require.NoError(t, first.ClearOpenAIModelPoolSuccess(ctx, 9, service.APIProtocolResponses, "model-a", "owner-a"))
	allowed, _, err = first.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "owner-c")
	require.NoError(t, err)
	require.False(t, allowed)

	require.NoError(t, second.ClearOpenAIModelPoolSuccess(ctx, 9, service.APIProtocolResponses, "model-a", "owner-b"))
	allowed, probe, err = first.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "closed")
	require.NoError(t, err)
	require.True(t, allowed)
	require.False(t, probe)
}

func TestOpenAIModelPoolRedisCircuitReclaimsExpiredProbe(t *testing.T) {
	server, first, second := modelPoolRedisStores(t)
	ctx := context.Background()
	tripModelPoolRedisCircuit(t, first, second, 9, service.APIProtocolResponses, "model-a")
	base := time.Unix(1_700_000_000, 0).Add(openAIModelPoolCircuitCooldown)
	server.SetTime(base)
	allowed, probe, err := first.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "expired")
	require.NoError(t, err)
	require.True(t, allowed)
	require.True(t, probe)

	server.SetTime(base.Add(openAIModelPoolCircuitProbeLease))
	allowed, probe, err = second.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "new-owner")
	require.NoError(t, err)
	require.True(t, allowed)
	require.True(t, probe)
	tripped, err := first.RecordOpenAIModelPoolExhaustion(ctx, 9, service.APIProtocolResponses, "model-a", "server", "expired")
	require.NoError(t, err)
	require.False(t, tripped)
	require.NoError(t, first.ReleaseOpenAIModelPoolProbe(ctx, 9, service.APIProtocolResponses, "model-a", "expired"))
	allowed, _, err = first.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "third")
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestOpenAIModelPoolRedisCircuitClassWindowAndKeyIsolation(t *testing.T) {
	server, first, second := modelPoolRedisStores(t)
	ctx := context.Background()
	tripped, err := first.RecordOpenAIModelPoolExhaustion(ctx, 9, service.APIProtocolResponses, "model-a", "server", "")
	require.NoError(t, err)
	require.False(t, tripped)
	tripped, err = second.RecordOpenAIModelPoolExhaustion(ctx, 9, service.APIProtocolResponses, "model-a", "overload", "")
	require.NoError(t, err)
	require.False(t, tripped)
	tripped, err = first.RecordOpenAIModelPoolExhaustion(ctx, 9, service.APIProtocolResponses, "model-a", "overload", "")
	require.NoError(t, err)
	require.True(t, tripped)

	for _, scope := range []struct {
		groupID  int64
		protocol string
		model    string
	}{
		{10, service.APIProtocolResponses, "model-a"},
		{9, service.APIProtocolChatCompletions, "model-a"},
		{9, service.APIProtocolAnthropic, "model-a"},
		{9, service.APIProtocolResponses, "model-b"},
	} {
		allowed, probe, err := second.CheckAndClaimOpenAIModelPool(ctx, scope.groupID, scope.protocol, scope.model, "scope")
		require.NoError(t, err)
		require.True(t, allowed)
		require.False(t, probe)
	}
	key, err := openAIModelPoolCircuitKey(9, service.APIProtocolResponses, "model-a")
	require.NoError(t, err)
	require.False(t, strings.Contains(key, "model-a"))

	server.SetTime(time.Unix(1_700_000_000, 0).Add(openAIModelPoolCircuitCooldown))
	tripped, err = first.RecordOpenAIModelPoolExhaustion(ctx, 9, service.APIProtocolResponses, "model-a", "overload", "")
	require.NoError(t, err)
	require.False(t, tripped)

	windowModel := "window-model"
	tripped, err = first.RecordOpenAIModelPoolExhaustion(ctx, 9, service.APIProtocolResponses, windowModel, "server", "")
	require.NoError(t, err)
	require.False(t, tripped)
	server.SetTime(time.Unix(1_700_000_000, 0).Add(openAIModelPoolCircuitCooldown + openAIModelPoolCircuitWindow + time.Second))
	tripped, err = second.RecordOpenAIModelPoolExhaustion(ctx, 9, service.APIProtocolResponses, windowModel, "server", "")
	require.NoError(t, err)
	require.False(t, tripped)
}

func TestOpenAIModelPoolRedisCircuitTTLAndDisconnect(t *testing.T) {
	server, first, second := modelPoolRedisStores(t)
	ctx := context.Background()
	key, err := openAIModelPoolCircuitKey(9, service.APIProtocolResponses, "model-a")
	require.NoError(t, err)
	tripped, err := first.RecordOpenAIModelPoolExhaustion(ctx, 9, service.APIProtocolResponses, "model-a", "server", "")
	require.NoError(t, err)
	require.False(t, tripped)
	ttl, err := first.rdb.TTL(ctx, key).Result()
	require.NoError(t, err)
	require.Positive(t, ttl)
	require.LessOrEqual(t, ttl, openAIModelPoolCircuitWindow)

	tripped, err = second.RecordOpenAIModelPoolExhaustion(ctx, 9, service.APIProtocolResponses, "model-a", "server", "")
	require.NoError(t, err)
	require.True(t, tripped)
	ttl, err = first.rdb.TTL(ctx, key).Result()
	require.NoError(t, err)
	require.GreaterOrEqual(t, ttl, openAIModelPoolCircuitCooldown)
	require.LessOrEqual(t, ttl, openAIModelPoolCircuitCooldown+openAIModelPoolCircuitRetention)
	server.FastForward(openAIModelPoolCircuitCooldown + openAIModelPoolCircuitRetention + time.Second)
	require.False(t, server.Exists(key))

	require.NoError(t, first.rdb.Close())
	allowed, probe, err := first.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "disconnected")
	require.Error(t, err)
	require.True(t, allowed)
	require.False(t, probe)
	_, err = first.RecordOpenAIModelPoolExhaustion(ctx, 9, service.APIProtocolResponses, "model-a", "server", "")
	require.Error(t, err)
	require.Error(t, first.ClearOpenAIModelPoolSuccess(ctx, 9, service.APIProtocolResponses, "model-a", ""))
	require.Error(t, first.ReleaseOpenAIModelPoolProbe(ctx, 9, service.APIProtocolResponses, "model-a", "disconnected"))
	_, err = first.RenewOpenAIModelPoolProbe(ctx, 9, service.APIProtocolResponses, "model-a", "disconnected")
	require.Error(t, err)
}

func TestOpenAIModelPoolRedisCircuitRejectsInvalidKeys(t *testing.T) {
	_, first, _ := modelPoolRedisStores(t)
	ctx := context.Background()
	for _, input := range []struct {
		groupID  int64
		protocol string
		model    string
		owner    string
	}{
		{0, service.APIProtocolResponses, "model-a", "owner"},
		{9, service.APIProtocolAdaptive, "model-a", "owner"},
		{9, service.APIProtocolResponses, " model-a", "owner"},
		{9, service.APIProtocolResponses, "model-a", ""},
		{9, service.APIProtocolResponses, "model-a", strings.Repeat("x", 129)},
	} {
		allowed, probe, err := first.CheckAndClaimOpenAIModelPool(ctx, input.groupID, input.protocol, input.model, input.owner)
		require.Error(t, err)
		require.True(t, allowed)
		require.False(t, probe)
	}
}

func modelPoolRedisGateway(cache service.TempUnschedCache) *service.OpenAIGatewayService {
	cfg := &config.Config{}
	rateLimits := service.NewRateLimitService(nil, nil, cfg, nil, cache)
	return service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg,
		nil, nil, nil, rateLimits, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
}

func TestOpenAIModelPoolPermitRedisServiceIntegration(t *testing.T) {
	server, firstCache, secondCache := modelPoolRedisStores(t)
	first := modelPoolRedisGateway(firstCache)
	second := modelPoolRedisGateway(secondCache)
	groupID := int64(9)
	protocol := service.APIProtocolResponses
	ctx := service.WithModelAliases(context.Background(), &service.ModelAliasPolicy{Groups: []service.ModelAliasGroup{
		{Canonical: "model-a", Aliases: []string{"provider/model-a"}},
	}})
	failures := []service.OpenAIModelPoolFailure{
		{AccountID: 11, Err: &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway}},
		{AccountID: 12, Err: &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway}},
	}
	trip := func() {
		firstPermit, allowed := first.AcquireOpenAIModelPool(ctx, &groupID, protocol, "provider/model-a")
		require.True(t, allowed)
		require.NotNil(t, firstPermit)
		require.False(t, firstPermit.Exhausted(ctx, 2, failures))
		secondPermit, allowed := second.AcquireOpenAIModelPool(ctx, &groupID, protocol, "model-a")
		require.True(t, allowed)
		require.NotNil(t, secondPermit)
		require.True(t, secondPermit.Exhausted(ctx, 2, failures))
		permit, allowed := first.AcquireOpenAIModelPool(ctx, &groupID, protocol, "model-a")
		require.False(t, allowed)
		require.Nil(t, permit)
	}
	trip()
	key, err := openAIModelPoolCircuitKey(groupID, protocol, "model-a")
	require.NoError(t, err)
	require.True(t, server.Exists(key))

	base := time.Unix(1_700_000_000, 0)
	server.SetTime(base.Add(openAIModelPoolCircuitCooldown))
	probe, allowed := second.AcquireOpenAIModelPool(ctx, &groupID, protocol, "provider/model-a")
	require.True(t, allowed)
	require.NotNil(t, probe)
	_, allowed = first.AcquireOpenAIModelPool(ctx, &groupID, protocol, "model-a")
	require.False(t, allowed)
	probe.Success(ctx)
	require.False(t, server.Exists(key))
	permit, allowed := first.AcquireOpenAIModelPool(ctx, &groupID, protocol, "model-a")
	require.True(t, allowed)
	permit.Release(ctx)

	trip()
	server.SetTime(base.Add(2 * openAIModelPoolCircuitCooldown))
	probeCtx, cancel := context.WithCancel(ctx)
	probe, allowed = first.AcquireOpenAIModelPool(probeCtx, &groupID, protocol, "model-a")
	require.True(t, allowed)
	require.NotNil(t, probe)
	cancel()
	probe.Release(probeCtx)
	newProbe, allowed := second.AcquireOpenAIModelPool(ctx, &groupID, protocol, "provider/model-a")
	require.True(t, allowed)
	require.NotNil(t, newProbe)
	_, allowed = first.AcquireOpenAIModelPool(ctx, &groupID, protocol, "model-a")
	require.False(t, allowed)
	newProbe.Success(ctx)
	require.False(t, server.Exists(key))
}

func TestOpenAIModelPoolRedisCircuitRenewProtectsOwner(t *testing.T) {
	server, first, second := modelPoolRedisStores(t)
	ctx := context.Background()
	tripModelPoolRedisCircuit(t, first, second, 9, service.APIProtocolResponses, "model-a")
	probeAt := time.Unix(1_700_000_000, 0).Add(openAIModelPoolCircuitCooldown)
	server.SetTime(probeAt)
	allowed, probe, err := first.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "owner")
	require.NoError(t, err)
	require.True(t, allowed)
	require.True(t, probe)

	server.SetTime(probeAt.Add(20 * time.Second))
	held, err := second.RenewOpenAIModelPoolProbe(ctx, 9, service.APIProtocolResponses, "model-a", "wrong-owner")
	require.NoError(t, err)
	require.False(t, held)
	held, err = first.RenewOpenAIModelPoolProbe(ctx, 9, service.APIProtocolResponses, "model-a", "owner")
	require.NoError(t, err)
	require.True(t, held)
	server.SetTime(probeAt.Add(35 * time.Second))
	allowed, _, err = second.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "other")
	require.NoError(t, err)
	require.False(t, allowed)

	require.NoError(t, first.ReleaseOpenAIModelPoolProbe(ctx, 9, service.APIProtocolResponses, "model-a", "owner"))
	held, err = first.RenewOpenAIModelPoolProbe(ctx, 9, service.APIProtocolResponses, "model-a", "owner")
	require.NoError(t, err)
	require.False(t, held)
	allowed, probe, err = second.CheckAndClaimOpenAIModelPool(ctx, 9, service.APIProtocolResponses, "model-a", "new-owner")
	require.NoError(t, err)
	require.True(t, allowed)
	require.True(t, probe)
	held, err = first.RenewOpenAIModelPoolProbe(ctx, 9, service.APIProtocolResponses, "model-a", "owner")
	require.NoError(t, err)
	require.False(t, held)
	server.SetTime(probeAt.Add(35*time.Second + openAIModelPoolCircuitProbeLease))
	held, err = second.RenewOpenAIModelPoolProbe(ctx, 9, service.APIProtocolResponses, "model-a", "new-owner")
	require.NoError(t, err)
	require.False(t, held)
}

func TestOpenAIModelPoolPermitRedisHeartbeatLongRequest(t *testing.T) {
	// 网关缓存含运行时终结器，构造留在虚拟时间外，避免终结器访问泡内通道。
	firstCache := &tempUnschedCache{}
	secondCache := &tempUnschedCache{}
	first := modelPoolRedisGateway(firstCache)
	second := modelPoolRedisGateway(secondCache)
	synctest.Test(t, func(t *testing.T) {
		server := miniredis.NewMiniRedis()
		require.NoError(t, server.Start())
		// 虚拟时间用内存连接，关闭监听器以避免真实网络阻塞时钟前进。
		server.Server().Close()
		defer server.Close()
		newClient := func() *redis.Client {
			return redis.NewClient(&redis.Options{
				Addr: "miniredis:6379",
				Dialer: func(context.Context, string, string) (net.Conn, error) {
					serverConn, clientConn := net.Pipe()
					server.Server().ServeConn(serverConn)
					return clientConn, nil
				},
			})
		}
		firstClient := newClient()
		secondClient := newClient()
		defer firstClient.Close()
		defer secondClient.Close()
		firstCache.rdb = firstClient
		secondCache.rdb = secondClient
		groupID := int64(9)
		ctx := context.Background()
		failures := []service.OpenAIModelPoolFailure{
			{AccountID: 11, Err: &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway}},
			{AccountID: 12, Err: &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway}},
		}
		for _, gateway := range []*service.OpenAIGatewayService{first, second} {
			permit, allowed := gateway.AcquireOpenAIModelPool(ctx, &groupID, service.APIProtocolResponses, "model-a")
			require.True(t, allowed)
			require.NotNil(t, permit)
			permit.Exhausted(ctx, 2, failures)
		}
		time.Sleep(openAIModelPoolCircuitCooldown)
		probe, allowed := first.AcquireOpenAIModelPool(ctx, &groupID, service.APIProtocolResponses, "model-a")
		require.True(t, allowed)
		require.NotNil(t, probe)
		defer probe.Release(ctx)
		time.Sleep(2*openAIModelPoolCircuitProbeLease + time.Second)
		synctest.Wait()
		_, allowed = second.AcquireOpenAIModelPool(ctx, &groupID, service.APIProtocolResponses, "model-a")
		require.False(t, allowed)
		probe.Release(ctx)
		newProbe, allowed := second.AcquireOpenAIModelPool(ctx, &groupID, service.APIProtocolResponses, "model-a")
		require.True(t, allowed)
		require.NotNil(t, newProbe)
		newProbe.Success(ctx)
	})
}
