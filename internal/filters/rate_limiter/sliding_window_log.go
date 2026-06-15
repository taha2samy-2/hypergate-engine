package rate_limiter

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/coocood/freecache"
	mylogger "github.com/taha/myprog/internal/logger"
	"github.com/taha/myprog/internal/redis"
	"go.uber.org/zap"
)

var logCounter uint64

type slidingWindowLogExecutor struct {
	client     redis.Client
	options    FilterOptions
	localCache *freecache.Cache
	scriptSha1 string
	luaBody    string
	policies   map[string]YamlDescriptor
}

func getLogLuaScriptBody() string {
	return `
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local now = tonumber(ARGV[2])
local window = tonumber(ARGV[3])
local member = ARGV[4]

redis.call("ZREMRANGEBYSCORE", key, 0, now - window)

local count = redis.call("ZCARD", key)

if count < limit then
    redis.call("ZADD", key, now, member)
    redis.call("EXPIRE", key, math.ceil(window / 1000))
    return {1, limit - count - 1}
else
    local oldest = redis.call("ZRANGE", key, 0, 0, "WITHSCORES")
    local reset_duration = window
    if oldest and oldest[2] then
        local oldest_ts = tonumber(oldest[2])
        reset_duration = window - (now - oldest_ts)
    end
    
    reset_duration = math.max(0, reset_duration)
    
    return {0, math.ceil(reset_duration)}
end
`
}

func NewSlidingWindowLogExecutor(client redis.Client, opts FilterOptions, localCache *freecache.Cache) RateLimitExecutor {
	body := getLogLuaScriptBody()
	hasher := sha1.New()
	hasher.Write([]byte(body))
	sha := hex.EncodeToString(hasher.Sum(nil))

	policies := make(map[string]YamlDescriptor, len(opts.Descriptors))
	for _, d := range opts.Descriptors {
		policies[d.Key] = d
	}

	return &slidingWindowLogExecutor{
		client:     client,
		options:    opts,
		localCache: localCache,
		scriptSha1: sha,
		luaBody:    body,
		policies:   policies,
	}
}

