package rate_limiter

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/coocood/freecache"
	mylogger "github.com/taha/myprog/internal/logger"
	"github.com/taha/myprog/internal/redis"
	"go.uber.org/zap"
)

type leakyBucketExecutor struct {
	client     redis.Client
	options    FilterOptions
	localCache *freecache.Cache
	scriptSha1 string
	luaBody    string
	policies   map[string]YamlDescriptor
}

func getLeakyLuaScriptBody() string {
	return `
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local leak_rate = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local now = tonumber(ARGV[4])
local ttl = tonumber(ARGV[5])

local bucket = redis.call('HMGET', key, 'w', 'l')
local water = 0
local last_updated = now

if bucket[1] then
    water = tonumber(bucket[1])
end
if bucket[2] then
    last_updated = tonumber(bucket[2])
end

local elapsed = math.max(0, now - last_updated)
local leaked = elapsed * leak_rate
water = math.max(0, water - leaked)

local allowed = 0
if water + cost <= capacity then
    water = water + cost
    allowed = 1
    redis.call('HMSET', key, 'w', water, 'l', now)
    redis.call('EXPIRE', key, ttl)
end

local reset_duration = 0
if leak_rate > 0 then
    if allowed == 1 then
        reset_duration = water / leak_rate
    else
        local missing = (water + cost) - capacity
        reset_duration = missing / leak_rate
    end
end

return {allowed, water, math.ceil(reset_duration)}
`
}
func NewLeakyBucketExecutor(client redis.Client, opts FilterOptions, localCache *freecache.Cache) RateLimitExecutor {
	body := getLeakyLuaScriptBody()
	hasher := sha1.New()
	hasher.Write([]byte(body))
	sha := hex.EncodeToString(hasher.Sum(nil))

	policies := make(map[string]YamlDescriptor, len(opts.Descriptors))
	for _, d := range opts.Descriptors {
		policies[d.Key] = d
	}

	return &leakyBucketExecutor{
		client:     client,
		options:    opts,
		localCache: localCache,
		scriptSha1: sha,
		luaBody:    body,
		policies:   policies,
	}
}

