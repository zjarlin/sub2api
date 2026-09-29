package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	openAIModelPoolFailureThreshold = 2
	openAIModelPoolFailureWindow    = time.Minute
	openAIModelPoolCooldown         = time.Minute
	openAIModelPoolMaxEntries       = 4096
	openAIModelPoolProbeLease       = 30 * time.Second
	openAIModelPoolHeartbeat        = 10 * time.Second
	openAIModelPoolRetention        = 30 * time.Minute
	openAIModelPoolPersistTimeout   = 3 * time.Second
)

type openAIModelPoolKey struct {
	groupID  int64
	protocol string
	model    string
}

type openAIModelPoolFailureClass uint8

const (
	openAIModelPoolFailureServer openAIModelPoolFailureClass = iota + 1
	openAIModelPoolFailureOverload
)

type openAIModelPoolEntry struct {
	failureCount int
	failureClass openAIModelPoolFailureClass
	windowStart  time.Time
	blockedUntil time.Time
	lastTouched  time.Time
	probeOwner   string
	probeUntil   time.Time
}

type openAIModelPoolCircuit struct {
	mu         sync.Mutex
	entries    map[openAIModelPoolKey]openAIModelPoolEntry
	maxEntries int
}

// OpenAIModelPoolFailure 是一次完整模型池尝试中某个账号的最终上游故障。
type OpenAIModelPoolFailure struct {
	AccountID int64
	Err       error
}

// OpenAIModelPoolCircuitStore 原子协调跨实例冷却和带所有者的半开探针。
type OpenAIModelPoolCircuitStore interface {
	CheckAndClaimOpenAIModelPool(ctx context.Context, groupID int64, protocol, canonicalModel, ownerToken string) (allowed, probe bool, err error)
	RecordOpenAIModelPoolExhaustion(ctx context.Context, groupID int64, protocol, canonicalModel, failureClass, ownerToken string) (tripped bool, err error)
	ClearOpenAIModelPoolSuccess(ctx context.Context, groupID int64, protocol, canonicalModel, ownerToken string) error
	ReleaseOpenAIModelPoolProbe(ctx context.Context, groupID int64, protocol, canonicalModel, ownerToken string) error
	RenewOpenAIModelPoolProbe(ctx context.Context, groupID int64, protocol, canonicalModel, ownerToken string) (held bool, err error)
}

// OpenAIModelPoolPermit 固定本轮池键、存储和所有者，只接受一次最终结论。
type OpenAIModelPoolPermit struct {
	mu              sync.Mutex
	completed       bool
	key             openAIModelPoolKey
	store           OpenAIModelPoolCircuitStore
	circuit         *openAIModelPoolCircuit
	owner           string
	probe           bool
	heartbeatCancel context.CancelFunc
	heartbeatDone   chan struct{}
}

func newOpenAIModelPoolCircuit(maxEntries int) *openAIModelPoolCircuit {
	if maxEntries <= 0 {
		maxEntries = openAIModelPoolMaxEntries
	}
	return &openAIModelPoolCircuit{
		entries:    make(map[openAIModelPoolKey]openAIModelPoolEntry),
		maxEntries: maxEntries,
	}
}

func (s *OpenAIGatewayService) getOpenAIModelPoolCircuit() *openAIModelPoolCircuit {
	if s == nil {
		return nil
	}
	s.openaiModelPoolCircuitOnce.Do(func() {
		if s.openaiModelPoolCircuit == nil {
			s.openaiModelPoolCircuit = newOpenAIModelPoolCircuit(0)
		}
	})
	return s.openaiModelPoolCircuit
}

func openAIModelPoolCircuitKey(ctx context.Context, groupID *int64, protocol, model string) (openAIModelPoolKey, bool) {
	if ctx == nil || groupID == nil || *groupID <= 0 {
		return openAIModelPoolKey{}, false
	}
	// 现有池键没有平台维度，其他供应商不得复用 OpenAI 池的失败证据。
	if platform, resolved := ResolvedTargetPlatformFromContext(ctx); resolved && NormalizeOpenAICompatiblePlatform(platform) != PlatformOpenAI {
		return openAIModelPoolKey{}, false
	}
	switch protocol {
	case APIProtocolResponses, APIProtocolChatCompletions, APIProtocolAnthropic:
	default:
		return openAIModelPoolKey{}, false
	}
	model = strings.TrimSpace(ModelAliasesFromContext(ctx).Canonicalize(model))
	if model == "" || len(model) > unsupportedModelKeyMaxBytes {
		return openAIModelPoolKey{}, false
	}
	return openAIModelPoolKey{groupID: *groupID, protocol: protocol, model: model}, true
}

