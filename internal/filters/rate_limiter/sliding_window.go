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

type slidingWindowExecutor struct {
	client     redis.Client
	options    FilterOptions
	localCache *freecache.Cache // Embedded L1 cache bypass
	scriptSha1 string
	luaBody    string
	policies   map[string]YamlDescriptor
}

// getSlidingLuaScriptBody implements an atomic check-then-increment sliding window counter.
func getSlidingLuaScriptBody() string {
	return `
local current_key = KEYS[1]
local previous_key = KEYS[2]
local limit = tonumber(ARGV[1])
local cost = tonumber(ARGV[2])
local weight = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])

local previous_count = tonumber(redis.call('GET', previous_key) or "0")
local current_count = tonumber(redis.call('GET', current_key) or "0")

local estimated_count = math.ceil(previous_count * weight + current_count)

if estimated_count + cost <= limit then
    redis.call('INCRBY', current_key, cost)
    redis.call('EXPIRE', current_key, ttl)
    -- FIXED: Remaining quota must account for the sliding window estimation
    return {1, limit - (estimated_count + cost)}
else
    return {0, limit - estimated_count}
end
`
}

func NewSlidingWindowExecutor(client redis.Client, opts FilterOptions, localCache *freecache.Cache) RateLimitExecutor {
	body := getSlidingLuaScriptBody()
	hasher := sha1.New()
	hasher.Write([]byte(body))
	sha := hex.EncodeToString(hasher.Sum(nil))

	policies := make(map[string]YamlDescriptor, len(opts.Descriptors))
	for _, d := range opts.Descriptors {
		policies[d.Key] = d
	}

	return &slidingWindowExecutor{
		client:     client,
		options:    opts,
		localCache: localCache,
		scriptSha1: sha,
		luaBody:    body,
		policies:   policies,
	}
}

