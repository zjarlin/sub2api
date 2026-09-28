package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	openAIModelPoolCircuitPrefix       = "openai_model_pool_circuit:"
	openAIModelPoolCircuitWindow       = time.Minute
	openAIModelPoolCircuitCooldown     = time.Minute
	openAIModelPoolCircuitProbeLease   = 30 * time.Second
	openAIModelPoolCircuitRetention    = 30 * time.Minute
	openAIModelPoolCircuitModelMaxByte = 512
)

var openAIModelPoolCheckAndClaimScript = redis.NewScript(`
local key = KEYS[1]
local now = redis.call('TIME')
local now_ms = (tonumber(now[1]) * 1000) + math.floor(tonumber(now[2]) / 1000)
local blocked_until = tonumber(redis.call('HGET', key, 'blocked_until')) or 0
if blocked_until == 0 then
  return {1, 0}
end
if now_ms < blocked_until then
  return {0, 0}
end
local probe_owner = redis.call('HGET', key, 'probe_owner')
local probe_until = tonumber(redis.call('HGET', key, 'probe_until')) or 0
if probe_owner and now_ms < probe_until then
  return {0, 0}
end
redis.call('HSET', key, 'probe_owner', ARGV[1], 'probe_until', now_ms + tonumber(ARGV[2]))
redis.call('PEXPIRE', key, tonumber(ARGV[2]) + tonumber(ARGV[3]))
return {1, 1}
`)

var openAIModelPoolRecordExhaustionScript = redis.NewScript(`
local key = KEYS[1]
local failure_class = ARGV[1]
local owner_token = ARGV[2]
local window_ms = tonumber(ARGV[3])
local cooldown_ms = tonumber(ARGV[4])
local retention_ms = tonumber(ARGV[5])
local now = redis.call('TIME')
local now_ms = (tonumber(now[1]) * 1000) + math.floor(tonumber(now[2]) / 1000)
local blocked_until = tonumber(redis.call('HGET', key, 'blocked_until')) or 0
local probe_owner = redis.call('HGET', key, 'probe_owner')
local probe_until = tonumber(redis.call('HGET', key, 'probe_until')) or 0

if probe_owner then
  if owner_token == '' or owner_token ~= probe_owner or now_ms >= probe_until then
    return 0
  end
  redis.call('HSET', key, 'failure_class', failure_class, 'failure_count', 2,
    'window_start', now_ms, 'blocked_until', now_ms + cooldown_ms)
  redis.call('HDEL', key, 'probe_owner', 'probe_until')
  redis.call('PEXPIRE', key, cooldown_ms + retention_ms)
  return 1
end
if blocked_until > 0 then
  return 0
end

local window_start = tonumber(redis.call('HGET', key, 'window_start')) or 0
local previous_class = redis.call('HGET', key, 'failure_class')
local failure_count = tonumber(redis.call('HGET', key, 'failure_count')) or 0
if window_start == 0 or now_ms < window_start or now_ms - window_start > window_ms or previous_class ~= failure_class then
  window_start = now_ms
  failure_count = 0
end
failure_count = failure_count + 1
if failure_count >= 2 then
  redis.call('HSET', key, 'failure_class', failure_class, 'failure_count', failure_count,
    'window_start', window_start, 'blocked_until', now_ms + cooldown_ms)
  redis.call('PEXPIRE', key, cooldown_ms + retention_ms)
  return 1
end
redis.call('HSET', key, 'failure_class', failure_class, 'failure_count', failure_count,
  'window_start', window_start)
redis.call('PEXPIRE', key, window_ms)
return 0
`)

var openAIModelPoolClearSuccessScript = redis.NewScript(`
local owner = redis.call('HGET', KEYS[1], 'probe_owner')
if owner and owner ~= ARGV[1] then
  return 0
end
return redis.call('DEL', KEYS[1])
`)

var openAIModelPoolReleaseProbeScript = redis.NewScript(`
local owner = redis.call('HGET', KEYS[1], 'probe_owner')
if not owner or owner ~= ARGV[1] then
  return 0
end
redis.call('HDEL', KEYS[1], 'probe_owner', 'probe_until')
redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[2]))
return 1
`)

var openAIModelPoolRenewProbeScript = redis.NewScript(`
local owner = redis.call('HGET', KEYS[1], 'probe_owner')
if not owner or owner ~= ARGV[1] then
  return 0
end
local now = redis.call('TIME')
local now_ms = (tonumber(now[1]) * 1000) + math.floor(tonumber(now[2]) / 1000)
local probe_until = tonumber(redis.call('HGET', KEYS[1], 'probe_until')) or 0
if now_ms >= probe_until then
  return 0
end
redis.call('HSET', KEYS[1], 'probe_until', now_ms + tonumber(ARGV[2]))
redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[2]) + tonumber(ARGV[3]))
return 1
`)

func openAIModelPoolCircuitKey(groupID int64, protocol, canonicalModel string) (string, error) {
	if groupID <= 0 {
		return "", errors.New("model pool circuit requires a positive group ID")
	}
	switch protocol {
	case service.APIProtocolResponses, service.APIProtocolChatCompletions, service.APIProtocolAnthropic:
	default:
		return "", fmt.Errorf("unsupported model pool circuit protocol: %q", protocol)
	}
	if canonicalModel == "" || strings.TrimSpace(canonicalModel) != canonicalModel || len(canonicalModel) > openAIModelPoolCircuitModelMaxByte {
		return "", errors.New("invalid canonical model for pool circuit")
	}
	modelHash := sha256.Sum256([]byte(canonicalModel))
	key := openAIModelPoolCircuitPrefix + "{" + strconv.FormatInt(groupID, 10) + "}:" + protocol + ":" + hex.EncodeToString(modelHash[:])
	return key, nil
}

