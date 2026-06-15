package rate_limiter

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/coocood/freecache"
	mylogger "github.com/taha/myprog/internal/logger"
	"github.com/taha/myprog/internal/redis"
	"go.uber.org/zap"
)

type fixedWindowExecutor struct {
	client                     redis.Client
	options                    FilterOptions
	localCache                 *freecache.Cache // Embedded L1 cache bypass
	jitterRand                 *rand.Rand
	expirationJitterMaxSeconds int64
	policies                   map[string]YamlDescriptor
}

func NewFixedWindowExecutor(client redis.Client, opts FilterOptions, localCache *freecache.Cache, jitterRand *rand.Rand, jitterMax int64) RateLimitExecutor {
	policies := make(map[string]YamlDescriptor, len(opts.Descriptors))
	for _, d := range opts.Descriptors {
		policies[d.Key] = d
	}

	return &fixedWindowExecutor{
		client:                     client,
		options:                    opts,
		localCache:                 localCache,
		jitterRand:                 jitterRand,
		expirationJitterMaxSeconds: jitterMax,
		policies:                   policies,
	}
}

// getDivider converts a unit string into its corresponding seconds representation.
func getDivider(unit string) int64 {
	switch strings.ToLower(unit) {
	case "second":
		return 1
	case "minute":
		return 60
	case "hour":
		return 3600
	case "day":
		return 86400
	case "week":
		return 604800
	case "month":
		return 2592000 // approx 30 days
	case "year":
		return 31536000
	default:
		return 60 // default to minute if unrecognized
	}
}

func (e *fixedWindowExecutor) Evaluate(ctx context.Context, descriptors []DescriptorEntry, cost int64) (Decision, error) {
	var finalDecision Decision
	finalDecision.LimitRemaining = ^uint32(0) // Start with max uint32 to find the minimum

	now := time.Now().Unix()
	var batchBuf [512]byte
	offset := 0

	for _, entry := range descriptors {
		policy, ok := e.policies[entry.Key]
		if !ok {
			continue // No policy mapped for this descriptor
		}

		divider := getDivider(policy.Unit)
		roundedTimestamp := (now / divider) * divider
		resetSeconds := divider - (now % divider)

		// 1. Zero-Alloc Key Generation with safe fallback
		estimatedSize := len(e.options.Domain) + len(entry.Key) + len(entry.Value) + 30
		var keyStr string
		var buf []byte

		if offset+estimatedSize <= len(batchBuf) {
			buf = batchBuf[offset : offset : offset+estimatedSize]
			buf = append(buf, e.options.Domain...)
			buf = append(buf, '_')
			buf = append(buf, entry.Key...)
			buf = append(buf, '_')
			buf = append(buf, entry.Value...)
			buf = append(buf, '_')
			buf = strconv.AppendInt(buf, roundedTimestamp, 10)

			keyStr = unsafe.String(unsafe.SliceData(buf), len(buf))
			offset += len(buf)
		} else {
			buf = make([]byte, 0, estimatedSize)
			buf = append(buf, e.options.Domain...)
			buf = append(buf, '_')
			buf = append(buf, entry.Key...)
			buf = append(buf, '_')
			buf = append(buf, entry.Value...)
			buf = append(buf, '_')
			buf = strconv.AppendInt(buf, roundedTimestamp, 10)

			keyStr = string(buf)
		}

		// 2. L1 Local Cache Fast-Bypass
		if e.localCache != nil {
			if _, err := e.localCache.Get(buf); err == nil {
				// 3. Stop-Increment Check (if stop_increment_when_overlimit is implicitly handled by the cache presence)
				mylogger.Debug("L1 cache hit: request blocked", zap.String("key", keyStr))
				return Decision{
					Blocked:        true,
					Limit:          policy.Limit,
					LimitRemaining: 0,
					ResetDuration:  time.Duration(resetSeconds) * time.Second,
				}, nil
			}
		}

		// 3. Optimized radix/v4 Pipelining (The Core Dance)
		var count uint64
		var p redis.Pipeline
		p = e.client.PipeAppend(p, &count, "INCRBY", keyStr, cost)

		ttl := resetSeconds
		if e.expirationJitterMaxSeconds > 0 && e.jitterRand != nil {
			ttl += e.jitterRand.Int63n(e.expirationJitterMaxSeconds)
		}
		p = e.client.PipeAppend(p, nil, "EXPIRE", keyStr, ttl)

		if err := e.client.PipeDo(ctx, p); err != nil {
			mylogger.Error("Redis pipeline execution failed", zap.Error(err), zap.String("key", keyStr))
			if policy.FailOpen {
				continue // Gracefully fallback to allowing the request
			}
			return Decision{}, fmt.Errorf("redis pipeline fail for key %s: %w", keyStr, err)
		}

		// 4. Metrics Propagation & Logging
		if count > uint64(policy.Limit) {
			mylogger.Debug("Rate limit metric: OverLimit", zap.String("key", keyStr), zap.Uint64("count", count))
		} else if count > uint64(float64(policy.Limit)*0.8) {
			mylogger.Debug("Rate limit metric: NearLimit", zap.String("key", keyStr), zap.Uint64("count", count))
		} else {
			mylogger.Debug("Rate limit metric: WithinLimit", zap.String("key", keyStr), zap.Uint64("count", count))
		}

		// 5. Polymorphic Decision Mapping
		if count > uint64(policy.Limit) {
			if policy.ShadowMode {
				mylogger.Debug("Rate limit metric: ShadowMode violation", zap.String("key", keyStr))
				// Still update limits to reflect exhaustion, but do not block
				remain := uint32(0)
				if remain < finalDecision.LimitRemaining {
					finalDecision.LimitRemaining = remain
					finalDecision.Limit = policy.Limit
					finalDecision.ResetDuration = time.Duration(resetSeconds) * time.Second
				}
				continue
			}

			// Not shadow mode: client is blocked
			if e.localCache != nil {
				// If the rejection was caused by a cost of 1, OR if the counter prior to this cost was already at or above the limit
				if cost == 1 || (count-uint64(cost)) >= uint64(policy.Limit) {
					_ = e.localCache.Set(buf, []byte{1}, int(resetSeconds))
				}
			}

			return Decision{
				Blocked:        true,
				Limit:          policy.Limit,
				LimitRemaining: 0,
				ResetDuration:  time.Duration(resetSeconds) * time.Second,
			}, nil
		}

		// Update the final decision using the most restrictive limit observed
		remain := policy.Limit - uint32(count)
		if remain < finalDecision.LimitRemaining {
			finalDecision.LimitRemaining = remain
			finalDecision.Limit = policy.Limit
			finalDecision.ResetDuration = time.Duration(resetSeconds) * time.Second
		}
	}

	// Normalise LimitRemaining if no valid descriptors matched
	if finalDecision.LimitRemaining == ^uint32(0) {
		finalDecision.LimitRemaining = 0
		finalDecision.Limit = 0
	}

	finalDecision.Blocked = false
	return finalDecision, nil
}