func (e *slidingWindowExecutor) Evaluate(ctx context.Context, descriptors []DescriptorEntry, cost int64) (Decision, error) {
	var finalDecision Decision
	finalDecision.LimitRemaining = ^uint32(0) // Max uint32 for tracking the lowest limit remaining

	now := time.Now().Unix()
	var batchBuf [1024]byte
	offset := 0

	for _, entry := range descriptors {
		policy, ok := e.policies[entry.Key]
		if !ok {
			continue // No policy mapped for this descriptor
		}

		windowSize := getDivider(policy.Unit)
		currentRoundedTimestamp := (now / windowSize) * windowSize
		previousRoundedTimestamp := currentRoundedTimestamp - windowSize

		val := entry.Value
		if policy.ShareThresholdPattern != "" {
			val = policy.ShareThresholdPattern
		}

		// 1. Dual-Key Generation with Wildcard Support and safe fallback
		// Sizing includes '{' and '}' for Redis Cluster Hash Tags
		estimatedSize := (len(e.options.Domain) + len(entry.Key) + len(val) + 30) * 2
		var currentKeyStr, previousKeyStr string

		if offset+estimatedSize <= len(batchBuf) {
			bufC := batchBuf[offset:offset]
			bufC = append(bufC, '{') // Hash Tag Start
			bufC = append(bufC, e.options.Domain...)
			bufC = append(bufC, '_')
			bufC = append(bufC, entry.Key...)
			bufC = append(bufC, '_')
			bufC = append(bufC, val...)
			bufC = append(bufC, '}') // Hash Tag End
			bufC = append(bufC, '_')
			prefixLen := len(bufC)

			bufC = strconv.AppendInt(bufC, currentRoundedTimestamp, 10)
			currentKeyStr = unsafe.String(unsafe.SliceData(bufC), len(bufC))
			offset += len(bufC)

			bufP := batchBuf[offset:offset]
			bufP = append(bufP, batchBuf[offset-len(bufC):offset-len(bufC)+prefixLen]...)
			bufP = strconv.AppendInt(bufP, previousRoundedTimestamp, 10)
			previousKeyStr = unsafe.String(unsafe.SliceData(bufP), len(bufP))
			offset += len(bufP)
		} else {
			// Fallback allocation if descriptors are huge
			bufC := make([]byte, 0, estimatedSize/2)
			bufC = append(bufC, '{')
			bufC = append(bufC, e.options.Domain...)
			bufC = append(bufC, '}')
			bufC = append(bufC, '_')
			bufC = append(bufC, entry.Key...)
			bufC = append(bufC, '_')
			bufC = append(bufC, val...)
			bufC = append(bufC, '_')
			prefixBytes := make([]byte, len(bufC))
			copy(prefixBytes, bufC)

			bufC = strconv.AppendInt(bufC, currentRoundedTimestamp, 10)
			currentKeyStr = string(bufC)

			bufP := strconv.AppendInt(prefixBytes, previousRoundedTimestamp, 10)
			previousKeyStr = string(bufP)
		}

		// 2. L1 Local Cache Fast-Bypass Check
		if e.localCache != nil {
			if _, err := e.localCache.Get(unsafe.Slice(unsafe.StringData(currentKeyStr), len(currentKeyStr))); err == nil {
				mylogger.Debug("L1 cache hit: request blocked", zap.String("key", currentKeyStr))
				return Decision{
					Blocked:        true,
					Limit:          policy.Limit,
					LimitRemaining: 0,
					ResetDuration:  time.Duration(windowSize-(now%windowSize)) * time.Second,
				}, nil
			}
		}

		// 3. Calculate sliding window parameters
		elapsed := now % windowSize
		weight := float64(windowSize-elapsed) / float64(windowSize)
		ttlSeconds := 2 * windowSize

		// 4. Format string arguments for Lua engine
		limitStr := strconv.FormatUint(uint64(policy.Limit), 10)
		costStr := strconv.FormatInt(cost, 10)
		weightStr := strconv.FormatFloat(weight, 'f', 4, 64)
		ttlStr := strconv.FormatInt(ttlSeconds, 10)

		var result []interface{}

		// 5. Execute atomic check-then-increment script via EVALSHA
		err := e.client.DoCmd(&result, "EVALSHA", "", e.scriptSha1, "2", currentKeyStr, previousKeyStr, limitStr, costStr, weightStr, ttlStr)

		// Fallback state machine if script is not pre-loaded (NOSCRIPT)
		if err != nil && strings.Contains(err.Error(), "NOSCRIPT") {
			var newSha string
			errLoad := e.client.DoCmd(&newSha, "SCRIPT", "", "LOAD", e.luaBody)
			if errLoad != nil {
				if policy.FailOpen {
					continue
				}
				return Decision{}, fmt.Errorf("failed to load sliding window lua script: %w", errLoad)
			}
			e.scriptSha1 = newSha
			err = e.client.DoCmd(&result, "EVALSHA", "", e.scriptSha1, "2", currentKeyStr, previousKeyStr, limitStr, costStr, weightStr, ttlStr)
		}

		if err != nil {
			mylogger.Error("Redis EVALSHA sliding window execution failed", zap.Error(err), zap.String("key", currentKeyStr))
			if policy.FailOpen {
				continue
			}
			return Decision{}, fmt.Errorf("redis sliding window evaluate failed for key %s: %w", currentKeyStr, err)
		}

		if len(result) < 2 {
			if policy.FailOpen {
				continue
			}
			return Decision{}, fmt.Errorf("invalid lua script response length")
		}

		// 6. Parse Lua Response: {allowed (1/0), remaining_quota}
		var allowed int64
		if a, ok := result[0].(int64); ok {
			allowed = a
		}

		var remainingQuota int64
		if r, ok := result[1].(int64); ok {
			remainingQuota = r
		}
		remain := uint32(math.Max(0, float64(remainingQuota)))

		resetSeconds := windowSize - elapsed

		// 7. Decision Mapping
		if allowed == 0 { // Blocked
			if policy.ShadowMode {
				mylogger.Debug("Sliding window metric: ShadowMode violation", zap.String("key", currentKeyStr))
				if 0 < finalDecision.LimitRemaining {
					finalDecision.LimitRemaining = 0
					finalDecision.Limit = policy.Limit
					finalDecision.ResetDuration = time.Duration(resetSeconds) * time.Second
				}
				continue
			}

			// Add blocked key to L1 local cache
			if e.localCache != nil && resetSeconds > 0 {
				_ = e.localCache.Set(unsafe.Slice(unsafe.StringData(currentKeyStr), len(currentKeyStr)), []byte{1}, int(resetSeconds))
			}

			return Decision{
				Blocked:        true,
				Limit:          policy.Limit,
				LimitRemaining: 0,
				ResetDuration:  time.Duration(resetSeconds) * time.Second,
			}, nil
		}

		// Allowed request
		mylogger.Debug("Sliding window metric: WithinLimit", zap.String("key", currentKeyStr), zap.Uint32("remaining", remain))
		if remain < finalDecision.LimitRemaining {
			finalDecision.LimitRemaining = remain
			finalDecision.Limit = policy.Limit
			finalDecision.ResetDuration = time.Duration(resetSeconds) * time.Second
		}
	}

	if finalDecision.LimitRemaining == ^uint32(0) {
		finalDecision.LimitRemaining = 0
		finalDecision.Limit = 0
	}

	finalDecision.Blocked = false
	return finalDecision, nil
}