func validateOpenAIModelPoolOwnerToken(ownerToken string) error {
	if ownerToken == "" || len(ownerToken) > 128 {
		return errors.New("model pool circuit requires a bounded owner token")
	}
	return nil
}

// CheckAndClaimOpenAIModelPool 在冷却结束时原子认领唯一的半开探针。
// Redis 不可用时返回 allowed=true 和错误，由调用层记录告警并放行。
func (c *tempUnschedCache) CheckAndClaimOpenAIModelPool(
	ctx context.Context,
	groupID int64,
	protocol, canonicalModel, ownerToken string,
) (allowed, probe bool, err error) {
	key, err := openAIModelPoolCircuitKey(groupID, protocol, canonicalModel)
	if err != nil {
		return true, false, err
	}
	if err := validateOpenAIModelPoolOwnerToken(ownerToken); err != nil {
		return true, false, err
	}
	if c == nil || c.rdb == nil {
		return true, false, errors.New("model pool circuit Redis cache unavailable")
	}
	values, err := openAIModelPoolCheckAndClaimScript.Run(
		ctx, c.rdb, []string{key}, ownerToken,
		openAIModelPoolCircuitProbeLease.Milliseconds(), openAIModelPoolCircuitRetention.Milliseconds(),
	).Slice()
	if err != nil {
		return true, false, fmt.Errorf("check model pool circuit: %w", err)
	}
	if len(values) != 2 {
		return true, false, fmt.Errorf("check model pool circuit: unexpected result length %d", len(values))
	}
	allowedValue, allowedOK := values[0].(int64)
	probeValue, probeOK := values[1].(int64)
	if !allowedOK || !probeOK {
		return true, false, fmt.Errorf("check model pool circuit: unexpected result types %T/%T", values[0], values[1])
	}
	return allowedValue == 1, probeValue == 1, nil
}

// RecordOpenAIModelPoolExhaustion 记录服务层已核实的完整池耗尽事件。
// failureClass 仅接受 server 或 overload；半开失败须携带认领时的 ownerToken。
func (c *tempUnschedCache) RecordOpenAIModelPoolExhaustion(
	ctx context.Context,
	groupID int64,
	protocol, canonicalModel, failureClass, ownerToken string,
) (bool, error) {
	key, err := openAIModelPoolCircuitKey(groupID, protocol, canonicalModel)
	if err != nil {
		return false, err
	}
	if failureClass != "server" && failureClass != "overload" {
		return false, fmt.Errorf("invalid model pool failure class: %q", failureClass)
	}
	if ownerToken != "" {
		if err := validateOpenAIModelPoolOwnerToken(ownerToken); err != nil {
			return false, err
		}
	}
	if c == nil || c.rdb == nil {
		return false, errors.New("model pool circuit Redis cache unavailable")
	}
	tripped, err := openAIModelPoolRecordExhaustionScript.Run(
		ctx, c.rdb, []string{key}, failureClass, ownerToken,
		openAIModelPoolCircuitWindow.Milliseconds(), openAIModelPoolCircuitCooldown.Milliseconds(),
		openAIModelPoolCircuitRetention.Milliseconds(),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("record model pool circuit exhaustion: %w", err)
	}
	return tripped == 1, nil
}

// ClearOpenAIModelPoolSuccess 清除成功池的状态；半开期间只接受探针所有者。
func (c *tempUnschedCache) ClearOpenAIModelPoolSuccess(
	ctx context.Context,
	groupID int64,
	protocol, canonicalModel, ownerToken string,
) error {
	key, err := openAIModelPoolCircuitKey(groupID, protocol, canonicalModel)
	if err != nil {
		return err
	}
	if c == nil || c.rdb == nil {
		return errors.New("model pool circuit Redis cache unavailable")
	}
	return openAIModelPoolClearSuccessScript.Run(ctx, c.rdb, []string{key}, ownerToken).Err()
}

// ReleaseOpenAIModelPoolProbe 在取消或无结论时释放探针，不改变池的冷却结论。
func (c *tempUnschedCache) ReleaseOpenAIModelPoolProbe(
	ctx context.Context,
	groupID int64,
	protocol, canonicalModel, ownerToken string,
) error {
	key, err := openAIModelPoolCircuitKey(groupID, protocol, canonicalModel)
	if err != nil {
		return err
	}
	if err := validateOpenAIModelPoolOwnerToken(ownerToken); err != nil {
		return err
	}
	if c == nil || c.rdb == nil {
		return errors.New("model pool circuit Redis cache unavailable")
	}
	return openAIModelPoolReleaseProbeScript.Run(
		ctx, c.rdb, []string{key}, ownerToken, openAIModelPoolCircuitRetention.Milliseconds(),
	).Err()
}

// RenewOpenAIModelPoolProbe 仅延长当前未过期所有者的探针租约。
func (c *tempUnschedCache) RenewOpenAIModelPoolProbe(
	ctx context.Context,
	groupID int64,
	protocol, canonicalModel, ownerToken string,
) (bool, error) {
	key, err := openAIModelPoolCircuitKey(groupID, protocol, canonicalModel)
	if err != nil {
		return false, err
	}
	if err := validateOpenAIModelPoolOwnerToken(ownerToken); err != nil {
		return false, err
	}
	if c == nil || c.rdb == nil {
		return false, errors.New("model pool circuit Redis cache unavailable")
	}
	held, err := openAIModelPoolRenewProbeScript.Run(
		ctx, c.rdb, []string{key}, ownerToken,
		openAIModelPoolCircuitProbeLease.Milliseconds(), openAIModelPoolCircuitRetention.Milliseconds(),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("renew model pool probe: %w", err)
	}
	return held == 1, nil
}
