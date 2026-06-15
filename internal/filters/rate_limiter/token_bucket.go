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

type tokenBucketExecutor struct {
	client     redis.Client
	options    FilterOptions
	localCache *freecache.Cache
	scriptSha1 string
	luaBody    string
	policies   map[string]YamlDescriptor
}

func getLuaScriptBody() string {
	return `
local key = KEYS[1]
local max_tokens = tonumber(ARGV[1])
local fill_rate = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local now = tonumber(ARGV[4])
local ttl = tonumber(ARGV[5])

local bucket = redis.call('HMGET', key, 't', 'l')
local tokens = max_tokens
local last_updated = now

if bucket[1] then
    tokens = tonumber(bucket[1])
end
if bucket[2] then
    last_updated = tonumber(bucket[2])
end

local elapsed = math.max(0, now - last_updated)
local replenished = tokens + (elapsed * fill_rate)
tokens = math.max(0, math.min(max_tokens, replenished)) 

local allowed = 0
if tokens >= cost then
    tokens = tokens - cost
    allowed = 1
    redis.call('HMSET', key, 't', tokens, 'l', now)
    redis.call('EXPIRE', key, ttl)
end

local reset_duration = 0
if fill_rate > 0 then
    if allowed == 1 then
        reset_duration = (max_tokens - tokens) / fill_rate
    else
        local missing = cost - tokens
        reset_duration = missing / fill_rate
    end
end

reset_duration = math.max(0, reset_duration)

return {allowed, tokens, math.ceil(reset_duration)}
`
}

func NewTokenBucketExecutor(client redis.Client, opts FilterOptions, localCache *freecache.Cache) RateLimitExecutor {
	body := getLuaScriptBody()
	hasher := sha1.New()
	hasher.Write([]byte(body))
	sha := hex.EncodeToString(hasher.Sum(nil))

	policies := make(map[string]YamlDescriptor, len(opts.Descriptors))
	for _, d := range opts.Descriptors {
		policies[d.Key] = d
	}

	return &tokenBucketExecutor{
		client:     client,
		options:    opts,
		localCache: localCache,
		scriptSha1: sha,
		luaBody:    body,
		policies:   policies,
	}
}