func (e *slidingWindowLogExecutor) Evaluate(ctx context.Context, descriptors []DescriptorEntry, cost int64) (Decision, error) {
	var finalDecision Decision
	finalDecision.LimitRemaining = ^uint32(0)

	// Generate exact millisecond timestamp
	nowMs := time.Now().UnixNano() / int64(time.Millisecond)
	var batchBuf [512]byte
	offset := 0

	for _, entry := range descriptors {
		policy, ok := e.policies[entry.Key]
		if !ok {
			continue
		}

		windowSeconds := getDivider(policy.Unit)
		windowMs := windowSeconds * 1000

		val := entry.Value
		if policy.ShareThresholdPattern != "" {
			val = policy.ShareThresholdPattern
		}

		// Key Generation with safe fallback
		estimatedSize := len(e.options.Domain) + len(entry.Key) + len(val) + len("_sliding_log") + 30
		var keyStr string
		var buf []byte

		if offset+estimatedSize <= len(batchBuf) {
			buf = batchBuf[offset : offset : offset+estimatedSize]
			buf = append(buf, e.options.Domain...)
			buf = append(buf, '_')
			buf = append(buf, entry.Key...)
			buf = append(buf, '_')
			buf = append(buf, val...)
			buf = append(buf, "_sliding_log"...)

			keyStr = unsafe.String(unsafe.SliceData(buf), len(buf))
			offset += len(buf)
		} else {
			buf = make([]byte, 0, estimatedSize)
			buf = append(buf, e.options.Domain...)
			buf = append(buf, '_')
			buf = append(buf, entry.Key...)
			buf = append(buf, '_')
			buf = append(buf, val...)
			buf = append(buf, "_sliding_log"...)

			keyStr = string(buf)
		}

		// L1 Local Cache Fast-Bypass
		if e.localCache != nil {
			if _, err := e.localCache.Get(buf); err == nil {
				mylogger.Debug("L1 cache hit: request blocked", zap.String("key", keyStr))
				return Decision{
					Blocked:        true,
					Limit:          policy.Limit,
					LimitRemaining: 0,
					ResetDuration:  time.Duration(windowMs) * time.Millisecond,
				}, nil
			}
		}

		// EVALSHA Pipeline
		limitStr := strconv.FormatUint(uint64(policy.Limit), 10)
		nowStr := strconv.FormatInt(nowMs, 10)
		windowStr := strconv.FormatInt(windowMs, 10)
		var reqID string
		if rid, ok := ctx.Value(requestIDKey).(string); ok {
			reqID = rid
		}
		var memberBuf [128]byte
		mb := memberBuf[:0]
		mb = strconv.AppendInt(mb, time.Now().UnixMicro(), 10)
		if reqID != "" {
			mb = append(mb, '_')
			mb = append(mb, reqID...)
		} else {
			mb = append(mb, '_')
			mb = strconv.AppendUint(mb, atomic.AddUint64(&logCounter, 1), 10)
		}
		memberID := unsafe.String(unsafe.SliceData(mb), len(mb))

		var result []interface{}

		err := e.client.DoCmd(&result, "EVALSHA", "", e.scriptSha1, "1", keyStr, limitStr, nowStr, windowStr, memberID)

		// NOSCRIPT Fallback Loop
		if err != nil && strings.Contains(err.Error(), "NOSCRIPT") {
			mylogger.Info("EVALSHA NOSCRIPT error caught, loading log script...", zap.String("sha", e.scriptSha1))

			var newSha string
			errLoad := e.client.DoCmd(&newSha, "SCRIPT", "", "LOAD", e.luaBody)
			if errLoad != nil {
				if policy.FailOpen {
					mylogger.Error("Failed to SCRIPT LOAD Lua rate limiter, failing open", zap.Error(errLoad))
					continue
				}
				return Decision{}, fmt.Errorf("failed to SCRIPT LOAD sliding log lua script: %w", errLoad)
			}

			e.scriptSha1 = newSha
			err = e.client.DoCmd(&result, "EVALSHA", "", e.scriptSha1, "1", keyStr, limitStr, nowStr, windowStr, memberID)
		}

		if err != nil {
			mylogger.Error("Redis EVALSHA sliding log execution failed", zap.Error(err), zap.String("key", keyStr))
			if policy.FailOpen {
				continue
			}
			return Decision{}, fmt.Errorf("redis sliding log fail for key %s: %w", keyStr, err)
		}

		if len(result) < 2 {
			if policy.FailOpen {
				continue
			}
			return Decision{}, fmt.Errorf("invalid sliding log script response length")
		}

		var allowed int64
		if a, ok := result[0].(int64); ok {
			allowed = a
		}

		// Metrics & Decision Mapping
		if allowed == 0 {
			var resetDurationMs int64
			if r, ok := result[1].(int64); ok {
				resetDurationMs = r
			}

			if policy.ShadowMode {
				mylogger.Debug("Sliding log metric: ShadowMode violation", zap.String("key", keyStr))
				if 0 < finalDecision.LimitRemaining {
					finalDecision.LimitRemaining = 0
					finalDecision.Limit = policy.Limit
					finalDecision.ResetDuration = time.Duration(resetDurationMs) * time.Millisecond
				}
				continue
			}

			if e.localCache != nil && resetDurationMs > 0 {
				cacheTTL := int(math.Ceil(float64(resetDurationMs) / 1000.0))
				if cacheTTL < 1 {
					cacheTTL = 1
				}
				_ = e.localCache.Set(buf, []byte{1}, cacheTTL)
			}

			return Decision{
				Blocked:        true,
				Limit:          policy.Limit,
				LimitRemaining: 0,
				ResetDuration:  time.Duration(resetDurationMs) * time.Millisecond,
			}, nil
		}

		// Allowed branch
		var remainInt int64
		if r, ok := result[1].(int64); ok {
			remainInt = r
		}
		remain := uint32(math.Max(0, float64(remainInt)))

		if remain < finalDecision.LimitRemaining {
			finalDecision.LimitRemaining = remain
			finalDecision.Limit = policy.Limit
			finalDecision.ResetDuration = time.Duration(windowMs) * time.Millisecond
		}
	}

	if finalDecision.LimitRemaining == ^uint32(0) {
		finalDecision.LimitRemaining = 0
		finalDecision.Limit = 0
	}

	finalDecision.Blocked = false
	return finalDecision, nil
}