// AcquireOpenAIModelPool 固定本轮许可；无有效池键或 Redis 出错时无状态放行。
func (s *OpenAIGatewayService) AcquireOpenAIModelPool(ctx context.Context, groupID *int64, protocol, model string) (*OpenAIModelPoolPermit, bool) {
	key, ok := openAIModelPoolCircuitKey(ctx, groupID, protocol, model)
	if !ok || s == nil || ctx.Err() != nil {
		return nil, true
	}
	permit := &OpenAIModelPoolPermit{key: key, owner: uuid.NewString()}
	if s.rateLimitService != nil {
		permit.store, _ = s.rateLimitService.tempUnschedCache.(OpenAIModelPoolCircuitStore)
	}
	var allowed bool
	if permit.store != nil {
		checkCtx, cancel := context.WithTimeout(ctx, openAIModelPoolPersistTimeout)
		var err error
		allowed, permit.probe, err = permit.store.CheckAndClaimOpenAIModelPool(checkCtx, key.groupID, key.protocol, key.model, permit.owner)
		cancel()
		if err != nil {
			logOpenAIModelPoolStoreError("openai.model_pool_circuit_check_failed", key, err)
			return nil, true
		}
		if !allowed {
			return nil, false
		}
		permit.startHeartbeat(ctx)
		return permit, true
	}
	permit.circuit = s.getOpenAIModelPoolCircuit()
	allowed, permit.probe = permit.circuit.checkAndClaim(key, permit.owner, time.Now())
	if !allowed {
		return nil, false
	}
	permit.startHeartbeat(ctx)
	return permit, true
}

// Exhausted 只接受全部可调度候选均已实际失败的证据；无结论则释放许可。
// candidateCount 必须是本轮完整池的候选数，不是已尝试账号数。
func (p *OpenAIModelPoolPermit) Exhausted(ctx context.Context, candidateCount int, failures []OpenAIModelPoolFailure) bool {
	if p == nil {
		return false
	}
	failureClass, eligible := classifyOpenAIModelPoolExhaustion(candidateCount, failures)
	if !eligible || ctx == nil || ctx.Err() != nil {
		p.Release(ctx)
		return false
	}
	if !p.finish() {
		return false
	}
	var tripped bool
	if p.store != nil {
		failureName := "server"
		if failureClass == openAIModelPoolFailureOverload {
			failureName = "overload"
		}
		recordCtx, cancel := context.WithTimeout(ctx, openAIModelPoolPersistTimeout)
		var err error
		tripped, err = p.store.RecordOpenAIModelPoolExhaustion(recordCtx, p.key.groupID, p.key.protocol, p.key.model, failureName, p.owner)
		cancel()
		if err != nil {
			logOpenAIModelPoolStoreError("openai.model_pool_circuit_exhaustion_failed", p.key, err)
			p.releaseProbe(ctx)
			return false
		}
	} else {
		tripped, _ = p.circuit.recordExhaustion(p.key, failureClass, p.owner, time.Now())
	}
	if tripped {
		logger.L().Warn("openai.model_pool_circuit_tripped",
			zap.Int64("group_id", p.key.groupID),
			zap.String("protocol", p.key.protocol),
			zap.String("model", p.key.model),
			zap.Int("candidate_count", candidateCount),
			zap.Duration("cooldown", openAIModelPoolCooldown),
		)
	}
	return tripped
}

// Success 清除本轮成功池的状态，半开探针仍须匹配所有者。
func (p *OpenAIModelPoolPermit) Success(ctx context.Context) {
	if p == nil || !p.finish() {
		return
	}
	if p.store == nil {
		p.circuit.recordSuccess(p.key, p.owner)
		return
	}
	persistCtx, cancel := openAIModelPoolDetachedContext(ctx)
	defer cancel()
	if err := p.store.ClearOpenAIModelPoolSuccess(persistCtx, p.key.groupID, p.key.protocol, p.key.model, p.owner); err != nil {
		logOpenAIModelPoolStoreError("openai.model_pool_circuit_success_failed", p.key, err)
		p.releaseProbe(ctx)
	}
}

// Release 在取消或无结论时释放本轮半开探针，取消上下文不妨碍清理。
func (p *OpenAIModelPoolPermit) Release(ctx context.Context) {
	if p == nil || !p.finish() {
		return
	}
	p.releaseProbe(ctx)
}