func (e *tokenBucketExecutor) Evaluate(ctx context.Context, descriptors []DescriptorEntry, cost int64) (Decision, error) {
	var finalDecision Decision
	finalDecision.LimitRemaining = ^uint32(0) // Start with max uint32

	// Use float64 for highly precise Lua calculation of elapsed time
	nowFloat := float64(time.Now().UnixNano()) / float64(time.Second)

	var batchBuf [512]byte
	offset := 0

	for _, entry := range descriptors {
		policy, ok := e.policies[entry.Key]
		if !ok {
			continue
		}

		// Calculate TTL: safe upper limit to completely refill the bucket
		ttlSeconds := int64(0)
		if policy.FillRate > 0 {
			ttlSeconds = int64(math.Ceil(policy.MaxTokens / policy.FillRate))
		}
		if ttlSeconds <= 0 {
			ttlSeconds = 60 // Fallback minimum TTL
		}

		val := entry.Value
		if policy.ShareThresholdPattern != "" {
			val = policy.ShareThresholdPattern
		}

		// 1. Zero-Alloc Key Generation with safe fallback
		estimatedSize := len(e.options.Domain) + len(entry.Key) + len(val) + 30
		var keyStr string
		var bufC []byte

		if offset+estimatedSize <= len(batchBuf) {
			bufC = batchBuf[offset : offset : offset+estimatedSize]
			bufC = append(bufC, e.options.Domain...)
			bufC = append(bufC, '_')
			bufC = append(bufC, entry.Key...)
			bufC = append(bufC, '_')
			bufC = append(bufC, val...)

			keyStr = unsafe.String(unsafe.SliceData(bufC), len(bufC))
			offset += len(bufC)
		} else {
			bufC = make([]byte, 0, estimatedSize)
			bufC = append(bufC, e.options.Domain...)
			bufC = append(bufC, '_')
			bufC = append(bufC, entry.Key...)
			bufC = append(bufC, '_')
			bufC = append(bufC, val...)

			keyStr = string(bufC)
		}

		// 2. L1 Local Cache Fast-Bypass
		if e.localCache != nil {
			if _, err := e.localCache.Get(bufC); err == nil {
				mylogger.Debug("L1 cache hit: request blocked", zap.String("key", keyStr))
				return Decision{
					Blocked:        true,
					Limit:          uint32(policy.MaxTokens),
					LimitRemaining: 0,
					ResetDuration:  time.Duration(ttlSeconds) * time.Second,
				}, nil
			}
		}

		// 3. radix/v4 EVALSHA Execution & Fallback State Machine
		maxTokensStr := strconv.FormatFloat(policy.MaxTokens, 'f', -1, 64)
		fillRateStr := strconv.FormatFloat(policy.FillRate, 'f', -1, 64)
		costStr := strconv.FormatInt(cost, 10)
		nowStr := strconv.FormatFloat(nowFloat, 'f', 6, 64)
		ttlStr := strconv.FormatInt(ttlSeconds, 10)

		var result []interface{}

		// Attempt EVALSHA
		err := e.client.DoCmd(&result, "EVALSHA", "", e.scriptSha1, "1", keyStr, maxTokensStr, fillRateStr, costStr, nowStr, ttlStr)

		// NOSCRIPT Fallback Loop
		if err != nil && strings.Contains(err.Error(), "NOSCRIPT") {
			mylogger.Info("EVALSHA NOSCRIPT error caught, loading script...", zap.String("sha", e.scriptSha1))

			var newSha string
			errLoad := e.client.DoCmd(&newSha, "SCRIPT", "", "LOAD", e.luaBody)
			if errLoad != nil {
				if policy.FailOpen {
					mylogger.Error("Failed to SCRIPT LOAD Lua rate limiter, failing open", zap.Error(errLoad))
					continue
				}
				return Decision{}, fmt.Errorf("failed to SCRIPT LOAD Lua rate limiter: %w", errLoad)
			}

			e.scriptSha1 = newSha // Update stored SHA locally

			// Re-execute EVALSHA
			err = e.client.DoCmd(&result, "EVALSHA", "", e.scriptSha1, "1", keyStr, maxTokensStr, fillRateStr, costStr, nowStr, ttlStr)
		}

		if err != nil {
			mylogger.Error("Redis EVALSHA execution failed", zap.Error(err), zap.String("key", keyStr))
			if policy.FailOpen {
				continue
			}
			return Decision{}, fmt.Errorf("redis token bucket fail for key %s: %w", keyStr, err)
		}

		// Parse the Lua response: {allowed (0/1), tokens (float), reset_duration (int)}
		if len(result) < 3 {
			mylogger.Error("Invalid Lua script response length", zap.Any("result", result))
			if policy.FailOpen {
				continue
			}
			return Decision{}, fmt.Errorf("invalid lua script response")
		}

		// Go's interface{} parsing from radix often returns int64 for integers and string/[]byte for others
		var allowed int64
		if a, ok := result[0].(int64); ok {
			allowed = a
		}

		var remainingTokensFloat float64
		switch v := result[1].(type) {
		case int64:
			remainingTokensFloat = float64(v)
		case []byte:
			remainingTokensFloat, _ = strconv.ParseFloat(string(v), 64)
		}
		remainingTokens := uint32(math.Max(0, remainingTokensFloat))

		var resetDuration int64
		if r, ok := result[2].(int64); ok {
			resetDuration = r
		}

		// 4. Metrics & Decision Mapping
		if allowed == 0 {
			if policy.ShadowMode {
				mylogger.Debug("Token Bucket metric: ShadowMode violation", zap.String("key", keyStr))
				if 0 < finalDecision.LimitRemaining {
					finalDecision.LimitRemaining = 0
					finalDecision.Limit = uint32(policy.MaxTokens)
					finalDecision.ResetDuration = time.Duration(resetDuration) * time.Second
				}
				continue
			}

			if e.localCache != nil && resetDuration > 0 {
				if remainingTokens == 0 || cost == 1 {
					_ = e.localCache.Set(bufC, []byte{1}, int(resetDuration))
				}
			}

			return Decision{
				Blocked:        true,
				Limit:          uint32(policy.MaxTokens),
				LimitRemaining: remainingTokens,
				ResetDuration:  time.Duration(resetDuration) * time.Second,
			}, nil
		}

		// Allowed
		mylogger.Debug("Token Bucket metric: WithinLimit", zap.String("key", keyStr), zap.Uint32("remaining", remainingTokens))
		if remainingTokens < finalDecision.LimitRemaining {
			finalDecision.LimitRemaining = remainingTokens
			finalDecision.Limit = uint32(policy.MaxTokens)
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