func (e *leakyBucketExecutor) Evaluate(ctx context.Context, descriptors []DescriptorEntry, cost int64) (Decision, error) {
	var finalDecision Decision
	finalDecision.LimitRemaining = ^uint32(0)

	nowFloat := float64(time.Now().UnixNano()) / float64(time.Second)
	var batchBuf [512]byte
	offset := 0

	for _, entry := range descriptors {
		policy, ok := e.policies[entry.Key]
		if !ok {
			continue
		}

		divider := float64(getDivider(policy.Unit))
		leakRatePerSecond := policy.LeakRate / divider
		ttlSeconds := int64(0)
		if leakRatePerSecond > 0 {
			ttlSeconds = int64(math.Ceil(float64(policy.BucketCapacity) / leakRatePerSecond))
		}
		if ttlSeconds <= 0 {
			ttlSeconds = 60 // Fallback minimum TTL
		}

		val := entry.Value
		if policy.ShareThresholdPattern != "" {
			val = policy.ShareThresholdPattern
		}

		// 1. Zero-Alloc Key Generation with safe fallback
		estimatedSize := len(e.options.Domain) + len(entry.Key) + len(val) + len("_leaky_queue") + 30
		var keyStr string
		var buf []byte

		if offset+estimatedSize <= len(batchBuf) {
			buf = batchBuf[offset : offset : offset+estimatedSize]
			buf = append(buf, e.options.Domain...)
			buf = append(buf, '_')
			buf = append(buf, entry.Key...)
			buf = append(buf, '_')
			buf = append(buf, val...)
			buf = append(buf, "_leaky_queue"...)

			keyStr = unsafe.String(unsafe.SliceData(buf), len(buf))
			offset += len(buf)
		} else {
			buf = make([]byte, 0, estimatedSize)
			buf = append(buf, e.options.Domain...)
			buf = append(buf, '_')
			buf = append(buf, entry.Key...)
			buf = append(buf, '_')
			buf = append(buf, val...)
			buf = append(buf, "_leaky_queue"...)

			keyStr = string(buf)
		}

		// 2. L1 Local Cache Fast-Bypass
		if e.localCache != nil {
			if _, err := e.localCache.Get(buf); err == nil {
				mylogger.Debug("L1 cache hit: request blocked", zap.String("key", keyStr))
				return Decision{
					Blocked:        true,
					Limit:          policy.BucketCapacity,
					LimitRemaining: 0,
					ResetDuration:  time.Duration(ttlSeconds) * time.Second,
				}, nil
			}
		}

		// 3. radix/v4 EVALSHA Execution & Fallback State Machine
		capacityStr := strconv.FormatUint(uint64(policy.BucketCapacity), 10)
		leakRateStr := strconv.FormatFloat(leakRatePerSecond, 'f', -1, 64)
		costStr := strconv.FormatInt(cost, 10)
		nowStr := strconv.FormatFloat(nowFloat, 'f', 6, 64)
		ttlStr := strconv.FormatInt(ttlSeconds, 10)

		var result []interface{}

		// Attempt EVALSHA
		err := e.client.DoCmd(&result, "EVALSHA", "", e.scriptSha1, "1", keyStr, capacityStr, leakRateStr, costStr, nowStr, ttlStr)

		// NOSCRIPT Fallback Loop
		if err != nil && strings.Contains(err.Error(), "NOSCRIPT") {
			mylogger.Info("EVALSHA NOSCRIPT error caught, loading script...", zap.String("sha", e.scriptSha1))

			var newSha string
			errLoad := e.client.DoCmd(&newSha, "SCRIPT", "", "LOAD", e.luaBody)
			if errLoad != nil {
				if policy.FailOpen {
					mylogger.Error("Failed to SCRIPT LOAD Lua leaky bucket rate limiter, failing open", zap.Error(errLoad))
					continue
				}
				return Decision{}, fmt.Errorf("failed to SCRIPT LOAD Lua leaky bucket rate limiter: %w", errLoad)
			}

			e.scriptSha1 = newSha // Update stored SHA locally

			// Re-execute EVALSHA
			err = e.client.DoCmd(&result, "EVALSHA", "", e.scriptSha1, "1", keyStr, capacityStr, leakRateStr, costStr, nowStr, ttlStr)
		}

		if err != nil {
			mylogger.Error("Redis EVALSHA execution failed", zap.Error(err), zap.String("key", keyStr))
			if policy.FailOpen {
				continue
			}
			return Decision{}, fmt.Errorf("redis leaky bucket fail for key %s: %w", keyStr, err)
		}

		// Parse the Lua response: {allowed (0/1), water (float), reset_duration (int)}
		if len(result) < 3 {
			mylogger.Error("Invalid Lua script response length", zap.Any("result", result))
			if policy.FailOpen {
				continue
			}
			return Decision{}, fmt.Errorf("invalid lua script response")
		}

		var allowed int64
		if a, ok := result[0].(int64); ok {
			allowed = a
		}

		var waterFloat float64
		switch v := result[1].(type) {
		case int64:
			waterFloat = float64(v)
		case []byte:
			waterFloat, _ = strconv.ParseFloat(string(v), 64)
		}

		var resetDuration int64
		if r, ok := result[2].(int64); ok {
			resetDuration = r
		}

		remainingCapacity := uint32(0)
		if float64(policy.BucketCapacity) > waterFloat {
			remainingCapacity = uint32(float64(policy.BucketCapacity) - waterFloat)
		}

		// 4. Metrics & Decision Mapping
		if allowed == 0 {
			if policy.ShadowMode {
				mylogger.Debug("Leaky Bucket metric: ShadowMode violation", zap.String("key", keyStr))
				if 0 < finalDecision.LimitRemaining {
					finalDecision.LimitRemaining = 0
					finalDecision.Limit = policy.BucketCapacity
					finalDecision.ResetDuration = time.Duration(resetDuration) * time.Second
				}
				continue
			}

			if e.localCache != nil && resetDuration > 0 {
				if remainingCapacity == 0 || cost == 1 {
					_ = e.localCache.Set(buf, []byte{1}, int(resetDuration))
				}
			}

			return Decision{
				Blocked:        true,
				Limit:          policy.BucketCapacity,
				LimitRemaining: remainingCapacity,
				ResetDuration:  time.Duration(resetDuration) * time.Second,
			}, nil
		}

		// Allowed
		mylogger.Debug("Leaky Bucket metric: WithinLimit", zap.String("key", keyStr), zap.Uint32("remaining", remainingCapacity))
		if remainingCapacity < finalDecision.LimitRemaining {
			finalDecision.LimitRemaining = remainingCapacity
			finalDecision.Limit = policy.BucketCapacity
			finalDecision.ResetDuration = time.Duration(resetDuration) * time.Second
		}
	}

	if finalDecision.LimitRemaining == ^uint32(0) {
		finalDecision.LimitRemaining = 0
		finalDecision.Limit = 0
	}

	finalDecision.Blocked = false
	return finalDecision, nil
}