func (p *OpenAIModelPoolPermit) finish() bool {
	p.mu.Lock()
	if p.completed {
		p.mu.Unlock()
		return false
	}
	p.completed = true
	if p.heartbeatCancel != nil {
		p.heartbeatCancel()
	}
	done := p.heartbeatDone
	p.mu.Unlock()
	if done != nil {
		<-done
	}
	return true
}

func (p *OpenAIModelPoolPermit) startHeartbeat(ctx context.Context) {
	if !p.probe {
		return
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	p.heartbeatCancel = cancel
	p.heartbeatDone = make(chan struct{})
	go func() {
		ticker := time.NewTicker(openAIModelPoolHeartbeat)
		defer ticker.Stop()
		defer close(p.heartbeatDone)
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if !p.renewProbe(heartbeatCtx) {
					return
				}
			}
		}
	}()
}

func (p *OpenAIModelPoolPermit) renewProbe(ctx context.Context) bool {
	// 续租持有许可锁，终结必须先等待当前写入，避免结论之后重新写租约。
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.completed || ctx.Err() != nil {
		return false
	}
	var held bool
	if p.store == nil {
		held = p.circuit.renewProbe(p.key, p.owner, time.Now())
	} else {
		renewCtx, cancel := context.WithTimeout(ctx, openAIModelPoolPersistTimeout)
		var err error
		held, err = p.store.RenewOpenAIModelPoolProbe(renewCtx, p.key.groupID, p.key.protocol, p.key.model, p.owner)
		cancel()
		if err != nil {
			logOpenAIModelPoolStoreError("openai.model_pool_circuit_renew_failed", p.key, err)
			return true
		}
	}
	if !held {
		logger.L().Warn("openai.model_pool_circuit_probe_lost",
			zap.Int64("group_id", p.key.groupID),
			zap.String("protocol", p.key.protocol),
			zap.String("model", p.key.model),
		)
	}
	return held
}

func (p *OpenAIModelPoolPermit) releaseProbe(ctx context.Context) {
	if !p.probe {
		return
	}
	if p.store == nil {
		p.circuit.releaseProbe(p.key, p.owner)
		return
	}
	releaseCtx, cancel := openAIModelPoolDetachedContext(ctx)
	defer cancel()
	if err := p.store.ReleaseOpenAIModelPoolProbe(releaseCtx, p.key.groupID, p.key.protocol, p.key.model, p.owner); err != nil {
		logOpenAIModelPoolStoreError("openai.model_pool_circuit_release_failed", p.key, err)
	}
}

func openAIModelPoolDetachedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), openAIModelPoolPersistTimeout)
}

func logOpenAIModelPoolStoreError(event string, key openAIModelPoolKey, err error) {
	logger.L().Warn(event,
		zap.Int64("group_id", key.groupID),
		zap.String("protocol", key.protocol),
		zap.String("model", key.model),
		zap.Error(err),
	)
}

func classifyOpenAIModelPoolExhaustion(candidateCount int, failures []OpenAIModelPoolFailure) (openAIModelPoolFailureClass, bool) {
	if candidateCount < 2 || len(failures) != candidateCount {
		return 0, false
	}
	seen := make(map[int64]struct{}, candidateCount)
	var commonClass openAIModelPoolFailureClass
	for _, failure := range failures {
		if failure.AccountID <= 0 {
			return 0, false
		}
		if _, duplicate := seen[failure.AccountID]; duplicate {
			return 0, false
		}
		seen[failure.AccountID] = struct{}{}
		failureClass, eligible := classifyOpenAIModelPoolFailure(failure.Err)
		if !eligible || (commonClass != 0 && commonClass != failureClass) {
			return 0, false
		}
		commonClass = failureClass
	}
	return commonClass, true
}

func classifyOpenAIModelPoolFailure(err error) (openAIModelPoolFailureClass, bool) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0, false
	}
	var failoverErr *UpstreamFailoverError
	if !errors.As(err, &failoverErr) || failoverErr == nil {
		return 0, false
	}
	if failoverErr.IsCredentialFailure() || failoverErr.SkipAccountScheduleFailure ||
		failoverErr.NextAccountAction == NextAccountStop || failoverErr.Scope == GatewayFailureScopeAccount ||
		(failoverErr.Stage != "" && failoverErr.Stage != GatewayFailureStageInference) {
		return 0, false
	}
	status := failoverErr.StatusCode
	serverError := status >= http.StatusInternalServerError && status < 600
	if failoverErr.RequestScopedTransient && (status == http.StatusTooManyRequests || serverError) {
		return openAIModelPoolFailureOverload, true
	}
	if failoverErr.Scope == GatewayFailureScopeRequest || !serverError {
		return 0, false
	}
	return openAIModelPoolFailureServer, true
}

func (c *openAIModelPoolCircuit) checkAndClaim(key openAIModelPoolKey, owner string, now time.Time) (allowed, probe bool) {
	if c == nil {
		return true, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return true, false
	}
	if openAIModelPoolEntryExpired(entry, now) {
		delete(c.entries, key)
		return true, false
	}
	if entry.blockedUntil.IsZero() {
		return true, false
	}
	if now.Before(entry.blockedUntil) || (entry.probeOwner != "" && now.Before(entry.probeUntil)) {
		return false, false
	}
	entry.probeOwner = owner
	entry.probeUntil = now.Add(openAIModelPoolProbeLease)
	entry.lastTouched = now
	c.entries[key] = entry
	return true, true
}

func (c *openAIModelPoolCircuit) recordExhaustion(key openAIModelPoolKey, failureClass openAIModelPoolFailureClass, owner string, now time.Time) (bool, time.Time) {
	if c == nil {
		return false, time.Time{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, exists := c.entries[key]
	if entry.probeOwner != "" {
		if owner != entry.probeOwner || !now.Before(entry.probeUntil) {
			return false, entry.blockedUntil
		}
		entry = openAIModelPoolEntry{
			failureCount: openAIModelPoolFailureThreshold,
			failureClass: failureClass,
			windowStart:  now,
			blockedUntil: now.Add(openAIModelPoolCooldown),
			lastTouched:  now,
		}
		c.entries[key] = entry
		return true, entry.blockedUntil
	}
	if exists && !entry.blockedUntil.IsZero() {
		return false, entry.blockedUntil
	}
	if !exists {
		c.ensureCapacityLocked(now)
	}
	if entry.windowStart.IsZero() || now.Before(entry.windowStart) || now.Sub(entry.windowStart) > openAIModelPoolFailureWindow ||
		entry.failureClass != failureClass {
		entry = openAIModelPoolEntry{windowStart: now, failureClass: failureClass}
	}
	entry.failureCount++
	entry.lastTouched = now
	if entry.failureCount >= openAIModelPoolFailureThreshold {
		entry.blockedUntil = now.Add(openAIModelPoolCooldown)
	}
	c.entries[key] = entry
	return entry.failureCount == openAIModelPoolFailureThreshold, entry.blockedUntil
}

func (c *openAIModelPoolCircuit) recordSuccess(key openAIModelPoolKey, owner string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if ok && entry.probeOwner != "" && entry.probeOwner != owner {
		return
	}
	delete(c.entries, key)
}

func (c *openAIModelPoolCircuit) releaseProbe(key openAIModelPoolKey, owner string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || entry.probeOwner == "" || entry.probeOwner != owner {
		return
	}
	entry.probeOwner = ""
	entry.probeUntil = time.Time{}
	c.entries[key] = entry
}

func (c *openAIModelPoolCircuit) renewProbe(key openAIModelPoolKey, owner string, now time.Time) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || entry.probeOwner == "" || entry.probeOwner != owner || !now.Before(entry.probeUntil) {
		return false
	}
	entry.probeUntil = now.Add(openAIModelPoolProbeLease)
	entry.lastTouched = now
	c.entries[key] = entry
	return true
}

func openAIModelPoolEntryExpired(entry openAIModelPoolEntry, now time.Time) bool {
	if entry.blockedUntil.IsZero() {
		return now.Sub(entry.windowStart) > openAIModelPoolFailureWindow
	}
	return !now.Before(entry.blockedUntil) && now.Sub(entry.lastTouched) > openAIModelPoolRetention
}

func (c *openAIModelPoolCircuit) ensureCapacityLocked(now time.Time) {
	if len(c.entries) < c.maxEntries {
		return
	}
	for key, entry := range c.entries {
		if openAIModelPoolEntryExpired(entry, now) {
			delete(c.entries, key)
		}
	}
	if len(c.entries) < c.maxEntries {
		return
	}
	var oldestKey openAIModelPoolKey
	var oldestTime time.Time
	for key, entry := range c.entries {
		if oldestTime.IsZero() || entry.lastTouched.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.lastTouched
		}
	}
	delete(c.entries, oldestKey)
}
